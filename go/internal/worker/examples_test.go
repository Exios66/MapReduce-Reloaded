package worker_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Each example program gives, on a cluster, the files that local mode gives:
// the oracle of §4.7 holds for every program, not only for word count.
func TestClusterExamplesMatchLocal(t *testing.T) {
	dir := t.TempDir()
	generate := func(name string, n int, line func(i int) string) []string {
		var b strings.Builder
		for i := range n {
			b.WriteString(line(i) + "\n")
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return []string{path}
	}
	weblog := generate("access.log", 3000, func(i int) string {
		return fmt.Sprintf(`10.0.0.%d - - [10/Oct/2000:13:55:36 -0700] "GET /page%d.html HTTP/1.0" 200 %d`, i%250, i%37, 100+i)
	})
	links := generate("links.tsv", 3000, func(i int) string { return fmt.Sprintf("page%d\tpage%d", i%53, i%17) })
	documents := generate("documents.tsv", 3000, func(i int) string {
		return fmt.Sprintf("http://host%d.com/p%d\tword%d word%d common", i%11, i, i%7, i%13)
	})

	for _, tc := range []struct {
		program string
		inputs  []string
		params  map[string]string
	}{
		{"wordcount", inputs, nil},
		{"grep", inputs, map[string]string{"pattern": "Raven"}},
		{"invertedindex", inputs, nil},
		{"distsort", inputs, nil},
		{"urlfreq", weblog, nil},
		{"reverselinks", links, nil},
		{"termvector", documents, nil},
	} {
		t.Run(tc.program, func(t *testing.T) {
			program, ok := api.Lookup(tc.program)
			if !ok {
				t.Fatalf("program %q is not registered", tc.program)
			}
			splitSize, err := split.SizeFor(t.Context(), tc.inputs, 5)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(t.TempDir(), "out")
			if _, err := local.Run(api.JobSpec{
				Program: program, Inputs: tc.inputs, Output: want, SplitSize: splitSize, R: 4, Params: tc.params,
			}, t.TempDir()); err != nil {
				t.Fatal(err)
			}

			c := newCluster(t)
			sub, err := c.client.SubmitJob(c.ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
				Program: tc.program, Inputs: tc.inputs, Output: c.output(), Params: tc.params,
				SplitSizeBytes: splitSize, NumReduceTasks: 4, UseCombiner: true,
			}})
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				c.addWorker()
			}
			status := c.wait(sub.GetJobId())
			if status.GetMapTasksTotal() < 2 {
				t.Fatalf("%d map tasks; the input should split", status.GetMapTasksTotal())
			}
			got, _ := filepath.Glob(filepath.Join(c.output(), "*"))
			assertSameFiles(t, got, want)
		})
	}
}
