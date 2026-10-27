// Command worker runs tasks for a coordinator until it says to exit.
//
//	worker -coordinator host:7070 [-listen :7071] [-advertise host:7071] [-work-dir dir] [-min-free-mib n] [-http :8081] [-log-format text|json]
//
// It keeps map output in its work directory and serves it to reducers on
// -listen; other workers reach it at -advertise. It deletes a job's map
// output once the coordinator says the job has ended, and takes no task
// while the work directory has under -min-free-mib free (default 1024).
// Without -work-dir it uses a new temporary directory and removes it on
// exit, intermediate files included. With -listen "" it serves nothing: every worker must then share
// the same -work-dir, and reducers read map output from there. With -http
// it serves Prometheus metrics at /metrics (§4.8).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/logs"
	"github.com/exios66/mapreduce-reloaded/go/internal/rpcguard"
	"github.com/exios66/mapreduce-reloaded/go/internal/shuffle"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

type config struct {
	coordinator, listen, advertise, workDir, http, logFormat string
	minFreeMiB                                               uint64
}

// main runs the worker until the coordinator says to exit, or it is
// interrupted or terminated. It exits with status 2 for bad flags and 1 for
// any other error, printed to stderr.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := start(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

// start parses args, sets up logging and runs the worker until ctx is done,
// and returns the process's exit status, printing errors to stderr.
func start(ctx context.Context, args []string, stderr io.Writer) int {
	c, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "worker:", err)
		return 2
	}
	if err = logs.Setup(c.logFormat); err == nil {
		err = run(ctx, c, nil)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "worker:", err)
		return 1
	}
	return 0
}

// parse parses the worker's flags, writing usage to stderr on error.
func parse(args []string, stderr io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.coordinator, "coordinator", "localhost:7070", "coordinator address")
	fs.StringVar(&c.listen, "listen", ":7071", `address to serve map output on; "" to use a shared -work-dir instead`)
	fs.StringVar(&c.advertise, "advertise", "", "address other workers fetch map output from (default: this host and the -listen port)")
	fs.StringVar(&c.workDir, "work-dir", "", "directory for map output (default: a temporary directory)")
	fs.StringVar(&c.http, "http", "", "address to serve /metrics on (default: none)")
	fs.Uint64Var(&c.minFreeMiB, "min-free-mib", 1024, "take no task while the -work-dir file system has less than this free, in MiB")
	fs.StringVar(&c.logFormat, "log-format", "text", "log format: text or json")
	err := fs.Parse(args)
	switch {
	case err != nil:
	case fs.NArg() > 0:
		err = fmt.Errorf("unexpected arguments %q", fs.Args())
	case c.listen == "" && c.workDir == "":
		err = errors.New(`-listen "" needs a shared -work-dir`)
	case c.minFreeMiB > math.MaxUint64>>20:
		err = fmt.Errorf("-min-free-mib %d is too large", c.minFreeMiB)
	}
	return c, err
}

// run serves map output and runs tasks using c until the coordinator says to
// exit or ctx is done, checking c.minFreeMiB (in MiB) before requesting
// work. An empty workDir creates a temporary directory removed on return,
// after deleting those that dead workers left behind; an empty listen
// requires a shared workDir. If listening is not nil, run calls it with the
// worker's information and its metrics address (nil without -http) before
// it registers.
// It returns setup and worker errors, joined with temporary-directory
// cleanup errors. A done ctx ends the worker successfully, though cleanup
// can still fail. Background shuffle and metrics server errors are not
// returned.
func run(ctx context.Context, c config, listening func(info *pb.WorkerInfo, metrics net.Addr)) (err error) {
	host, _ := os.Hostname() // only a hint for locality scheduling (§3.4)
	info := &pb.WorkerInfo{Host: host, Slots: 1}
	if c.workDir == "" {
		var release func() error
		if c.workDir, release, err = tempWorkDir(""); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, release()) }()
	}
	if c.listen != "" {
		lis, err := net.Listen("tcp", c.listen)
		if err != nil {
			return err
		}
		gs := grpc.NewServer(rpcguard.ServerOptions()...)
		pb.RegisterShuffleServiceServer(gs, shuffle.NewServer(c.workDir))
		go func() { _ = gs.Serve(lis) }()
		defer gs.Stop()
		if info.ShuffleAddress = c.advertise; c.advertise == "" {
			_, port, _ := net.SplitHostPort(lis.Addr().String())
			info.ShuffleAddress = net.JoinHostPort(host, port)
		}
	}

	conn, err := grpc.NewClient(c.coordinator, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	w := worker.New(pb.NewCoordinatorServiceClient(conn), c.workDir, info)
	w.MinFreeDisk = c.minFreeMiB << 20
	var metricsAddr net.Addr
	if c.http != "" {
		ml, err := net.Listen("tcp", c.http)
		if err != nil {
			return err
		}
		metricsAddr = ml.Addr()
		metrics := &http.Server{Handler: w.MetricsHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metrics.Serve(ml); !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics", "err", err)
			}
		}()
		defer func() { _ = metrics.Close() }()
	}
	if listening != nil {
		listening(info, metricsAddr)
	}
	err = w.Run(ctx)
	if ctx.Err() != nil {
		return nil // stopped by a signal, as asked
	}
	return err
}
