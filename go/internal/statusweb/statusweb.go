// Package statusweb serves a coordinator's status over HTTP (§4.8): a page
// for people, refreshed every few seconds, and Prometheus metrics at
// /metrics.
package statusweb

import (
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/promtext"
)

// Source is where the status comes from: a *coordinator.Server.
type Source interface {
	Status() coordinator.Status
}

//go:embed status.html
var page string

var tmpl = template.Must(template.New("status").Funcs(template.FuncMap{
	// duration shows how long a job has run, to a tenth of a second.
	// It uses now for unfinished jobs and returns "" if submission time is unknown.
	"duration": func(j *pb.GetJobStatusResponse, now time.Time) string {
		d, ok := duration(j, now)
		if !ok {
			return ""
		}
		return d.Round(100 * time.Millisecond).String()
	},
	// percent is done as a rounded percentage of total, "0%" if there is none.
	"percent": func(done, total uint32) string {
		if total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%d%%", (uint64(done)*100+uint64(total)/2)/uint64(total))
	},
	// state is the job state without its enum prefix, for the badge: "mapping".
	"state": func(s pb.JobState) string {
		return strings.ToLower(strings.TrimPrefix(s.String(), "JOB_STATE_"))
	},
	// counter is one job counter, or 0 if the job has none by that name.
	"counter": func(c *pb.Counters, name string) int64 { return c.GetValues()[name] },
}).Parse(page))

// pageData is the page's template data: the status, and whether dark mode
// was asked for with ?theme=dark (the README captures need it).
type pageData struct {
	coordinator.Status
	Dark bool
}

// Handler serves the status page at / and the metrics at /metrics.
func Handler(src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := pageData{Status: src.Status(), Dark: r.URL.Query().Get("theme") == "dark"}
		if err := tmpl.Execute(w, data); err != nil {
			slog.Warn("status page", "err", err)
		}
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", promtext.ContentType)
		_, _ = w.Write(metrics(src.Status()))
	})
	return mux
}
