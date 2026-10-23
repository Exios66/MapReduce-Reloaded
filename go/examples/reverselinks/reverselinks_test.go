package reverselinks_test

import (
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/examples/reverselinks"
)

func TestReverseLinks(t *testing.T) {
	in := run.Write(t, "c\tx\na\tx\nno tab\nb\ty\nc\tx\nb\tx\n")
	got, err := run.Local(t, reverselinks.Program, []string{in}, 2, nil)
	if want := "x\ta,b,c\ny\tb\n"; err != nil || string(got) != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}
