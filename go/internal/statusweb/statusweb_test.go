package statusweb

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

type source coordinator.Status

func (s source) Status() coordinator.Status { return coordinator.Status(s) }

var m0 = coordinator.Task{Kind: pb.TaskKind_TASK_KIND_MAP, Index: 0}

// at is when the status was taken: job-1 has run for 2.5 s, and job-2 ran
// for 90 s. job-3 was replayed from a log with no times.
var at = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

var status = source{
	At: at,
	Jobs: []*pb.GetJobStatusResponse{{
		JobId: "job-1", State: pb.JobState_JOB_STATE_MAPPING, MapTasksTotal: 7, MapTasksCompleted: 3, ReduceTasksTotal: 4,
		Counters:    &pb.Counters{Values: map[string]int64{"map.input_records": 12, `odd"name`: 1}},
		Error:       "<script>",
		SubmittedAt: timestamppb.New(at.Add(-2500 * time.Millisecond)),
	}, {
		JobId: "job-2", State: pb.JobState_JOB_STATE_SUCCEEDED,
		SubmittedAt: timestamppb.New(at.Add(-time.Hour)),
		FinishedAt:  timestamppb.New(at.Add(-time.Hour + 90*time.Second)),
	}, {
		JobId: "job-3", State: pb.JobState_JOB_STATE_SUCCEEDED,
	}},
	Workers: []coordinator.WorkerStatus{
		{Worker: coordinator.Worker{ID: "w2", Info: &pb.WorkerInfo{Host: "host-b"}}, Running: []coordinator.Task{m0}},
		{Worker: coordinator.Worker{ID: "w3"}},
	},
	Lost: []coordinator.LostWorker{{ID: "w1", At: time.Now(), Tasks: []coordinator.Task{m0}}},
}

func get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(Handler(status))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err = errors.Join(err, resp.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestPage(t *testing.T) {
	resp, body := get(t, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %s", resp.Status)
	}
	for _, want := range []string{"job-1", "JOB_STATE_MAPPING", "<td>2.5s</td>", "<td>1m30s</td>", "3/7", "map.input_records: 12", "host-b", "w3", "w1", m0.String(), "&lt;script&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("page did not escape the job's error")
	}
	if resp, _ := get(t, "/nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/nope: status %s, want 404", resp.Status)
	}
}

func TestMetrics(t *testing.T) {
	resp, body := get(t, "/metrics")
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("content type %q", ct)
	}
	for _, want := range []string{
		"# TYPE mr_workers gauge\nmr_workers 2\n",
		`mr_job_tasks{job="job-1",kind="map",state="completed"} 3` + "\n",
		`mr_job_tasks{job="job-1",kind="reduce",state="total"} 4` + "\n",
		`mr_job_counter{job="job-1",name="map.input_records"} 12` + "\n",
		`mr_job_counter{job="job-1",name="odd\"name"} 1` + "\n",
		`mr_job_duration_seconds{job="job-1"} 2.5` + "\n",
		`mr_job_duration_seconds{job="job-2"} 90` + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `mr_job_duration_seconds{job="job-3"}`) {
		t.Errorf("metrics give a duration for a job with no submit time:\n%s", body)
	}
}

// A job submitted before the clock was set back has run for 0 s, not less.
func TestDurationClockSetBack(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	j := &pb.GetJobStatusResponse{SubmittedAt: timestamppb.New(at)}
	if d, ok := duration(j, at.Add(-time.Second)); d != 0 || !ok {
		t.Errorf("running: duration = %v, %v; want 0, true", d, ok)
	}
	j.FinishedAt = timestamppb.New(at.Add(-time.Second))
	if d, ok := duration(j, at); d != 0 || !ok {
		t.Errorf("finished: duration = %v, %v; want 0, true", d, ok)
	}
}
