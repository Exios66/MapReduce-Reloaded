package extsort

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tests in this file kill mutants that make mutate found surviving
// (docs/testing.md): each checks a boundary or a counter no other test pins.

func TestMergeSkipsEmptyStreams(t *testing.T) {
	m := Merge(&memSlice{i: -1}, &memSlice{recs: []record{{"a", "1"}}, i: -1},
		&memSlice{i: -1}, &memSlice{recs: []record{{"b", "2"}}, i: -1})
	want := []record{{"a", "1"}, {"b", "2"}}
	if got := collect(t, m); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if m.Next() {
		t.Fatal("Next after the end returned true")
	}
}

// TestMergeTies checks that equal keys come out in stream order even when
// each stream holds several of them.
func TestMergeTies(t *testing.T) {
	streams := [][]record{
		{{"a", "0"}, {"a", "0"}, {"b", "0"}},
		{{"a", "1"}, {"b", "1"}, {"b", "1"}},
		{{"a", "2"}, {"a", "2"}, {"a", "2"}},
	}
	var in []Records
	var want []record
	for _, s := range streams {
		in = append(in, &memSlice{recs: s, i: -1})
		want = append(want, s...)
	}
	slices.SortStableFunc(want, func(a, b record) int { return strings.Compare(a.key, b.key) })
	if got := collect(t, Merge(in...)); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBudgetDefault(t *testing.T) {
	for budget, want := range map[int]int{-1: DefaultBudget, 0: DefaultBudget, 1: 1} {
		if got := New(Config{Budget: budget}).cfg.Budget; got != want {
			t.Errorf("Budget %d became %d, want %d", budget, got, want)
		}
	}
}

// cost is what one record of key k and value v counts against the budget.
func cost(k, v string) int { return len(k) + len(v) + recordOverhead }

func add(t *testing.T, s *Sorter, k, v string) {
	t.Helper()
	if err := s.Add([]byte(k), []byte(v)); err != nil {
		t.Fatal(err)
	}
}

func newSorter(t *testing.T, cfg Config) *Sorter {
	t.Helper()
	cfg.Dir = t.TempDir()
	s := New(cfg)
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

// TestSpillBoundary checks that the buffer spills when it reaches the
// budget exactly, and that a spill empties it.
func TestSpillBoundary(t *testing.T) {
	s := newSorter(t, Config{Budget: 2 * cost("k", "v")})
	add(t, s, "k", "v")
	if s.Runs() != 0 {
		t.Fatal("spilled under budget")
	}
	add(t, s, "k", "v")
	if s.Runs() != 1 {
		t.Fatalf("%d runs at the budget, want 1", s.Runs())
	}
	add(t, s, "k", strings.Repeat("v", 26)) // one byte under the budget
	if s.Runs() != 1 {
		t.Fatalf("%d runs one byte under the budget, want 1", s.Runs())
	}
	it, err := s.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, it); len(got) != 3 {
		t.Fatalf("%d records, want 3: the one left in the buffer was lost", len(got))
	}
	if s.Runs() != 2 {
		t.Fatalf("%d runs, want 2", s.Runs())
	}
}

// TestNoEmptyRun checks that Sorted writes no run when the last Add
// emptied the buffer.
func TestNoEmptyRun(t *testing.T) {
	s := newSorter(t, Config{Budget: cost("k", "v")})
	for range 3 {
		add(t, s, "k", "v")
	}
	if _, err := s.Sorted(); err != nil {
		t.Fatal(err)
	}
	if s.Runs() != 3 {
		t.Fatalf("%d runs, want 3", s.Runs())
	}
}

// TestRunFiles checks that the runs are in a directory of their own under
// Config.Dir, and that SpilledBytes is their total size.
func TestRunFiles(t *testing.T) {
	s := newSorter(t, Config{Budget: 256})
	for _, r := range randomRecords(100) {
		add(t, s, r.key, r.value)
	}
	if _, err := s.Sorted(); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(s.dir) != s.cfg.Dir {
		t.Fatalf("run directory %q is not in %q", s.dir, s.cfg.Dir)
	}
	var total int64
	for _, path := range s.runs {
		if filepath.Dir(path) != s.dir {
			t.Fatalf("run %q is not in %q", path, s.dir)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		total += fi.Size()
	}
	if total == 0 || s.SpilledBytes() != total {
		t.Fatalf("SpilledBytes = %d, runs total %d", s.SpilledBytes(), total)
	}
}

// TestCombine checks that every record passes through Combine, spilled or
// not.
func TestCombine(t *testing.T) {
	count := func(in Records, emit func(k, v []byte) error) error {
		for key, values := range Groups(in) {
			n := 0
			for v := range values {
				n += len(v)
			}
			if err := emit(key, []byte(strings.Repeat("x", n))); err != nil {
				return err
			}
		}
		return nil
	}
	for _, budget := range []int{1 << 20, 3 * cost("a", "x")} {
		s := newSorter(t, Config{Budget: budget, Combine: count})
		for _, k := range []string{"b", "a", "b", "a", "a"} {
			add(t, s, k, "x")
		}
		it, err := s.Sorted()
		if err != nil {
			t.Fatal(err)
		}
		// Each run is combined on its own, so merge the counts again.
		n := map[string]int{}
		for _, r := range collect(t, it) {
			n[r.key] += len(r.value)
		}
		if n["a"] != 3 || n["b"] != 2 || len(n) != 2 {
			t.Fatalf("budget %d: counts %v, want a=3 b=2", budget, n)
		}
	}
}
