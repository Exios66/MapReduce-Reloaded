package contract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	mrpb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

func TestRPCServiceDescriptors(t *testing.T) {
	cases := []struct {
		service   protoreflect.ServiceDescriptor
		methods   []string
		streaming bool
	}{
		{mrpb.File_mapreduce_v1_coordinator_proto.Services().ByName("CoordinatorService"),
			[]string{"RegisterWorker", "RequestTask", "ReportTask", "Heartbeat", "SubmitJob", "GetJobStatus"}, false},
		{mrpb.File_mapreduce_v1_shuffle_proto.Services().ByName("ShuffleService"), []string{"FetchPartition"}, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.service.Name()), func(t *testing.T) {
			if tc.service.Methods().Len() != len(tc.methods) {
				t.Fatalf("unexpected methods: %v", tc.service.Methods())
			}
			for _, name := range tc.methods {
				method := tc.service.Methods().ByName(protoreflect.Name(name))
				if method == nil {
					t.Fatalf("missing RPC %s", name)
				}
				if got := string(method.Input().FullName()); got != "mapreduce.v1."+name+"Request" {
					t.Errorf("%s request type: %s", name, got)
				}
				if got := string(method.Output().FullName()); got != "mapreduce.v1."+name+"Response" {
					t.Errorf("%s response type: %s", name, got)
				}
				if method.IsStreamingClient() || method.IsStreamingServer() != tc.streaming {
					t.Errorf("%s has incorrect streaming shape", name)
				}
			}
		})
	}
}

func TestDefaultServicersReturnUnimplemented(t *testing.T) {
	server := mrpb.UnimplementedCoordinatorServiceServer{}
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
	}{
		{"RegisterWorker", func() error { _, err := server.RegisterWorker(ctx, &mrpb.RegisterWorkerRequest{}); return err }},
		{"RequestTask", func() error { _, err := server.RequestTask(ctx, &mrpb.RequestTaskRequest{}); return err }},
		{"ReportTask", func() error { _, err := server.ReportTask(ctx, &mrpb.ReportTaskRequest{}); return err }},
		{"Heartbeat", func() error { _, err := server.Heartbeat(ctx, &mrpb.HeartbeatRequest{}); return err }},
		{"SubmitJob", func() error { _, err := server.SubmitJob(ctx, &mrpb.SubmitJobRequest{}); return err }},
		{"GetJobStatus", func() error { _, err := server.GetJobStatus(ctx, &mrpb.GetJobStatusRequest{}); return err }},
		{"FetchPartition", func() error {
			return (mrpb.UnimplementedShuffleServiceServer{}).FetchPartition(&mrpb.FetchPartitionRequest{}, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); status.Code(err) != codes.Unimplemented {
				t.Fatalf("got %v, want codes.Unimplemented", err)
			}
		})
	}
}

// Only the streaming operations used by the generated shuffle client are faked.
// Embedding the interfaces makes unexpected operations fail the test immediately.
type shuffleConn struct {
	grpc.ClientConnInterface
	stream *shuffleStream
	err    error
	method string
	desc   *grpc.StreamDesc
}

func (c *shuffleConn) NewStream(_ context.Context, desc *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	c.method, c.desc = method, desc
	return c.stream, c.err
}

type shuffleStream struct {
	grpc.ClientStream
	request  *mrpb.FetchPartitionRequest
	sendErr  error
	closeErr error
	closed   bool
	chunks   [][]byte
	next     int
}

func (s *shuffleStream) SendMsg(message any) error {
	s.request = message.(*mrpb.FetchPartitionRequest)
	return s.sendErr
}

func (s *shuffleStream) CloseSend() error {
	s.closed = true
	return s.closeErr
}

func (s *shuffleStream) RecvMsg(message any) error {
	if s.next == len(s.chunks) {
		return io.EOF
	}
	message.(*mrpb.FetchPartitionResponse).Data = s.chunks[s.next]
	s.next++
	return nil
}

func TestShuffleClientForwardsResumeRequestAndStreamsBinaryChunks(t *testing.T) {
	chunks := [][]byte{{0, 0xff, 0x80}, {}, {'a', '\n', 'b'}}
	transport := &shuffleStream{chunks: chunks}
	conn := &shuffleConn{stream: transport}
	request := &mrpb.FetchPartitionRequest{JobId: "job-1", MapIndex: 7, Partition: 3, Offset: 1<<53 + 1}
	stream, err := mrpb.NewShuffleServiceClient(conn).FetchPartition(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if conn.method != "/mapreduce.v1.ShuffleService/FetchPartition" {
		t.Fatalf("wrong RPC path: %s", conn.method)
	}
	if conn.desc == nil || conn.desc.ClientStreams || !conn.desc.ServerStreams {
		t.Fatalf("FetchPartition must be server streaming: %v", conn.desc)
	}
	if !transport.closed || !proto.Equal(transport.request, request) {
		t.Fatalf("request was not sent and closed correctly: %v", transport.request)
	}
	for i, want := range chunks {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if !bytes.Equal(response.GetData(), want) {
			t.Fatalf("chunk %d: got %x, want %x", i, response.GetData(), want)
		}
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream: got %v, want EOF", err)
	}
}

func TestShuffleClientPropagatesSetupErrors(t *testing.T) {
	want := status.Error(codes.Unavailable, "shuffle unavailable")
	for _, stage := range []string{"open", "send", "close"} {
		t.Run(stage, func(t *testing.T) {
			transport := &shuffleStream{}
			conn := &shuffleConn{stream: transport}
			switch stage {
			case "open":
				conn.err = want
			case "send":
				transport.sendErr = want
			case "close":
				transport.closeErr = want
			}
			stream, err := mrpb.NewShuffleServiceClient(conn).FetchPartition(context.Background(), &mrpb.FetchPartitionRequest{})
			if stream != nil || !errors.Is(err, want) {
				t.Fatalf("got (%v, %v), want nil stream and original error", stream, err)
			}
			if transport.closed != (stage == "close") {
				t.Fatalf("close called after failed %s: %v", stage, transport.closed)
			}
		})
	}
}
