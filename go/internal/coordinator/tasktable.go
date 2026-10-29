// Package coordinator holds the master's state (§3.2): every task's state
// and attempts, where each completed map task's output lives, and which
// workers are alive.
//
// Its types do no locking. The coordinator guards all of them with one
// mutex; see docs/design/0006-coordinator-concurrency.md.
package coordinator

import (
	"fmt"
	"math"
	"slices"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// Task names one task of the job.
type Task struct {
	Kind  pb.TaskKind
	Index uint32
}

func (t Task) String() string { return fmt.Sprintf("%v %d", t.Kind, t.Index) }

type task struct {
	state    pb.TaskState
	attempts uint32                  // attempts started; the next one gets this number
	running  map[uint32]run          // each running attempt
	worker   string                  // worker of the accepted attempt, once completed
	outputs  []*pb.PartitionLocation // map tasks: one per partition, once completed
	crashes  map[uint64]int          // attempts that crashed on each record
	failures int                     // failed attempts, except fetches of lost map output
	skip     []uint64                // records later attempts skip
}

// run is a running attempt.
type run struct {
	worker  string
	started time.Time
	backup  bool // started while another attempt of the task was running (§3.6)

	fraction float32   // of its input the worker last reported processed; 0 before the first report
	reported time.Time // when the coordinator heard it; zero before the first report
}

// SkipAfter is how many attempts must crash on a record before later
// attempts skip it: more than one, so that one crash with another cause
// loses no record (§4.6).
const SkipAfter = 2

// TaskTable tracks the state of a job's M map and R reduce tasks:
//
//	IDLE --Assign--> IN_PROGRESS --Complete--> COMPLETED
//	  ^                   |                        |
//	  +--- Fail, or ResetWorker on its worker -----+ (map tasks only)
//
// A task returns to IDLE when its last running attempt fails or its worker
// is lost. A completed map task also returns to IDLE when its worker is
// lost or a reduce task cannot fetch its output, since that output was on
// the worker's disk; a completed reduce
// task's output is in the global file system, so it stays done (§3.3).
// The first attempt to complete wins; later reports are rejected.
//
// Near the end of a phase, an in-progress task may get one backup attempt
// on another worker, so that one slow machine does not hold up the job
// (§3.6). The task that has run longest gets the first backup.
type TaskTable struct {
	maps, reduces []task
	mapsDone      int
	reducesDone   int
	skipped       int // records on every task's skip list
	backups       int // backup attempts started
	backupWins    int // tasks completed by a backup attempt

	// speeds is each worker's record of completed attempts, per kind of
	// task, for LateBackup.
	speeds map[pb.TaskKind]map[string]speed
}

// speed is the sum of 1/duration, in 1/s, over a worker's completed
// attempts of one kind, and their number.
type speed struct {
	sum float64
	n   int
}

func (s speed) mean() float64 { return s.sum / float64(s.n) }

// NewTaskTable returns a table of m idle map tasks and r idle reduce tasks.
func NewTaskTable(m, r int) *TaskTable {
	t := &TaskTable{maps: make([]task, m), reduces: make([]task, r)}
	for _, ts := range [][]task{t.maps, t.reduces} {
		for i := range ts {
			ts[i] = task{state: pb.TaskState_TASK_STATE_IDLE, running: map[uint32]run{}}
		}
	}
	return t
}

// Next returns an idle task to run: a map task, or a reduce task once every
// map task has completed (§3.1). If rank is not nil, it is the map task that
// rank ranks lowest, the first one if several tie. It returns false if there
// is none.
func (t *TaskTable) Next(rank func(index uint32) int) (Task, bool) {
	return t.find(func(ts *task) bool { return ts.state == pb.TaskState_TASK_STATE_IDLE }, rank)
}

// Backup returns the in-progress task of the current phase that may get a
// backup attempt on worker and whose attempt started first, so the likeliest
// straggler is backed up first (§3.6). It returns false if there is none.
func (t *TaskTable) Backup(worker string) (Task, bool) {
	kind, tasks := t.phase()
	best := -1
	for i := range tasks {
		if tasks[i].canBackUp(worker) && (best < 0 || tasks[i].started().Before(tasks[best].started())) {
			best = i
		}
	}
	if best < 0 {
		return Task{}, false
	}
	return Task{kind, uint32(best)}, true
}

// LateParams are the settings of LateBackup, from Zaharia et al., "Improving
// MapReduce Performance in Heterogeneous Environments" (OSDI 2008). A zero
// field has no default here: Config fills them in.
type LateParams struct {
	// SpeculativeCap is the share of the workers that may be running backup
	// attempts at once, with room for at least one.
	SpeculativeCap float64
	// SlowTaskPercentile is the percentile of the candidates' progress rates
	// at or below which a task may get a backup.
	SlowTaskPercentile float64
	// SlowNodePercentile is the percentile of the workers' speeds below
	// which a worker gets no backup.
	SlowNodePercentile float64
	// MinRuntime is how long an attempt must have run before it may get a
	// backup.
	MinRuntime time.Duration
}

// LateBackup returns the in-progress task of the current phase that should
// get a backup attempt on worker at now, following LATE (Zaharia et al.
// 2008), or false if there is none. nWorkers is the number of registered
// workers. Its rules:
//
//  1. If the running backup attempts number at least SpeculativeCap of
//     nWorkers (rounded down, but at least 1), there is none.
//  2. A worker's speed is the mean of 1/duration over its completed attempts
//     of the phase's kind. If worker has completed some and its speed is
//     below the SlowNodePercentile of the speeds of all workers that have,
//     there is none. A worker with no history passes.
//  3. The candidates are the tasks that can get a backup on worker (see
//     Backup) whose one attempt has run for at least MinRuntime and has
//     reported its progress at least once. An attempt's rate is the fraction
//     it reported done divided by the seconds it has run. The slow
//     candidates are those with a rate at or below the SlowTaskPercentile of
//     the candidates' rates.
//  4. Of those, the task with the most time left, (1-fraction)/rate, wins,
//     and a rate of 0 counts as infinite time left. Ties go to the attempt
//     that started first, then the lowest task index.
//
// A percentile is the linear interpolation between the closest ranks of the
// sorted values: at rank p/100*(n-1). Backup does not use any of this.
func (t *TaskTable) LateBackup(worker string, now time.Time, p LateParams, nWorkers int) (Task, bool) {
	if t.runningBackups() >= max(1, int(p.SpeculativeCap*float64(nWorkers))) {
		return Task{}, false
	}
	kind, tasks := t.phase()
	if mine, ok := t.speeds[kind][worker]; ok {
		var all []float64
		for _, s := range t.speeds[kind] {
			all = append(all, s.mean())
		}
		slices.Sort(all)
		if mine.mean() < percentile(all, p.SlowNodePercentile) {
			return Task{}, false
		}
	}
	type candidate struct {
		index    int
		started  time.Time
		rate     float64
		timeLeft float64
	}
	var candidates []candidate
	var rates []float64
	for i := range tasks {
		if !tasks[i].canBackUp(worker) {
			continue
		}
		for _, r := range tasks[i].running { // the only one
			elapsed := now.Sub(r.started)
			if elapsed < p.MinRuntime || elapsed <= 0 || r.reported.IsZero() {
				continue
			}
			c := candidate{i, r.started, float64(r.fraction) / elapsed.Seconds(), math.Inf(1)}
			if c.rate > 0 {
				c.timeLeft = (1 - float64(r.fraction)) / c.rate
			}
			candidates = append(candidates, c)
			rates = append(rates, c.rate)
		}
	}
	slices.Sort(rates)
	slow := percentile(rates, p.SlowTaskPercentile)
	best := -1
	for i, c := range candidates {
		if c.rate > slow {
			continue
		}
		if best < 0 || c.timeLeft > candidates[best].timeLeft ||
			c.timeLeft == candidates[best].timeLeft && c.started.Before(candidates[best].started) {
			best = i
		}
	}
	if best < 0 {
		return Task{}, false
	}
	return Task{kind, uint32(candidates[best].index)}, true
}

// runningBackups returns the number of running backup attempts.
func (t *TaskTable) runningBackups() int {
	n := 0
	for _, ts := range [][]task{t.maps, t.reduces} {
		for i := range ts {
			for _, r := range ts[i].running {
				if r.backup {
					n++
				}
			}
		}
	}
	return n
}

// percentile returns the p-th percentile of sorted, which must not be empty
// unless the result is unused: the linear interpolation between the values
// at the ranks on either side of p/100*(len-1).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := min(max(p, 0), 100) / 100 * float64(len(sorted)-1)
	lo := int(rank)
	if lo+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	// The conversion keeps arm64 from fusing the product into an FMA, which
	// would round differently from Python.
	return sorted[lo] + float64((sorted[lo+1]-sorted[lo])*(rank-float64(lo)))
}

