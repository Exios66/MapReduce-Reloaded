package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Each kind of mutation is found, in source order, and applying one changes
// just those bytes.
func TestMutants(t *testing.T) {
	src := []byte(`package p

func f(a, b int, ok bool) bool {
	a++
	return a == b && !ok || true
}
`)
	ms, err := mutants("p.go", src)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.String())
	}
	want := []string{
		"p.go:4: ++ -> --",
		"p.go:5: || -> &&",
		"p.go:5: && -> ||",
		"p.go:5: == -> !=",
		"p.go:5: ! -> ",
		"p.go:5: true -> false",
	}
	if !slices.Equal(got, want) {
		t.Errorf("mutants:\n%q\nwant\n%q", got, want)
	}
	if out := string(apply(src, ms[3])); out != string(src[:ms[3].Offset])+"!="+string(src[ms[3].Offset+2:]) {
		t.Errorf("apply(%v) = %s", ms[3], out)
	}
	for _, lit := range []string{"1", "0x10"} {
		ms, err := mutants("q.go", []byte("package p\n\nvar n = "+lit+"\n"))
		if err != nil || len(ms) != 1 || ms[0].New != "("+lit+"+1)" {
			t.Errorf("literal %s: %v, %v", lit, ms, err)
		}
	}
	if _, err := mutants("bad.go", []byte("package")); err == nil {
		t.Error("a file that does not parse: no error")
	}
}

// Files a mutant's tests leave in the package directory are removed, and
// the package's own are kept.
func TestRemoveNew(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"run-00000.kv", "spill/run-00001.kv"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeNew(dir, before)
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Name() != "a.go" {
		t.Errorf("left %v, want only a.go", after)
	}
}
