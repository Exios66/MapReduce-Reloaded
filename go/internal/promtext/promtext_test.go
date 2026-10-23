package promtext_test

import (
	"bytes"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/promtext"
)

func TestFamily(t *testing.T) {
	var b bytes.Buffer
	promtext.Family(&b, "mr_x_total", "counter", "Xs seen.")
	if want := "# HELP mr_x_total Xs seen.\n# TYPE mr_x_total counter\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestLabel(t *testing.T) {
	if got, want := promtext.Label("a\\b\"c\nd"), `"a\\b\"c\nd"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
