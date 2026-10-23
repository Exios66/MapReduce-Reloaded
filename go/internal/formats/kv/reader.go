package kv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Reader reads records from an MRKV file, one block at a time, so memory use
// is bounded by the largest block. Each block's checksum is verified before
// any of its records is returned.
//
//	r := kv.NewReader(f)
//	for r.Next() {
//		use(r.Key(), r.Value())
//	}
//	if err := r.Err(); err != nil { ... }
//
// A file is only known to be complete once Next has returned false and Err
// is nil: records from earlier blocks may be returned before a later block
// turns out to be corrupt.
type Reader struct {
	r          *bufio.Reader
	block      []byte // verified payload of the current block
	pos        int    // read offset in block
	left       uint32 // records not yet returned from block
	key, value []byte
	records    uint64
	blocks     uint64
	started    bool
	err        error // sticky; io.EOF after a valid footer
}

// NewReader returns a Reader that reads an MRKV file from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r)}
}

// Next advances to the next record and reports whether there is one.
func (r *Reader) Next() bool {
	if r.err != nil {
		return false
	}
	if !r.started {
		r.started = true
		r.err = r.readHeader()
	}
	for r.err == nil && r.left == 0 {
		r.err = r.readBlock()
	}
	if r.err == nil {
		r.err = r.readRecord()
	}
	return r.err == nil
}

// Key returns the current record's key. It is valid until the next call to
// Next.
func (r *Reader) Key() []byte { return r.key }

// Value returns the current record's value. It is valid until the next call
// to Next.
func (r *Reader) Value() []byte { return r.value }

// Err returns the first error encountered, or nil if the whole file was read
// and is valid.
func (r *Reader) Err() error {
	if r.err == io.EOF {
		return nil
	}
	return r.err
}

func (r *Reader) readHeader() error {
	// The magic is checked first, so a short file that is not MRKV at all
	// reports bad_magic rather than truncated.
	var h [headerSize]byte
	if err := r.readFull(h[:4]); err != nil {
		return err
	}
	if string(h[:4]) != magic {
		return ErrBadMagic
	}
	if err := r.readFull(h[4:]); err != nil {
		return err
	}
	switch {
	case h[4] != version:
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, h[4])
	case h[5] != codecNone:
		return fmt.Errorf("%w: %d", ErrUnsupportedCodec, h[5])
	case h[6] != 0 || h[7] != 0:
		return ErrBadHeader
	}
	return nil
}

// readBlock loads and verifies the next block, or reads the footer and
// returns io.EOF when there are no more blocks.
func (r *Reader) readBlock() error {
	var h [blockHdrSize]byte
	if err := r.readFull(h[:4]); err != nil {
		return err
	}
	size := binary.LittleEndian.Uint32(h[0:])
	if size == 0 {
		return r.readFooter()
	}
	if size > MaxBlockSize {
		return fmt.Errorf("%w: block %d is %d bytes, limit %d", ErrBadBlock, r.blocks, size, MaxBlockSize)
	}
	if err := r.readFull(h[4:]); err != nil {
		return err
	}
	r.block = slices.Grow(r.block[:0], int(size))[:size]
	if err := r.readFull(r.block); err != nil {
		return err
	}
	if checksum(r.block) != binary.LittleEndian.Uint32(h[8:]) {
		return fmt.Errorf("%w: block %d", ErrChecksumMismatch, r.blocks)
	}
	r.left = binary.LittleEndian.Uint32(h[4:])
	if r.left == 0 {
		return fmt.Errorf("%w: block %d has record_count 0", ErrBadBlock, r.blocks)
	}
	r.pos = 0
	r.blocks++
	return nil
}

func (r *Reader) readRecord() error {
	key, ok := r.field()
	value, ok2 := r.field()
	if !ok || !ok2 {
		return fmt.Errorf("%w: block %d: record runs past end of payload", ErrBadBlock, r.blocks-1)
	}
	r.key, r.value = key, value
	r.records++
	r.left--
	if r.left == 0 && r.pos != len(r.block) {
		return fmt.Errorf("%w: block %d: bytes left over after last record", ErrBadBlock, r.blocks-1)
	}
	return nil
}

// field decodes one length-prefixed byte string from the current block.
func (r *Reader) field() ([]byte, bool) {
	n, k := binary.Uvarint(r.block[r.pos:])
	if k <= 0 || n > uint64(len(r.block)-r.pos-k) {
		return nil, false
	}
	start := r.pos + k
	r.pos = start + int(n)
	return r.block[start:r.pos:r.pos], true
}

func (r *Reader) readFooter() error {
	var f [footerSize - 4]byte // the end marker is already read
	if err := r.readFull(f[:]); err != nil {
		return err
	}
	if checksum(f[:16]) != binary.LittleEndian.Uint32(f[16:]) {
		return fmt.Errorf("%w: footer", ErrChecksumMismatch)
	}
	records, blocks := binary.LittleEndian.Uint64(f[0:]), binary.LittleEndian.Uint64(f[8:])
	if records != r.records || blocks != r.blocks {
		return fmt.Errorf("%w: footer says %d records in %d blocks, read %d in %d",
			ErrCountMismatch, records, blocks, r.records, r.blocks)
	}
	if _, err := r.r.ReadByte(); err != io.EOF {
		if err != nil {
			return err
		}
		return ErrTrailingData
	}
	return io.EOF
}

// readFull fills b, reporting a short read as ErrTruncated.
func (r *Reader) readFull(b []byte) error {
	_, err := io.ReadFull(r.r, b)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrTruncated
	}
	return err
}
