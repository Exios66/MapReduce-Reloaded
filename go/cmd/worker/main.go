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

// main parses worker flags and runs the worker, exiting with status 1 after
// printing any logging setup or run error to stderr.
func main() {
	var c config
	flag.StringVar(&c.coordinator, "coordinator", "localhost:7070", "coordinator address")
	flag.StringVar(&c.listen, "listen", ":7071", `address to serve map output on; "" to use a shared -work-dir instead`)
	flag.StringVar(&c.advertise, "advertise", "", "address other workers fetch map output from (default: this host and the -listen port)")
	flag.StringVar(&c.workDir, "work-dir", "", "directory for map output (default: a temporary directory)")
	flag.StringVar(&c.http, "http", "", "address to serve /metrics on (default: none)")
	flag.Uint64Var(&c.minFreeMiB, "min-free-mib", 1024, "take no task while the -work-dir file system has less than this free, in MiB")
	flag.StringVar(&c.logFormat, "log-format", "text", "log format: text or json")
	flag.Parse()
	err := logs.Setup(c.logFormat)
	if err == nil {
		err = run(c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

// run serves map output and runs tasks using c, checking c.minFreeMiB
// (in MiB) before requesting work. An empty workDir creates a temporary
// directory removed on return, after deleting those that dead workers left
// behind; an empty listen requires a shared workDir.
// It returns setup and worker errors, joined with temporary-directory
// cleanup errors. A signal ends worker execution successfully, though
// cleanup can still fail. Background shuffle and metrics server errors
// are not returned.
func run(c config) (err error) {
	host, _ := os.Hostname() // only a hint for locality scheduling (§3.4)
	info := &pb.WorkerInfo{Host: host, Slots: 1}
	switch {
	case c.listen == "" && c.workDir == "":
		return errors.New(`-listen "" needs a shared -work-dir`)
	case c.workDir == "":
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := worker.New(pb.NewCoordinatorServiceClient(conn), c.workDir, info)
	if c.minFreeMiB > math.MaxUint64>>20 {
		return fmt.Errorf("-min-free-mib %d is too large", c.minFreeMiB)
	}
	w.MinFreeDisk = c.minFreeMiB << 20
	if c.http != "" {
		metrics := &http.Server{Addr: c.http, Handler: w.MetricsHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metrics.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics", "err", err)
			}
		}()
		defer func() { _ = metrics.Close() }()
	}
	err = w.Run(ctx)
	if ctx.Err() != nil {
		return nil // stopped by a signal, as asked
	}
	return err
}
