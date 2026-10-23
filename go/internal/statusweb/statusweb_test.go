package statusweb

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

type source coordinator.Status

func (s source) Status() coordinator.Status { return coordinator.Status(s) }

var m0 = coordinator.Task{Kind: pb.TaskKind_TASK_KIND_MAP, Index: 0}

var status = source{
	Jobs: []*pb.GetJobStatusResponse{{
		JobId: "job-1", State: pb.JobState_JOB_STATE_MAPPING, MapTasksTotal: 7, MapTasksCompleted: 3, ReduceTasksTotal: 4,
		Counters: &pb.Counters{Values: map[string]int64{"map.input_records": 12, `odd"name`: 1}},
		Error:    "<script>",
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
	for _, want := range []string{"job-1", "JOB_STATE_MAPPING", "3/7", "map.input_records: 12", "host-b", "w3", "w1", m0.String(), "&lt;script&gt;"} {
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
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}
