package termvector_test

import (
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/examples/termvector"
)

const docs = "http://a.com/x\tthe cat the\n" +
	"http://a.com/y\tthe dog\n" +
	"https://b.org\tcat cat\n" +
	"http://a.com?q=1\tthe\n" + // a query or fragment ends the host too
	"https://b.org#top\tcat\n" +
	"no tab\n" +
	"nohost\tword\n"

func TestTermVector(t *testing.T) {
	for _, tc := range []struct {
		params map[string]string
		want   string
	}{
		{nil, "a.com\tthe:4\nb.org\tcat:3\n"},
		{map[string]string{"min": "1"}, "a.com\tthe:4 cat:1 dog:1\nb.org\tcat:3\n"},
	} {
		got, err := run.Local(t, termvector.Program, []string{run.Write(t, docs)}, 2, tc.params)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("params %v: got %q, want %q", tc.params, got, tc.want)
		}
	}
}

func TestBadMin(t *testing.T) {
	_, err := run.Local(t, termvector.Program, []string{run.Write(t, docs)}, 1, map[string]string{"min": "0"})
	if err == nil {
		t.Fatal("min=0 was accepted")
	}
}

// A term with a Unicode space in it, which is not a word boundary,
// survives the combiner and the reducer.
func TestUnicodeSpaceInTerm(t *testing.T) {
	in := "http://h/\ta\u00a0b a\u00a0b c\u0085\n"
	got, err := run.Local(t, termvector.Program, []string{run.Write(t, in)}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "h\ta\u00a0b:2\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
