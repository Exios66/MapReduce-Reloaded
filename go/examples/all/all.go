// Package all registers every example program. Binaries import it for its
// side effect, so that each program can be run by name.
package all

import (
	_ "github.com/exios66/mapreduce-reloaded/go/examples/grep"
	_ "github.com/exios66/mapreduce-reloaded/go/examples/reverselinks"
	_ "github.com/exios66/mapreduce-reloaded/go/examples/urlfreq"
	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
)
