package showcase_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// metricsFor is a worker's /metrics for a worker on host.
func metricsFor(host string) string {
	return "mr_worker_info{host=\"" + host + "\"} 1\nmr_worker_map_input_bytes_total 1000\n"
}

// serve starts a server that answers every request with body and a 200.
func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// server answers every request with its body, which a test may change.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	body string
}

func newServer(t *testing.T, body string) *server {
	t.Helper()
	s := &server{body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		b := s.body
		s.mu.Unlock()
		_, _ = io.WriteString(w, b)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) set(body string) {
	s.mu.Lock()
	s.body = body
	s.mu.Unlock()
}

func (s *server) addr() string { return addrOf(s.Server) }

// addrOf is a server's host:port, as Discover returns worker addresses.
func addrOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

// fixed is a Discover that always returns addrs.
func fixed(addrs ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return addrs, nil }
}

// fakeDNS is a Discover whose answer a test changes between rounds. The
// lookups run in the sampler's background goroutine, so a test must wait
// for one to be stored before the next round reads it: see setAndWait.
type fakeDNS struct {
	mu    sync.Mutex
	addrs []string
	calls int
}

func (d *fakeDNS) discover(context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return slices.Clone(d.addrs), nil
}

// setAndWait changes the answer, then returns once two lookups have started
// after the change. The first of them has read the new answer, and the
// sampler stores its result before it starts the second, so any round that
// starts after setAndWait sees the new answer.
func (d *fakeDNS) setAndWait(addrs []string) {
	d.mu.Lock()
	d.addrs = addrs
	want := d.calls + 2
	d.mu.Unlock()
	for {
		d.mu.Lock()
		n := d.calls
		d.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// sample runs Sample until stop reports true, and returns the frames it
// emitted. stop is called after each frame, in the sampler's goroutine, so
// it may change what Discover and Now return. A test that never stops
// returns after 10 s with the frames it has.
func sample(t *testing.T, c showcase.Config, stop func(frames []showcase.Frame) bool) []showcase.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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

// haveWorkers stops a sample once its latest frame lists n workers.
// Discovery runs in the background, so the first frames may list none.
func haveWorkers(n int) func([]showcase.Frame) bool {
	return func(fs []showcase.Frame) bool { return len(fs[len(fs)-1].Workers) == n }
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

// entriesAt returns every sample of addr in f: a recycled address has two.
func entriesAt(f showcase.Frame, addr string) []showcase.WorkerSample {
	var ws []showcase.WorkerSample
	for _, w := range f.Workers {
		if w.Address == addr {
			ws = append(ws, w)
		}
	}
	return ws
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
		Discover:  fixed(addrOf(answering), addrOf(gone)),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
	}
	frames := sample(t, c, haveWorkers(2))
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	f := frames[len(frames)-1]
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
		Discover:  fixed(addrOf(silent)),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
	}
	frames := sample(t, c, haveWorkers(1))
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	if w, ok := sampleOf(frames[len(frames)-1], addrOf(silent)); !ok || w.OK {
		t.Errorf("worker with no counters = %+v, want OK false", w)
	}
}

// A worker gone from DNS stays in frames, unreachable, for 60 s after it was
// last seen in DNS. A worker that answers while gone resets that clock.
func TestSampleKeepsWorkersDiscoveryLost(t *testing.T) {
	start := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now := start
	gone := newServer(t, workerMetrics)
	alive := newServer(t, workerMetrics)
	dns := &fakeDNS{addrs: []string{gone.addr(), alive.addr()}}
	c := showcase.Config{
		StatusURL: serve(t, statusJSON).URL + "/status.json",
		Discover:  dns.discover,
		Interval:  time.Millisecond,
		Timeout:   time.Second,
		Now:       func() time.Time { return now },
	}
	k := -1 // the index of the first frame that lists both workers
	frames := sample(t, c, func(fs []showcase.Frame) bool {
		if k < 0 {
			if len(fs[len(fs)-1].Workers) == 2 {
				k = len(fs) - 1
				// gone is removed from DNS and stops answering; alive is
				// removed from DNS but keeps answering, so it must stay.
				gone.Close()
				dns.setAndWait([]string{alive.addr()})
				now = start.Add(10 * time.Second)
			}
			return false
		}
		switch len(fs) - k {
		case 2:
			now = start.Add(59 * time.Second)
		case 3:
			now = start.Add(60 * time.Second)
		case 4:
			return true
		}
		return false
	})
	if k < 0 || len(frames) < k+4 {
		t.Fatalf("got %d frames, with both workers from frame %d; want 4 after that", len(frames), k)
	}
	f := frames[k:]
	if w, ok := sampleOf(f[0], gone.addr()); !ok || !w.OK {
		t.Errorf("frame 1: worker before it left DNS = %+v, %v", w, ok)
	}
	for i := range f {
		if w, ok := sampleOf(f[i], alive.addr()); !ok || !w.OK {
			t.Errorf("frame %d: answering worker = %+v, %v; want OK", i+1, w, ok)
		}
	}
	for i := 1; i < 3; i++ {
		if w, ok := sampleOf(f[i], gone.addr()); !ok || w.OK {
			t.Errorf("frame %d: worker gone from DNS = %+v, %v; want present with OK false", i+1, w, ok)
		}
	}
	if w, ok := sampleOf(f[3], gone.addr()); ok {
		t.Errorf("frame 4: worker kept after 60 s: %+v", w)
	}
}

// A source slower than Interval delays the next round. Frames are not
// queued: after the source is fast again, every frame is at least Interval
// after the one before it.
func TestSampleSkipsRoundsWhenSlow(t *testing.T) {
	const (
		slow     = 60 * time.Millisecond
		interval = 10 * time.Millisecond
	)
	var isSlow atomic.Bool
	isSlow.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSlow.Load() {
			select {
			case <-time.After(slow):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, statusJSON)
	}))
	t.Cleanup(srv.Close)
	c := showcase.Config{
		StatusURL: srv.URL + "/status.json",
		Interval:  interval,
		Timeout:   time.Second,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool {
		switch len(fs) {
		case 3:
			isSlow.Store(false)
		case 7:
			return true
		}
		return false
	})
	if len(frames) != 7 {
		t.Fatalf("got %d frames, want 7", len(frames))
	}
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
		switch {
		case i >= 1 && i <= 3 && f.SampleInterval < slow:
			t.Errorf("frame %d SampleInterval = %v, want at least %v for a slow round", i, f.SampleInterval, slow)
		case i >= 4 && f.SampleInterval < interval:
			t.Errorf("frame %d SampleInterval = %v, want at least %v: rounds were queued", i, f.SampleInterval, interval)
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
		Discover:  fixed(addrOf(wk)),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
	}
	frames := sample(t, c, haveWorkers(1))
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	f := frames[len(frames)-1]
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

