package kv

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
)

// Codec is the compression applied to every block payload in a file.
type Codec uint8

// The codecs in docs/file-format.md.
const (
	CodecNone   Codec = 0
	CodecZstd   Codec = 1 // one zstd frame per block
	CodecSnappy Codec = 2 // Snappy's block format
)

var codecNames = []string{CodecNone: "none", CodecZstd: "zstd", CodecSnappy: "snappy"}

func (c Codec) String() string {
	if int(c) < len(codecNames) {
		return codecNames[c]
	}
	return fmt.Sprintf("codec(%d)", uint8(c))
}

// ParseCodec returns the codec named s: "none", "zstd" or "snappy".
func ParseCodec(s string) (Codec, error) {
	if i := slices.Index(codecNames, s); i >= 0 {
		return Codec(i), nil
	}
	return 0, fmt.Errorf("%w: %q", ErrUnsupportedCodec, s)
}

func (c Codec) valid() bool { return int(c) < len(codecNames) }

// One encoder and one decoder serve every file: their single-shot methods
// are safe for concurrent use.
var (
	zstdEncoder = sync.OnceValue(func() *zstd.Encoder {
		// A single-segment frame always records its content size.
		e, err := zstd.NewWriter(nil, zstd.WithSingleSegment(true))
		if err != nil {
			panic(err) // only for invalid options
		}
		return e
	})
	zstdDecoder = sync.OnceValue(func() *zstd.Decoder {
		d, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(MaxBlockSize))
		if err != nil {
			panic(err)
		}
		return d
	})
)

// compress appends payload, compressed with c, to dst.
func compress(c Codec, dst, payload []byte) []byte {
	switch c {
	case CodecZstd:
		return zstdEncoder().EncodeAll(payload, dst)
	case CodecSnappy:
		n := len(dst)
		dst = slices.Grow(dst, s2.MaxEncodedLen(len(payload)))
		return dst[:n+len(s2.EncodeSnappy(dst[n:cap(dst)], payload))]
	}
	return append(dst, payload...)
}

// decompress decodes a stored payload into dst, refusing to produce more
// than MaxBlockSize bytes.
func decompress(c Codec, dst, stored []byte) ([]byte, error) {
	switch c {
	case CodecZstd:
		// The payload must be exactly one frame that records its content size.
		var h zstd.Header
		if err := h.Decode(stored); err != nil {
			return nil, err
		}
		if h.Skippable || !h.HasFCS || h.DictionaryID != 0 {
			return nil, errors.New("not a frame with a content size and no dictionary")
		}
		if h.FrameContentSize > MaxBlockSize {
			return nil, fmt.Errorf("frame declares %d bytes, limit %d", h.FrameContentSize, MaxBlockSize)
		}
		if n, err := frameLen(stored, &h); err != nil {
			return nil, err
		} else if n != len(stored) {
			return nil, errors.New("bytes follow the frame")
		}
		out, err := zstdDecoder().DecodeAll(stored, dst[:0])
		if err == nil && uint64(len(out)) != h.FrameContentSize {
			err = errors.New("frame does not match its content size")
		}
		return out, err
	case CodecSnappy:
		n, err := s2.DecodedLen(stored)
		if err != nil {
			return nil, err
		}
		if n > MaxBlockSize {
			return nil, fmt.Errorf("decodes to %d bytes, limit %d", n, MaxBlockSize)
		}
		return s2.Decode(slices.Grow(dst[:0], n), stored)
	}
	return stored, nil
}

// frameLen returns the length of the zstd frame at the start of b, found by
// walking its block headers (RFC 8878, section 3.1.1).
func frameLen(b []byte, h *zstd.Header) (int, error) {
	n := h.HeaderSize
	for {
		if n+3 > len(b) {
			return 0, errors.New("frame runs past end of payload")
		}
		bh := int(b[n]) | int(b[n+1])<<8 | int(b[n+2])<<16
		size := bh >> 3
		if bh>>1&3 == 1 { // an RLE block stores one byte
			size = 1
		}
		n += 3 + size
		if bh&1 == 1 { // the last block
			break
		}
	}
	if h.HasCheckSum {
		n += 4
	}
	return n, nil
}
