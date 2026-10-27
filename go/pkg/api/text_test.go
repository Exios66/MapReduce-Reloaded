package api_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

func isSpace(r rune) bool { return r == ' ' || '\t' <= r && r <= '\r' }

// Words splits on ASCII whitespace only, as bytes.FieldsFunc does with the
// same test, on any bytes.
func TestWords(t *testing.T) {
	for _, line := range []string{
		"", " ", "a", "  a b\tc\n", "a\vb\fc\rd", "x y\u0085z", "\xff \xfe\xfd", "ünï cödé  ",
	} {
		got, want := api.Words([]byte(line)), bytes.FieldsFunc([]byte(line), isSpace)
		if !slices.EqualFunc(got, want, bytes.Equal) {
			t.Errorf("Words(%q) = %q, want %q", line, got, want)
		}
	}
}

func FuzzWords(f *testing.F) {
	f.Add([]byte("a b\tc"))
	f.Fuzz(func(t *testing.T, line []byte) {
		if got, want := api.Words(line), bytes.FieldsFunc(line, isSpace); !slices.EqualFunc(got, want, bytes.Equal) {
			t.Errorf("Words(%q) = %q, want %q", line, got, want)
		}
	})
}

type collect [][2]string

func (c *collect) Emit(k, v []byte) error {
	*c = append(*c, [2]string{string(k), string(v)})
	return nil
}

func TestSum(t *testing.T) {
	var out collect
	if err := api.Sum(nil, []byte("w"), slices.Values([][]byte{[]byte("2"), []byte("40")}), &out); err != nil {
		t.Fatal(err)
	}
	if want := (collect{{"w", "42"}}); !slices.Equal(out, want) {
		t.Errorf("Sum emitted %v, want %v", out, want)
	}
	out = nil
	if err := api.Sum(nil, []byte("w"), slices.Values([][]byte{[]byte("x")}), &out); err == nil || len(out) != 0 {
		t.Errorf("Sum of a bad count: err %v, emitted %v", err, out)
	}
}
