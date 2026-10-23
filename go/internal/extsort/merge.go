package extsort

import (
	"bytes"
	"container/heap"
	"iter"
)

// Merge combines sorted streams into one sorted stream. Records with equal
// keys come out in stream order, so merging stable runs stays stable.
func Merge(streams ...Records) Records {
	m := &merger{}
	for i, r := range streams {
		if r.Next() {
			m.h = append(m.h, cursor{r, i})
		} else if m.err = r.Err(); m.err != nil {
			return m
		}
	}
	heap.Init(&m.h)
	return m
}

type merger struct {
	h       cursorHeap
	started bool // whether h[0] is the current record
	err     error
}

func (m *merger) Next() bool {
	if m.err != nil {
		return false
	}
	// Advance the stream of the current record only now, so the record
	// returned last stays valid until this call.
	if m.started && len(m.h) > 0 {
		if top := m.h[0]; top.Next() {
			heap.Fix(&m.h, 0)
		} else if m.err = top.Err(); m.err == nil {
			heap.Pop(&m.h)
		}
	}
	m.started = true
	return m.err == nil && len(m.h) > 0
}

func (m *merger) Key() []byte   { return m.h[0].Key() }
func (m *merger) Value() []byte { return m.h[0].Value() }
func (m *merger) Err() error    { return m.err }

type cursor struct {
	Records
	index int // position of the stream in Merge's arguments, for ties
}

type cursorHeap []cursor

func (h cursorHeap) Len() int { return len(h) }
func (h cursorHeap) Less(i, j int) bool {
	c := bytes.Compare(h[i].Key(), h[j].Key())
	return c < 0 || c == 0 && h[i].index < h[j].index
}
func (h cursorHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *cursorHeap) Push(x any)   { *h = append(*h, x.(cursor)) }
func (h *cursorHeap) Pop() any {
	old := *h
	c := old[len(old)-1]
	*h = old[:len(old)-1]
	return c
}

// Groups yields each distinct key of a sorted stream with an iterator over
// its values, without holding a key's values in memory.
//
// The key is valid only during its iteration. Values may be ranged over at
// most once, and only before moving to the next key; values left unread are
// skipped. Check r.Err() after the loop.
func Groups(r Records) iter.Seq2[[]byte, iter.Seq[[]byte]] {
	return func(yield func([]byte, iter.Seq[[]byte]) bool) {
		var key []byte
		more := r.Next()
		for more {
			key = append(key[:0], r.Key()...)
			done := false // whether the group's values are exhausted
			next := func() {
				more = r.Next()
				done = !more || !bytes.Equal(r.Key(), key)
			}
			values := func(yieldValue func([]byte) bool) {
				for !done {
					keep := yieldValue(r.Value())
					next()
					if !keep {
						return
					}
				}
			}
			if !yield(key, values) {
				return
			}
			for !done {
				next()
			}
		}
	}
}
