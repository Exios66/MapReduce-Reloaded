package wal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzOpen writes records, then tears or damages the file, and reopens it.
// Open must replay a prefix of the records written, never a record that was
// not written, and fail only with ErrCorrupt, only for damage. Without
// damage, it replays every record that fits before the cut, and a record
// appended afterwards follows them (#152).
func FuzzOpen(f *testing.F) {
	f.Add([]byte("a\xffbb\xffccc"), uint16(0), uint16(0), byte(0))
	f.Add([]byte("a\xffbb\xffccc"), uint16(20), uint16(0), byte(0))
	f.Add([]byte("a\xffbb\xffccc"), uint16(0), uint16(9), byte(1))
	f.Add([]byte("one\xfftwo"), uint16(5), uint16(0), byte(0))
	f.Fuzz(func(t *testing.T, data []byte, cut, at uint16, flip byte) {
		var records [][]byte
		for r := range bytes.SplitSeq(data, []byte{0xff}) {
			if len(r) > 0 {
				records = append(records, r)
			}
		}
		path := filepath.Join(t.TempDir(), "log")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		l := &Log{path: path, f: file} // writes Append's frames without its syncs, which are slow
		var ends []int                 // where each record's frame ends
		end := 0
		for _, r := range records {
			if err := l.write(r); err != nil {
				t.Fatal(err)
			}
			end += headerSize + len(r)
			ends = append(ends, end)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		bs, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		damaged := flip != 0 && len(bs) > 0
		if damaged {
			bs[int(at)%len(bs)] ^= flip
		}
		size := int(cut) % (len(bs) + 1)
		if cut == 0 {
			size = len(bs)
		}
		if err := os.WriteFile(path, bs[:size], 0o644); err != nil {
			t.Fatal(err)
		}

		var got [][]byte
		l, err = Open(path, func(p []byte) error {
			got = append(got, bytes.Clone(p))
			return nil
		})
		if err != nil {
			if !damaged || !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Open: %v (damaged %v)", err, damaged)
			}
			return
		}
		if len(got) > len(records) {
			t.Fatalf("replayed %d records of %d", len(got), len(records))
		}
		for i, p := range got {
			if !bytes.Equal(p, records[i]) {
				t.Fatalf("record %d is %q, want %q", i, p, records[i])
			}
		}
		if !damaged {
			want := 0
			for want < len(ends) && ends[want] <= size {
				want++
			}
			if len(got) != want {
				t.Fatalf("replayed %d records, want the %d before byte %d", len(got), want, size)
			}
		}
		if err := l.Append([]byte("new")); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		var again [][]byte
		l, err = Open(path, func(p []byte) error {
			again = append(again, bytes.Clone(p))
			return nil
		})
		if err != nil {
			t.Fatalf("reopening after an append: %v", err)
		}
		_ = l.Close()
		if want := append(got, []byte("new")); len(again) != len(want) || !bytes.Equal(again[len(again)-1], []byte("new")) {
			t.Fatalf("after the append the log replays %q, want %q", again, want)
		}
	})
}
