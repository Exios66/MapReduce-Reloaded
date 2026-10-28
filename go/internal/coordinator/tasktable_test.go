package coordinator

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

const (
	mapKind    = pb.TaskKind_TASK_KIND_MAP
	reduceKind = pb.TaskKind_TASK_KIND_REDUCE
	idle       = pb.TaskState_TASK_STATE_IDLE
	inProgress = pb.TaskState_TASK_STATE_IN_PROGRESS
	completed  = pb.TaskState_TASK_STATE_COMPLETED
)

// outputs returns a valid output list for map task i of a job with r
// reduce tasks.
func outputs(i uint32, r int) []*pb.PartitionLocation {
	locs := make([]*pb.PartitionLocation, r)
	for p := range locs {
		locs[p] = &pb.PartitionLocation{MapIndex: i, Partition: uint32(p), Uri: "file:///map-output"}
	}
	return locs
}

// clock is when the next attempt that mustAssign starts begins: one second
// after the last.
var clock = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func mustAssign(t *testing.T, tt *TaskTable, id Task, worker string) uint32 {
	t.Helper()
	clock = clock.Add(time.Second)
	attempt, err := tt.Assign(id, worker, clock)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func assertState(t *testing.T, tt *TaskTable, id Task, want pb.TaskState) {
	t.Helper()
	if got, _ := tt.State(id); got != want {
		t.Fatalf("%v is %v, want %v", id, got, want)
	}
}

// A job runs its maps, then its reduces, each completing exactly once.
func TestLifecycle(t *testing.T) {
	tt := NewTaskTable(2, 1)
	m0, m1, r0 := Task{mapKind, 0}, Task{mapKind, 1}, Task{reduceKind, 0}

	a := mustAssign(t, tt, m0, "w1")
	if next, _ := tt.Next(nil); next != m1 {
		t.Fatalf("Next = %v, want %v", next, m1)
	}
	b := mustAssign(t, tt, m1, "w2")
	if _, ok := tt.Next(nil); ok {
		t.Fatal("a reduce task was offered before the maps completed")
	}
	for _, c := range []struct {
		id      Task
		attempt uint32
		worker  string
	}{{m0, a, "w1"}, {m1, b, "w2"}} {
		if ok, err := tt.Complete(c.id, c.attempt, c.worker, outputs(c.id.Index, 1)); !ok || err != nil {
			t.Fatalf("Complete(%v) = %v, %v", c.id, ok, err)
		}
	}
	if next, _ := tt.Next(nil); next != r0 {
		t.Fatalf("Next = %v, want %v", next, r0)
	}
	if src := tt.Sources(0); len(src) != 2 || src[1].GetMapIndex() != 1 {
		t.Fatalf("Sources(0) = %v", src)
	}
	c := mustAssign(t, tt, r0, "w1")
	if ok, _ := tt.Complete(r0, c, "w1", nil); !ok || !tt.Done() {
		t.Fatal("job not done after its last reduce completed")
	}
}

// Next offers the idle map task that ranks lowest, the first of any tie,
// and ignores the rank for reduce tasks.
func TestNextRank(t *testing.T) {
	tt := NewTaskTable(3, 2)
	ranks := []int{2, 1, 1}
	rank := func(i uint32) int { return ranks[i] }
	if next, _ := tt.Next(rank); next != (Task{mapKind, 1}) {
		t.Fatalf("Next = %v, want map 1", next)
	}
	mustAssign(t, tt, Task{mapKind, 1}, "w1")
	if next, _ := tt.Next(rank); next != (Task{mapKind, 2}) {
		t.Fatalf("Next = %v, want map 2", next)
	}
	for i, id := range []Task{{mapKind, 0}, {mapKind, 1}, {mapKind, 2}} {
		a := uint32(0)
		if i != 1 {
			a = mustAssign(t, tt, id, "w1")
		}
		if ok, err := tt.Complete(id, a, "w1", outputs(id.Index, 2)); !ok || err != nil {
			t.Fatalf("Complete(%v) = %v, %v", id, ok, err)
		}
	}
	if next, _ := tt.Next(func(uint32) int { panic("ranked a reduce task") }); next != (Task{reduceKind, 0}) {
		t.Fatalf("Next = %v, want reduce 0", next)
	}
	if next, _ := NewTaskTable(3, 1).Next(func(i uint32) int { return []int{1, 2, 3}[i] }); next != (Task{mapKind, 0}) {
		t.Fatalf("Next = %v, want map 0, which ranks lowest", next)
	}
}

func TestFirstCompletionWins(t *testing.T) {
	tt := NewTaskTable(1, 1)
	m0 := Task{mapKind, 0}
	a := mustAssign(t, tt, m0, "w1")
	if ok, _ := tt.Complete(m0, a, "w2", outputs(0, 1)); ok {
		t.Fatal("completion reported by another worker accepted")
	}
	if ok, _ := tt.Complete(m0, a, "w1", outputs(0, 1)); !ok {
		t.Fatal("first completion rejected")
	}
	if ok, _ := tt.Complete(m0, a, "w1", outputs(0, 1)); ok {
		t.Fatal("second completion accepted")
	}
	if maps, _ := tt.Progress(); maps != 1 {
		t.Fatalf("%d maps done, want 1", maps)
	}
}

func TestFailReturnsToIdle(t *testing.T) {
	tt := NewTaskTable(1, 1)
	m0 := Task{mapKind, 0}
	a := mustAssign(t, tt, m0, "w1")
	for _, report := range []struct {
		attempt uint32
		worker  string
	}{{a + 1, "w1"}, {a, "w2"}} { // not running on that worker: ignored
		if err := tt.Fail(m0, report.attempt, report.worker, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertState(t, tt, m0, inProgress)
	if err := tt.Fail(m0, a, "w1", nil); err != nil {
		t.Fatal(err)
	}
	assertState(t, tt, m0, idle)
	if b := mustAssign(t, tt, m0, "w2"); b != a+1 {
		t.Fatalf("second attempt is %d, want %d", b, a+1)
	}
	if ok, _ := tt.Complete(m0, a, "w1", outputs(0, 1)); ok {
		t.Fatal("completion of the failed attempt accepted")
	}
}

// Losing a worker re-runs its completed maps and running tasks, but not its
// completed reduces (§3.3).
func TestResetWorker(t *testing.T) {
	tt := NewTaskTable(2, 2)
	m0, m1, r0, r1 := Task{mapKind, 0}, Task{mapKind, 1}, Task{reduceKind, 0}, Task{reduceKind, 1}
	for _, id := range []Task{m0, m1} {
		a := mustAssign(t, tt, id, "w1")
		if _, err := tt.Complete(id, a, "w1", outputs(id.Index, 2)); err != nil {
			t.Fatal(err)
		}
	}
	a := mustAssign(t, tt, r0, "w1")
	if _, err := tt.Complete(r0, a, "w1", nil); err != nil {
		t.Fatal(err)
	}
	mustAssign(t, tt, r1, "w1")

	got := tt.ResetWorker("w1")
	want := []Task{m0, m1, r1}
	if !slices.Equal(got, want) {
		t.Fatalf("ResetWorker = %v, want %v", got, want)
	}
	assertState(t, tt, r0, completed)
	if maps, reduces := tt.Progress(); maps != 0 || reduces != 1 {
		t.Fatalf("Progress = %d, %d; want 0, 1", maps, reduces)
	}
	if tt.ResetWorker("w2") != nil {
		t.Fatal("resetting an unknown worker changed tasks")
	}

	// A task that keeps an attempt on another worker is not reset.
	tt = NewTaskTable(1, 1)
	mustAssign(t, tt, m0, "w1")
	mustAssign(t, tt, m0, "w2")
	if got := tt.ResetWorker("w1"); got != nil {
		t.Fatalf("ResetWorker = %v, want nothing: w2 still runs %v", got, m0)
	}
	assertState(t, tt, m0, inProgress)
}

// A reduce that cannot fetch a map output sends that map task back to idle,
// unless it has already run again elsewhere (§3.3).
func TestFailWithLostOutput(t *testing.T) {
	tt := NewTaskTable(1, 1)
	m0, r0 := Task{mapKind, 0}, Task{reduceKind, 0}
	a := mustAssign(t, tt, m0, "w1")
	outs := outputs(0, 1)
	outs[0].WorkerAddress = "w1:7000"
	if _, err := tt.Complete(m0, a, "w1", outs); err != nil {
		t.Fatal(err)
	}
	lost := tt.Sources(0)[0]
	stale := &pb.PartitionLocation{MapIndex: 0, WorkerAddress: "w9:7000"}
	noMap := &pb.PartitionLocation{MapIndex: 1, WorkerAddress: "w1:7000"}
	noPartition := &pb.PartitionLocation{MapIndex: 0, Partition: 1, WorkerAddress: "w1:7000"}
	for _, l := range []*pb.PartitionLocation{nil, stale, noMap, noPartition} {
		if err := tt.Fail(r0, mustAssign(t, tt, r0, "w2"), "w2", &pb.TaskFailure{LostOutput: l}); err != nil {
			t.Fatal(err)
		}
		assertState(t, tt, m0, completed)
	}
	if err := tt.Fail(r0, 99, "w2", &pb.TaskFailure{LostOutput: lost}); err != nil { // not a running attempt
		t.Fatal(err)
	}
	assertState(t, tt, m0, completed)
	if err := tt.Fail(r0, mustAssign(t, tt, r0, "w2"), "w2", &pb.TaskFailure{LostOutput: lost}); err != nil {
		t.Fatal(err)
	}
	assertState(t, tt, m0, idle)
	assertState(t, tt, r0, idle)
	if _, ok := tt.Next(nil); !ok {
		t.Fatal("no task to run")
	}
	checkCounts(t, tt)
}

// A record is skipped once SkipAfter attempts have crashed on it. Crashes
// that are not reported by a running attempt do not count (§4.6).
func TestFailWithCrash(t *testing.T) {
	tt := NewTaskTable(1, 1)
	m0 := Task{mapKind, 0}
	crash := func(n uint64) *pb.TaskFailure { return &pb.TaskFailure{FailedRecord: &n} }
	for _, f := range []*pb.TaskFailure{crash(5), {}, crash(7), crash(5)} {
		if err := tt.Fail(m0, mustAssign(t, tt, m0, "w1"), "w1", f); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Fail(m0, 99, "w1", crash(7)); err != nil { // not a running attempt
		t.Fatal(err)
	}
	if skip, err := tt.Skip(m0); err != nil || !slices.Equal(skip, []uint64{5}) {
		t.Fatalf("Skip = %v, %v; want [5]", skip, err)
	}
	if skip, _ := tt.Skip(Task{reduceKind, 0}); len(skip) != 0 || tt.Skipped() != 1 {
		t.Fatalf("reduce task skips %v; %d records skipped in all, want 1", skip, tt.Skipped())
	}
}

// An in-progress task gets at most one backup attempt, on another worker.
// Whichever attempt completes first wins (§3.6).
func TestBackups(t *testing.T) {
	tt := NewTaskTable(2, 1)
	m0, m1 := Task{mapKind, 0}, Task{mapKind, 1}
	a := mustAssign(t, tt, m0, "w1")
	if next, _ := tt.Backup("w2"); next != m0 {
		t.Fatalf("Backup(w2) = %v, want %v", next, m0)
	}
	b := mustAssign(t, tt, m1, "w2")
	if next, _ := tt.Backup("w1"); next != m1 {
		t.Fatalf("Backup(w1) = %v, want %v, which w1 is not running", next, m1)
	}
	backup := mustAssign(t, tt, m1, "w1")
	if next, ok := tt.Backup("w3"); next != m0 || !ok {
		t.Fatalf("Backup(w3) = %v, %v; want %v", next, ok, m0)
	}
	mustAssign(t, tt, m0, "w3")
	if next, ok := tt.Backup("w4"); ok {
		t.Fatalf("Backup(w4) = %v; want nothing: every task has its backup", next)
	}

	// The backup of m1 wins; the original's report is rejected.
	if ok, _ := tt.Complete(m1, backup, "w1", outputs(1, 1)); !ok {
		t.Fatal("backup's completion rejected")
	}
	if ok, _ := tt.Complete(m1, b, "w2", outputs(1, 1)); ok {
		t.Fatal("original's completion accepted after the backup's")
	}
	// m0 loses its original to a failure, and its backup carries on.
	if err := tt.Fail(m0, a, "w1", &pb.TaskFailure{}); err != nil {
		t.Fatal(err)
	}
	assertState(t, tt, m0, inProgress)
	if next, _ := tt.Backup("w1"); next != m0 {
		t.Fatalf("Backup(w1) = %v, want %v again", next, m0)
	}
	// Losing the backup's worker too leaves m0 with no attempt.
	tt.ResetWorker("w3")
	assertState(t, tt, m0, idle)
	checkCounts(t, tt)
	if started, won := tt.Backups(); started != 2 || won != 1 {
		t.Fatalf("Backups() = %d started, %d won; want 2, 1", started, won)
	}
}

// The task whose attempt started first, the likeliest straggler, gets the
// first backup, whatever its index; then the next oldest (§3.6).
func TestBackupOldestFirst(t *testing.T) {
	for _, kind := range []pb.TaskKind{mapKind, reduceKind} {
		tt := NewTaskTable(3, 3)
		if kind == reduceKind {
			for i := range uint32(3) {
				m := Task{mapKind, i}
				if ok, _ := tt.Complete(m, mustAssign(t, tt, m, "w0"), "w0", outputs(i, 3)); !ok {
					t.Fatal("map task not completed")
				}
			}
		}
		for i, w := range []string{"w3", "w1", "w2"} { // task 2 first, then 0, then 1
			mustAssign(t, tt, Task{kind, uint32((i + 2) % 3)}, w)
		}
		for _, want := range []uint32{2, 0, 1} {
			next, ok := tt.Backup("w4")
			if !ok || next != (Task{kind, want}) {
				t.Fatalf("Backup(w4) = %v, %v; want %v", next, ok, Task{kind, want})
			}
			mustAssign(t, tt, next, "w4")
		}
	}
}

func TestErrors(t *testing.T) {
	tt := NewTaskTable(1, 2)
	m0 := Task{mapKind, 0}
	for name, err := range map[string]error{
		"unknown task": tt.Fail(Task{reduceKind, 2}, 0, "w1", nil),
		"no kind":      tt.Fail(Task{Index: 0}, 0, "w1", nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "no task") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := tt.State(Task{mapKind, 1}); err == nil {
		t.Error("State of an unknown task succeeded")
	}
	if _, err := tt.Skip(Task{mapKind, 1}); err == nil {
		t.Error("Skip of an unknown task succeeded")
	}
	if a, err := tt.Assign(Task{mapKind, 1}, "w1", clock); a != 0 || err == nil {
		t.Errorf("Assign of an unknown task = %d, %v", a, err)
	}
	if ok, err := tt.Complete(Task{mapKind, 1}, 0, "w1", nil); ok || err == nil {
		t.Errorf("Complete of an unknown task = %v, %v", ok, err)
	}
	if n := tt.Failures(Task{mapKind, 1}); n != 0 {
		t.Errorf("Failures of an unknown task = %d", n)
	}
	a := mustAssign(t, tt, m0, "w1")
	if _, err := tt.Assign(m0, "w1", clock); err == nil {
		t.Error("backed a task up on its own worker")
	}
	mustAssign(t, tt, m0, "w2")
	if a, err := tt.Assign(m0, "w3", clock); a != 0 || err == nil {
		t.Errorf("backed a task up twice: %d, %v", a, err)
	}
	nowhere := outputs(0, 2)
	nowhere[1].Uri = ""
	for _, bad := range [][]*pb.PartitionLocation{outputs(0, 1), outputs(1, 2), nowhere, {nil, nil}, nil} {
		if ok, err := tt.Complete(m0, a, "w1", bad); ok || err == nil {
			t.Errorf("Complete accepted outputs %v", bad)
		}
	}
	assertState(t, tt, m0, inProgress)
}

// Random sequences of operations keep the table's invariants.
func TestInvariants(t *testing.T) {
	const m, r = 4, 3
	workers := []string{"w1", "w2", "w3"}
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 0))
		tt := NewTaskTable(m, r)
		attempts := map[Task]uint32{}
		for range 100 {
			id := Task{[]pb.TaskKind{mapKind, reduceKind}[rng.IntN(2)], uint32(rng.IntN(m))}
			if id.Kind == reduceKind {
				id.Index %= r
			}
			before, _ := tt.State(id)
			switch rng.IntN(4) {
			case 0:
				w := workers[rng.IntN(len(workers))]
				next, ok := tt.Next(nil)
				if !ok {
					next, ok = tt.Backup(w)
				}
				if ok {
					if next.Kind == reduceKind && !mapsComplete(tt) {
						t.Fatalf("seed %d: reduce offered before the maps completed", seed)
					}
					a := mustAssign(t, tt, next, w)
					if a != attempts[next] {
						t.Fatalf("seed %d: %v got attempt %d, want %d", seed, next, a, attempts[next])
					}
					attempts[next]++
				}
			case 1:
				ok, err := tt.Complete(id, uint32(rng.IntN(3)), workers[rng.IntN(len(workers))], outputs(id.Index, r))
				if err != nil {
					t.Fatal(err)
				}
				if ok && before != inProgress {
					t.Fatalf("seed %d: %v completed from %v", seed, id, before)
				}
			case 2:
				if err := tt.Fail(id, uint32(rng.IntN(3)), workers[rng.IntN(len(workers))], nil); err != nil {
					t.Fatal(err)
				}
				if after, _ := tt.State(id); before == completed && after != completed {
					t.Fatalf("seed %d: Fail undid a completed task", seed)
				}
			case 3:
				_, reducesBefore := tt.Progress()
				tt.ResetWorker(workers[rng.IntN(len(workers))])
				if _, reduces := tt.Progress(); reduces != reducesBefore {
					t.Fatalf("seed %d: a worker loss undid a completed reduce", seed)
				}
			}
			checkCounts(t, tt)
		}
	}
}

func mapsComplete(tt *TaskTable) bool {
	maps, _ := tt.Progress()
	return maps == len(tt.maps)
}

// checkCounts checks that the progress counters match the task states, and
// that exactly the completed map tasks have outputs.
func checkCounts(t *testing.T, tt *TaskTable) {
	t.Helper()
	count := func(tasks []task) (n int) {
		for _, ts := range tasks {
			if ts.state == completed {
				n++
			}
			if (ts.state == inProgress) != (len(ts.running) > 0) || len(ts.running) > 2 {
				t.Fatalf("task in state %v has %d running attempts", ts.state, len(ts.running))
			}
		}
		return n
	}
	maps, reduces := tt.Progress()
	if count(tt.maps) != maps || count(tt.reduces) != reduces {
		t.Fatalf("Progress = %d, %d; states say %d, %d", maps, reduces, count(tt.maps), count(tt.reduces))
	}
	for _, ts := range tt.maps {
		if (ts.state == completed) != (ts.outputs != nil) {
			t.Fatalf("map in state %v has outputs %v", ts.state, ts.outputs)
		}
	}
}
