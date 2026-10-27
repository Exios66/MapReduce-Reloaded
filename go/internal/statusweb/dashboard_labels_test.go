package statusweb

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

func TestDashboardLegendsMatchServedLabels(t *testing.T) {
	recorder := httptest.NewRecorder()
	worker.New(nil, "", nil).MetricsHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	samples := string(metrics(coordinator.Status(status))) + recorder.Body.String()
	metricPattern := regexp.MustCompile(`(?m)^(mr_\w+)(?:\{([^}]*)\})? `)
	labelPattern := regexp.MustCompile(`(\w+)=`)
	labels := map[string]map[string]bool{}
	for _, sample := range metricPattern.FindAllStringSubmatch(samples, -1) {
		if labels[sample[1]] == nil {
			labels[sample[1]] = map[string]bool{}
		}
		for _, label := range labelPattern.FindAllStringSubmatch(sample[2], -1) {
			labels[sample[1]][label[1]] = true
		}
	}
	data, err := os.ReadFile(dashboard)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Panels []struct {
			Title   string
			Targets []struct{ Expr, LegendFormat string }
		}
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	legendPattern := regexp.MustCompile(`\{\{\s*(\w+)\s*\}\}`)
	namePattern := regexp.MustCompile(`\bmr_\w+`)
	groupPattern := regexp.MustCompile(`\bsum\s+by\s*\(([^)]*)\)`)
	ungroupedSum := regexp.MustCompile(`\bsum\s*\(`)
	checked := 0
	for _, panel := range document.Panels {
		for _, target := range panel.Targets {
			for _, legend := range legendPattern.FindAllStringSubmatch(target.LegendFormat, -1) {
				checked++
				names := namePattern.FindAllString(target.Expr, -1)
				if len(names) == 0 {
					t.Errorf("panel %q has legend label %q but queries no MapReduce metrics", panel.Title, legend[1])
				}
				for _, name := range names {
					if !labels[name][legend[1]] {
						t.Errorf("panel %q legend uses %q but %s does not serve that label", panel.Title, legend[1], name)
					}
				}
				for _, grouping := range groupPattern.FindAllStringSubmatch(target.Expr, -1) {
					kept := false
					for _, label := range strings.Split(grouping[1], ",") {
						kept = kept || strings.TrimSpace(label) == legend[1]
					}
					if !kept {
						t.Errorf("panel %q aggregation drops legend label %q", panel.Title, legend[1])
					}
				}
				if ungroupedSum.MatchString(target.Expr) {
					t.Errorf("panel %q ungrouped sum drops legend label %q", panel.Title, legend[1])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the dashboard has no label-based legends")
	}
}
