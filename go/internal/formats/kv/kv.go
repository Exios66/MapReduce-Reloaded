// Package kv reads and writes MRKV files, the on-disk format for intermediate
// and key/value output data. The format is specified in docs/file-format.md.
package kv

import (
	"errors"
	"hash/crc32"
)

const (
	magic        = "MRKV"
	version      = 1
	codecNone    = 0
	headerSize   = 8
	blockHdrSize = 12
	footerSize   = 24

	// BlockTarget is the decompressed payload size at which the writer
	// closes a block.
	BlockTarget = 64 << 10

	// MaxBlockSize bounds a block's payload, and therefore a single record.
	// It caps the reader's allocation when a length field is corrupt.
	MaxBlockSize = 64 << 20
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func checksum(b []byte) uint32 { return crc32.Checksum(b, castagnoli) }

// Errors returned by Reader, one per error kind in the spec. They are wrapped
// with detail, so compare with errors.Is.
var (
	ErrBadMagic           = errors.New("mrkv: bad_magic")
	ErrUnsupportedVersion = errors.New("mrkv: unsupported_version")
	ErrUnsupportedCodec   = errors.New("mrkv: unsupported_codec")
	ErrBadHeader          = errors.New("mrkv: bad_header")
	ErrChecksumMismatch   = errors.New("mrkv: checksum_mismatch")
	ErrBadBlock           = errors.New("mrkv: bad_block")
	ErrTruncated          = errors.New("mrkv: truncated")
	ErrCountMismatch      = errors.New("mrkv: count_mismatch")
	ErrTrailingData       = errors.New("mrkv: trailing_data")
)

// ErrRecordTooLarge is returned by Writer.Write for a record whose encoding
// exceeds MaxBlockSize.
var ErrRecordTooLarge = errors.New("mrkv: record larger than MaxBlockSize")