// phase returns the kind and tasks of the current phase: map tasks until
// every one has completed, then reduce tasks (§3.1).
func (t *TaskTable) phase() (pb.TaskKind, []task) {
	if t.mapsDone == len(t.maps) {
		return pb.TaskKind_TASK_KIND_REDUCE, t.reduces
	}
	return pb.TaskKind_TASK_KIND_MAP, t.maps
}

// find returns the task of the current phase that ok accepts and rank, for
// map tasks, ranks lowest: the first such task if there are ties, or if rank
// is nil.
func (t *TaskTable) find(ok func(*task) bool, rank func(uint32) int) (Task, bool) {
	kind, tasks := t.phase()
	if kind == pb.TaskKind_TASK_KIND_REDUCE {
		rank = nil
	}
	best, bestRank := -1, 0
	for i := range tasks {
		if !ok(&tasks[i]) {
			continue
		}
		if rank == nil {
			return Task{kind, uint32(i)}, true
		}
		if r := rank(uint32(i)); best < 0 || r < bestRank {
			best, bestRank = i, r
		}
	}
	if best < 0 {
		return Task{}, false
	}
	return Task{kind, uint32(best)}, true
}

// Assign starts a new attempt on worker at started, of an idle task, or a
// backup attempt of one running elsewhere, and returns its number.
func (t *TaskTable) Assign(id Task, worker string, started time.Time) (uint32, error) {
	ts, err := t.task(id)
	if err != nil {
		return 0, err
	}
	backup := ts.state != pb.TaskState_TASK_STATE_IDLE
	if backup && !ts.canBackUp(worker) {
		return 0, fmt.Errorf("coordinator: assign %v to %s: task is %v on %d workers", id, worker, ts.state, len(ts.running))
	}
	if backup {
		t.backups++
	}
	attempt := ts.attempts
	ts.attempts++
	ts.running[attempt] = run{worker: worker, started: started, backup: backup}
	ts.state = pb.TaskState_TASK_STATE_IN_PROGRESS
	return attempt, nil
}

