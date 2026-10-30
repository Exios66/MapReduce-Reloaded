package memlimit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRead checks the finite, unlimited, empty and garbage cases.
func TestRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want int64
		ok   bool
	}{
		{"v2", "268435456\n", 268435456, true},
		{"v2 no limit", "max\n", 0, false},
		{"v1 no limit", "9223372036854771712\n", 0, false},
		{"zero", "0\n", 0, false},
		{"empty", "", 0, false},
		{"garbage", "lots\n", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "limit")
			if err := os.WriteFile(path, []byte(tc.text), 0o644); err != nil {
				t.Fatal(err)
			}
			got, ok := read(path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("read(%q) = %d, %v; want %d, %v", tc.text, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestReadMissing reports no limit for a file that does not exist.
func TestReadMissing(t *testing.T) {
	if _, ok := read(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("a missing file reported a limit")
	}
}

// TestFromCgroupIsSane reads this machine's cgroup without panicking.
func TestFromCgroupIsSane(t *testing.T) {
	if limit := FromCgroup(); limit < 0 {
		t.Fatalf("FromCgroup() = %d", limit)
	}
}
