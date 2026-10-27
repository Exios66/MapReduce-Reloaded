package storage_test

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
)

func put(t *testing.T, name, content string) {
	t.Helper()
	w, err := Create(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func wantOpError(t *testing.T, err error, op string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), op) {
		t.Errorf("got %v, want an error that names %s", err, op)
	}
}

// Each operation that keeps failing returns an error naming the S3 call; one
// that fails once is retried and succeeds.
func TestS3Errors(t *testing.T) {
	ctx := t.Context()
	srv := s3test.StartServer(t)
	dir := "s3://" + s3test.Bucket + "/d"
	if err := MkdirAll(ctx, dir); err != nil {
		t.Fatal(err)
	}
	put(t, dir+"/f", "content")

	calls := []struct {
		op  string
		run func() error
	}{
		{"ListObjectsV2", func() error { _, err := List(ctx, dir+"/*"); return err }},
		{"HeadObject", func() error { _, err := Size(ctx, dir+"/f"); return err }},
		{"GetObject", func() error { _, err := Open(ctx, dir+"/f", 0); return err }},
		{"PutObject", func() error {
			w, err := Create(ctx, dir+"/g")
			if err != nil {
				return err
			}
			return w.Close()
		}},
		{"PutObject", func() error { return MkdirAll(ctx, dir+"/sub") }},
		{"DeleteObject", func() error { return Delete(ctx, dir+"/g") }},
		{"DeleteObjects", func() error {
			if err := MkdirAll(ctx, dir+"/sub"); err != nil { // something to delete
				return err
			}
			return DeleteAll(ctx, dir+"/sub")
		}},
	}
	for _, c := range calls {
		for _, code := range []struct {
			status int
			name   string
		}{{500, "InternalError"}, {503, "SlowDown"}} {
			srv.Fail(c.op, 1, code.status, code.name)
			if err := c.run(); err != nil {
				t.Errorf("%s after one %d: %v, want a retry that succeeds", c.op, code.status, err)
			}
			srv.Fail(c.op, -1, code.status, code.name)
			wantOpError(t, c.run(), c.op)
			srv.Reset()
		}
	}

	// Create checks the directory marker first.
	srv.Fail("HeadObject", -1, 500, "InternalError")
	_, err := Create(ctx, dir+"/h")
	wantOpError(t, err, "HeadObject")
	srv.Reset()

	// Wrong credentials and an unknown bucket are errors, not retries.
	srv.Fail("GetObject", -1, 403, "InvalidAccessKeyId")
	_, err = Open(ctx, dir+"/f", 0)
	wantOpError(t, err, "InvalidAccessKeyId")
	srv.Reset()
	_, err = Open(ctx, "s3://no-such-bucket/f", 0)
	wantOpError(t, err, "GetObject")
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing bucket is not a missing file: %v", err)
	}
}

// A commit is the copy: if the copy fails nothing is committed, and if only
// the delete of the temporary file fails the commit still succeeds (ADR
// 0013).
func TestS3CommitFailures(t *testing.T) {
	ctx := t.Context()
	srv := s3test.StartServer(t)
	dir := "s3://" + s3test.Bucket + "/d"
	if err := MkdirAll(ctx, dir); err != nil {
		t.Fatal(err)
	}
	put(t, dir+"/tmp", "data")

	srv.Fail("CopyObject", -1, 500, "InternalError")
	wantOpError(t, Commit(ctx, dir+"/tmp", dir+"/out"), "CopyObject")
	if _, err := Size(ctx, dir+"/out"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("after a failed copy, the target: %v, want missing", err)
	}
	srv.Reset()

	srv.Fail("DeleteObject", -1, 500, "InternalError")
	if err := Commit(ctx, dir+"/tmp", dir+"/out"); err != nil {
		t.Errorf("a commit whose delete fails: %v, want success", err)
	}
	if n, err := Size(ctx, dir+"/out"); err != nil || n != 4 {
		t.Errorf("the committed file: %d bytes, %v", n, err)
	}
}

