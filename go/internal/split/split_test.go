package split

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
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

func TestAllocate(t *testing.T) {
	for _, tc := range []struct {
		sizes []int64
		m     int
		want  []int
	}{
		{[]int64{100}, 8, []int{8}},
		{[]int64{100, 300}, 4, []int{1, 3}},
		{[]int64{100, 300}, 8, []int{2, 6}},
		{[]int64{1, 1, 1}, 4, []int{2, 1, 1}},  // a tie goes to the first file
		{[]int64{5, 5, 5}, 2, []int{1, 1, 1}},  // more files than m
		{[]int64{1, 1 << 40}, 10, []int{1, 9}}, // a tiny file still gets one
		{[]int64{1 << 62, 1 << 62}, 1 << 30, []int{1 << 29, 1 << 29}},
		{nil, 64, []int{}}, // every input file empty
	} {
		got := Allocate(tc.sizes, tc.m)
		if !slices.Equal(got, tc.want) {
			t.Errorf("Allocate(%v, %d) = %v, want %v", tc.sizes, tc.m, got, tc.want)
		}
	}
}

// PlanM gives exactly m splits for files that can be cut that finely,
// covering each file in order.
func TestPlanM(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 200 {
		sizes := make([]int, rng.IntN(4)+1)
		for i := range sizes {
			sizes[i] = rng.IntN(5000) + 1000
		}
		m := rng.IntN(20) + 1
		splits, err := PlanM(t.Context(), formats.Text{}, []string{writeFiles(t, sizes...)}, m)
		if err != nil {
			t.Fatal(err)
		}
		if want := max(m, len(sizes)); len(splits) != want {
			t.Fatalf("sizes %v, m %d: %d splits, want %d", sizes, m, len(splits), want)
		}
		covered := map[string]int64{}
		for _, s := range splits {
			if s.Start != covered[s.Path] {
				t.Fatalf("sizes %v, m %d: split %+v leaves a gap", sizes, m, s)
			}
			covered[s.Path] = s.End
		}
		for i, n := range sizes {
			if covered[filepath.Join(filepath.Dir(splits[0].Path), fileName(i))] != int64(n) {
				t.Fatalf("sizes %v, m %d: file %d not covered", sizes, m, i)
			}
		}
	}
}

func TestPlanMSkipsEmptyFilesAndRejectsZero(t *testing.T) {
	glob := writeFiles(t, 0, 100)
	splits, err := PlanM(t.Context(), formats.Text{}, []string{glob}, 2)
	if err != nil || len(splits) != 2 || filepath.Base(splits[0].Path) != fileName(1) {
		t.Fatalf("got %v, %v; want two splits of the non-empty file", splits, err)
	}
	// Only empty files: no splits, so the caller reports an empty input.
	if splits, err := PlanM(t.Context(), formats.Text{}, []string{writeFiles(t, 0, 0)}, 64); err != nil || len(splits) != 0 {
		t.Fatalf("empty files: got %v, %v; want no splits", splits, err)
	}
	if _, err := PlanM(t.Context(), formats.Text{}, []string{glob}, 0); err == nil {
		t.Fatal("m = 0: no error")
	}
}
