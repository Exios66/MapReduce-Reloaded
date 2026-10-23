// Command coordinator serves the coordinator ("master", §3.2) of a cluster.
//
//	coordinator [-addr :7070]
//
// It runs until interrupted, then tells workers to exit.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

func main() {
	addr := flag.String("addr", ":7070", "address to listen on")
	flag.Parse()
	if err := run(*addr); err != nil {
		fmt.Fprintln(os.Stderr, "coordinator:", err)
		os.Exit(1)
	}
}

func run(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := coordinator.NewServer(coordinator.Config{
		HeartbeatInterval: 3 * time.Second,
		WorkerTimeout:     10 * time.Second, // three missed heartbeats
		RetryAfter:        time.Second,
	})
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Monitor(ctx)
	go func() {
		<-ctx.Done()
		srv.Shutdown()
		// Give idle workers one retry interval to hear that they should exit.
		time.Sleep(time.Second)
		gs.GracefulStop()
	}()
	slog.Info("coordinator listening", "addr", lis.Addr())
	return gs.Serve(lis)
}
