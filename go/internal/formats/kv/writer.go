package kv

import (
	"encoding/binary"
	"errors"
	"io"
)

// Writer writes records to an MRKV file. It buffers at most one block, so
// memory use is bounded by BlockTarget plus the largest record.
//
// Errors are sticky: after a failed call, every later call returns the same
// error. Close must be called to write the footer; it does not close the
// underlying writer.
type Writer struct {
	w           io.Writer
	buf         []byte // block header followed by the payload being built
	count       uint32 // records in buf
	records     uint64
	blocks      uint64
	blockTarget int // BlockTarget; tests lower it to reproduce the golden files
	started     bool
	err         error
}

var errClosed = errors.New("mrkv: write after Close")

// NewWriter returns a Writer that writes an uncompressed MRKV file to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w, buf: make([]byte, blockHdrSize, blockHdrSize+BlockTarget), blockTarget: BlockTarget}
}

// Write appends one record. Keys and values may be empty.
func (w *Writer) Write(key, value []byte) error {
	if w.err != nil {
		return w.err
	}
	size := recordSize(key, value)
	if size > MaxBlockSize {
		return ErrRecordTooLarge // not sticky: the file is still valid
	}
	if w.count > 0 && len(w.buf)-blockHdrSize+size > MaxBlockSize {
		w.flush()
	}
	w.buf = binary.AppendUvarint(w.buf, uint64(len(key)))
	w.buf = append(w.buf, key...)
	w.buf = binary.AppendUvarint(w.buf, uint64(len(value)))
	w.buf = append(w.buf, value...)
	w.count++
	if len(w.buf)-blockHdrSize >= w.blockTarget {
		w.flush()
	}
	return w.err
}

// Close flushes the last block and writes the footer.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.count > 0 {
		w.flush()
	}
	w.start()
	var f [footerSize]byte // end marker stays 0
	binary.LittleEndian.PutUint64(f[4:], w.records)
	binary.LittleEndian.PutUint64(f[12:], w.blocks)
	binary.LittleEndian.PutUint32(f[20:], checksum(f[4:20]))
	w.write(f[:])
	if w.err == nil {
		w.err = errClosed
		return nil
	}
	return w.err
}

// flush writes the buffered block and resets the buffer.
func (w *Writer) flush() {
	payload := w.buf[blockHdrSize:]
	binary.LittleEndian.PutUint32(w.buf[0:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(w.buf[4:], w.count)
	binary.LittleEndian.PutUint32(w.buf[8:], checksum(payload))
	w.start()
	w.write(w.buf)
	w.records += uint64(w.count)
	w.blocks++
	w.buf, w.count = w.buf[:blockHdrSize], 0
}

// start writes the file header before the first block or footer.
func (w *Writer) start() {
	if !w.started {
		w.started = true
		w.write(append([]byte(magic), version, codecNone, 0, 0))
	}
}

func (w *Writer) write(b []byte) {
	if w.err == nil {
		_, w.err = w.w.Write(b)
	}
}

func recordSize(key, value []byte) int {
	return uvarintLen(len(key)) + len(key) + uvarintLen(len(value)) + len(value)
}

func uvarintLen(n int) int {
	var b [binary.MaxVarintLen64]byte
	return binary.PutUvarint(b[:], uint64(n))
}
