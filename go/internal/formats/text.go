package formats

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
)

// Text reads and writes lines of text.
//
// As input, each line is a record whose key is the line's byte offset in the
// file, in decimal, and whose value is the line without its "\n" or "\r\n".
// A line belongs to the split in which it starts, so every line is read
// exactly once however the file is split.
//
// As output, each record is written as key, a tab, value and "\n".
type Text struct{}

// Splits cuts the file into byte ranges; Open aligns them to lines.
func (Text) Splits(ctx context.Context, path string, splitSize int64) ([]Split, error) {
	return byteSplits(ctx, path, splitSize)
}

// Open returns a reader for the lines that start in s.
func (Text) Open(ctx context.Context, s Split) (RecordReader, error) {
	// Start one byte early: if that byte ends a line, the line at s.Start is
	// ours; otherwise the partial line belongs to the previous split.
	pos := max(s.Start-1, 0)
	f, err := storage.Open(ctx, s.Path, pos)
	if err != nil {
		return nil, err
	}
	r := &textReader{f: f, br: bufio.NewReader(f), pos: pos, end: s.End}
	if s.Start > 0 {
		r.readLine()
	}
	return r, nil
}

type textReader struct {
	f        io.Closer
	br       *bufio.Reader
	pos, end int64 // offset of the next unread byte; end of the split
	key      []byte
	line     []byte // the current line, including its terminator
	err      error
}

func (r *textReader) Next() bool {
	if r.err != nil || r.pos >= r.end {
		return false
	}
	start := r.pos
	if !r.readLine() {
		return false
	}
	r.key = strconv.AppendInt(r.key[:0], start, 10)
	return true
}

// readLine reads the next line into r.line and reports whether there was one.
func (r *textReader) readLine() bool {
	r.line = r.line[:0]
	for {
		frag, err := r.br.ReadSlice('\n')
		r.line = append(r.line, frag...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			r.err = err
		}
		r.pos += int64(len(r.line))
		return len(r.line) > 0 && r.err == nil
	}
}

func (r *textReader) Key() []byte { return r.key }

func (r *textReader) Value() []byte {
	line := bytes.TrimSuffix(r.line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r"))
}

func (r *textReader) Err() error   { return r.err }
func (r *textReader) Close() error { return r.f.Close() }

// Create creates a text output file at path.
func (Text) Create(ctx context.Context, path string) (RecordWriter, error) {
	f, err := storage.Create(ctx, path)
	if err != nil {
		return nil, err
	}
	return &textWriter{f: f, bw: bufio.NewWriter(f)}, nil
}

type textWriter struct {
	f  io.Closer
	bw *bufio.Writer
}

func (w *textWriter) Write(key, value []byte) error {
	// bufio.Writer errors are sticky, so checking the last write suffices.
	_, _ = w.bw.Write(key)
	_ = w.bw.WriteByte('\t')
	_, _ = w.bw.Write(value)
	return w.bw.WriteByte('\n')
}

func (w *textWriter) Close() error { return errors.Join(w.bw.Flush(), w.f.Close()) }
