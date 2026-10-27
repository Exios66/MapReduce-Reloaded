package rpcguard

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// panicky panics in a unary and a streaming RPC.
type panicky struct {
	pb.UnimplementedCoordinatorServiceServer
	pb.UnimplementedShuffleServiceServer
}

func (panicky) SubmitJob(context.Context, *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	var s []int
	_ = s[1] // index out of range, as a real bug would
	return nil, nil
}

func (panicky) FetchPartition(*pb.FetchPartitionRequest, grpc.ServerStreamingServer[pb.FetchPartitionResponse]) error {
	panic("boom")
}

// A panicking call fails with Internal, and the server keeps serving.
func TestPanicIsAnError(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer(ServerOptions()...)
	pb.RegisterCoordinatorServiceServer(gs, panicky{})
	pb.RegisterShuffleServiceServer(gs, panicky{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	coordinator := pb.NewCoordinatorServiceClient(conn)
	for range 2 { // the second call shows the server survived the first
		if _, err := coordinator.SubmitJob(t.Context(), &pb.SubmitJobRequest{}); status.Code(err) != codes.Internal {
			t.Fatalf("SubmitJob: got %v, want Internal", err)
		}
	}
	fetch, err := pb.NewShuffleServiceClient(conn).FetchPartition(t.Context(), &pb.FetchPartitionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetch.Recv(); status.Code(err) != codes.Internal {
		t.Fatalf("FetchPartition: got %v, want Internal", err)
	}
	if _, err := coordinator.GetJobStatus(t.Context(), &pb.GetJobStatusRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("after the panics: got %v, want the server still answering", err)
	}
}
