// Command worker runs tasks for a coordinator until it says to exit.
//
//	worker -coordinator host:7070 -work-dir /shared/mr
//
// Every worker must see the same -work-dir: map output is written there and
// reducers read it from there, until the shuffle server (#18) exists.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

func main() {
	addr := flag.String("coordinator", "localhost:7070", "coordinator address")
	workDir := flag.String("work-dir", "", "directory for map output, shared by all workers")
	flag.Parse()
	if err := run(*addr, *workDir); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run(addr, workDir string) error {
	if workDir == "" {
		return fmt.Errorf("-work-dir is required")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	host, _ := os.Hostname() // only a hint for locality scheduling (§3.4)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return worker.New(pb.NewCoordinatorServiceClient(conn), workDir, &pb.WorkerInfo{Host: host, Slots: 1}).Run(ctx)
}
