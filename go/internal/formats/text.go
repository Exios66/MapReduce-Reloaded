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
	r := &textReader{f: f, br: bufio.NewReader(f), pos: pos, start: s.Start, end: s.End}
	if s.Start > 0 {
		r.skipPartialLine(s.End - s.Start)
	}
	return r, nil
}

// skipPartialLine discards the line that began before the split, without
// reading past the split's end. limit is how many bytes from pos can end it,
// the newline at the latest being the split's second-to-last byte: a line
// ending later starts beyond the split, so none starts in it, and the
// reader is left empty. Without the bound, every split of a very long line
// would read the rest of it.
func (r *textReader) skipPartialLine(limit int64) {
	var read int64
	for read <= limit {
		frag, err := r.br.ReadSlice('\n')
		read += int64(len(frag))
		switch {
		case err == bufio.ErrBufferFull:
			continue
		case err != nil:
			if err != io.EOF {
				r.err = err
			}
			r.pos = r.end
			return
		case read <= limit:
			r.pos += read
			return
		}
	}
	r.pos = r.end
}

type textReader struct {
	f        io.Closer
	br       *bufio.Reader
	pos, end int64 // offset of the next unread byte; end of the split
	start    int64 // start of the split
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

// Offset returns the bytes of the split consumed, including the partial line
// before its first line.
func (r *textReader) Offset() int64 { return max(r.pos-r.start, 0) }

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
