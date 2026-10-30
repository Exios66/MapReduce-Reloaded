package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

// frame is a frame with the given Seq, as the sampler makes one.
func frame(seq uint64) showcase.Frame {
	return showcase.Frame{
		Seq:      seq,
		At:       time.Date(2026, 10, 30, 12, 0, int(seq), 0, time.UTC),
		Status:   json.RawMessage(`{"jobs":[]}`),
		StatusOK: true,
		Workers:  []showcase.WorkerSample{},
	}
}

// writeRecording writes frames to path, through the recorder, as a run does.
func writeRecording(t *testing.T, path string, frames ...showcase.Frame) {
	t.Helper()
	rec, err := showcase.NewRecorder(path, showcase.DefaultRecordingCap)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		rec.Write(f)
	}
}

// statusServer serves a coordinator's /status.json.
func statusServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[],"workers":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/status.json"
}

// startRun runs c in the background, waits until it listens, and returns the
// base URL of its page and the channel run's error comes on when it returns.
func startRun(t *testing.T, ctx context.Context, c config) (string, <-chan error) {
	t.Helper()
	listening := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, c, func(a net.Addr) { listening <- a }) }()
	select {
	case a := <-listening:
		return "http://" + a.String(), done
	case err := <-done:
		t.Fatalf("run returned before it listened: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("run did not listen")
	}
	return "", done
}

// replyOf reads GET url and decodes the frames reply.
func replyOf(t *testing.T, url string) framesReply {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, body)
	}
	var reply framesReply
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return reply
}

// framesReply is the body of GET /frames, as far as these tests read it.
type framesReply struct {
	Frames []showcase.Frame `json:"frames"`
	Resync bool             `json:"resync"`
	Info   struct {
		Mode     string `json:"mode"`
		RunID    string `json:"run_id"`
		Recorder struct {
			Path    string `json:"path"`
			Bytes   int64  `json:"bytes"`
			Stopped bool   `json:"stopped"`
			Reason  string `json:"reason"`
		} `json:"recorder"`
		Skipped int `json:"skipped"`
	} `json:"info"`
}

// seqsOf lists the Seqs of frames.
func seqsOf(frames []showcase.Frame) []uint64 {
	out := make([]uint64, len(frames))
	for i, f := range frames {
		out[i] = f.Seq
	}
	return out
}

func TestParseDefaults(t *testing.T) {
	c, err := parse(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := config{
		http:           ":8080",
		status:         "http://coordinator:8080/status.json",
		workers:        "worker",
		workerPort:     8081,
		interval:       250 * time.Millisecond,
		maxRecordBytes: showcase.DefaultRecordingCap,
	}
	if c != want {
		t.Errorf("parse() = %+v, want %+v", c, want)
	}
}

func TestParseRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-bogus"},
		{"-interval", "0s"},
		{"-worker-port", "0"},
		{"-worker-port", "70000"},
		{"-max-record-bytes", "0"},
		{"-status", ""},
		{"stray"},
		// -replay opens only the named file, so it cannot also record.
		{"-replay", "x.jsonl", "-out", "dir"},
	} {
		if _, err := parse(args, io.Discard); err == nil {
			t.Errorf("parse(%q) succeeded, want an error", args)
		}
	}
}

