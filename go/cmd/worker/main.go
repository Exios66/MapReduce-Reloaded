// Command worker runs tasks for a coordinator until it says to exit.
//
//	worker -coordinator host:7070 [-listen :7071] [-advertise host:7071] [-work-dir dir] [-http :8081]
//
// It keeps map output in its work directory and serves it to reducers on
// -listen; other workers reach it at -advertise. Without -work-dir it uses a
// new temporary directory and removes it on exit, intermediate files
// included. With -listen "" it serves nothing: every worker must then share
// the same -work-dir, and reducers read map output from there. With -http
// it serves Prometheus metrics at /metrics (§4.8).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
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
	"github.com/exios66/mapreduce-reloaded/go/internal/shuffle"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

type config struct {
	coordinator, listen, advertise, workDir, http string
}

func main() {
	var c config
	flag.StringVar(&c.coordinator, "coordinator", "localhost:7070", "coordinator address")
	flag.StringVar(&c.listen, "listen", ":7071", `address to serve map output on; "" to use a shared -work-dir instead`)
	flag.StringVar(&c.advertise, "advertise", "", "address other workers fetch map output from (default: this host and the -listen port)")
	flag.StringVar(&c.workDir, "work-dir", "", "directory for map output (default: a temporary directory)")
	flag.StringVar(&c.http, "http", "", "address to serve /metrics on (default: none)")
	flag.Parse()
	if err := run(c); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run(c config) (err error) {
	host, _ := os.Hostname() // only a hint for locality scheduling (§3.4)
	info := &pb.WorkerInfo{Host: host, Slots: 1}
	switch {
	case c.listen == "" && c.workDir == "":
		return errors.New(`-listen "" needs a shared -work-dir`)
	case c.workDir == "":
		if c.workDir, err = os.MkdirTemp("", "mr-worker-"); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, os.RemoveAll(c.workDir)) }()
	}
	if c.listen != "" {
		lis, err := net.Listen("tcp", c.listen)
		if err != nil {
			return err
		}
		gs := grpc.NewServer()
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
