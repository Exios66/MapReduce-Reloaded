package split

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
)

// writeFiles creates files of the given sizes in a new directory and returns
// a glob that matches them.
func writeFiles(t *testing.T, sizes ...int) string {
	t.Helper()
	dir := t.TempDir()
	for i, n := range sizes {
		path := filepath.Join(dir, fileName(i))
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "*.txt")
}

func fileName(i int) string { return fmt.Sprintf("f%d.txt", i) }

// Splits cover every file exactly, in order, with no gaps or overlaps, and
// none is larger than the split size.
func TestPlanCoversInput(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		sizes := make([]int, rng.IntN(4)+1)
		for i := range sizes {
			sizes[i] = rng.IntN(300)
		}
		size := uint64(rng.IntN(100) + 1)
		splits, err := Plan(t.Context(), formats.Text{}, []string{writeFiles(t, sizes...)}, size)
		if err != nil {
			t.Fatal(err)
		}
		i := 0
		for f, n := range sizes {
			for end := int64(0); end < int64(n); i++ {
				s := splits[i]
				if filepath.Base(s.Path) != fileName(f) || s.Start != end ||
					s.End <= s.Start || s.End-s.Start > int64(size) {
					t.Fatalf("sizes %v, size %d: split %d is %+v", sizes, size, i, s)
				}
				end = s.End
			}
		}
		if i != len(splits) {
			t.Fatalf("sizes %v, size %d: %d extra splits", sizes, size, len(splits)-i)
		}
	}
}

func TestPlanDefaultSize(t *testing.T) {
	splits, err := Plan(t.Context(), formats.Text{}, []string{writeFiles(t, 10)}, 0)
	if err != nil || len(splits) != 1 {
		t.Fatalf("got %v, %v; want one split", splits, err)
	}
}

func TestSizeFor(t *testing.T) {
	glob := writeFiles(t, 100, 1)
	for _, m := range []int{1, 3, 7, 101, 500} {
		size, err := SizeFor(t.Context(), []string{glob}, m)
		if err != nil {
			t.Fatal(err)
		}
		splits, _ := Plan(t.Context(), formats.Text{}, []string{glob}, size)
		if len(splits) > m+1 { // at most m per file, and about m in total
			t.Errorf("m=%d: size %d gives %d splits", m, size, len(splits))
		}
	}
	if _, err := SizeFor(t.Context(), []string{glob}, 0); err == nil {
		t.Error("m=0 accepted")
	}
}

func TestDefaultR(t *testing.T) {
	if DefaultR(0) != 2 || DefaultR(4) != 8 {
		t.Fatalf("DefaultR(0), DefaultR(4) = %d, %d", DefaultR(0), DefaultR(4))
	}
}

func TestExpandErrors(t *testing.T) {
	for pattern, want := range map[string]string{
		filepath.Join(t.TempDir(), "*"): "no input matches",
		"[":                             "syntax error",
	} {
		if _, err := Expand(t.Context(), []string{pattern}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Expand(%q) = %v, want an error containing %q", pattern, err, want)
		}
	}
}
