package shuffle

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// chunked serves a partition from req.Offset in chunks of the sizes given,
// cycling, and drops each stream once after drop chunks, with a transient
// error, until it has been dropped drops times.
type chunked struct {
	data        []byte
	sizes       []byte
	drop, drops int
}

type chunkStream struct {
	grpc.ClientStream
	c    *chunked
	off  int
	sent int
}

func (c *chunked) FetchPartition(_ context.Context, req *pb.FetchPartitionRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.FetchPartitionResponse], error) {
	if req.GetOffset() > uint64(len(c.data)) {
		return nil, status.Error(codes.OutOfRange, "offset past the end")
	}
	return &chunkStream{c: c, off: int(req.GetOffset())}, nil
}

func (s *chunkStream) Recv() (*pb.FetchPartitionResponse, error) {
	if s.c.drops > 0 && s.sent == s.c.drop {
		s.c.drops--
		return nil, status.Error(codes.Unavailable, "dropped")
	}
	if s.off == len(s.c.data) {
		return nil, io.EOF
	}
	n := 1 + int(s.c.sizes[s.sent%len(s.c.sizes)])
	n = min(n, len(s.c.data)-s.off)
	s.off += n
	s.sent++
	return &pb.FetchPartitionResponse{Data: s.c.data[s.off-n : s.off]}, nil
}

// FuzzReader reads a partition from any offset, over any chunking, with up
// to MaxRetries dropped streams. It must give exactly the bytes from the
// offset on, resuming each new stream where the last one stopped (#152).
func FuzzReader(f *testing.F) {
	defer func(d time.Duration) { baseDelay = d }(baseDelay)
	baseDelay = 0
	f.Add([]byte("MRKV some partition bytes"), []byte{3}, uint16(0), uint8(2), uint8(1))
	f.Add([]byte("abcdefghij"), []byte{0, 7, 1}, uint16(4), uint8(0), uint8(4))
	f.Add([]byte(""), []byte{0}, uint16(0), uint8(0), uint8(0))
	f.Fuzz(func(t *testing.T, data, sizes []byte, offset uint16, drop, drops uint8) {
		if len(sizes) == 0 {
			sizes = []byte{0}
		}
		off := int(offset) % (len(data) + 1)
		c := &chunked{data: data, sizes: sizes, drop: int(drop % 8), drops: int(drops % (MaxRetries + 1))}
		got, err := io.ReadAll(NewReader(t.Context(), c, &pb.FetchPartitionRequest{Offset: uint64(off)}))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, data[off:]) {
			t.Fatalf("read %q from offset %d, want %q", got, off, data[off:])
		}
	})
}
