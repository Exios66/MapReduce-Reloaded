// Package extsort sorts records that may not fit in memory (§3.1, step 5),
// merges sorted streams, and groups them by key for reduce (§4.2).
//
// Keys compare as raw bytes. Sorting is stable: records with equal keys keep
// the order in which they were added, as docs/file-format.md requires.
package extsort

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

// Records is a stream of records. Key and Value are valid until the next call
// to Next, and Err reports why Next returned false. *kv.Reader satisfies it.
type Records interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
}

// DefaultBudget is the memory budget used when Config.Budget is 0.
const DefaultBudget = 64 << 20

// recordOverhead approximates the bookkeeping memory per buffered record.
const recordOverhead = 24

// Config configures a Sorter.
type Config struct {
	// Dir is where sorted runs are spilled; "" means os.TempDir().
	Dir string
	// Budget is the approximate memory, in bytes, for buffered records.
	// When it is exceeded, the buffer is sorted and spilled to a run file.
	Budget int
}

// A Sorter accepts records in any order and returns them sorted by key. It
// keeps at most about Budget bytes in memory, spilling sorted runs to disk
// and merging them at the end. Close removes the run files.
type Sorter struct {
	cfg    Config
	arena  []byte // keys and values of buffered records, back to back
	recs   []span // buffered records, as offsets into arena
	used   int    // approximate memory used by the buffer
	dir    string // run directory, created on the first spill
	runs   []string
	files  []*os.File // runs opened by Sorted
	sorted bool
}

type span struct{ off, klen, vlen int }

// New returns an empty Sorter.
func New(cfg Config) *Sorter {
	if cfg.Budget <= 0 {
		cfg.Budget = DefaultBudget
	}
	return &Sorter{cfg: cfg}
}

// Add buffers a copy of one record, spilling the buffer if it is over budget.
func (s *Sorter) Add(key, value []byte) error {
	if s.sorted {
		return errors.New("extsort: Add after Sorted")
	}
	s.recs = append(s.recs, span{len(s.arena), len(key), len(value)})
	s.arena = append(append(s.arena, key...), value...)
	s.used += len(key) + len(value) + recordOverhead
	if s.used >= s.cfg.Budget {
		return s.spill()
	}
	return nil
}

// Runs returns the number of runs spilled to disk so far.
func (s *Sorter) Runs() int { return len(s.runs) }

// Sorted returns all records in key order. It may be called once.
func (s *Sorter) Sorted() (Records, error) {
	if s.sorted {
		return nil, errors.New("extsort: Sorted called twice")
	}
	s.sorted = true
	if len(s.runs) == 0 {
		s.sort()
		return &memRecords{s: s, i: -1}, nil
	}
	if len(s.recs) > 0 {
		if err := s.spill(); err != nil {
			return nil, err
		}
	}
	runs := make([]Records, len(s.runs))
	for i, path := range s.runs {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		s.files = append(s.files, f)
		runs[i] = kv.NewReader(f)
	}
	return Merge(runs...), nil
}

// Close closes and removes the run files.
func (s *Sorter) Close() error {
	var errs []error
	for _, f := range s.files {
		errs = append(errs, f.Close())
	}
	if s.dir != "" {
		errs = append(errs, os.RemoveAll(s.dir))
	}
	return errors.Join(errs...)
}

func (s *Sorter) key(r span) []byte { return s.arena[r.off : r.off+r.klen] }

func (s *Sorter) value(r span) []byte {
	return s.arena[r.off+r.klen : r.off+r.klen+r.vlen]
}

func (s *Sorter) sort() {
	slices.SortStableFunc(s.recs, func(a, b span) int { return bytes.Compare(s.key(a), s.key(b)) })
}

// spill writes the buffer, sorted, to a new run file and empties it.
func (s *Sorter) spill() error {
	if s.dir == "" {
		dir, err := os.MkdirTemp(s.cfg.Dir, "extsort-")
		if err != nil {
			return err
		}
		s.dir = dir
	}
	s.sort()
	path := filepath.Join(s.dir, fmt.Sprintf("run-%05d.kv", len(s.runs)))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := kv.NewWriter(f)
	for _, r := range s.recs {
		if err := w.Write(s.key(r), s.value(r)); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	if err := errors.Join(w.Close(), f.Close()); err != nil {
		return err
	}
	s.runs = append(s.runs, path)
	s.arena, s.recs, s.used = s.arena[:0], s.recs[:0], 0
	return nil
}

// memRecords iterates over the sorted buffer when nothing was spilled.
type memRecords struct {
	s *Sorter
	i int
}

func (m *memRecords) Next() bool    { m.i++; return m.i < len(m.s.recs) }
func (m *memRecords) Key() []byte   { return m.s.key(m.s.recs[m.i]) }
func (m *memRecords) Value() []byte { return m.s.value(m.s.recs[m.i]) }
func (m *memRecords) Err() error    { return nil }