// A read cut off half way is an error, not a short file.
func TestS3TruncatedRead(t *testing.T) {
	ctx := t.Context()
	srv := s3test.StartServer(t)
	name := "s3://" + s3test.Bucket + "/f"
	put(t, name, strings.Repeat("x", 1<<16))
	srv.Truncate()
	r, err := Open(ctx, name, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if b, err := io.ReadAll(r); err == nil {
		t.Errorf("a cut-off read returned %d bytes and no error", len(b))
	}
}

// A backend with no client is configured from the AWS_* variables.
func TestS3FromEnvironment(t *testing.T) {
	srv := s3test.StartServer(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "none"))
	for _, tc := range []struct{ region, endpoint string }{
		{"", srv.URL},          // no region: us-east-1
		{"eu-west-1", srv.URL}, // the store ignores the region
	} {
		t.Setenv("AWS_REGION", tc.region)
		t.Setenv("AWS_ENDPOINT_URL", tc.endpoint)
		Register("s3", &S3{})
		if _, err := List(t.Context(), "s3://"+s3test.Bucket+"/*"); err != nil {
			t.Errorf("region %q: %v", tc.region, err)
		}
	}

	// An endpoint without a scheme is reported, naming it.
	t.Setenv("AWS_ENDPOINT_URL", strings.TrimPrefix(srv.URL, "http://"))
	Register("s3", &S3{})
	if _, err := List(t.Context(), "s3://"+s3test.Bucket+"/*"); err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(srv.URL, "http://")) {
		t.Errorf("an endpoint without a scheme: %v, want an error naming it", err)
	}

	t.Setenv("AWS_ENDPOINT_URL", srv.URL)
	t.Setenv("AWS_MAX_ATTEMPTS", "many") // LoadDefaultConfig rejects it
	Register("s3", &S3{})
	for _, call := range []func() error{
		func() error { _, err := List(t.Context(), "s3://b/*"); return err },
		func() error { _, err := Size(t.Context(), "s3://b/k"); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "storage: s3") {
			t.Errorf("with a bad AWS_MAX_ATTEMPTS: %v, want a configuration error", err)
		}
	}
}

// Names that are not s3://bucket/key, and patterns that are not valid, are
// rejected before any request.
func TestS3BadNames(t *testing.T) {
	s3test.Start(t)
	for _, name := range []string{"s3://", "s3:///key"} {
		if _, err := Size(t.Context(), name); err == nil {
			t.Errorf("Size(%q) succeeded", name)
		}
		if _, err := List(t.Context(), name); err == nil {
			t.Errorf("List(%q) succeeded", name)
		}
	}
	if _, err := List(t.Context(), "s3://"+s3test.Bucket+"/["); err == nil {
		t.Error("List of a bad pattern succeeded")
	}
	if err := Commit(t.Context(), "s3://", "s3://"+s3test.Bucket+"/x"); err == nil {
		t.Error("Commit from a bad name succeeded")
	}
}

// Every function fails for a scheme with no backend, and Commit across two
// backends fails.
func TestNoBackend(t *testing.T) {
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"List":      func() error { _, err := List(ctx, "nope://x/*"); return err },
		"Size":      func() error { _, err := Size(ctx, "nope://x/f"); return err },
		"Open":      func() error { _, err := Open(ctx, "nope://x/f", 0); return err },
		"Create":    func() error { _, err := Create(ctx, "nope://x/f"); return err },
		"Commit":    func() error { return Commit(ctx, "nope://x/a", "nope://x/b") },
		"Delete":    func() error { return Delete(ctx, "nope://x/f") },
		"MkdirAll":  func() error { return MkdirAll(ctx, "nope://x/d") },
		"DeleteAll": func() error { return DeleteAll(ctx, "nope://x/d") },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "no backend") {
			t.Errorf("%s: %v, want no backend", name, err)
		}
	}
	if err := Commit(ctx, filepath.Join(t.TempDir(), "a"), "s3://b/a"); err == nil {
		t.Error("Commit from a local file to s3:// succeeded")
	}
}

// The local backend: a negative offset, and MkdirAll over a file.
func TestLocalErrors(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	put(t, f, "x")
	if _, err := Open(t.Context(), f, -1); err == nil {
		t.Error("Open at offset -1 succeeded")
	}
	if err := MkdirAll(t.Context(), f); err == nil {
		t.Error("MkdirAll over a file succeeded")
	}
	if err := MkdirAll(t.Context(), filepath.Join(f, "sub")); err == nil {
		t.Error("MkdirAll under a file succeeded")
	}
}
