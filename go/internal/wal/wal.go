// Package wal is an append-only log of records, kept in one file. The
// coordinator writes each decision to it before acting on it, so that a
// restarted coordinator can replay them (§3.3).
//
// Each record is framed as a little-endian uint32 length, a CRC-32C of the
// payload, and the payload. Every append is synced to disk before it
// returns. A crash can leave the last record incomplete: Open drops such a
// torn tail, but reports damage anywhere else as an error.
//
// Compact rewrites the log with only the records a caller keeps, into a
// temporary file that replaces the log by a rename, so a crash leaves
// either the old log or the new one, never a mix.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
)

const headerSize = 8

var crc = crc32.MakeTable(crc32.Castagnoli)

// ErrCorrupt is returned by Open for a record that is damaged but is not
// the last one in the file.
var ErrCorrupt = errors.New("wal: corrupt record")

// Log is an open log file, positioned to append.
type Log struct {
	path string
	f    *os.File
	err  error // sticky: after a failed append, the file's end is unknown
}

// compactSuffix names the temporary file Compact writes next to the log.
const compactSuffix = ".compact"

// Open opens the log at path, creating it if needed, and calls replay with
// each record's payload in order. The payload is only valid during the
// call. A torn last record is removed from the file.
func Open(path string, replay func(payload []byte) error) (*Log, error) {
	// A compaction that crashed before its rename left the log untouched.
	if err := os.Remove(path + compactSuffix); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	end, err := readAll(f, replay)
	if err == nil {
		err = truncate(f, end)
	}
	if err == nil {
		err = syncDir(path)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &Log{path: path, f: f}, nil
}

// readAll replays f's records and returns the offset where the valid
// records end.
func readAll(f *os.File, replay func([]byte) error) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	var off int64
	var h [headerSize]byte
	var payload []byte
	for off < size {
		if size-off < headerSize {
			return off, nil // torn header
		}
		if _, err := f.ReadAt(h[:], off); err != nil {
			return 0, err
		}
		n, sum := binary.LittleEndian.Uint32(h[:]), binary.LittleEndian.Uint32(h[4:])
		next := off + headerSize + int64(n)
		if next > size {
			return off, nil // torn payload
		}
		payload = slices.Grow(payload[:0], int(n))[:n]
		if _, err := f.ReadAt(payload, off+headerSize); err != nil {
			return 0, err
		}
		if n == 0 || crc32.Checksum(payload, crc) != sum { // Append never writes empty records
			if next == size {
				return off, nil // torn: the last record was not fully written
			}
			if zero, err := zeroed(f, off, size); err != nil || zero {
				return off, err // torn: the file system extended the file with zeros
			}
			return 0, fmt.Errorf("%w at offset %d", ErrCorrupt, off)
		}
		if err := replay(payload); err != nil {
			return 0, err
		}
		off = next
	}
	return off, nil
}

// zeroed reports whether f holds only zero bytes from off to size.
func zeroed(f *os.File, off, size int64) (bool, error) {
	buf := make([]byte, 32<<10)
	for off < size {
		n, err := f.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(buf[:n], func(b byte) bool { return b != 0 }) {
			return false, nil
		}
		off += int64(n)
	}
	return true, nil
}

func truncate(f *os.File, end int64) error {
	if err := f.Truncate(end); err != nil {
		return err
	}
	_, err := f.Seek(end, io.SeekStart)
	return err
}

// syncDir makes the log file's directory entry durable.
func syncDir(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// Append writes a non-empty record and syncs it to disk. After an error,
// every later Append fails too.
func (l *Log) Append(payload []byte) error {
	if len(payload) == 0 || len(payload) > math.MaxUint32 {
		return fmt.Errorf("wal: record of %d bytes", len(payload))
	}
	if l.err != nil {
		return l.err
	}
	if l.err = l.write(payload); l.err == nil {
		l.err = l.f.Sync()
	}
	return l.err
}

// Compact rewrites the log with the records for which keep returns true,
// in order, then appends go on after them. If it fails before the new log
// replaces the old one, the old log is left as it was and stays usable; if
// it fails after, every later Append fails too. keep must not use the
// payload after it returns.
func (l *Log) Compact(keep func(payload []byte) (bool, error)) (err error) {
	if l.err != nil {
		return l.err
	}
	tmp := l.path + compactSuffix
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
		}
	}()
	w := &Log{f: out}
	if _, err = readAll(l.f, func(payload []byte) error {
		ok, err := keep(payload)
		if err == nil && ok {
			err = w.write(payload)
		}
		return err
	}); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, l.path); err != nil {
		return err
	}
	// The new log is in place; from here a failure leaves the file's end
	// unknown, so the log refuses further appends.
	l.err = syncDir(l.path)
	if l.err == nil {
		var f *os.File
		if f, l.err = os.OpenFile(l.path, os.O_RDWR, 0); l.err == nil {
			if _, l.err = f.Seek(0, io.SeekEnd); l.err != nil {
				_ = f.Close()
			} else {
				_ = l.f.Close()
				l.f = f
			}
		}
	}
	return l.err
}

// write frames payload and writes it, without syncing.
func (l *Log) write(payload []byte) error {
	if len(payload) == 0 || len(payload) > math.MaxUint32 {
		return fmt.Errorf("wal: record of %d bytes", len(payload))
	}
	buf := make([]byte, headerSize, headerSize+len(payload))
	binary.LittleEndian.PutUint32(buf, uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[4:], crc32.Checksum(payload, crc))
	_, err := l.f.Write(append(buf, payload...))
	return err
}

// Close closes the log file.
func (l *Log) Close() error { return l.f.Close() }
