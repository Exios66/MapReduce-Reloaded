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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/logs"
	"github.com/exios66/mapreduce-reloaded/go/internal/statusweb"
	"github.com/exios66/mapreduce-reloaded/go/internal/wal"
)

func main() {
	addr := flag.String("addr", ":7070", "address to listen on")
	httpAddr := flag.String("http", ":8080", "address of the status page and /metrics (empty: none)")
	dataDir := flag.String("data-dir", "", "directory for the write-ahead log (default: none, so a restart loses the job)")
	backups := flag.Bool("backups", true, "run backup attempts of the last tasks of each phase")
	keepJobs := flag.Int("keep-jobs", 100, "ended jobs to keep for mr status; older ones are forgotten (0: all)")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	flag.Parse()
	err := logs.Setup(*logFormat)
	if err == nil {
		err = run(*addr, *httpAddr, *dataDir, *backups, *keepJobs)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "coordinator:", err)
		os.Exit(1)
	}
}

func run(addr, httpAddr, dataDir string, backups bool, keepJobs int) (err error) {
	cfg := coordinator.Config{
		HeartbeatInterval: 3 * time.Second,
		WorkerTimeout:     10 * time.Second, // three missed heartbeats
		RetryAfter:        time.Second,
		Backups:           backups,
		KeepJobs:          keepJobs,
	}
	var srv *coordinator.Server
	if dataDir == "" {
		srv = coordinator.NewServer(cfg)
	} else {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return err
		}
		var log *wal.Log
		if srv, log, err = coordinator.Recover(cfg, filepath.Join(dataDir, "coordinator.log")); err != nil {
			return err
		}
		defer func() { err = errors.Join(err, log.Close()) }()
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var status *http.Server
	if httpAddr != "" {
		status = &http.Server{Addr: httpAddr, Handler: statusweb.Handler(srv), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := status.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				slog.Error("status page", "err", err)
			}
		}()
	}
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
	return gs.Serve(lis)
}