func TestStartExitStatus(t *testing.T) {
	ctx := context.Background()
	if code := start(ctx, []string{"-bogus"}, io.Discard); code != 2 {
		t.Errorf("bad flag: exit %d, want 2", code)
	}
	if code := start(ctx, []string{"-h"}, io.Discard); code != 0 {
		t.Errorf("-h: exit %d, want 0", code)
	}
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	var stderr bytes.Buffer
	if code := start(ctx, []string{"-replay", missing}, &stderr); code != 1 {
		t.Errorf("missing recording: exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "showcase:") {
		t.Errorf("stderr = %q, want the error printed", stderr.String())
	}
}

func TestRecordingIsNamedByItsStartTimeInUTC(t *testing.T) {
	dir := t.TempDir()
	// 12:00:05 at UTC+5 is 07:00:05 UTC.
	start := time.Date(2026, 10, 30, 12, 0, 5, 0, time.FixedZone("UTC+5", 5*3600))
	rec, err := openRecording(dir, start, showcase.DefaultRecordingCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(rec.State().Path); got != "20261030T070005Z.jsonl" {
		t.Errorf("recording is %q, want 20261030T070005Z.jsonl", got)
	}
}

func TestRecordingFailsOverToASuffixInsteadOfOverwriting(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 30, 7, 0, 5, 0, time.UTC)
	first := filepath.Join(dir, "20261030T070005Z.jsonl")
	if err := os.WriteFile(first, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := openRecording(dir, now, showcase.DefaultRecordingCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(rec.State().Path); got != "20261030T070005Z-1.jsonl" {
		t.Errorf("recording is %q, want the -1 suffix", got)
	}
	if b, err := os.ReadFile(first); err != nil || string(b) != "keep me\n" {
		t.Errorf("existing recording changed: %q, %v", b, err)
	}
}

func TestReplayServesARecordingWithNoCluster(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261030T120000Z.jsonl")
	writeRecording(t, path, frame(1), frame(2), frame(3))
	// A blank line is skipped and a cut-off line is counted, as a crash leaves them.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{\"seq\":4,\"at\":\"2026-10-30T12:0"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The status URL is unreachable: a replay must not sample it.
	base, done := startRun(t, ctx, config{http: "127.0.0.1:0", status: "http://127.0.0.1:1/status.json", replay: path})

	reply := replyOf(t, base+"/frames?after=0")
	if got := seqsOf(reply.Frames); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("replayed frames = %v, want [1 2 3]", got)
	}
	if reply.Info.Mode != showcase.ModeReplay || reply.Info.RunID != "20261030T120000Z" {
		t.Errorf("info = %+v, want replay of run 20261030T120000Z", reply.Info)
	}
	if reply.Info.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", reply.Info.Skipped)
	}
	if rest := replyOf(t, base+"/frames?after=1"); len(rest.Frames) != 2 || rest.Frames[0].Seq != 2 {
		t.Errorf("frames after 1 = %v, want [2 3]", seqsOf(rest.Frames))
	}
	time.Sleep(100 * time.Millisecond) // a sampler at 10 ms would have added frames by now
	if n := len(replyOf(t, base+"/frames?after=0&limit=2000").Frames); n != 3 {
		t.Errorf("replay holds %d frames after a wait, want 3", n)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run returned %v after cancel, want nil", err)
	}
}

func TestLiveServesFramesFromTheSampler(t *testing.T) {
	out := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base, done := startRun(t, ctx, config{
		http:           "127.0.0.1:0",
		status:         statusServer(t),
		interval:       10 * time.Millisecond,
		out:            out,
		maxRecordBytes: showcase.DefaultRecordingCap,
	})

	var reply framesReply
	deadline := time.Now().Add(5 * time.Second)
	for {
		reply = replyOf(t, base+"/frames?after=0")
		if len(reply.Frames) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(reply.Frames) < 2 {
		t.Fatalf("live view has %d frames, want at least 2", len(reply.Frames))
	}
	if reply.Info.Mode != showcase.ModeLive || reply.Info.RunID == "" {
		t.Errorf("info = %+v, want a live run with an id", reply.Info)
	}
	if reply.Info.Recorder.Stopped || !strings.HasSuffix(reply.Info.Recorder.Path, ".jsonl") {
		t.Errorf("recorder = %+v, want a running recording", reply.Info.Recorder)
	}
	if _, err := os.Stat(reply.Info.Recorder.Path); err != nil {
		t.Errorf("recording file: %v", err)
	}
	if frames, _, err := showcase.ReadRecording(reply.Info.Recorder.Path); err != nil || len(frames) < 2 {
		t.Errorf("recording holds %d frames, err %v; want at least 2", len(frames), err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run returned %v after cancel, want nil", err)
	}
}

func TestRecordingThatCannotStartDoesNotStopTheLiveView(t *testing.T) {
	// The recording directory is under a regular file, so it cannot be made.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base, done := startRun(t, ctx, config{
		http:           "127.0.0.1:0",
		status:         statusServer(t),
		interval:       10 * time.Millisecond,
		out:            filepath.Join(blocker, "sub"),
		maxRecordBytes: showcase.DefaultRecordingCap,
	})

	deadline := time.Now().Add(5 * time.Second)
	var reply framesReply
	for {
		reply = replyOf(t, base+"/frames?after=0")
		if len(reply.Frames) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(reply.Frames) == 0 {
		t.Fatal("the live view stopped with the recording")
	}
	rec := reply.Info.Recorder
	if !rec.Stopped || rec.Reason == "" {
		t.Errorf("recorder = %+v, want stopped with a reason", rec)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("run returned %v after cancel, want nil", err)
	}
}

func TestReplayKeepsEveryFrameOfALargeRecording(t *testing.T) {
	// Fifty frames that each cost 1 MiB are more than the live ring's byte
	// budget. A replay must still hold them all, since the recording's own cap
	// is what bounds a replay. The frames share one status slice, so the test
	// stays cheap; the ring counts its length, not its memory.
	status := json.RawMessage(make([]byte, 1<<20))
	frames := make([]showcase.Frame, 0, 50)
	for seq := uint64(1); seq <= 50; seq++ {
		f := frame(seq)
		f.Status = status
		frames = append(frames, f)
	}
	// The premise: a live ring of this many frames would evict the first.
	live := showcase.NewRing(len(frames))
	for _, f := range frames {
		live.Add(f)
	}
	if got, _ := live.After(0, 1); len(got) != 1 || got[0].Seq == 1 {
		t.Fatalf("live ring keeps frame 1; the test needs a budget that evicts it")
	}

	src := newReplay(frames, 0, "run")
	if got, _ := src.After(0, 1); len(got) != 1 || got[0].Seq != 1 {
		t.Errorf("replay starts at %v, want frame 1", seqsOf(got))
	}
	if got, _ := src.After(0, 2000); len(got) != 50 {
		t.Errorf("replay holds %d frames, want all 50", len(got))
	}
}

func TestEachStartHasANewRunID(t *testing.T) {
	c := config{status: "http://127.0.0.1:1/status.json", interval: time.Second, maxRecordBytes: showcase.DefaultRecordingCap}
	a, err := newSource(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := newSource(c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.Info().RunID == "" || a.Info().RunID == b.Info().RunID {
		t.Errorf("run ids %q and %q; want two different non-empty ids", a.Info().RunID, b.Info().RunID)
	}
}

func TestRunReportsAnAddressItCannotListenOn(t *testing.T) {
	err := run(context.Background(), config{http: "not a host:port", status: "http://127.0.0.1:1/x", interval: time.Second, maxRecordBytes: 1}, nil)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("run on a bad address = %v, want a listen error", err)
	}
}
