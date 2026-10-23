package shuffle

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
)

// flaky breaks its first `fails` fetches after one chunk, or fails every
// fetch at once while down, and counts every fetch.
type flaky struct {
	*Server
	fails, calls atomic.Int32
	down         atomic.Bool
}

func (f *flaky) FetchPartition(req *pb.FetchPartitionRequest, stream grpc.ServerStreamingServer[pb.FetchPartitionResponse]) error {
	f.calls.Add(1)
	if f.down.Load() {
		return status.Error(codes.Unavailable, "down")
	}
	if f.fails.Add(-1) >= 0 {
		stream = &cut{ServerStreamingServer: stream}
	}
	return f.Server.FetchPartition(req, stream)
}

// cut is a stream that breaks after its first message.
type cut struct {
	grpc.ServerStreamingServer[pb.FetchPartitionResponse]
	sent bool
}

func (c *cut) Send(m *pb.FetchPartitionResponse) error {
	if c.sent {
		return status.Error(codes.Unavailable, "connection lost")
	}
	c.sent = true
	return c.ServerStreamingServer.Send(m)
}

// serve writes data as map 0's partition 1 of job-1, serves it, and returns
// the server and a client.
func serve(t *testing.T, data []byte, fails int32) (*flaky, pb.ShuffleServiceClient) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "job-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.MapOutput(filepath.Join(dir, "job-1"), 0, 1), data, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &flaky{Server: NewServer(dir)}
	srv.fails.Store(fails)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	pb.RegisterShuffleServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, pb.NewShuffleServiceClient(conn)
}

func fetch(client pb.ShuffleServiceClient, req *pb.FetchPartitionRequest) ([]byte, error) {
	r := NewReader(context.Background(), client, req)
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

func randomBytes(n int) []byte {
	data := make([]byte, n)
	rng := rand.NewChaCha8([32]byte{})
	_, _ = rng.Read(data)
	return data
}

func TestFetch(t *testing.T) {
	data := randomBytes(3*ChunkSize + 17)
	_, client := serve(t, data, 0)
	for _, offset := range []int{0, 5, ChunkSize, len(data), len(data) + 1} {
		got, err := fetch(client, &pb.FetchPartitionRequest{JobId: "job-1", Partition: 1, Offset: uint64(offset)})
		if err != nil {
			t.Fatalf("offset %d: %v", offset, err)
		}
		if want := data[min(offset, len(data)):]; !bytes.Equal(got, want) {
			t.Fatalf("offset %d: got %d bytes, want %d", offset, len(got), len(want))
		}
	}
}

// A fetch that breaks resumes where it stopped, so the bytes still arrive
// once each and in order.
func TestFetchResumes(t *testing.T) {
	data := randomBytes(4 * ChunkSize)
	srv, client := serve(t, data, 2)
	got, err := fetch(client, &pb.FetchPartitionRequest{JobId: "job-1", Partition: 1})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("got %d bytes (%v), want %d", len(got), err, len(data))
	}
	if n := srv.calls.Load(); n != 3 {
		t.Fatalf("%d fetches, want 3", n)
	}
}

// A fetch that makes no progress gives up after MaxRetries retries.
func TestFetchGivesUp(t *testing.T) {
	srv, client := serve(t, randomBytes(ChunkSize), 0)
	srv.down.Store(true)
	empty := NewReader(context.Background(), client, &pb.FetchPartitionRequest{JobId: "job-1", Partition: 1})
	if n, err := empty.Read(nil); n != 0 || err != nil || srv.calls.Load() != 0 {
		t.Fatalf("empty Read = %d, %v; want 0, nil without fetching", n, err)
	}
	if _, err := fetch(client, &pb.FetchPartitionRequest{JobId: "job-1", Partition: 1}); status.Code(err) != codes.Unavailable {
		t.Fatalf("got %v, want Unavailable", err)
	}
	if n := srv.calls.Load(); n != MaxRetries+1 {
		t.Fatalf("%d fetches, want %d", n, MaxRetries+1)
	}
}

// Errors that retrying cannot fix are returned at once.
func TestFetchErrors(t *testing.T) {
	srv, client := serve(t, nil, 0)
	for _, tc := range []struct {
		req  *pb.FetchPartitionRequest
		want codes.Code
	}{
		{&pb.FetchPartitionRequest{JobId: "job-1", Partition: 0}, codes.NotFound},
		{&pb.FetchPartitionRequest{JobId: "job-2", Partition: 1}, codes.NotFound},
		{&pb.FetchPartitionRequest{JobId: "../job-1", Partition: 1}, codes.InvalidArgument},
		{&pb.FetchPartitionRequest{JobId: "", Partition: 1}, codes.InvalidArgument},
	} {
		if _, err := fetch(client, tc.req); status.Code(err) != tc.want {
			t.Errorf("fetch(%v) = %v, want %v", tc.req, err, tc.want)
		}
	}
	if n := srv.calls.Load(); n != 4 {
		t.Fatalf("%d fetches, want 4: errors were retried", n)
	}
}

func TestClients(t *testing.T) {
	var c Clients
	a, err := c.Get("localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := c.Get("localhost:1"); a == nil || b == nil || len(c.conns) != 1 {
		t.Fatalf("got %d connections, want 1", len(c.conns))
	}
	if err := c.Close(); err != nil || c.conns != nil {
		t.Fatalf("Close = %v, %d connections left", err, len(c.conns))
	}
}