// SetFraction records that attempt of task id, running on worker, reported at
// at that it had processed fraction of its input, clamped to 0 to 1. It
// reports whether it recorded it: not if the attempt is not running on
// worker, or fraction is NaN. The attempt keeps the larger of the stored
// and the new fraction, so heartbeats that arrive out of order cannot lower
// it. The paper's rule for backups (Backup) ignores the fraction; LateBackup
// uses it (ADR 0019).
func (t *TaskTable) SetFraction(id Task, attempt uint32, worker string, fraction float32, at time.Time) bool {
	ts, err := t.task(id)
	if err != nil || !ts.runsOn(attempt, worker) || math.IsNaN(float64(fraction)) {
		return false
	}
	r := ts.running[attempt]
	r.fraction, r.reported = max(r.fraction, min(max(fraction, 0), 1)), at
	ts.running[attempt] = r
	return true
}

// Fraction returns the fraction of its input that the attempt of task id
// running on worker last reported processed, or 0 if it has not reported or
// no attempt of the task runs on worker.
func (t *TaskTable) Fraction(id Task, worker string) float32 {
	ts, err := t.task(id)
	if err != nil {
		return 0
	}
	for _, r := range ts.running {
		if r.worker == worker {
			return r.fraction
		}
	}
	return 0
}

