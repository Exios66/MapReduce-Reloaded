package coordinator

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// captureLogs sends the default logger's records, at debug and above, to
// the returned function, which returns them since the last call. The tests
// that use it do not run in parallel.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var b bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return func() []map[string]any {
		var recs []map[string]any
		dec := json.NewDecoder(&b)
		for dec.More() {
			var r map[string]any
			if err := dec.Decode(&r); err != nil {
				t.Fatal(err)
			}
			recs = append(recs, r)
		}
		b.Reset()
		return recs
	}
}

// find returns the first record with message msg, or fails the test.
func find(t *testing.T, recs []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, r := range recs {
		if r["msg"] == msg {
			return r
		}
	}
	t.Fatalf("no %q record in %v", msg, recs)
	return nil
}

// A job's life is in the log: its workers, submission, every attempt's
// assignment and report, and how it ended.
func TestEventsOfASuccessfulJob(t *testing.T) {
	logged := captureLogs(t)
	s, w, id := newJob(t, 1)
	recs := logged()
	if r := find(t, recs, "worker registered"); r["worker"] != w || r["workers"] != float64(1) {
		t.Fatalf("worker registered: %v", r)
	}
	r := find(t, recs, "job submitted")
	if r["job"] != id || r["program"] != "wordcount" || r["map_tasks"] != float64(1) || r["reduce_tasks"] != float64(1) {
		t.Fatalf("job submitted: %v", r)
	}

	succeed(t, s, w, next(t, s, w), outputs(0, 1))
	recs = logged()
	if r := find(t, recs, "task assigned"); r["job"] != id || r["kind"] != "map" || r["index"] != float64(0) || r["worker"] != w || r["backup"] != false {
		t.Fatalf("task assigned: %v", r)
	}
	if r := find(t, recs, "task attempt finished"); r["kind"] != "map" || r["accepted"] != true {
		t.Fatalf("task attempt finished: %v", r)
	}

	succeed(t, s, w, next(t, s, w), nil)
	recs = logged()
	if r := find(t, recs, "task assigned"); r["kind"] != "reduce" {
		t.Fatalf("task assigned: %v", r)
	}
	if r := find(t, recs, "job succeeded"); r["job"] != id || r["seconds"] == nil {
		t.Fatalf("job succeeded: %v", r)
	}
}

// A failure is logged with its message, and the failure that fails the job
// is logged with the reason.
func TestEventsOfAFailedJob(t *testing.T) {
	logged := captureLogs(t)
	s, w, id := newJobWith(t, Config{MaxTaskFailures: 1}, 1)
	logged()
	attempt := next(t, s, w)
	_, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Failure{
		Failure: &pb.TaskFailure{Message: "boom"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	recs := logged()
	if r := find(t, recs, "task attempt failed"); r["level"] != "WARN" || r["err"] != "boom" || r["fatal"] != false || r["worker"] != w {
		t.Fatalf("task attempt failed: %v", r)
	}
	if r := find(t, recs, "job failed"); r["level"] != "ERROR" || r["job"] != id || r["err"] == "" {
		t.Fatalf("job failed: %v", r)
	}
}

// A backup attempt is logged as one, and an attempt the coordinator no
// longer wants is logged when a heartbeat cancels it.
func TestEventsOfBackupsAndCancels(t *testing.T) {
	logged := captureLogs(t)
	s, w, _ := newJobWith(t, Config{Backups: true}, 1)
	w2 := register(t, s)
	first := next(t, s, w)
	logged()
	backup := next(t, s, w2)
	if r := find(t, logged(), "task assigned"); r["backup"] != true || r["worker"] != w2 {
		t.Fatalf("backup not logged as one")
	}
	succeed(t, s, w, first, outputs(0, 1))
	logged()
	_, err := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w2, Running: []*pb.TaskProgress{{Attempt: backup}}})
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, logged(), "attempt told to stop"); r["level"] != "DEBUG" || r["worker"] != w2 {
		t.Fatalf("cancel not logged: %v", r)
	}
	_, err = s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: "w-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	find(t, logged(), "heartbeat from an unknown worker; told to register again")
}

// Recovering a server from its log replays old events and logs none of
// them again.
func TestReplayLogsNoEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := recoverServer(t, Config{}, path)
	w := register(t, s)
	if _, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	succeed(t, s, w, next(t, s, w), outputs(0, 1))
	succeed(t, s, w, next(t, s, w), nil)

	logged := captureLogs(t)
	recoverServer(t, Config{}, path)
	for _, r := range logged() {
		switch r["msg"] {
		case "job submitted", "task assigned", "task attempt finished", "task attempt failed", "job succeeded", "job failed":
			t.Errorf("replay logged %v", r)
		}
	}
}
