// Package contract checks that the generated gRPC/protobuf code in go/gen
// matches the shared contract in proto/ and agrees with the Python
// implementation on the wire format.
package contract

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	mrpb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

const goldenDir = "../../../testdata/proto"

// TestGoldenMapTask serializes the shared text vector and compares the bytes
// with the golden file that the Python tests also check against.
func TestGoldenMapTask(t *testing.T) {
	text, err := os.ReadFile(filepath.Join(goldenDir, "map_task.txtpb"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(goldenDir, "map_task.binpb"))
	if err != nil {
		t.Fatal(err)
	}

	var task mrpb.MapTask
	if err := prototext.Unmarshal(text, &task); err != nil {
		t.Fatalf("parse text vector: %v", err)
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(&task)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Go serialization differs from golden bytes:\n got %x\nwant %x", got, want)
	}

	// And the other direction: the golden bytes decode to the same message.
	var decoded mrpb.MapTask
	if err := proto.Unmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&decoded, &task) {
		t.Fatalf("decoded golden bytes differ from text vector")
	}
}

// fakeCoordinator hands out a single map task, then tells workers to wait.
type fakeCoordinator struct {
	mrpb.UnimplementedCoordinatorServiceServer
	handedOut bool
}

func (f *fakeCoordinator) RequestTask(_ context.Context, _ *mrpb.RequestTaskRequest) (*mrpb.RequestTaskResponse, error) {
	if !f.handedOut {
		f.handedOut = true
		return &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_MapTask{MapTask: &mrpb.MapTask{
			Attempt: &mrpb.TaskAttemptId{Task: &mrpb.TaskId{JobId: "job-1", Kind: mrpb.TaskKind_TASK_KIND_MAP}},
			Split:   &mrpb.InputSplit{Uri: "file:///in.txt", Length: 100},
		}}}, nil
	}
	return &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_Wait{
		Wait: &mrpb.WaitInstruction{RetryAfter: durationpb.New(500 * time.Millisecond)},
	}}, nil
}

// TestCoordinatorServiceRoundTrip runs the generated client and server
// against each other over an in-memory connection.
func TestCoordinatorServiceRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	mrpb.RegisterCoordinatorServiceServer(server, &fakeCoordinator{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := mrpb.NewCoordinatorServiceClient(conn)
	ctx := context.Background()

	first, err := client.RequestTask(ctx, &mrpb.RequestTaskRequest{WorkerId: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := first.GetMapTask().GetSplit().GetUri(); got != "file:///in.txt" {
		t.Fatalf("first assignment: want map task for file:///in.txt, got %v", first)
	}

	second, err := client.RequestTask(ctx, &mrpb.RequestTaskRequest{WorkerId: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.GetWait() == nil {
		t.Fatalf("second assignment: want wait instruction, got %v", second)
	}

	// RPCs the fake does not implement return codes.Unimplemented rather than
	// crashing: that is what the embedded Unimplemented server is for.
	_, err = client.Heartbeat(ctx, &mrpb.HeartbeatRequest{WorkerId: "w1"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("Heartbeat: want codes.Unimplemented, got %v", err)
	}
}
