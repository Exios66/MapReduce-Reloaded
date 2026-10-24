// Package coordinator holds the master's state (§3.2): every task's state
// and attempts, where each completed map task's output lives, and which
// workers are alive.
//
// Its types do no locking. The coordinator guards all of them with one
// mutex; see docs/design/0006-coordinator-concurrency.md.
package coordinator

import (
	"fmt"

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
	running  map[uint32]string       // worker of each running attempt
	worker   string                  // worker of the accepted attempt, once completed
	outputs  []*pb.PartitionLocation // map tasks: one per partition, once completed
	crashes  map[uint64]int          // attempts that crashed on each record
	skip     []uint64                // records later attempts skip
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
// (§3.6).
type TaskTable struct {
	maps, reduces []task
	mapsDone      int
	reducesDone   int
	skipped       int // records on every task's skip list
}

// NewTaskTable returns a table of m idle map tasks and r idle reduce tasks.
func NewTaskTable(m, r int) *TaskTable {
	t := &TaskTable{maps: make([]task, m), reduces: make([]task, r)}
	for _, ts := range [][]task{t.maps, t.reduces} {
		for i := range ts {
			ts[i] = task{state: pb.TaskState_TASK_STATE_IDLE, running: map[uint32]string{}}
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

// Backup returns an in-progress task of the current phase that may get a
// backup attempt on worker (§3.6). It returns false if there is none.
func (t *TaskTable) Backup(worker string) (Task, bool) {
	return t.find(func(ts *task) bool { return ts.canBackUp(worker) }, nil)
}

// find returns the task of the current phase that ok accepts and rank, for
// map tasks, ranks lowest: the first such task if there are ties, or if rank
// is nil.
func (t *TaskTable) find(ok func(*task) bool, rank func(uint32) int) (Task, bool) {
	kind, tasks := pb.TaskKind_TASK_KIND_MAP, t.maps
	if t.mapsDone == len(t.maps) {
		kind, tasks, rank = pb.TaskKind_TASK_KIND_REDUCE, t.reduces, nil
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

// Assign starts a new attempt on worker of an idle task, or a backup
// attempt of one running elsewhere, and returns its number.
func (t *TaskTable) Assign(id Task, worker string) (uint32, error) {
	ts, err := t.task(id)
	if err != nil {
		return 0, err
	}
	if ts.state != pb.TaskState_TASK_STATE_IDLE && !ts.canBackUp(worker) {
		return 0, fmt.Errorf("coordinator: assign %v to %s: task is %v on %d workers", id, worker, ts.state, len(ts.running))
	}
	attempt := ts.attempts
	ts.attempts++
	ts.running[attempt] = worker
	ts.state = pb.TaskState_TASK_STATE_IN_PROGRESS
	return attempt, nil
}

// Complete records that worker finished an attempt. A map task's outputs
// must hold one location per partition, in order. It reports whether the
// result was accepted: false if the task already completed or the attempt is
// not running on worker, for example because its worker was declared lost.
func (t *TaskTable) Complete(id Task, attempt uint32, worker string, outputs []*pb.PartitionLocation) (bool, error) {
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
	clear(ts.running)
	ts.state, ts.worker = pb.TaskState_TASK_STATE_COMPLETED, worker
	return true, nil
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
		if f != nil && f.FailedRecord != nil && ts.crashed(f.GetFailedRecord()) {
			t.skipped++
		}
	}
	return nil
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
// stop, and its completed map tasks must run again. It returns the tasks
// that became idle.
func (t *TaskTable) ResetWorker(worker string) []Task {
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
// the reduce task that cannot fetch it.
func (t *TaskTable) StopAll() {
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
			for _, w := range ts[i].running {
				running[w] = append(running[w], Task{kind, uint32(i)})
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
	if int(id.Index) >= len(tasks) {
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
	for _, w := range ts.running {
		if w == worker {
			return false
		}
	}
	return true
}

// runsOn reports whether attempt is running on worker.
func (ts *task) runsOn(attempt uint32, worker string) bool {
	w, running := ts.running[attempt]
	return running && w == worker
}

// stopAttemptsOn stops the task's running attempts on worker, and reports
// whether that made the task idle.
func (ts *task) stopAttemptsOn(worker string) bool {
	stopped := false
	for attempt, w := range ts.running {
		if w == worker {
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
