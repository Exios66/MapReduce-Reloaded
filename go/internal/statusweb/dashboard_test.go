package statusweb

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

const dashboard = "../../../deploy/compose/monitoring/grafana/dashboards/mapreduce.json"

// The Grafana dashboard queries only metrics that the coordinator and the
// workers serve, so renaming one breaks this test, not the dashboard.
func TestDashboardQueriesServedMetrics(t *testing.T) {
	families := regexp.MustCompile(`(?m)^# TYPE (\S+)`)
	served := map[string]bool{}
	rec := httptest.NewRecorder()
	worker.New(nil, "", nil).MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, text := range [][]byte{metrics(coordinator.Status{}), rec.Body.Bytes()} {
		for _, m := range families.FindAllSubmatch(text, -1) {
			served[string(m[1])] = true
		}
	}

	data, err := os.ReadFile(dashboard)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Panels []struct {
			Title   string
			Targets []struct{ Expr string }
		}
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	queried := 0
	for _, p := range d.Panels {
		for _, target := range p.Targets {
			for _, name := range regexp.MustCompile(`\bmr_\w+`).FindAllString(target.Expr, -1) {
				queried++
				if !served[name] {
					t.Errorf("panel %q queries %s, which is not served", p.Title, name)
				}
			}
		}
	}
	if queried == 0 {
		t.Fatal("the dashboard queries no metrics")
	}
}
