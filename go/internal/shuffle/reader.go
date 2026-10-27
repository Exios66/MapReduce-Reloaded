package shuffle

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// Retry policy for a fetch: after a transient error the reader reconnects,
// resuming at the bytes it already has, up to MaxRetries times in a row.
const MaxRetries = 4

// baseDelay is the wait before the first reconnect, doubled for each
// further one; a variable so the fuzz test need not wait.
var baseDelay = 100 * time.Millisecond

// IdleTimeout ends a fetch attempt that receives nothing for this long, as
// from a frozen worker or a dead connection. It counts as a transient error.
const IdleTimeout = 30 * time.Second

// Reader reads one map output file from the worker that holds it. It is a
// single contiguous stream from the start of the file, however many times
// it reconnects, so the file's checksums still cover everything read.
type Reader struct {
	ctx      context.Context
	client   pb.ShuffleServiceClient
	req      *pb.FetchPartitionRequest // Offset is the next byte wanted
	stream   grpc.ServerStreamingClient[pb.FetchPartitionResponse]
	cancel   context.CancelFunc // ends stream
	buf      []byte
	failures int
	idle     time.Duration
}

// NewReader returns a reader for the file that req names, starting at
// req.Offset. Closing it ends any fetch in progress.
func NewReader(ctx context.Context, client pb.ShuffleServiceClient, req *pb.FetchPartitionRequest) *Reader {
	return &Reader{ctx: ctx, client: client, req: proto.CloneOf(req), idle: IdleTimeout}
}

func (r *Reader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 && len(p) > 0 {
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// fill receives the next chunk, reconnecting after transient errors.
func (r *Reader) fill() error {
	if r.stream == nil {
		ctx, cancel := context.WithCancel(r.ctx)
		stream, err := r.client.FetchPartition(ctx, r.req)
		if err != nil {
			cancel()
			return r.retry(err)
		}
		r.stream, r.cancel = stream, cancel
	}
	timer := time.AfterFunc(r.idle, r.cancel)
	resp, err := r.stream.Recv()
	if !timer.Stop() {
		err = status.Errorf(codes.DeadlineExceeded, "shuffle: no data for %v", r.idle)
	}
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	if err != nil {
		r.closeStream()
		return r.retry(err)
	}
	r.buf = resp.GetData()
	r.req.Offset += uint64(len(r.buf))
	r.failures = 0
	return nil
}

// retry waits before the next attempt, or returns err if it is not
// transient or the retries are used up.
func (r *Reader) retry(err error) error {
	if !transient(err) || r.failures == MaxRetries {
		return err
	}
	delay := baseDelay << r.failures
	r.failures++
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func transient(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.ResourceExhausted, codes.Aborted, codes.DeadlineExceeded:
		return true
	}
	return false
}

func (r *Reader) closeStream() {
	if r.cancel != nil {
		r.cancel()
	}
	r.stream, r.cancel = nil, nil
}

// Close ends the fetch. It always returns nil.
func (r *Reader) Close() error {
	r.closeStream()
	return nil
}

// Clients keeps one connection per worker address. It is safe for
// concurrent use.
type Clients struct {
	mu    sync.Mutex
	conns map[string]*grpc.ClientConn
}

// Get returns a client for the shuffle server at addr.
func (c *Clients) Get(addr string) (pb.ShuffleServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, ok := c.conns[addr]
	if !ok {
		var err error
		if conn, err = grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
			return nil, err
		}
		if c.conns == nil {
			c.conns = map[string]*grpc.ClientConn{}
		}
		c.conns[addr] = conn
	}
	return pb.NewShuffleServiceClient(conn), nil
}

// Close closes every connection.
func (c *Clients) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, conn := range c.conns {
		errs = append(errs, conn.Close())
	}
	c.conns = nil
	return errors.Join(errs...)
}
