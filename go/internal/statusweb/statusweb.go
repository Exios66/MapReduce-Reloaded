// Package statusweb serves a coordinator's status over HTTP (§4.8): a page
// for people, refreshed every few seconds, and Prometheus metrics at
// /metrics.
package statusweb

import (
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
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
}).Parse(page))

// Handler serves the status page at / and the metrics at /metrics.
func Handler(src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, src.Status()); err != nil {
			slog.Warn("status page", "err", err)
		}
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", promtext.ContentType)
		_, _ = w.Write(metrics(src.Status()))
	})
	return mux
}
