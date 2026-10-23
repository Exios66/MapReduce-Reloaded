package formats

import (
	"errors"
	"os"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

// KV reads and writes MRKV files (docs/file-format.md). Blocks cannot be
// located without reading the file from the start, so each file is one
// split.
type KV struct{}

// Splits returns the whole file as one split, whatever splitSize is.
func (KV) Splits(path string, _ int64) ([]Split, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return nil, err
	}
	return []Split{{path, 0, info.Size()}}, nil
}

// Open returns a reader for the file of s, which must cover the whole file.
func (KV) Open(s Split) (RecordReader, error) {
	if s.Start != 0 {
		return nil, errors.New("formats: kv files cannot be split")
	}
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	return kvReader{kv.NewReader(f), f}, nil
}

type kvReader struct {
	*kv.Reader
	f *os.File
}

func (r kvReader) Close() error { return r.f.Close() }

// Create creates an MRKV output file at path.
func (KV) Create(path string) (RecordWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &kvWriter{kv.NewWriter(f), f}, nil
}

// kvWriter needs no extra buffering: kv.Writer writes whole blocks.
type kvWriter struct {
	*kv.Writer
	f *os.File
}

func (w *kvWriter) Close() error { return errors.Join(w.Writer.Close(), w.f.Close()) }
