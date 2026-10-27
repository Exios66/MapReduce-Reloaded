package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

func TestParseDefaults(t *testing.T) {
	o, err := parse(nil, io.Discard)
	want := options{addr: ":7070", httpAddr: ":8080", logFormat: "text", backups: true, keepJobs: 100, maxTaskFailures: 8}
	if err != nil || o != want {
		t.Fatalf("parse() = %+v, %v; want %+v", o, err, want)
	}
	cfg := config(o)
	if cfg.HeartbeatInterval != 3*time.Second || cfg.WorkerTimeout != 10*time.Second || !cfg.Backups || cfg.KeepJobs != 100 || cfg.MaxTaskFailures != 8 {
		t.Fatalf("config %+v", cfg)
	}
}

func TestParseFlags(t *testing.T) {
	o, err := parse([]string{"-addr", ":1", "-http", "", "-data-dir", "d", "-backups=false", "-keep-jobs", "0", "-max-task-failures", "0", "-log-format", "json"}, io.Discard)
	want := options{addr: ":1", dataDir: "d", logFormat: "json", keepJobs: 0}
	if err != nil || o != want {
		t.Fatalf("parse = %+v, %v; want %+v", o, err, want)
	}
	if cfg := config(o); cfg.Backups || cfg.KeepJobs != 0 {
		t.Fatalf("-backups=false -keep-jobs 0 gave %+v", cfg)
	}
}

// Bad flags exit with status 2 and say why; a failure to run exits with 1.
func TestStartErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"-nope"}, 2, "flag provided but not defined: -nope"},
		{[]string{"extra"}, 2, `unexpected arguments ["extra"]`},
		{[]string{"-keep-jobs", "-1"}, 2, "-keep-jobs is -1"},
		{[]string{"-max-task-failures", "2"}, 2, "-max-task-failures is 2"},
		{[]string{"-log-format", "xml"}, 1, `log format "xml"`},
		{[]string{"-addr", "nowhere:-1", "-http", ""}, 1, "coordinator: listen tcp"},
		{[]string{"-h"}, 0, "Usage of coordinator"},
	} {
		var stderr bytes.Buffer
		if code := start(t.Context(), tc.args, &stderr); code != tc.code || !strings.Contains(stderr.String(), tc.msg) {
			t.Errorf("start(%q) = %d, %q; want %d and %q", tc.args, code, stderr.String(), tc.code, tc.msg)
		}
	}
}

// serve runs the coordinator with o until the test ends, and returns its
// RPC and status addresses and a function that stops it and returns run's
// error.
func serve(t *testing.T, o options) (rpc, status net.Addr, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready, done := make(chan [2]net.Addr, 1), make(chan error, 1)
	go func() { done <- run(ctx, o, func(r, s net.Addr) { ready <- [2]net.Addr{r, s} }) }()
	select {
	case a := <-ready:
		var once bool
		var err error
		stop = func() error {
			if !once {
				once = true
				cancel()
				err = <-done
			}
			return err
		}
		t.Cleanup(func() { _ = stop() })
		return a[0], a[1], stop
	case err := <-done:
		cancel()
		t.Fatal(err)
	}
	return nil, nil, nil
}

func client(t *testing.T, addr net.Addr) pb.CoordinatorServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(addr.String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewCoordinatorServiceClient(conn)
}

// With -data-dir the coordinator logs its job, and a new process on the
// same directory resumes it. With -http it serves its status page and
// metrics. Cancelling the context stops it without an error.
func TestRunRecoversAndServesStatus(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(in, []byte("a b a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := options{addr: "127.0.0.1:0", httpAddr: "127.0.0.1:0", dataDir: filepath.Join(dir, "wal"), backups: true}

	rpc, status, stop := serve(t, o)
	sub, err := client(t, rpc).SubmitJob(t.Context(), &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: filepath.Join(dir, "out"), NumReduceTasks: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/": sub.GetJobId(), "/metrics": `mr_job_tasks{job="job-1",kind="map",state="total"} 1`} {
		resp, err := http.Get("http://" + status.String() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("GET %s: %d, want 200 with %q:\n%.500s", path, resp.StatusCode, want, body)
		}
	}
	if err := stop(); err != nil {
		t.Fatalf("run after cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.dataDir, "coordinator.log")); err != nil {
		t.Fatal(err)
	}

	o.httpAddr = ""
	rpc, status, _ = serve(t, o)
	if status != nil {
		t.Fatalf("status page at %v without -http", status)
	}
	st, err := client(t, rpc).GetJobStatus(t.Context(), &pb.GetJobStatusRequest{JobId: sub.GetJobId()})
	if err != nil || st.GetState() != pb.JobState_JOB_STATE_MAPPING {
		t.Fatalf("after a restart: %v, %v; want the job, still mapping", st, err)
	}
}

// Without -data-dir there is no log, and a restart forgets the job.
func TestRunWithoutLog(t *testing.T) {
	rpc, _, _ := serve(t, options{addr: "127.0.0.1:0"})
	if _, err := client(t, rpc).GetJobStatus(t.Context(), &pb.GetJobStatusRequest{JobId: "job-1"}); err == nil {
		t.Fatal("found a job on a new coordinator")
	}
}
