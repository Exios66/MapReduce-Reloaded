package showcase_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
	"github.com/exios66/mapreduce-reloaded/go/internal/statusweb"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

const (
	statusJSON    = `{"at":1,"jobs":[],"workers":[],"lost":[]}`
	workerMetrics = "mr_worker_info{host=\"w-a\"} 1\nmr_worker_map_input_bytes_total 1000\n"
)

// serve starts a server that answers every request with body and a 200.
func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// addrOf is a server's host:port, as Discover returns worker addresses.
func addrOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

// sample runs Sample until stop reports true, and returns the frames it
// emitted. stop is called after each frame, in the sampler's goroutine, so
// it may change what Discover and Now return.
func sample(t *testing.T, c showcase.Config, stop func(frames []showcase.Frame) bool) []showcase.Frame {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var frames []showcase.Frame
	err := showcase.Sample(ctx, c, func(f showcase.Frame) {
		frames = append(frames, f)
		if stop(frames) {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return frames
}

// sampleOf returns the sample of addr in f, and whether f has one.
func sampleOf(f showcase.Frame, addr string) (showcase.WorkerSample, bool) {
	for _, w := range f.Workers {
		if w.Address == addr {
			return w, true
		}
	}
	return showcase.WorkerSample{}, false
}

func TestSampleRecordsAStatusOutageAsAGap(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	frames := sample(t, showcase.Config{StatusURL: srv.URL + "/status.json", Interval: time.Millisecond}, func(fs []showcase.Frame) bool {
		return len(fs) == 3
	})
	for i, f := range frames {
		if f.Seq != uint64(i+1) {
			t.Errorf("frame %d has Seq %d, want %d", i, f.Seq, i+1)
		}
		if f.StatusOK || len(f.Status) != 0 {
			t.Errorf("frame %d: status ok %v with %d bytes, want a gap", i, f.StatusOK, len(f.Status))
		}
	}
}

func TestSampleTreatsGarbageStatusAsAGap(t *testing.T) {
	srv := serve(t, "not json {")
	frames := sample(t, showcase.Config{StatusURL: srv.URL + "/status.json", Interval: time.Millisecond}, func(fs []showcase.Frame) bool {
		return len(fs) == 1
	})
	if f := frames[0]; f.StatusOK || len(f.Status) != 0 {
		t.Errorf("status ok %v with %q, want a gap", f.StatusOK, f.Status)
	}
}

func TestSampleRecordsAnUnansweredWorker(t *testing.T) {
	status := serve(t, statusJSON)
	answering := serve(t, workerMetrics)
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	c := showcase.Config{
		StatusURL: status.URL + "/status.json",
		Discover:  func(context.Context) ([]string, error) { return []string{addrOf(answering), addrOf(gone)}, nil },
		Interval:  time.Millisecond,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool { return len(fs) == 1 })
	f := frames[0]
	if len(f.Workers) != 2 {
		t.Fatalf("frame has %d workers, want 2", len(f.Workers))
	}
	up, ok := sampleOf(f, addrOf(answering))
	if !ok || !up.OK || up.Host != "w-a" || up.Bytes["mr_worker_map_input_bytes_total"] != 1000 {
		t.Errorf("answering worker = %+v", up)
	}
	down, ok := sampleOf(f, addrOf(gone))
	if !ok || down.OK || down.Host != "" || down.Bytes != nil {
		t.Errorf("unanswered worker = %+v, want OK false with no host or counters", down)
	}
}

func TestSampleTreatsAMetricsAnswerWithNoCountersAsAGap(t *testing.T) {
	status := serve(t, statusJSON)
	silent := serve(t, "# nothing here\n")
	c := showcase.Config{
		StatusURL: status.URL + "/status.json",
		Discover:  func(context.Context) ([]string, error) { return []string{addrOf(silent)}, nil },
		Interval:  time.Millisecond,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool { return len(fs) == 1 })
	if w, ok := sampleOf(frames[0], addrOf(silent)); !ok || w.OK {
		t.Errorf("worker with no counters = %+v, want OK false", w)
	}
}

// A worker gone from DNS stays in frames, unreachable, for 60 s after it was
// last seen in DNS. A worker that answers while gone resets that clock.
func TestSampleKeepsWorkersDiscoveryLost(t *testing.T) {
	start := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now := start
	gone := serve(t, workerMetrics)
	alive := serve(t, workerMetrics)
	dns := []string{addrOf(gone), addrOf(alive)}
	c := showcase.Config{
		StatusURL: serve(t, statusJSON).URL + "/status.json",
		Discover:  func(context.Context) ([]string, error) { return dns, nil },
		Interval:  time.Millisecond,
		Now:       func() time.Time { return now },
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool {
		switch len(fs) {
		case 1:
			// gone is removed from DNS and stops answering; alive is removed
			// from DNS but keeps answering, so it must stay.
			gone.Close()
			dns = []string{addrOf(alive)}
			now = start.Add(10 * time.Second)
		case 2:
			now = start.Add(59 * time.Second)
		case 3:
			now = start.Add(60 * time.Second)
		case 4:
			return true
		}
		return false
	})
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want 4", len(frames))
	}
	for i, f := range frames {
		if w, ok := sampleOf(f, addrOf(alive)); !ok || !w.OK {
			t.Errorf("frame %d: answering worker = %+v, %v; want OK", i+1, w, ok)
		}
	}
	if w, ok := sampleOf(frames[0], addrOf(gone)); !ok || !w.OK {
		t.Errorf("frame 1: worker before it left DNS = %+v, %v", w, ok)
	}
	for i := 1; i < 3; i++ {
		if w, ok := sampleOf(frames[i], addrOf(gone)); !ok || w.OK {
			t.Errorf("frame %d: worker gone from DNS = %+v, %v; want present with OK false", i+1, w, ok)
		}
	}
	if w, ok := sampleOf(frames[3], addrOf(gone)); ok {
		t.Errorf("frame 4: worker kept after 60 s: %+v", w)
	}
}

// A source slower than Interval delays the next round. Frames are not
// queued, and SampleInterval shows the real gap.
func TestSampleSkipsRoundsWhenSlow(t *testing.T) {
	const slow = 60 * time.Millisecond
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(slow):
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, statusJSON)
	}))
	t.Cleanup(slowSrv.Close)
	c := showcase.Config{
		StatusURL: slowSrv.URL + "/status.json",
		Interval:  10 * time.Millisecond,
		Timeout:   time.Second,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool { return len(fs) == 4 })
	if frames[0].SampleInterval != 0 {
		t.Errorf("first frame SampleInterval = %v, want 0", frames[0].SampleInterval)
	}
	for i, f := range frames {
		if f.Seq != uint64(i+1) {
			t.Errorf("frame %d has Seq %d", i, f.Seq)
		}
		if !f.StatusOK {
			t.Errorf("frame %d: status not answered within Timeout", i)
		}
		if i > 0 && f.SampleInterval < slow {
			t.Errorf("frame %d SampleInterval = %v, want at least %v", i, f.SampleInterval, slow)
		}
	}
}