// Complete records that worker finished an attempt. A map task's outputs
// must hold one location per partition, in order. It reports whether the
// result was accepted: false if the task already completed or the attempt is
// not running on worker, for example because its worker was declared lost.
// It does not record how long the attempt took; CompleteAt does.
func (t *TaskTable) Complete(id Task, attempt uint32, worker string, outputs []*pb.PartitionLocation) (bool, error) {
	return t.CompleteAt(id, attempt, worker, outputs, time.Time{})
}

// CompleteAt is Complete for an attempt that finished at at. If the result is
// accepted, it adds the time since the attempt started to worker's record for
// LateBackup; a zero at, or one not after the start, adds nothing.
func (t *TaskTable) CompleteAt(id Task, attempt uint32, worker string, outputs []*pb.PartitionLocation, at time.Time) (bool, error) {
	ts, err := t.task(id)
	if err != nil {
		return false, err
	}
	if !ts.runsOn(attempt, worker) {
		return false, nil
	}
	if id.Kind == pb.TaskKind_TASK_KIND_MAP {
		if err := t.checkOutputs(id.Index, outputs); err != nil {
			return false, err
		}
		ts.outputs = outputs
		t.mapsDone++
	} else {
		t.reducesDone++
	}
	if ts.running[attempt].backup {
		t.backupWins++
	}
	if d := at.Sub(ts.running[attempt].started).Seconds(); !at.IsZero() && d > 0 {
		t.record(id.Kind, worker, d)
	}
	clear(ts.running)
	ts.state, ts.worker = pb.TaskState_TASK_STATE_COMPLETED, worker
	return true, nil
}

// record adds an attempt of kind that worker completed in d seconds.
func (t *TaskTable) record(kind pb.TaskKind, worker string, d float64) {
	if t.speeds == nil {
		t.speeds = map[pb.TaskKind]map[string]speed{}
	}
	if t.speeds[kind] == nil {
		t.speeds[kind] = map[string]speed{}
	}
	s := t.speeds[kind][worker]
	s.sum, s.n = s.sum+1/d, s.n+1
	t.speeds[kind][worker] = s
}

// Fail records that an attempt on worker failed as f says. The task becomes
// idle again unless another attempt is still running. If the attempt could
// not fetch a map output, that map task runs again (§3.3); if it crashed on
// a record, later attempts may skip that record (§4.6). Reports for attempts
// that are not running on worker are ignored.
func (t *TaskTable) Fail(id Task, attempt uint32, worker string, f *pb.TaskFailure) error {
	ts, err := t.task(id)
	if err != nil {
		return err
	}
	if ts.runsOn(attempt, worker) {
		delete(ts.running, attempt)
		ts.idleIfStopped()
		t.lose(f.GetLostOutput())
		if f.GetLostOutput() == nil { // a lost map output is not this task's fault
			ts.failures++
		}
		if f != nil && f.FailedRecord != nil && ts.crashed(f.GetFailedRecord()) {
			t.skipped++
		}
	}
	return nil
}

// Failures returns how many attempts of a task failed, not counting those
// that could not fetch lost map output, or 0 for an invalid task.
func (t *TaskTable) Failures(id Task) int {
	ts, err := t.task(id)
	if err != nil {
		return 0
	}
	return ts.failures
}

