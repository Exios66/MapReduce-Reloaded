// Command showcase samples a cluster's coordinator and workers, and serves one
// read-only page of what they report (ADR 0023). It never changes the cluster.
//
//	showcase [-http :8080] [-status http://coordinator:8080/status.json] [-workers worker] [-worker-port 8081] [-interval 250ms] [-out dir] [-max-record-bytes n]
//	showcase -replay file.jsonl [-http 127.0.0.1:8080]
//
// It reads each worker's /metrics and the coordinator's /status.json every
// -interval. With -out it records the run to a new file in dir, named by its
// UTC start time, up to -max-record-bytes; a recording that cannot start, or
// reaches its cap, is shown as stopped and the live view goes on. With
// -replay it serves one recording with no cluster, and opens only that file.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/memlimit"
	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

type config struct {
	http, status, workers, out, replay string
	workerPort                         int
	interval                           time.Duration
	maxRecordBytes                     int64
}

// maxSuffix bounds the -1, -2, ... suffixes tried for a recording whose
// name is taken.
const maxSuffix = 1000

// main runs the showcase until it is interrupted or terminated. It exits with
// status 2 for bad flags and 1 for any other error, printed to stderr.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := start(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

// start parses args and runs the showcase until ctx is done, and returns the
// process's exit status, printing errors to stderr.
func start(ctx context.Context, args []string, stderr io.Writer) int {
	c, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "showcase:", err)
		return 2
	}
	limitHeap(memlimit.FromCgroup())
	if err = run(ctx, c, nil); err != nil {
		_, _ = fmt.Fprintln(stderr, "showcase:", err)
		return 1
	}
	return 0
}

// parse parses the showcase's flags, writing usage to stderr on error.
func parse(args []string, stderr io.Writer) (config, error) {
	var c config
	flags := flag.NewFlagSet("showcase", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.http, "http", "", "address to serve the page and /frames on (default :8080; 127.0.0.1:8080 with -replay)")
	flags.StringVar(&c.status, "status", "http://coordinator:8080/status.json", "the coordinator's status URL")
	flags.StringVar(&c.workers, "workers", "worker", `DNS name that finds the workers to sample; "" to sample none`)
	flags.IntVar(&c.workerPort, "worker-port", 8081, "port the workers serve /metrics on")
	flags.DurationVar(&c.interval, "interval", 250*time.Millisecond, "time between the starts of sampling rounds")
	flags.StringVar(&c.out, "out", "", "directory to record each run in, as <UTC start time>.jsonl; empty to record nothing")
	flags.Int64Var(&c.maxRecordBytes, "max-record-bytes", showcase.DefaultRecordingCap, "stop a recording at this many bytes")
	flags.StringVar(&c.replay, "replay", "", "serve this recording with no cluster; it is the only file opened")
	err := flags.Parse(args)
	if c.http == "" {
		c.http = ":8080"
		if c.replay != "" {
			c.http = "127.0.0.1:8080" // a replay runs on the host, not in a container
		}
	}
	switch {
	case err != nil:
	case flags.NArg() > 0:
		err = fmt.Errorf("unexpected arguments %q", flags.Args())
	case c.replay != "" && c.out != "":
		err = errors.New("-replay and -out cannot be used together")
	case c.replay == "" && c.status == "":
		err = errors.New("-status is empty; a live showcase needs the coordinator's status URL")
	case c.interval <= 0:
		err = fmt.Errorf("-interval %v must be positive", c.interval)
	case c.workerPort < 1 || c.workerPort > 65535:
		err = fmt.Errorf("-worker-port %d is not a port number", c.workerPort)
	case c.maxRecordBytes < 1:
		err = fmt.Errorf("-max-record-bytes %d must be positive", c.maxRecordBytes)
	}
	return c, err
}

// limitHeap keeps the Go heap under the container's memory limit, if there
// is one, as the worker does: the runtime collects harder instead of the
// kernel killing the process (#47). A quarter of the limit is left for the
// stacks and buffers outside the heap.
func limitHeap(limit int64) {
	if limit <= 0 {
		return
	}
	heap := limit - limit/4
	debug.SetMemoryLimit(heap)
	slog.Info("memory limit from the cgroup", "bytes", limit, "heap_limit_bytes", heap)
}

// run serves the page and /frames on c.http, and samples the cluster into
// them until ctx is done, or serves the replay of c.replay until then. If
// listening is not nil, run calls it with the listener's address before it
// samples. A done ctx ends run without error.
func run(ctx context.Context, c config, listening func(net.Addr)) error {
	// Listen before the source is made, so that a port conflict fails before
	// a recording is created.
	lis, err := net.Listen("tcp", c.http)
	if err != nil {
		return err
	}
	src, err := newSource(c, time.Now())
	if err != nil {
		_ = lis.Close()
		return err
	}
	srv := &http.Server{Handler: showcase.Handler(src), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("showcase: serve", "err", err)
		}
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if listening != nil {
		listening(lis.Addr())
	}
	if c.replay != "" {
		<-ctx.Done()
		return nil
	}
	return showcase.Sample(ctx, sampleConfig(c), src.add)
}

