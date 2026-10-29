package papersort_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/examples/papersort"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

func TestPaperSort(t *testing.T) {
	low := append([]byte("\x00\tabcdefgh"), bytes.Repeat([]byte("l"), 89)...)
	high := append([]byte("\xffabcdefghi"), bytes.Repeat([]byte("h"), 89)...)
	in := run.Write(t, string(high)+"\n"+string(low)+"\n")
	out := t.TempDir()
	if _, err := local.Run(api.JobSpec{Program: papersort.Program, Inputs: []string{in}, Output: out, SplitSize: 100, R: 8}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for r := range 8 {
		b, err := os.ReadFile(filepath.Join(out, fmt.Sprintf("part-r-%05d", r)))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, b...)
	}
	want := append(append(append([]byte{}, low[:10]...), '\t'), low...)
	want = append(want, '\n')
	want = append(want, high[:10]...)
	want = append(want, '\t')
	want = append(want, high...)
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatal("output is not sorted key-plus-original-record pairs")
	}
}

type emitter func([]byte, []byte) error

func (e emitter) Emit(k, v []byte) error { return e(k, v) }

func TestMapErrors(t *testing.T) {
	if err := papersort.Map(nil, nil, []byte("short"), nil); err == nil {
		t.Fatal("accepted a short benchmark record")
	}
	want := errors.New("write failed")
	if err := papersort.Map(nil, nil, make([]byte, 99), emitter(func(_, _ []byte) error { return want })); !errors.Is(err, want) {
		t.Fatalf("emitter error = %v, want %v", err, want)
	}
}
