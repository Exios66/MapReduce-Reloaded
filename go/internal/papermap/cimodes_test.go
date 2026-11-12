package papermap

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// testDirs returns the directories under go/, relative to the repository,
// whose test files match rx, sorted. The file named skip is left out.
func testDirs(t *testing.T, rx *regexp.Regexp, skip string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(filepath.Join(root, "go"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == skip {
			return err
		}
		src, err := os.ReadFile(path)
		if err == nil && rx.Match(src) {
			dir, _ := filepath.Rel(root, filepath.Dir(path))
			if !slices.Contains(got, dir) {
				got = append(got, dir)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// A PR that changes only README.md or docs/paper-mapping.md runs CI's go job
// in a narrow mode: make go-python-test, not every Go package (docs/ci.md,
// ADR 0025). That is sound only while cmd/mr and papermap, which that target
// tests, are the only Go tests that read a README or anything under docs/. A
// Go test that starts to must be added to go-python-test and to this list,
// or its file mapped to run-go in .circleci/mapping.txt.
func TestOnlyGoPythonTestPackagesReadTheDocs(t *testing.T) {
	got := testDirs(t, regexp.MustCompile(`"[^"\n]*(README\.md|docs/)[^"\n]*"`), "cimodes_test.go")
	if want := []string{"go/cmd/mr", "go/internal/papermap"}; !slices.Equal(got, want) {
		t.Errorf("Go tests that read a README or docs/ are in %q, want %q", got, want)
	}
}

// A PR that changes only shell scripts or the git hook runs CI's go job in
// a narrow mode: make sh-lint and no Go test (docs/ci.md, ADR 0025). That is
// sound only while no Go test reads or runs one. (scripts/ runs every job
// whatever its files are.) A Go test that starts to needs its script mapped
// to run-go in .circleci/mapping.txt, and its package listed here.
func TestNoGoTestReadsAShellScript(t *testing.T) {
	if got := testDirs(t, regexp.MustCompile(`\.sh\b|\.githooks`), "cimodes_test.go"); len(got) != 0 {
		t.Errorf("Go tests that name a shell script or the git hook are in %q, want none", got)
	}
}

// TestSortMemoryBounded and TestReduceMemoryBounded skip themselves under
// the race detector: they measure one goroutine's heap over 80 MB, which the
// detector slows tenfold and has nothing to find in (ADR 0025). They must
// still run on every pull request, so the coverage pass, which CI's go job
// and make check always run, has to run without -race and without -short;
// and no other test may use the same switch to leave the race pass.
func TestOnlyTheHeapMeasurementsSkipUnderTheRaceDetector(t *testing.T) {
	var got []string
	err := filepath.WalkDir(filepath.Join(root, "go"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || filepath.Base(path) == "cimodes_test.go" {
			return err
		}
		src, err := os.ReadFile(path)
		if err == nil && regexp.MustCompile(`raceDetector|//go:build .*\brace\b`).Match(src) {
			rel, _ := filepath.Rel(filepath.Join(root, "go"), path)
			got = append(got, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{
		"internal/extsort/limits_test.go", "internal/extsort/race_off_test.go", "internal/extsort/race_on_test.go",
		"internal/task/limits_test.go", "internal/task/race_off_test.go", "internal/task/race_on_test.go",
	}
	if !slices.Equal(got, want) {
		t.Errorf("files that know about the race detector: %q, want %q", got, want)
	}
	for _, limits := range []string{"internal/extsort/limits_test.go", "internal/task/limits_test.go"} {
		src, err := os.ReadFile(filepath.Join(root, "go", limits))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), "raceDetector"); n != 1 {
			t.Errorf("%s uses raceDetector %d times, want once, in its heap measurement", limits, n)
		}
	}

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	cover := regexp.MustCompile(`(?m)^go-test-cover:\n((?:\t.*\n)+)`).FindSubmatch(makefile)
	if cover == nil {
		t.Fatal("the Makefile has no go-test-cover target")
	}
	recipe := string(cover[1])
	if !strings.Contains(recipe, "go test -shuffle=on -coverpkg=./... -coverprofile=cover.out ./...") {
		t.Errorf("make go-test-cover does not test every package:\n%s", recipe)
	}
	for _, flag := range []string{"-race", "-short"} {
		if strings.Contains(recipe, flag) {
			t.Errorf("make go-test-cover runs with %s, which skips the heap measurements", flag)
		}
	}
}
