// Package s3test runs an in-process S3 server for tests.
package s3test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
)

// Bucket is the name of the bucket a server starts with.
const Bucket = "test"

// Server is an in-process S3 server that can be told to fail.
type Server struct {
	// URL is the server's endpoint, for clients configured from AWS_*.
	URL string

	mu     sync.Mutex
	faults []*fault
}

type fault struct {
	op       string
	status   int
	code     string
	times    int  // how many more requests fail; negative: all of them
	truncate bool // send half the body, then close the connection
}

// Start starts an S3 server with one empty bucket and registers a backend
// for it as s3://, until the test ends. It returns the bucket's URI.
func Start(t testing.TB) string {
	t.Helper()
	StartServer(t)
	return "s3://" + Bucket
}

// StartServer is Start, returning the server so the test can make it fail.
func StartServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{}
	fake := gofakes3.New(s3mem.New()).Server()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(fake, w, r) }))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		Retryer: retry.NewStandard(func(o *retry.StandardOptions) {
			o.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
		}),
	})
	if _, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(Bucket)}); err != nil {
		t.Fatal(err)
	}
	prev, _ := storage.For("s3://" + Bucket)
	storage.Register("s3", storage.NewS3(client))
	t.Cleanup(func() { storage.Register("s3", prev) })
	return s
}

// Fail makes the next times requests for op, an S3 operation name such as
// "GetObject" or "CopyObject", fail with status and an S3 error code;
// times < 0 fails every one.
func (s *Server) Fail(op string, times, status int, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &fault{op: op, status: status, code: code, times: times})
}

// Truncate makes the next GetObject send half of the object's body and
// then close the connection.
func (s *Server) Truncate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &fault{op: "GetObject", times: 1, truncate: true})
}

// Pending returns how many injected failures have not happened yet, not
// counting faults set to fail every request.
func (s *Server) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, f := range s.faults {
		n += max(0, f.times)
	}
	return n
}

// Reset clears every fault.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = nil
}

func (s *Server) take(op string) *fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.faults {
		if f.op == op && f.times != 0 {
			if f.times > 0 {
				f.times--
			}
			return f
		}
	}
	return nil
}

func (s *Server) serve(fake http.Handler, w http.ResponseWriter, r *http.Request) {
	f := s.take(operation(r))
	switch {
	case f == nil:
		fake.ServeHTTP(w, r)
	case f.truncate:
		rec := httptest.NewRecorder()
		fake.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		body := rec.Body.Bytes()
		_, _ = w.Write(body[:len(body)/2])
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	default:
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(f.status)
		_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>injected by s3test</Message></Error>", f.code)
	}
}

// operation names the S3 operation of a path-style request.
func operation(r *http.Request) string {
	q := r.URL.Query()
	hasKey := strings.Count(strings.Trim(r.URL.Path, "/"), "/") > 0
	switch r.Method {
	case http.MethodGet:
		if !hasKey {
			return "ListObjectsV2"
		}
		return "GetObject"
	case http.MethodHead:
		return "HeadObject"
	case http.MethodPut:
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			return "CopyObject"
		}
		if !hasKey {
			return "CreateBucket"
		}
		return "PutObject"
	case http.MethodDelete:
		return "DeleteObject"
	case http.MethodPost:
		if q.Has("delete") {
			return "DeleteObjects"
		}
	}
	return r.Method
}