// Skip returns the records that attempts of a task skip, in the order they
// were added.
func (t *TaskTable) Skip(id Task) ([]uint64, error) {
	ts, err := t.task(id)
	if err != nil {
		return nil, err
	}
	return ts.skip, nil
}

// lose makes a completed map task idle if its output for lost's partition
// is still where lost says. One already run again elsewhere is kept.
func (t *TaskTable) lose(lost *pb.PartitionLocation) {
	i, p, addr := lost.GetMapIndex(), lost.GetPartition(), lost.GetWorkerAddress()
	if addr == "" || int(i) >= len(t.maps) {
		return
	}
	if ts := &t.maps[i]; int(p) < len(ts.outputs) && ts.outputs[p].GetWorkerAddress() == addr {
		ts.state, ts.worker, ts.outputs = pb.TaskState_TASK_STATE_IDLE, "", nil
		t.mapsDone--
	}
}

// ResetWorker handles the loss of a worker (§3.3): its running attempts
// stop, its completed map tasks must run again, and its record of completed
// attempts for LateBackup is forgotten, so that a dead worker does not
// count in the slow-node percentile. It returns the tasks that became idle.
func (t *TaskTable) ResetWorker(worker string) []Task {
	for _, history := range t.speeds {
		delete(history, worker)
	}
	var reset []Task
	for i := range t.maps {
		ts := &t.maps[i]
		if ts.state == pb.TaskState_TASK_STATE_COMPLETED && ts.worker == worker {
			ts.state, ts.worker, ts.outputs = pb.TaskState_TASK_STATE_IDLE, "", nil
			t.mapsDone--
			reset = append(reset, Task{pb.TaskKind_TASK_KIND_MAP, uint32(i)})
		} else if ts.stopAttemptsOn(worker) {
			reset = append(reset, Task{pb.TaskKind_TASK_KIND_MAP, uint32(i)})
		}
	}
	for i := range t.reduces {
		if t.reduces[i].stopAttemptsOn(worker) {
			reset = append(reset, Task{pb.TaskKind_TASK_KIND_REDUCE, uint32(i)})
		}
	}
	return reset
}

// StopAll stops every running attempt, as when the coordinator restarts:
// the attempts' workers will register again under new IDs. Completed tasks
// stay completed; a map output that turns out to be lost is reported by
// the reduce task that cannot fetch it. The workers' records of completed
// attempts are forgotten too: they are under IDs that will not return.
func (t *TaskTable) StopAll() {
	clear(t.speeds)
	for _, ts := range [][]task{t.maps, t.reduces} {
		for i := range ts {
			clear(ts[i].running)
			ts[i].idleIfStopped()
		}
	}
}

// Sources returns where partition p of every map task's output lives, for a
// reduce task. It must be called only once all map tasks have completed.
func (t *TaskTable) Sources(p uint32) []*pb.PartitionLocation {
	sources := make([]*pb.PartitionLocation, len(t.maps))
	for i := range t.maps {
		sources[i] = t.maps[i].outputs[p]
	}
	return sources
}

// State returns the state of a task.
func (t *TaskTable) State(id Task) (pb.TaskState, error) {
	ts, err := t.task(id)
	if err != nil {
		return pb.TaskState_TASK_STATE_UNSPECIFIED, err
	}
	return ts.state, nil
}

// Running returns the tasks each worker is running an attempt of, in
// order: map tasks, then reduce tasks.
func (t *TaskTable) Running() map[string][]Task {
	running := map[string][]Task{}
	add := func(kind pb.TaskKind, ts []task) {
		for i := range ts {
			for _, r := range ts[i].running {
				running[r.worker] = append(running[r.worker], Task{kind, uint32(i)})
			}
		}
	}
	add(pb.TaskKind_TASK_KIND_MAP, t.maps)
	add(pb.TaskKind_TASK_KIND_REDUCE, t.reduces)
	return running
}

// Runs reports whether attempt of task id is running on worker.
func (t *TaskTable) Runs(id Task, attempt uint32, worker string) bool {
	ts, err := t.task(id)
	return err == nil && ts.runsOn(attempt, worker)
}

