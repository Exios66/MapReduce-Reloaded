// Package termvector is the paper's term vector per host (§2.3), registered
// as "termvector". Each input line is a document: a URL, a tab and the
// document's text. The host is the part of the URL between "://" and the
// next "/", "?" or "#"; lines without a tab or a host are skipped.
//
// It outputs each host with its term vector: the words of its documents,
// split as word count splits them, as word:count pairs, most frequent
// first, then by word. Terms that occur fewer than the job's "min"
// parameter times (default 2) are dropped.
package termvector

import (
	"bytes"
	"cmp"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program builds term vectors. Its combiner adds vectors and drops nothing,
// since a term rare in one map task may not be rare in the job.
var Program = api.Program{
	Mapper:   api.MapFunc(Map),
	Reducer:  api.ReduceFunc(Reduce),
	Combiner: api.ReduceFunc(Sum),
}

func init() { api.Register("termvector", Program) }

// A vector maps a term to how many times it occurs.
type vector map[string]int

// Map emits (host, the document's term vector).
func Map(_ api.Context, _, line []byte, out api.Emitter) error {
	url, text, ok := bytes.Cut(line, []byte("\t"))
	_, rest, _ := bytes.Cut(url, []byte("://"))
	host := rest
	if i := bytes.IndexAny(rest, "/?#"); i >= 0 {
		host = rest[:i]
	}
	if !ok || len(host) == 0 {
		return nil
	}
	v := vector{}
	for _, w := range wordcount.Words(text) {
		v[string(w)]++
	}
	return out.Emit(host, v.encode(1))
}

// Sum emits a host and the sum of its vectors.
func Sum(_ api.Context, host []byte, vectors iter.Seq[[]byte], out api.Emitter) error {
	v, err := sum(vectors)
	if err != nil {
		return err
	}
	return out.Emit(host, v.encode(1))
}

// Reduce emits a host and the sum of its vectors, without rare terms.
func Reduce(ctx api.Context, host []byte, vectors iter.Seq[[]byte], out api.Emitter) error {
	minCount := 2
	if s := ctx.Param("min"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return fmt.Errorf("termvector: min=%q is not a positive integer", s)
		}
		minCount = n
	}
	v, err := sum(vectors)
	if err != nil {
		return err
	}
	return out.Emit(host, v.encode(minCount))
}

func sum(vectors iter.Seq[[]byte]) (vector, error) {
	v := vector{}
	for enc := range vectors {
		for _, pair := range bytes.Fields(enc) {
			i := bytes.LastIndexByte(pair, ':')
			n, err := strconv.Atoi(string(pair[i+1:]))
			if i < 0 || err != nil {
				return nil, fmt.Errorf("termvector: bad term %q", pair)
			}
			v[string(pair[:i])] += n
		}
	}
	return v, nil
}

// encode returns the terms that occur at least minCount times as
// space-separated word:count pairs, most frequent first.
func (v vector) encode(minCount int) []byte {
	terms := slices.SortedFunc(maps.Keys(v), func(a, b string) int {
		return cmp.Or(cmp.Compare(v[b], v[a]), cmp.Compare(a, b))
	})
	var b []byte
	for _, t := range terms {
		if v[t] < minCount {
			continue
		}
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = strconv.AppendInt(append(append(b, t...), ':'), int64(v[t]), 10)
	}
	return b
}