// A lookup that never returns must not stall sampling. The Discover here
// ignores its context, as a stuck resolver can.
func TestSampleKeepsSamplingWhileDiscoveryBlocks(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c := showcase.Config{
		StatusURL: serve(t, statusJSON).URL + "/status.json",
		Discover: func(context.Context) ([]string, error) {
			<-release
			return nil, nil
		},
		Interval: time.Millisecond,
		Timeout:  time.Second,
	}
	frames := sample(t, c, func(fs []showcase.Frame) bool { return len(fs) == 3 })
	if len(frames) != 3 {
		t.Fatalf("got %d frames while discovery was blocked, want 3", len(frames))
	}
	for i, f := range frames {
		if !f.StatusOK {
			t.Errorf("frame %d: status not answered while discovery was blocked", i)
		}
	}
}

// Cancelling ctx while a source hangs ends Sample at once, and no frame is
// emitted for the cut-short round.
func TestSampleReturnsPromptlyWhenCancelledDuringAHang(t *testing.T) {
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	var emitted atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- showcase.Sample(ctx, showcase.Config{
			StatusURL: hang.URL + "/status.json",
			Interval:  time.Millisecond,
			Timeout:   time.Minute,
		}, func(showcase.Frame) { emitted.Add(1) })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Sample did not return after its context was cancelled")
	}
	if n := emitted.Load(); n != 0 {
		t.Errorf("%d frames emitted for a round a hang cut short", n)
	}
}

