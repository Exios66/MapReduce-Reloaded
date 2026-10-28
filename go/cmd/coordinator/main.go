// Command coordinator serves the coordinator ("master", §3.2) of a cluster.
//
//	coordinator [-addr :7070] [-http :8080] [-data-dir dir] [-keep-jobs n] [-log-format text|json]
//
// It runs until interrupted, then tells workers to exit. With -data-dir it
// logs every decision there, and a restarted coordinator resumes its job
// from that log (§3.3). With -http it serves a status page and Prometheus
// metrics (§4.8). It keeps the -keep-jobs most recent ended jobs for
// mr status and forgets older ones.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/logs"
	"github.com/exios66/mapreduce-reloaded/go/internal/rpcguard"
	"github.com/exios66/mapreduce-reloaded/go/internal/statusweb"
)

// options are the coordinator's flags.
type options struct {
	addr, httpAddr, dataDir, logFormat string
	backups                            bool
	keepJobs, maxTaskFailures          int
}

// main runs the coordinator until interrupted or terminated. It exits with
// status 2 for bad flags and 1 for any other error, printed to stderr.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := start(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

// start parses args, sets up logging and runs the coordinator until ctx is
// done, and returns the process's exit status, printing errors to stderr.
func start(ctx context.Context, args []string, stderr io.Writer) int {
	o, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "coordinator:", err)
		return 2
	}
	if err = logs.Setup(o.logFormat); err == nil {
		err = run(ctx, o, nil)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "coordinator:", err)
		return 1
	}
	return 0
}

// parse parses the coordinator's flags, writing usage to stderr on error.
func parse(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("coordinator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.addr, "addr", ":7070", "address to listen on")
	fs.StringVar(&o.httpAddr, "http", ":8080", "address of the status page and /metrics (empty: none)")
	fs.StringVar(&o.dataDir, "data-dir", "", "directory for the write-ahead log (default: none, so a restart loses the job)")
	fs.BoolVar(&o.backups, "backups", true, "run backup attempts of the last tasks of each phase")
	fs.IntVar(&o.keepJobs, "keep-jobs", 100, "ended jobs to keep for mr status; older ones are forgotten (0: all)")
	fs.IntVar(&o.maxTaskFailures, "max-task-failures", 8, "fail the job once one task has failed this many times (0: retry for ever)")
	fs.StringVar(&o.logFormat, "log-format", "text", "log format: text or json")
	err := fs.Parse(args)
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("unexpected arguments %q", fs.Args())
	}
	if err == nil && o.keepJobs < 0 {
		err = fmt.Errorf("-keep-jobs is %d; it must be 0 (all) or more", o.keepJobs)
	}
	if err == nil && o.maxTaskFailures != 0 && o.maxTaskFailures <= coordinator.SkipAfter {
		// Skipping a bad record takes SkipAfter crashes, then one more attempt.
		err = fmt.Errorf("-max-task-failures is %d; it must be 0 (no limit) or more than %d", o.maxTaskFailures, coordinator.SkipAfter)
	}
	return o, err
}

// config returns the coordinator's configuration for o.
func config(o options) coordinator.Config {
	return coordinator.Config{
		HeartbeatInterval: 3 * time.Second,
		WorkerTimeout:     10 * time.Second, // three missed heartbeats
		RetryAfter:        time.Second,
		Backups:           o.backups,
		KeepJobs:          o.keepJobs,
		MaxTaskFailures:   o.maxTaskFailures,
	}
}

// run serves coordinator RPCs at o.addr until ctx is done, then tells
// workers to exit. A nonempty o.dataDir enables log recovery and
// persistence; a nonempty o.httpAddr enables the status page and metrics.
// Without o.dataDir, worker and job IDs include a prefix derived from the
// current time.
// If listening is not nil, run calls it with the RPC and HTTP addresses
// (nil without -http) once both listen.
// It returns storage, recovery, listen, and RPC server errors, joined with
// any log close error, and an error if another coordinator holds
// o.dataDir. Status HTTP server errors do not stop RPCs.
func run(ctx context.Context, o options, listening func(rpc, status net.Addr)) (err error) {
	cfg := config(o)
	var srv *coordinator.Server
	if o.dataDir == "" {
		// Without a log, IDs start from 1 again: tell them from the last run's.
		cfg.IDPrefix = strconv.FormatInt(time.Now().UnixNano(), 36) + "-"
		srv = coordinator.NewServer(cfg)
	} else {
		if err := os.MkdirAll(o.dataDir, 0o755); err != nil {
			return err
		}
		var held *os.File
		if held, err = lock(o.dataDir); err != nil {
			return err
		}
		if held != nil {
			// Unlocked last: after the log is closed.
			defer func() { err = errors.Join(err, held.Close()) }()
		}
		if srv, _, err = coordinator.Recover(cfg, filepath.Join(o.dataDir, "coordinator.log")); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, srv.Close()) }()
	}
	lis, err := net.Listen("tcp", o.addr)
	if err != nil {
		return err
	}
	gs := grpc.NewServer(rpcguard.ServerOptions()...)
	pb.RegisterCoordinatorServiceServer(gs, srv)

	var status *http.Server
	var statusAddr net.Addr
	if o.httpAddr != "" {
		hl, err := net.Listen("tcp", o.httpAddr)
		if err != nil {
			_ = lis.Close()
			return err
		}
		statusAddr = hl.Addr()
		status = &http.Server{Handler: statusweb.Handler(srv), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := status.Serve(hl); !errors.Is(err, http.ErrServerClosed) {
				slog.Error("status page", "err", err)
			}
		}()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops Monitor if Serve fails
	go srv.Monitor(ctx)
	go func() {
		<-ctx.Done()
		if status != nil {
			_ = status.Close()
		}
		srv.Shutdown()
		// Give idle workers one retry interval to hear that they should exit.
		time.Sleep(time.Second)
		gs.GracefulStop()
	}()
	slog.Info("coordinator listening", "addr", lis.Addr())
	if listening != nil {
		listening(lis.Addr(), statusAddr)
	}
	return gs.Serve(lis)
}
