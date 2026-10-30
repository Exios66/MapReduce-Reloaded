package showcase_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/promtext"
	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

// workerText is a worker's /metrics as the worker writes it: the host
// label, the four byte counters the sampler keeps, and families it drops.
const workerText = `# HELP mr_worker_info The worker's host, as the coordinator's status lists it; the value is always 1.
# TYPE mr_worker_info gauge
mr_worker_info{host="abc123"} 1
# HELP mr_worker_tasks_total Task attempts finished, by kind and result.
# TYPE mr_worker_tasks_total counter
mr_worker_tasks_total{kind="map",result="ok"} 7
# HELP mr_worker_map_input_bytes_total Bytes of input splits mapped, counted when each map task ends.
# TYPE mr_worker_map_input_bytes_total counter
mr_worker_map_input_bytes_total 1000
# HELP mr_worker_map_output_bytes_total Bytes of map output written.
# TYPE mr_worker_map_output_bytes_total counter
mr_worker_map_output_bytes_total 2000
# HELP mr_worker_shuffle_fetched_bytes_total Bytes of map output fetched from workers.
# TYPE mr_worker_shuffle_fetched_bytes_total counter
mr_worker_shuffle_fetched_bytes_total 3000
# HELP mr_worker_pruned_bytes_total Bytes of map output deleted after their job ended.
# TYPE mr_worker_pruned_bytes_total counter
mr_worker_pruned_bytes_total 4000
# HELP mr_worker_reduce_output_bytes_total Bytes of keys and values reduced into the output, counted when each reduce task ends.
# TYPE mr_worker_reduce_output_bytes_total counter
mr_worker_reduce_output_bytes_total 5000
# HELP mr_worker_disk_free_bytes Free bytes on the work directory's file system, at the last task request.
# TYPE mr_worker_disk_free_bytes gauge
mr_worker_disk_free_bytes 6000
`

func TestParseMetricsKeepsTheFourByteCounters(t *testing.T) {
	bytes, _ := showcase.ParseMetrics(workerText)
	want := map[string]int64{
		"mr_worker_map_input_bytes_total":       1000,
		"mr_worker_map_output_bytes_total":      2000,
		"mr_worker_shuffle_fetched_bytes_total": 3000,
		"mr_worker_reduce_output_bytes_total":   5000,
	}
	if !reflect.DeepEqual(bytes, want) {
		t.Errorf("ParseMetrics bytes = %v, want %v", bytes, want)
	}
}

func TestParseMetricsIgnoresOtherFamiliesAndComments(t *testing.T) {
	bytes, _ := showcase.ParseMetrics(workerText)
	for _, name := range []string{"mr_worker_pruned_bytes_total", "mr_worker_disk_free_bytes", "mr_worker_tasks_total", "mr_worker_info"} {
		if _, ok := bytes[name]; ok {
			t.Errorf("ParseMetrics kept %s", name)
		}
	}
	if len(bytes) != 4 {
		t.Errorf("ParseMetrics kept %d keys, want 4", len(bytes))
	}
}

func TestParseMetricsReturnsTheHostLabelUnescaped(t *testing.T) {
	// promtext.Label writes the label; ParseMetrics must read back the same
	// host, whatever characters it holds.
	for _, host := range []string{"worker-1", "", `a"b`, `c\d`, "x\ny", `}{ `, `\n`} {
		text := fmt.Sprintf("mr_worker_info{host=%s} 1\n", promtext.Label(host))
		if _, got := showcase.ParseMetrics(text); got != host {
			t.Errorf("ParseMetrics host for %q = %q", host, got)
		}
	}
}

func TestFrameOfAnUnansweredSourceHasNoZeroes(t *testing.T) {
	f := showcase.Frame{
		Seq:     3,
		At:      time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
		Workers: []showcase.WorkerSample{{Address: "10.0.0.5:8081", OK: false}},
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"bytes"`) {
		t.Errorf("unanswered worker JSON has bytes: %s", raw)
	}
	if !strings.Contains(string(raw), `"ok":false`) {
		t.Errorf("unanswered worker JSON lacks ok false: %s", raw)
	}
}

func TestFrameJSONRoundTrips(t *testing.T) {
	f := showcase.Frame{
		Seq:            42,
		At:             time.Date(2026, 10, 30, 12, 0, 0, 500, time.UTC),
		Status:         json.RawMessage(`{"job":{"state":"RUNNING"}}`),
		StatusOK:       true,
		Workers:        []showcase.WorkerSample{{Address: "10.0.0.5:8081", Host: "abc123", OK: true, Bytes: map[string]int64{"mr_worker_map_input_bytes_total": 1000}}},
		SampleInterval: 250 * time.Millisecond,
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"seq":42`, `"status_ok":true`, `"sample_interval_seconds":0.25`, `"status":{"job":{"state":"RUNNING"}}`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("frame JSON lacks %s: %s", key, raw)
		}
	}
	var back showcase.Frame
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, f) {
		t.Errorf("round trip = %+v, want %+v", back, f)
	}
}