// A worker that stops answering keeps its host, so the page can say which
// worker is down.
func TestSampleKeepsTheHostOfAnUnansweredWorker(t *testing.T) {
	wk := newServer(t, metricsFor("h1"))
	c := showcase.Config{
		StatusURL: serve(t, statusJSON).URL + "/status.json",
		Discover:  fixed(wk.addr()),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
	}
	closed := false
	frames := sample(t, c, func(fs []showcase.Frame) bool {
		w, ok := sampleOf(fs[len(fs)-1], wk.addr())
		if !closed {
			if ok && w.OK {
				wk.Close()
				closed = true
			}
			return false
		}
		return ok && !w.OK
	})
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	if w, ok := sampleOf(frames[len(frames)-1], wk.addr()); !ok || w.OK || w.Host != "h1" {
		t.Errorf("unanswered worker = %+v, %v; want OK false with host h1", w, ok)
	}
}

// When an address that answered as one worker answers as another, the old
// worker stays listed, unreachable, for 60 s after it last answered; the new
// one is live at the same address.
func TestSampleRetainsAWorkerWhoseAddressWasRecycled(t *testing.T) {
	start := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now := start
	wk := newServer(t, metricsFor("old"))
	c := showcase.Config{
		StatusURL: serve(t, statusJSON).URL + "/status.json",
		Discover:  fixed(wk.addr()),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
		Now:       func() time.Time { return now },
	}
	k := -1 // the index of the first frame, where the old worker answers
	frames := sample(t, c, func(fs []showcase.Frame) bool {
		if k < 0 {
			if w, ok := sampleOf(fs[len(fs)-1], wk.addr()); ok && w.OK && w.Host == "old" {
				k = len(fs) - 1
				wk.set(metricsFor("new"))
				now = start.Add(10 * time.Second)
			}
			return false
		}
		switch len(fs) - k {
		case 2:
			now = start.Add(59 * time.Second)
		case 3:
			now = start.Add(60 * time.Second)
		case 4:
			return true
		}
		return false
	})
	if k < 0 || len(frames) < k+4 {
		t.Fatalf("got %d frames, with the old worker from frame %d; want 4 after that", len(frames), k)
	}
	f := frames[k:]
	if ws := entriesAt(f[1], wk.addr()); len(ws) != 2 {
		t.Fatalf("frame 2 has %d samples at the address, want the new worker and the old one: %+v", len(ws), ws)
	}
	for i := 1; i < 4; i++ {
		live, ok := sampleOf(f[i], wk.addr())
		if !ok || !live.OK || live.Host != "new" {
			t.Errorf("frame %d: live worker = %+v, %v; want OK with host new", i+1, live, ok)
		}
	}
	for _, i := range []int{1, 2} {
		old := false
		for _, w := range entriesAt(f[i], wk.addr()) {
			if w.Host == "old" {
				old = true
				if w.OK {
					t.Errorf("frame %d: old worker OK, want unreachable: %+v", i+1, w)
				}
			}
		}
		if !old {
			t.Errorf("frame %d: old worker not listed for its 60 s", i+1)
		}
	}
	for _, w := range entriesAt(f[3], wk.addr()) {
		if w.Host == "old" {
			t.Errorf("frame 4: old worker kept after 60 s: %+v", w)
		}
	}
}

// An answer over the size cap is a gap, for the coordinator and for workers.
func TestSampleTreatsAnOversizedAnswerAsAGap(t *testing.T) {
	big := strings.Repeat("x", 256<<10)
	status := serve(t, `{"pad":"`+big+`"}`)
	wk := serve(t, "# "+big+"\n"+metricsFor("w"))
	c := showcase.Config{
		StatusURL: status.URL + "/status.json",
		Discover:  fixed(addrOf(wk)),
		Interval:  time.Millisecond,
		Timeout:   time.Second,
	}
	frames := sample(t, c, haveWorkers(1))
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	f := frames[len(frames)-1]
	if f.StatusOK {
		t.Error("an oversized status was answered, want a gap")
	}
	if w, _ := sampleOf(f, addrOf(wk)); w.OK {
		t.Errorf("an oversized worker answer was accepted: %+v", w)
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
