package urlfreq_test

import (
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/examples/urlfreq"
)

func TestURLFreq(t *testing.T) {
	in := run.Write(t, `10.0.0.1 - - [10/Oct/2000:13:55:36 -0700] "GET /b HTTP/1.0" 200 10
10.0.0.2 - - [10/Oct/2000:13:55:37 -0700] "GET /a HTTP/1.0" 200 10
a short line
10.0.0.1 - - [10/Oct/2000:13:55:38 -0700] "GET /b HTTP/1.0" 404 0
10.0.0.3 - - [10/Oct/2000:13:55:39 -0700] "POST /b HTTP/1.0" 200 5
`)
	got, err := run.Local(t, urlfreq.Program, []string{in}, 2, nil)
	if want := "/a\t1\n/b\t3\n"; err != nil || string(got) != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

// Fields are split on ASCII spaces only, as the Python twin splits them: a
// URL with a no-break space in it is one field.
func TestUnicodeSpaceInURL(t *testing.T) {
	got, err := run.Local(t, urlfreq.Program, []string{run.Write(t, "a b c d e f /x y.html\n")}, 1, nil)
	if want := "/x y.html\t1\n"; err != nil || string(got) != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}
