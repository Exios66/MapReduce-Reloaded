package split

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
)

// FuzzPlan plans two files of any content (empty, no final newline, one
// line longer than a split) by size and by count. The splits must cover
// each file once, in order, with no gap or overlap (#152). Reading them is
// FuzzTextSplits's job, in internal/formats.
func FuzzPlan(f *testing.F) {
	f.Add([]byte("a\nb\nc"), []byte(""), uint16(1), uint8(2))
	f.Add([]byte(""), []byte("one long line without a newline"), uint16(4), uint8(7))
	f.Add([]byte("\n\n\n"), []byte("x\r\ny\n"), uint16(0), uint8(1))
	f.Fuzz(func(t *testing.T, a, b []byte, size uint16, m uint8) {
		dir := t.TempDir()
		files := [][]byte{a, b}
		for i, data := range files {
			if err := os.WriteFile(filepath.Join(dir, fileName(i)), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		glob := []string{filepath.Join(dir, "*.txt")}
		bySize, err := Plan(t.Context(), formats.Text{}, glob, uint64(size))
		if err != nil {
			t.Fatal(err)
		}
		check(t, "Plan", dir, files, bySize)
		if m > 0 {
			byCount, err := PlanM(t.Context(), formats.Text{}, glob, int(m))
			if err != nil {
				t.Fatal(err)
			}
			check(t, "PlanM", dir, files, byCount)
		}
	})
}

// check fails unless splits cover files, in order, exactly.
func check(t *testing.T, plan, dir string, files [][]byte, splits []formats.Split) {
	t.Helper()
	i := 0
	for n, data := range files {
		path, end := filepath.Join(dir, fileName(n)), int64(0)
		for i < len(splits) && splits[i].Path == path {
			if s := splits[i]; s.Start != end || s.End <= s.Start {
				t.Fatalf("%s: split %d is [%d, %d) of %s, want it to start at %d", plan, i, s.Start, s.End, path, end)
			}
			end = splits[i].End
			i++
		}
		if end != int64(len(data)) {
			t.Fatalf("%s: splits of %s end at %d, want %d", plan, path, end, len(data))
		}
	}
	if i != len(splits) {
		t.Fatalf("%s: %d splits, %d of them for the files", plan, len(splits), i)
	}
}
