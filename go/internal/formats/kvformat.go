package formats

import (
	"context"
	"errors"
	"io"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
)

// KV reads and writes MRKV files (docs/file-format.md). Blocks cannot be
// located without reading the file from the start, so each file is one
// split.
type KV struct {
	// Codec compresses the blocks of files it creates. Readers take the
	// codec from each file's header.
	Codec kv.Codec
}

// Splits returns the whole file as one split, whatever splitSize is.
func (KV) Splits(ctx context.Context, path string, _ int64) ([]Split, error) {
	size, err := storage.Size(ctx, path)
	if err != nil || size == 0 {
		return nil, err
	}
	return []Split{{path, 0, size}}, nil
}

// Open returns a reader for the file of s, which must cover the whole file.
func (KV) Open(ctx context.Context, s Split) (RecordReader, error) {
	if s.Start != 0 {
		return nil, errors.New("formats: kv files cannot be split")
	}
	f, err := storage.Open(ctx, s.Path, 0)
	if err != nil {
		return nil, err
	}
	c := &counted{ReadCloser: f}
	return kvReader{kv.NewReader(c), c}, nil
}

type kvReader struct {
	*kv.Reader
	f *counted
}

func (r kvReader) Close() error { return r.f.Close() }

// Offset returns the bytes of the file read so far.
func (r kvReader) Offset() int64 { return r.f.n }

// counted counts the bytes read from a file.
type counted struct {
	io.ReadCloser
	n int64
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}

// Create creates an MRKV output file at path.
func (k KV) Create(ctx context.Context, path string) (RecordWriter, error) {
	f, err := storage.Create(ctx, path)
	if err != nil {
		return nil, err
	}
	return &kvWriter{kv.NewWriterCodec(f, k.Codec), f}, nil
}

// kvWriter needs no extra buffering: kv.Writer writes whole blocks.
type kvWriter struct {
	*kv.Writer
	f io.Closer
}

func (w *kvWriter) Close() error { return errors.Join(w.Writer.Close(), w.f.Close()) }
