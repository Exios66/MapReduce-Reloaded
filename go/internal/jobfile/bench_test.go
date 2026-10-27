package jobfile

import (
	"bytes"
	"os"
	"testing"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

type count struct{ n int }

func (c *count) Emit(_, _ []byte) error { c.n++; return nil }

// benchPrograms returns the compiled wordcount, the example job file and
// E5's version of it without the compiled helpers.
func benchPrograms(b *testing.B) map[string]api.Program {
	ps := map[string]api.Program{}
	ps["compiled"], _ = api.Lookup("wordcount")
	for name, file := range map[string]string{"example": example, "naive": naive} {
		src, err := os.ReadFile(file)
		if err != nil {
			b.Fatal(err)
		}
		p, err := Load(string(src))
		if err != nil {
			b.Fatal(err)
		}
		ps[name] = p
	}
	return ps
}

// BenchmarkMap runs each word count's Map over the lines of the file
// $JOBFILE_BENCH_INPUT, e.g. made by bench/gen-zipf.py.
func BenchmarkMap(b *testing.B) {
	in, err := os.ReadFile(os.Getenv("JOBFILE_BENCH_INPUT"))
	if err != nil {
		b.Skip("set JOBFILE_BENCH_INPUT to a text file:", err)
	}
	lines := bytes.Split(in, []byte("\n"))
	for name, p := range benchPrograms(b) {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(in)))
			for b.Loop() {
				for _, l := range lines {
					if err := p.Mapper.Map(nil, nil, l, &count{}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// BenchmarkCombine combines 100 counts of one word, as a map task's
// combiner does for each word.
func BenchmarkCombine(b *testing.B) {
	one := []byte("1")
	values := func(yield func([]byte) bool) {
		for range 100 {
			if !yield(one) {
				return
			}
		}
	}
	for name, p := range benchPrograms(b) {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if err := p.Combiner.Reduce(nil, []byte("w"), values, &count{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
