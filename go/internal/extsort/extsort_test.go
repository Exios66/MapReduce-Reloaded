package extsort

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
)

type record struct{ key, value string }

// randomRecords returns n records with many duplicate keys; each value is
// its position, so stability is visible.
func randomRecords(n int) []record {
	rng := rand.New(rand.NewPCG(1, 2))
	recs := make([]record, n)
	for i := range recs {
		recs[i] = record{fmt.Sprintf("k%d", rng.IntN(50)), fmt.Sprint(i)}
	}
	return recs
}

func sortAll(t *testing.T, recs []record, budget int) ([]record, *Sorter) {
	t.Helper()
	s := New(Config{Dir: t.TempDir(), Budget: budget})
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, r := range recs {
		if err := s.Add([]byte(r.key), []byte(r.value)); err != nil {
			t.Fatal(err)
		}
	}
	it, err := s.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, it), s
}

func collect(t *testing.T, r Records) []record {
	t.Helper()
	var out []record
	for r.Next() {
		out = append(out, record{string(r.Key()), string(r.Value())})
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSort(t *testing.T) {
	recs := randomRecords(1000)
	want := slices.Clone(recs)
	slices.SortStableFunc(want, func(a, b record) int { return bytes.Compare([]byte(a.key), []byte(b.key)) })

	for _, budget := range []int{1, 256, 4096, DefaultBudget} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			got, s := sortAll(t, recs, budget)
			if !slices.Equal(got, want) {
				t.Fatal("output is not the stable sort of the input")
			}
			if budget == DefaultBudget && (s.Runs() != 0 || s.dir != "") {
				t.Fatalf("data within budget spilled %d runs", s.Runs())
			}
			if budget == 256 && s.Runs() < 3 {
				t.Fatalf("%d runs, want at least 3", s.Runs())
			}
		})
	}
}

func TestEdgeKeys(t *testing.T) {
	recs := []record{{"ab", "1"}, {"", ""}, {"a", "2"}, {"b\x00", "3"}, {"b", "4"}}
	got, _ := sortAll(t, recs, 1)
	want := []record{{"", ""}, {"a", "2"}, {"ab", "1"}, {"b", "4"}, {"b\x00", "3"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSorterLifecycle(t *testing.T) {
	s := New(Config{Dir: t.TempDir(), Budget: 1})
	if err := s.Add([]byte("k"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sorted(); err != nil {
		t.Fatal(err)
	}
	if s.Add(nil, nil) == nil {
		t.Fatal("Add after Sorted succeeded")
	}
	if _, err := s.Sorted(); err == nil {
		t.Fatal("second Sorted succeeded")
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("run directory still exists after Close: %v", err)
	}
}

// TestGroups checks that each key appears once, in increasing order, with
// all of its values, however much of each group the caller reads.
func TestGroups(t *testing.T) {
	recs := randomRecords(500)
	sorted, _ := sortAll(t, recs, 1<<20)
	readers := map[string]int{"all": -1, "none": 0, "first": 1}

	for name, limit := range readers {
		t.Run(name, func(t *testing.T) {
			var keys []string
			total := 0
			r := &memSlice{recs: sorted, i: -1}
			for key, values := range Groups(r) {
				keys = append(keys, string(key))
				n := 0
				for range values {
					if n++; n == limit {
						break
					}
				}
				total += n
			}
			if !slices.IsSorted(keys) || len(slices.Compact(slices.Clone(keys))) != len(keys) {
				t.Fatal("keys are not strictly increasing")
			}
			if len(keys) != 50 {
				t.Fatalf("%d groups, want 50", len(keys))
			}
			if limit == -1 && total != len(recs) {
				t.Fatalf("read %d values, want %d", total, len(recs))
			}
		})
	}
}

func TestGroupsEarlyExit(t *testing.T) {
	r := &memSlice{recs: []record{{"a", "1"}, {"b", "2"}}, i: -1}
	for range Groups(r) {
		break
	}
}

func TestMergeError(t *testing.T) {
	boom := errors.New("bad run")
	good := &memSlice{recs: []record{{"a", "1"}}, i: -1}
	m := Merge(good, &memSlice{i: -1, err: boom})
	for m.Next() {
	}
	if !errors.Is(m.Err(), boom) {
		t.Fatalf("Err = %v, want %v", m.Err(), boom)
	}
}

// memSlice is a Records over a slice, optionally failing at the end.
type memSlice struct {
	recs []record
	i    int
	err  error
}

func (m *memSlice) Next() bool    { m.i++; return m.i < len(m.recs) }
func (m *memSlice) Key() []byte   { return []byte(m.recs[m.i].key) }
func (m *memSlice) Value() []byte { return []byte(m.recs[m.i].value) }
func (m *memSlice) Err() error    { return m.err }
