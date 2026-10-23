package formats

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type record struct{ key, value string }

// readSplits reads every split of path in order.
func readSplits(t *testing.T, f InputFormat, path string, splitSize int64) []record {
	t.Helper()
	splits, err := f.Splits(path, splitSize)
	if err != nil {
		t.Fatal(err)
	}
	var out []record
	for _, s := range splits {
		r, err := f.Open(s)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			out = append(out, record{string(r.Key()), string(r.Value())})
		}
		if err := errors.Join(r.Err(), r.Close()); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// lines is the obvious reading of content as text records: split after each
// "\n", keyed by offset.
func lines(content []byte) []record {
	var out []record
	for off := 0; off < len(content); {
		n := bytes.IndexByte(content[off:], '\n') + 1
		if n == 0 {
			n = len(content) - off
		}
		text := bytes.TrimSuffix(bytes.TrimSuffix(content[off:off+n], []byte("\n")), []byte("\r"))
		out = append(out, record{strconv.Itoa(off), string(text)})
		off += n
	}
	return out
}

func writeFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// FuzzTextSplits checks that splitting at any size reads every line exactly
// once, at the right offset.
func FuzzTextSplits(f *testing.F) {
	f.Add([]byte("one\ntwo\r\n\nthree"), uint8(1))
	f.Add([]byte("\n\n\n"), uint8(2))
	f.Add(bytes.Repeat([]byte("a long line "), 500), uint8(7))
	f.Fuzz(func(t *testing.T, content []byte, splitSize uint8) {
		got := readSplits(t, Text{}, writeFile(t, content), int64(splitSize)+1)
		if want := lines(content); !slices.Equal(got, want) {
			t.Fatalf("split reading gave %q, want %q", got, want)
		}
	})
}

func TestTextLongLine(t *testing.T) {
	long := strings.Repeat("x", 10_000) // longer than bufio's buffer
	got := readSplits(t, Text{}, writeFile(t, []byte(long+"\nshort\n")), 3000)
	if want := []record{{"0", long}, {"10001", "short"}}; !slices.Equal(got, want) {
		t.Fatalf("got %d lines, want the long line and \"short\"", len(got))
	}
}

func TestSplits(t *testing.T) {
	if s, err := (Text{}).Splits(writeFile(t, nil), 10); err != nil || s != nil {
		t.Fatalf("empty file: %v, %v; want no splits", s, err)
	}
	if _, err := (Text{}).Splits(writeFile(t, []byte("x")), 0); err == nil {
		t.Fatal("split size 0 accepted")
	}
	s, err := (Text{}).Splits(writeFile(t, make([]byte, 25)), 10)
	if err != nil || len(s) != 3 || s[2] != (Split{s[2].Path, 20, 25}) {
		t.Fatalf("25 bytes in 10-byte splits: %+v, %v", s, err)
	}
	if s, err := (KV{}).Splits(writeFile(t, make([]byte, 25)), 10); err != nil || len(s) != 1 {
		t.Fatalf("kv: %+v, %v; want one split", s, err)
	}
}

func TestLookup(t *testing.T) {
	if f, err := Input(""); err != nil || f != (Text{}) {
		t.Fatalf(`Input("") = %v, %v; want Text`, f, err)
	}
	if _, err := Output("parquet"); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestOutputRoundTrip(t *testing.T) {
	recs := []record{{"apple", "3"}, {"pear", "1"}}
	tests := map[string][]record{
		"text": {{"0", "apple\t3"}, {"8", "pear\t1"}}, // read back as lines
		"kv":   recs,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			out, _ := Output(name)
			in, _ := Input(name)
			path := filepath.Join(t.TempDir(), "part-r-00000")
			w, err := out.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range recs {
				if err := w.Write([]byte(r.key), []byte(r.value)); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if got := readSplits(t, in, path, 4); !slices.Equal(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}
