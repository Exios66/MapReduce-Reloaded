// Package shuffle moves map output from the worker that wrote it to the
// reducers that need it (§3.1 step 5): every worker runs a Server over its
// own work directory, and reducers read through a Reader.
package shuffle

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
)

// ChunkSize is the most data one FetchPartition message carries, well under
// gRPC's 4 MiB message limit. It bounds the memory each fetch uses.
const ChunkSize = 64 << 10

// Server serves the map output under a worker's work directory, laid out as
// <dir>/<job>/ by the worker.
type Server struct {
	pb.UnimplementedShuffleServiceServer
	dir string
}

// NewServer returns a server for the map output under dir.
func NewServer(dir string) *Server { return &Server{dir: dir} }

// FetchPartition streams one map output file from the requested offset.
func (s *Server) FetchPartition(req *pb.FetchPartitionRequest, stream grpc.ServerStreamingServer[pb.FetchPartitionResponse]) error {
	job := req.GetJobId()
	if !filepath.IsLocal(job) || strings.ContainsAny(job, `/\`) {
		return status.Errorf(codes.InvalidArgument, "bad job id %q", job)
	}
	path := task.MapOutput(filepath.Join(s.dir, job), int(req.GetMapIndex()), int(req.GetPartition()))
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return status.Errorf(codes.NotFound, "no output for job %s map %d partition %d", job, req.GetMapIndex(), req.GetPartition())
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	r := io.NewSectionReader(f, int64(req.GetOffset()), 1<<63-1)
	buf := make([]byte, ChunkSize)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			if err := stream.Send(&pb.FetchPartitionResponse{Data: buf[:n]}); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
