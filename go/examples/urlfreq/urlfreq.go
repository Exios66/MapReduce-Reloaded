// Package urlfreq is the paper's count of URL access frequency (§2.3),
// registered as "urlfreq". Its input is a web server log in the Common Log
// Format, where the URL is the seventh space-separated field:
//
//	127.0.0.1 - - [10/Oct/2000:13:55:36 -0700] "GET /index.html HTTP/1.0" 200 2326
//
// It outputs each URL and how many requests it had. Lines with fewer
// fields are skipped.
package urlfreq

import (
	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program counts requests per URL. Its reducer doubles as the combiner.
var Program = api.Program{
	Mapper:   api.MapFunc(Map),
	Reducer:  api.ReduceFunc(wordcount.Sum),
	Combiner: api.ReduceFunc(wordcount.Sum),
}

func init() { api.Register("urlfreq", Program) }

var one = []byte("1")

// Map emits (URL, "1") for a request.
func Map(_ api.Context, _, line []byte, out api.Emitter) error {
	fields := wordcount.Words(line) // ASCII spaces only, as in the Python twin
	if len(fields) < 7 {
		return nil
	}
	return out.Emit(fields[6], one)
}