// sampleConfig is the sampler's configuration for a live run.
func sampleConfig(c config) showcase.Config {
	sc := showcase.Config{StatusURL: c.status, Interval: c.interval}
	if c.workers != "" {
		sc.Discover = showcase.DNSDiscover(c.workers, c.workerPort)
	}
	return sc
}

// recorder is where a live source writes its frames: a *showcase.Recorder,
// or a stand-in in tests.
type recorder interface {
	Write(showcase.Frame)
	State() showcase.RecorderState
}

// source is what the page is served from: the frames kept in memory, and
// the recording, if there is one. A live source adds the frames it samples;
// a replay source holds every frame of its recording.
type source struct {
	mode    string
	runID   string
	ring    *showcase.Ring
	skipped int // lines of a replayed recording that were skipped

	// mu guards rec and state. The sampler's goroutine changes them when a
	// recording stops, and the HTTP handlers read them.
	mu    sync.Mutex
	rec   recorder               // nil when not recording, or when recording could not start
	state showcase.RecorderState // the recorder's state when rec is nil
}

// newSource returns the source for c: the replay of c.replay, or a live
// source that records to c.out if it is set. A recording that cannot start is
// logged and shown as stopped; it does not fail the source.
func newSource(c config, now time.Time) (*source, error) {
	if c.replay != "" {
		return replaySource(c.replay)
	}
	s := &source{
		mode:  showcase.ModeLive,
		runID: rand.Text(), // new each start, so the page can tell runs apart
		ring:  showcase.NewRing(showcase.DefaultRingSize),
	}
	if c.out != "" {
		rec, err := openRecording(c.out, now, c.maxRecordBytes)
		if err != nil {
			slog.Warn("showcase: not recording; the live view goes on", "err", err)
			s.state = showcase.RecorderState{Stopped: true, Reason: err.Error()}
		} else {
			s.rec = rec
		}
	}
	return s, nil
}

// replaySource reads the recording at path. Its run id is the recording's
// file name.
func replaySource(path string) (*source, error) {
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		return nil, err
	}
	return newReplay(frames, skipped, strings.TrimSuffix(filepath.Base(path), ".jsonl")), nil
}

// newReplay holds all of a recording's frames, so that a replay serves every
// one of them. The live ring's byte budget does not apply: the recording's
// own cap has already bounded what a replay holds.
func newReplay(frames []showcase.Frame, skipped int, runID string) *source {
	ring := showcase.NewRing(len(frames))
	ring.LimitBytes(math.MaxInt64)
	for _, f := range frames {
		ring.Add(f)
	}
	return &source{mode: showcase.ModeReplay, runID: runID, ring: ring, skipped: skipped}
}

// openRecording creates the recording of a run that started at now, in dir.
// Its name is the UTC start time, 20060102T150405Z.jsonl. If that name is
// taken, it tries -1, -2 and so on, so no recording is overwritten.
func openRecording(dir string, now time.Time, maxBytes int64) (*showcase.Recorder, error) {
	base := now.UTC().Format("20060102T150405Z")
	for i := 0; ; i++ {
		name := base + ".jsonl"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.jsonl", base, i)
		}
		rec, err := showcase.NewRecorder(filepath.Join(dir, name), maxBytes)
		if errors.Is(err, fs.ErrExist) && i < maxSuffix {
			continue
		}
		return rec, err
	}
}

// add keeps a sampled frame, and records it if the recording is on.
func (s *source) add(f showcase.Frame) {
	s.ring.Add(f)
	s.mu.Lock()
	rec := s.rec
	s.mu.Unlock()
	if rec != nil {
		s.record(rec, f)
	}
}

// record writes f to rec. A panic in the recording stops the recording and is
// logged: the live view, the sampler and the other handlers go on.
func (s *source) record(rec recorder, f showcase.Frame) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		slog.Error("showcase: recording stopped after a panic; the live view goes on", "panic", p)
		s.mu.Lock()
		defer s.mu.Unlock()
		state := rec.State()
		state.Stopped, state.Reason = true, fmt.Sprintf("panic: %v", p)
		s.rec, s.state = nil, state
	}()
	rec.Write(f)
}

// After returns the frames after seq, as the ring does.
func (s *source) After(seq uint64, limit int) ([]showcase.Frame, bool) {
	return s.ring.After(seq, limit)
}

// Info says what the source is doing and how its recording stands.
func (s *source) Info() showcase.Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if s.rec != nil {
		state = s.rec.State()
	}
	return showcase.Info{Mode: s.mode, RunID: s.runID, Recorder: state, Skipped: s.skipped}
}
