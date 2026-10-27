package promtext

import (
	"strings"
	"testing"
)

// FuzzLabel: a label value is one quoted string with no raw newline, and
// undoing the format's three escapes gives the value back (#152).
func FuzzLabel(f *testing.F) {
	for _, v := range []string{"", "job-1", `a"b`, `c:\d`, "x\ny", `\n`, `"\`} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v string) {
		q := Label(v)
		if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' || strings.Contains(q, "\n") {
			t.Fatalf("Label(%q) = %q", v, q)
		}
		var b strings.Builder
		body := q[1 : len(q)-1]
		for i := 0; i < len(body); i++ {
			c := body[i]
			switch {
			case c == '"':
				t.Fatalf("Label(%q) = %q has an unescaped quote", v, q)
			case c != '\\':
				b.WriteByte(c)
			case i+1 == len(body):
				t.Fatalf("Label(%q) = %q ends in a lone backslash", v, q)
			default:
				i++
				switch body[i] {
				case '\\', '"':
					b.WriteByte(body[i])
				case 'n':
					b.WriteByte('\n')
				default:
					t.Fatalf("Label(%q) = %q has the escape \\%c", v, q, body[i])
				}
			}
		}
		if b.String() != v {
			t.Fatalf("Label(%q) = %q reads back as %q", v, q, b.String())
		}
	})
}