func TestSampleReadsARealCoordinatorAndWorker(t *testing.T) {
	coord := httptest.NewServer(statusweb.Handler(coordinator.NewServer(coordinator.Config{})))
	t.Cleanup(coord.Close)
	w := worker.New(nil, "", &pb.WorkerInfo{Host: "h-real"})
	wk := httptest.NewServer(w.MetricsHandler())
	t.Cleanup(wk.Close)
	c := showcase.Config{
		StatusURL: coord.URL + "/status.json",
		Discover:  func(context.Context) ([]string, error) { return []string{addrOf(wk)}, nil },
		Interval:  time.Millisecond,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool { return len(fs) == 1 })
	f := frames[0]
	if !f.StatusOK {
		t.Fatal("the real coordinator's status was not answered")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(f.Status, &doc); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	if _, ok := doc["jobs"]; !ok {
		t.Errorf("status has no jobs key: %s", f.Status)
	}
	got, ok := sampleOf(f, addrOf(wk))
	if !ok || !got.OK || got.Host != "h-real" {
		t.Fatalf("real worker = %+v, %v", got, ok)
	}
	for _, name := range []string{"mr_worker_map_input_bytes_total", "mr_worker_map_output_bytes_total", "mr_worker_shuffle_fetched_bytes_total", "mr_worker_reduce_output_bytes_total"} {
		if _, ok := got.Bytes[name]; !ok {
			t.Errorf("real worker lacks %s", name)
		}
	}
}

func TestDNSDiscoverFindsLocalhost(t *testing.T) {
	addrs, err := showcase.DNSDiscover("localhost", 9)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range addrs {
		if a == "127.0.0.1:9" || a == "[::1]:9" {
			found = true
		}
	}
	if !found {
		t.Errorf("DNSDiscover(localhost) = %v, want a loopback address on port 9", addrs)
	}
}