// Progress returns the number of completed map and reduce tasks.
func (t *TaskTable) Progress() (maps, reduces int) { return t.mapsDone, t.reducesDone }

// Skipped returns the number of records on every task's skip list.
func (t *TaskTable) Skipped() int { return t.skipped }

// Backups returns the number of backup attempts started, and of tasks that a
// backup attempt completed (§3.6).
func (t *TaskTable) Backups() (started, won int) { return t.backups, t.backupWins }

// Done reports whether every task has completed.
func (t *TaskTable) Done() bool {
	return t.mapsDone == len(t.maps) && t.reducesDone == len(t.reduces)
}

func (t *TaskTable) task(id Task) (*task, error) {
	var tasks []task
	switch id.Kind {
	case pb.TaskKind_TASK_KIND_MAP:
		tasks = t.maps
	case pb.TaskKind_TASK_KIND_REDUCE:
		tasks = t.reduces
	}
	if int64(id.Index) >= int64(len(tasks)) {
		return nil, fmt.Errorf("coordinator: no task %v", id)
	}
	return &tasks[id.Index], nil
}

func (t *TaskTable) checkOutputs(index uint32, outputs []*pb.PartitionLocation) error {
	if len(outputs) != len(t.reduces) {
		return fmt.Errorf("coordinator: map %d reported %d outputs, want %d", index, len(outputs), len(t.reduces))
	}
	for p, o := range outputs {
		if o.GetMapIndex() != index || o.GetPartition() != uint32(p) {
			return fmt.Errorf("coordinator: map %d reported output %d as map %d partition %d",
				index, p, o.GetMapIndex(), o.GetPartition())
		}
		if o.GetWorkerAddress() == "" && o.GetUri() == "" {
			return fmt.Errorf("coordinator: map %d reported output %d with no location", index, p)
		}
	}
	return nil
}

// canBackUp reports whether the task may get a backup attempt on worker: it
// is in progress with one attempt, on another worker.
func (ts *task) canBackUp(worker string) bool {
	if ts.state != pb.TaskState_TASK_STATE_IN_PROGRESS || len(ts.running) != 1 {
		return false
	}
	for _, r := range ts.running {
		if r.worker == worker {
			return false
		}
	}
	return true
}

// started returns when the task's earliest running attempt started.
func (ts *task) started() time.Time {
	var first time.Time
	for _, r := range ts.running {
		if first.IsZero() || r.started.Before(first) {
			first = r.started
		}
	}
	return first
}

// runsOn reports whether attempt is running on worker.
func (ts *task) runsOn(attempt uint32, worker string) bool {
	r, running := ts.running[attempt]
	return running && r.worker == worker
}

// stopAttemptsOn stops the task's running attempts on worker, and reports
// whether that made the task idle.
func (ts *task) stopAttemptsOn(worker string) bool {
	stopped := false
	for attempt, r := range ts.running {
		if r.worker == worker {
			delete(ts.running, attempt)
			stopped = true
		}
	}
	return stopped && ts.idleIfStopped()
}

// crashed counts an attempt that crashed on record n, and skips n once
// SkipAfter attempts have. It reports whether it added n to the skip list.
func (ts *task) crashed(n uint64) bool {
	if ts.crashes == nil {
		ts.crashes = map[uint64]int{}
	}
	ts.crashes[n]++
	if ts.crashes[n] != SkipAfter {
		return false
	}
	ts.skip = append(ts.skip, n)
	return true
}

// idleIfStopped makes an in-progress task with no running attempt idle, and
// reports whether it did.
func (ts *task) idleIfStopped() bool {
	if ts.state != pb.TaskState_TASK_STATE_IN_PROGRESS || len(ts.running) > 0 {
		return false
	}
	ts.state = pb.TaskState_TASK_STATE_IDLE
	return true
}
