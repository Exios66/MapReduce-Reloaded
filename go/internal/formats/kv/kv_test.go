package kv

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/iotest"
)

const formatDir = "../../../../testdata/format"

// kinds maps the spec's error kinds, as named in vectors.json, to errors.
var kinds = map[string]error{
	"bad_magic":           ErrBadMagic,
	"unsupported_version": ErrUnsupportedVersion,
	"unsupported_codec":   ErrUnsupportedCodec,
	"bad_header":          ErrBadHeader,
	"checksum_mismatch":   ErrChecksumMismatch,
	"bad_block":           ErrBadBlock,
	"truncated":           ErrTruncated,
	"count_mismatch":      ErrCountMismatch,
	"trailing_data":       ErrTrailingData,
}

type record struct{ key, value []byte }

type vector struct {
	File        string     `json:"file"`
	BlockTarget int        `json:"block_target"`
	Records     [][]string `json:"records"`
	Error       string     `json:"error"`
}

func loadVectors(t *testing.T) map[string]vector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(formatDir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vs map[string]vector
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	return vs
}

func (v vector) records(t *testing.T) []record {
	t.Helper()
	out := make([]record, len(v.Records))
	for i, kv := range v.Records {
		k, err1 := base64.StdEncoding.DecodeString(kv[0])
		val, err2 := base64.StdEncoding.DecodeString(kv[1])
		if err := errors.Join(err1, err2); err != nil {
			t.Fatal(err)
		}
		out[i] = record{k, val}
	}
	return out
}

// readAll returns every record the reader yields, copied, and its final error.
func readAll(r io.Reader) ([]record, error) {
	kr := NewReader(r)
	var out []record
	for kr.Next() {
		out = append(out, record{slices.Clone(kr.Key()), slices.Clone(kr.Value())})
	}
	return out, kr.Err()
}

func writeAll(t *testing.T, recs []record, blockTarget int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.blockTarget = blockTarget
	for _, r := range recs {
		if err := w.Write(r.key, r.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func equalRecords(a, b []record) bool {
	return slices.EqualFunc(a, b, func(x, y record) bool {
		return bytes.Equal(x.key, y.key) && bytes.Equal(x.value, y.value)
	})
}

func TestGoldenVectors(t *testing.T) {
	for name, v := range loadVectors(t) {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(formatDir, v.File))
			if err != nil {
				t.Fatal(err)
			}
			got, err := readAll(iotest.OneByteReader(bytes.NewReader(data)))
			if v.Error != "" {
				if !errors.Is(err, kinds[v.Error]) {
					t.Fatalf("read error = %v, want %s", err, v.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := v.records(t)
			if !equalRecords(got, want) {
				t.Fatalf("read %d records, want %d (or contents differ)", len(got), len(want))
			}
			if out := writeAll(t, want, v.BlockTarget); !bytes.Equal(out, data) {
				t.Fatalf("writer output differs from %s", v.File)
			}
		})
	}
}

// Builders for hand-made malformed files.

func header(version, codec, r0, r1 byte) []byte {
	return []byte{'M', 'R', 'K', 'V', version, codec, r0, r1}
}

func block(count uint32, payload []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))
	b = binary.LittleEndian.AppendUint32(b, count)
	b = binary.LittleEndian.AppendUint32(b, checksum(payload))
	return append(b, payload...)
}

func footer(records, blocks uint64) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 0)
	b = binary.LittleEndian.AppendUint64(b, records)
	b = binary.LittleEndian.AppendUint64(b, blocks)
	return binary.LittleEndian.AppendUint32(b, checksum(b[4:]))
}

