package storage

import (
	"strings"
	"testing"
)

// FuzzSchemeJoin: joining names under a name keeps its scheme, so a path
// built under an s3:// output stays in S3 and a local one stays local, and
// a URI keeps every element (#152).
func FuzzSchemeJoin(f *testing.F) {
	for _, s := range [][2]string{
		{"s3://bucket/out", "part-r-00000"}, {"/data/out", "_temporary"}, {"s3://", "key"},
		{"out", "s3://bucket"}, {`a\b://c`, "d"}, {"file:///x/", "y"}, {"", ""},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, name, elem string) {
		joined := Join(name, elem)
		if Scheme(joined) != Scheme(name) {
			t.Fatalf("Join(%q, %q) = %q has scheme %q, want %q", name, elem, joined, Scheme(joined), Scheme(name))
		}
		if Scheme(name) != "" && !strings.HasSuffix(joined, "/"+elem) {
			t.Fatalf("Join(%q, %q) = %q lost the element", name, elem, joined)
		}
	})
}
