package kv

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// corpusRecords returns the corpus as two kinds of intermediate data: one
// record per word with the value "1", as word count's map emits, and one
// record per line keyed by its byte offset, as the text input format reads.
func corpusRecords(tb testing.TB) map[string][][2][]byte {
	tb.Helper()
	files, err := filepath.Glob("../../../../testdata/corpus/*.txt")
	if err != nil || len(files) == 0 {
		tb.Fatalf("corpus: %v %v", files, err)
	}
	recs := map[string][][2][]byte{}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			tb.Fatal(err)
		}
		for _, w := range bytes.Fields(data) {
			recs["words"] = append(recs["words"], [2][]byte{w, []byte("1")})
		}
		off := 0
		for line := range bytes.Lines(data) {
			recs["lines"] = append(recs["lines"], [2][]byte{[]byte(strconv.Itoa(off)), line})
			off += len(line)
		}
	}
	return recs
}

func encode(tb testing.TB, recs [][2][]byte, c Codec) []byte {
	tb.Helper()
	var buf bytes.Buffer
	w := NewWriterCodec(&buf, c)
	for _, r := range recs {
		if err := w.Write(r[0], r[1]); err != nil {
			tb.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// TestCompressionRatio checks that each codec keeps the corpus's
// intermediate data under a fraction of its uncompressed size: the measured
// ratio plus a margin of 0.05, so that a change that makes a codec much
// worse, or turns it off, fails here and not only in bench/README.md's
// numbers.
func TestCompressionRatio(t *testing.T) {
	limits := map[string]map[Codec]float64{ // measured: words 0.368 and 0.532, lines 0.446 and 0.673
		"words": {CodecZstd: 0.42, CodecSnappy: 0.58},
		"lines": {CodecZstd: 0.50, CodecSnappy: 0.72},
	}
	for kind, recs := range corpusRecords(t) {
		plain := len(encode(t, recs, CodecNone))
		for _, c := range []Codec{CodecZstd, CodecSnappy} {
			t.Run(fmt.Sprint(kind, "/", c), func(t *testing.T) {
				ratio := float64(len(encode(t, recs, c))) / float64(plain)
				t.Logf("%d records, %d bytes uncompressed, ratio %.3f", len(recs), plain, ratio)
				if ratio > limits[kind][c] {
					t.Errorf("ratio %.3f, want at most %.2f", ratio, limits[kind][c])
				}
			})
		}
	}
}

func BenchmarkWrite(b *testing.B) {
	recs := corpusRecords(b)["lines"]
	for _, c := range []Codec{CodecNone, CodecZstd, CodecSnappy} {
		b.Run(c.String(), func(b *testing.B) {
			b.SetBytes(int64(len(encode(b, recs, CodecNone))))
			for b.Loop() {
				encode(b, recs, c)
			}
		})
	}
}

func BenchmarkRead(b *testing.B) {
	recs := corpusRecords(b)["lines"]
	for _, c := range []Codec{CodecNone, CodecZstd, CodecSnappy} {
		b.Run(c.String(), func(b *testing.B) {
			b.SetBytes(int64(len(encode(b, recs, CodecNone))))
			data := encode(b, recs, c)
			for b.Loop() {
				r := NewReader(bytes.NewReader(data))
				for r.Next() {
				}
				if err := r.Err(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
