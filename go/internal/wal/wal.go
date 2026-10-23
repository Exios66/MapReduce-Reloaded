// Package wal is an append-only log of records, kept in one file. The
// coordinator writes each decision to it before acting on it, so that a
// restarted coordinator can replay them (§3.3).
//
// Each record is framed as a little-endian uint32 length, a CRC-32C of the
// payload, and the payload. Every append is synced to disk before it
// returns. A crash can leave the last record incomplete: Open drops such a
// torn tail, but reports damage anywhere else as an error.
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
	f   *os.File
	err error // sticky: after a failed append, the file's end is unknown
}

// Open opens the log at path, creating it if needed, and calls replay with
// each record's payload in order. The payload is only valid during the
// call. A torn last record is removed from the file.
func Open(path string, replay func(payload []byte) error) (*Log, error) {
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
	return &Log{f: f}, nil
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
	buf := make([]byte, headerSize, headerSize+len(payload))
	binary.LittleEndian.PutUint32(buf, uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[4:], crc32.Checksum(payload, crc))
	if _, l.err = l.f.Write(append(buf, payload...)); l.err == nil {
		l.err = l.f.Sync()
	}
	return l.err
}

// Close closes the log file.
func (l *Log) Close() error { return l.f.Close() }