func file(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestMalformed(t *testing.T) {
	ok := header(1, 0, 0, 0)
	rec := []byte{1, 'k', 1, 'v'}
	hugeLen := binary.LittleEndian.AppendUint32(nil, 0xFFFFFFFF)
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"empty input", nil, ErrTruncated},
		{"short header", []byte("MRKV"), ErrTruncated},
		{"short non-MRKV", []byte("PK\x03\x04\x14"), ErrBadMagic},
		{"version 2", file(header(2, 0, 0, 0), footer(0, 0)), ErrUnsupportedVersion},
		{"zstd codec", file(header(1, 1, 0, 0), footer(0, 0)), ErrUnsupportedCodec},
		{"reserved set", file(header(1, 0, 0, 1), footer(0, 0)), ErrBadHeader},
		{"no footer", ok, ErrTruncated},
		{"zero record count", file(ok, block(0, rec), footer(0, 1)), ErrBadBlock},
		{"count too high", file(ok, block(2, rec), footer(2, 1)), ErrBadBlock},
		{"leftover bytes", file(ok, block(1, append(rec, 0)), footer(1, 1)), ErrBadBlock},
		{"length past payload", file(ok, block(1, []byte{9, 'k', 0}), footer(1, 1)), ErrBadBlock},
		{"varint overflow", file(ok, block(1, bytes.Repeat([]byte{0xFF}, 11)), footer(1, 1)), ErrBadBlock},
		{"block over limit", file(ok, hugeLen), ErrBadBlock},
		{"block past end", file(ok, block(1, rec)[:14]), ErrTruncated},
		{"short footer", file(ok, footer(0, 0)[:10]), ErrTruncated},
		{"footer checksum", file(ok, footer(0, 0)[:23], []byte{0}), ErrChecksumMismatch},
		{"block count wrong", file(ok, block(1, rec), footer(1, 2)), ErrCountMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readAll(bytes.NewReader(tt.data)); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCorruptBlockCheckedBeforeCount(t *testing.T) {
	b := block(0, []byte{0, 0})
	b[8] ^= 1 // corrupt the checksum of a block that also has record_count 0
	_, err := readAll(bytes.NewReader(file(header(1, 0, 0, 0), b, footer(0, 1))))
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want checksum_mismatch", err)
	}
}

// TestStreamsGoodBlocks checks that records from intact blocks are returned
// before a later problem is reported, and that the error is sticky.
func TestStreamsGoodBlocks(t *testing.T) {
	recs := []record{{[]byte("a"), []byte("1")}, {[]byte("b"), []byte("2")}}
	data := writeAll(t, recs, 1) // one record per block
	r := NewReader(bytes.NewReader(data[:len(data)-1]))
	n := 0
	for r.Next() {
		n++
	}
	if n != len(recs) || !errors.Is(r.Err(), ErrTruncated) {
		t.Fatalf("got %d records and %v, want %d and truncated", n, r.Err(), len(recs))
	}
	if r.Next() {
		t.Fatal("Next returned true after an error")
	}
}

func TestReadErrorPassesThrough(t *testing.T) {
	boom := errors.New("disk on fire")
	if _, err := readAll(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}

func TestWriterBlocks(t *testing.T) {
	big := bytes.Repeat([]byte("x"), BlockTarget+1)
	recs := []record{{[]byte("a"), nil}, {[]byte("big"), big}, {nil, nil}}
	data := writeAll(t, recs, BlockTarget)
	got, err := readAll(bytes.NewReader(data))
	if err != nil || !equalRecords(got, recs) {
		t.Fatalf("round trip failed: %v", err)
	}
	// "a" and the big record fill the first block; the empty record gets a second.
	if blocks := binary.LittleEndian.Uint64(data[len(data)-12:]); blocks != 2 {
		t.Fatalf("block count = %d, want 2", blocks)
	}
}

func TestWriterErrors(t *testing.T) {
	w := NewWriter(io.Discard)
	if err := w.Write(make([]byte, MaxBlockSize), nil); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("oversized record: %v, want ErrRecordTooLarge", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(nil, nil); err == nil {
		t.Fatal("Write after Close succeeded")
	}

	boom := errors.New("disk full")
	w = NewWriter(failWriter{boom})
	w.blockTarget = 1
	if err := w.Write([]byte("k"), nil); !errors.Is(err, boom) {
		t.Fatalf("Write error = %v, want %v", err, boom)
	}
	if err := w.Close(); !errors.Is(err, boom) {
		t.Fatalf("Close error = %v, want the sticky %v", err, boom)
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("hello\x00world"), uint8(3), uint16(64))
	f.Fuzz(func(t *testing.T, data []byte, split uint8, target uint16) {
		// Cut data into records of varying size, alternating key and value.
		var recs []record
		for len(data) > 0 {
			n := min(int(split)%17, len(data))
			recs = append(recs, record{data[:n], data[n:min(2*n, len(data))]})
			data = data[min(2*n+1, len(data)):]
		}
		got, err := readAll(bytes.NewReader(writeAll(t, recs, int(target)+1)))
		if err != nil || !equalRecords(got, recs) {
			t.Fatalf("round trip of %d records failed: %v", len(recs), err)
		}
	})
}

// FuzzReader feeds arbitrary bytes to the reader. It must not panic, and any
// file it accepts must round-trip through the writer.
func FuzzReader(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join(formatDir, "*.mrkv"))
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		recs, err := readAll(bytes.NewReader(data))
		if err != nil {
			return
		}
		got, err := readAll(bytes.NewReader(writeAll(t, recs, BlockTarget)))
		if err != nil || !equalRecords(got, recs) {
			t.Fatalf("accepted file does not round-trip: %v", err)
		}
	})
}
