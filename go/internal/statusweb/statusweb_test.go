package statusweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
		Counters:    &pb.Counters{Values: map[string]int64{"map.input_records": 12, "map.input.records": 20, "map.output.records": 8, `odd"name`: 1}},
		Error:       "<script>",
		SubmittedAt: timestamppb.New(at.Add(-2500 * time.Millisecond)),
	}, {
		JobId: "job-2", State: pb.JobState_JOB_STATE_SUCCEEDED, BackupAttempts: 3, BackupWins: 1, MapTasksHostLocal: 5, MapTasksZoneLocal: 2,
		SubmittedAt: timestamppb.New(at.Add(-time.Hour)),
		FinishedAt:  timestamppb.New(at.Add(-time.Hour + 90*time.Second)),
	}, {
		JobId: "job-3", State: pb.JobState_JOB_STATE_SUCCEEDED,
	}},
	Workers: []coordinator.WorkerStatus{
		{Worker: coordinator.Worker{ID: "w2", LastSeen: at.Add(-3 * time.Second), Info: &pb.WorkerInfo{Host: "host-b", Zone: "zone-x", Slots: 4}}, Running: []coordinator.Task{m0}, Progress: map[coordinator.Task]float32{m0: 0.426}},
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
	for _, want := range []string{"job-1", "badge state-mapping", ">mapping<", ">2.5s<", ">1m30s<", "3 started, 1 won", "3/7 &middot; 43%", `max="7" value="3"`, "map in <b>20</b>", "map.input_records: 12", "host-b", "w3", "w1", m0.String(), m0.String() + "</span> 43%<br>", "&lt;script&gt;", "prefers-color-scheme: dark", `id="job-job-1"`, `href="#job-job-1"`, "local maps: host <b>5</b>, zone <b>2</b>", "zone-x", "1/4", "3s ago"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Count(body, "<script") != 1 {
		t.Error("page has a script besides its own: the job's error was not escaped")
	}
	if !strings.Contains(body, "<body>") {
		t.Error("page is dark by default")
	}
	if _, dark := get(t, "/?theme=dark"); !strings.Contains(dark, `<body class="dark">`) {
		t.Error("?theme=dark did not set the dark class")
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
		`mr_job_backups{job="job-2",state="started"} 3` + "\n",
		`mr_job_backups{job="job-2",state="won"} 1` + "\n",
		`mr_job_map_tasks_local{job="job-2",level="host"} 5` + "\n",
		`mr_job_map_tasks_local{job="job-2",level="zone"} 2` + "\n",
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

// The page works offline: its one script is inline, and it loads nothing.
func TestPageIsSelfContained(t *testing.T) {
	_, body := get(t, "/")
	body = strings.ReplaceAll(body, "&lt;script&gt;", "")
	for _, bad := range []string{"http://", "https://", "@import", "<link", "<img", "src="} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if strings.Count(body, "<script>") != 1 || strings.Count(body, "<script") != 1 {
		t.Error("page does not have exactly one inline script")
	}
	if !strings.Contains(body, `<noscript><meta http-equiv="refresh"`) {
		t.Error("page does not refresh without its script")
	}
}

func TestStatusJSON(t *testing.T) {
	resp, body := get(t, "/status.json")
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	var doc struct {
		At      float64
		Jobs    []map[string]any
		Workers []map[string]any
		Lost    []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		return strings.Join(ks, " ")
	}
	// The Python server's test lists the same keys.
	if got, want := keys(doc.Jobs[0]), "backup_attempts backup_wins counters duration_seconds error finished_at id map_completed map_host_local map_total map_zone_local reduce_completed reduce_total state submitted_at"; got != want {
		t.Errorf("job keys\n got %s\nwant %s", got, want)
	}
	if got, want := keys(doc.Workers[0]), "host id last_seen running slots zone"; got != want {
		t.Errorf("worker keys %s, want %s", got, want)
	}
	if got, want := keys(doc.Lost[0]), "at id tasks"; got != want {
		t.Errorf("lost keys %s, want %s", got, want)
	}
	j1, j2, j3 := doc.Jobs[0], doc.Jobs[1], doc.Jobs[2]
	if j1["id"] != "job-1" || j1["state"] != "mapping" || j1["map_completed"] != 3.0 || j1["duration_seconds"] != 2.5 {
		t.Errorf("job-1 = %v", j1)
	}
	if j2["duration_seconds"] != 90.0 || j2["backup_wins"] != 1.0 || j2["map_zone_local"] != 2.0 {
		t.Errorf("job-2 = %v", j2)
	}
	if j3["duration_seconds"] != nil || j3["submitted_at"] != nil {
		t.Errorf("job-3 = %v", j3)
	}
	run := doc.Workers[0]["running"].([]any)[0].(map[string]any)
	if run["task"] != m0.String() {
		t.Errorf("running task %v, want %v", run["task"], m0)
	}
	if doc.Workers[1]["last_seen"] != nil {
		t.Errorf("a worker never seen has last_seen %v", doc.Workers[1]["last_seen"])
	}
}
