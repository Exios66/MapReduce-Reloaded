package storage_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	. "github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
)

// testContract holds a backend to the rules every backend must keep
// (#156), in a directory dir that does not exist yet.
func testContract(t *testing.T, dir string) {
	ctx := t.Context()
	write := func(name string, data []byte) {
		t.Helper()
		w, err := Create(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []byte {
		t.Helper()
		r, err := Open(ctx, name, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	list := func(pattern string) []string {
		t.Helper()
		names, err := List(ctx, pattern)
		if err != nil {
			t.Fatalf("List(%q): %v", pattern, err)
		}
		return names
	}

	for range 2 { // MkdirAll of an existing directory is not an error
		if err := MkdirAll(ctx, Join(dir, "sub")); err != nil {
			t.Fatal(err)
		}
	}
	if got := list(Join(dir, "sub", "*")); len(got) != 0 {
		t.Errorf("an empty directory lists %q", got)
	}
	if got := list(Join(dir, "nothing-*")); len(got) != 0 {
		t.Errorf("a pattern with no match lists %q", got)
	}

	missing := Join(dir, "missing")
	if _, err := Open(ctx, missing, 0); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open of a missing file: %v, want fs.ErrNotExist", err)
	}
	if _, err := Size(ctx, missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Size of a missing file: %v, want fs.ErrNotExist", err)
	}
	if _, err := Create(ctx, Join(dir, "no-dir", "f")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create in a missing directory: %v, want fs.ErrNotExist", err)
	}

	empty := Join(dir, "sub", "empty")
	write(empty, nil)
	if size, err := Size(ctx, empty); err != nil || size != 0 {
		t.Errorf("Size of an empty file = %d, %v", size, err)
	}
	if b := read(empty); len(b) != 0 {
		t.Errorf("an empty file reads %q", b)
	}

	big := bytes.Repeat([]byte("0123456789abcdef"), 9<<16) // 9 MiB, past one upload part
	write(Join(dir, "sub", "big"), big)
	if !bytes.Equal(read(Join(dir, "sub", "big")), big) {
		t.Error("a 9 MiB file reads back different")
	}

	for _, name := range []string{"a+b", "a b", "café", "日本"} {
		p := Join(dir, "sub", name)
		write(p, []byte(name))
		if got := read(p); string(got) != name {
			t.Errorf("%q reads %q", name, got)
		}
		if got := list(p); !slices.Equal(got, []string{p}) {
			t.Errorf("List(%q) = %q", p, got)
		}
	}

	// Commit replaces an existing file whole (ADR 0013).
	target := Join(dir, "sub", "target")
	for _, content := range []string{"first", "second"} {
		tmp := Join(dir, "sub", "tmp-"+content)
		write(tmp, []byte(content))
		if err := Commit(ctx, tmp, target); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(target); string(got) != "second" {
		t.Errorf("after two commits the file holds %q", got)
	}

	// More names than one S3 list page holds (1,000), listed in order.
	many := Join(dir, "many")
	if err := MkdirAll(ctx, many); err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := 1049; i >= 0; i-- {
		p := Join(many, fmt.Sprintf("f%04d", i))
		write(p, nil)
		want = append(want, p)
	}
	slices.Sort(want)
	if got := list(Join(many, "f*")); !slices.Equal(got, want) {
		t.Errorf("List of 1,050 files gave %d names, sorted: %v", len(got), slices.IsSorted(got))
	}

	// DeleteAll of a missing path is not an error, and DeleteAll of out
	// leaves out2.
	if err := DeleteAll(ctx, Join(dir, "never")); err != nil {
		t.Errorf("DeleteAll of a missing path: %v", err)
	}
	for _, d := range []string{"out", "out2"} {
		if err := MkdirAll(ctx, Join(dir, d)); err != nil {
			t.Fatal(err)
		}
		write(Join(dir, d, "f"), []byte(d))
	}
	if err := DeleteAll(ctx, Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if got := list(Join(dir, "out*", "f")); !slices.Equal(got, []string{Join(dir, "out2", "f")}) {
		t.Errorf("after DeleteAll of out, out*/f lists %q", got)
	}
}

func TestLocalContract(t *testing.T) { testContract(t, filepath.Join(t.TempDir(), "c")) }

func TestS3Contract(t *testing.T) { testContract(t, s3test.Start(t)+"/c") }

var s3Endpoint = flag.String("s3.endpoint", "", "also hold a real S3 store at this endpoint, such as the Compose s3 profile's http://127.0.0.1:18790, to the contract, in its bucket out")

// The fake is checked against a real store: the same contract against the
// Compose s3 profile's gateway, which make e2e-s3 runs. The credentials
// are AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or the profile's.
func TestGatewayContract(t *testing.T) {
	if *s3Endpoint == "" {
		t.Skip("no -s3.endpoint")
	}
	id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if id == "" {
		id, secret = "mapreduce", "mapreduce-secret"
	}
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(*s3Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(id, secret, ""),
	})
	prev, _ := For("s3://out")
	Register("s3", NewS3(client))
	t.Cleanup(func() { Register("s3", prev) })
	dir := fmt.Sprintf("s3://out/contract-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := DeleteAll(context.Background(), dir); err != nil {
			t.Errorf("cleaning up %s: %v", dir, err)
		}
	})
	testContract(t, dir)
}
