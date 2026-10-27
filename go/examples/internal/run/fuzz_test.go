package run_test

import (
	"io"
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/reverselinks"
	"github.com/exios66/mapreduce-reloaded/go/examples/termvector"
	"github.com/exios66/mapreduce-reloaded/go/examples/urlfreq"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// context is the api.Context the fuzzer's calls get: no task, parameters
// or side files.
type context struct{}

func (context) Task() api.TaskID                     { return api.TaskID{} }
func (context) Param(string) string                  { return "" }
func (context) Input() string                        { return "" }
func (context) Count(string, int64)                  {}
func (context) Logger() *slog.Logger                 { return slog.New(slog.DiscardHandler) }
func (context) SideOutput(string) (io.Writer, error) { return io.Discard, nil }

type groups map[string][][]byte

func (g groups) Emit(k, v []byte) error {
	g[string(k)] = append(g[string(k)], slices.Clone(v))
	return nil
}

// FuzzExampleParsers runs each example that parses its input lines on any
// line (no tab, a \r, huge fields), then its combiner and reducer on what
// its map emitted. Map must not fail or panic, and the others must accept
// their own program's map output (#152).
func FuzzExampleParsers(f *testing.F) {
	for _, line := range []string{
		"http://a.com/x\thello world", "no tab here", "https://b.org/?q\r\tx\r", "\t", "://\t", "x://y#z\tw w w",
		"a.com b.com", "src\tdst", "http://h/p\t" + string(make([]byte, 4096)),
	} {
		f.Add([]byte(line))
	}
	programs := map[string]api.Program{"urlfreq": urlfreq.Program, "reverselinks": reverselinks.Program, "termvector": termvector.Program}
	f.Fuzz(func(t *testing.T, line []byte) {
		for name, p := range programs {
			out := groups{}
			if err := p.Mapper.Map(context{}, []byte("0"), line, out); err != nil {
				t.Fatalf("%s: Map(%q): %v", name, line, err)
			}
			for _, stage := range []api.Reducer{p.Combiner, p.Reducer} {
				if stage == nil {
					continue
				}
				for _, k := range slices.Sorted(maps.Keys(out)) {
					if err := stage.Reduce(context{}, []byte(k), slices.Values(out[k]), groups{}); err != nil {
						t.Fatalf("%s: reducing key %q of Map(%q): %v", name, k, line, err)
					}
				}
			}
		}
	})
}
