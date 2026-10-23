// Package papermap checks docs/paper-mapping.md, the table that links each
// feature of the paper to its code and tests (#34): every code path it
// names exists, and so does every test.
package papermap

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const root = "../../.."

var (
	row      = regexp.MustCompile(`(?m)^\| (F\d+) +\|(.*)\|\s*$`)
	quoted   = regexp.MustCompile("`([^`]+)`")
	goTest   = regexp.MustCompile(`^(Test|Example|Fuzz)\w*$`)
	pyTest   = regexp.MustCompile(`^test_\w+$`)
	statuses = []string{"planned", "in progress", "done"}
)

func TestPaperMapping(t *testing.T) {
	table, err := os.ReadFile(filepath.Join(root, "docs/paper-mapping.md"))
	if err != nil {
		t.Fatal(err)
	}
	goFuncs := definitions(t, "go", "_test.go", `func `)
	pyFuncs := definitions(t, "python/tests", ".py", `def `)
	rows := row.FindAllStringSubmatch(string(table), -1)
	if len(rows) == 0 {
		t.Fatal("no feature rows")
	}
	for i, r := range rows {
		id, cells := r[1], strings.Split(r[2], "|")
		if want := fmt.Sprint("F", i+1); id != want {
			t.Errorf("row %d is %s, want %s", i+1, id, want)
		}
		if len(cells) != 7 {
			t.Errorf("%s: %d cells, want 8", id, len(cells)+1)
			continue
		}
		status, tests := strings.TrimSpace(cells[6]), strings.TrimSpace(cells[5])
		if !slices.Contains(statuses, status) {
			t.Errorf("%s: status %q, want one of %q", id, status, statuses)
		}
		if status == "done" && tests == "" {
			t.Errorf("%s: done, but names no test", id)
		}
		for _, cell := range cells[3:6] {
			for _, m := range quoted.FindAllStringSubmatch(cell, -1) {
				name := m[1]
				switch {
				case goTest.MatchString(name):
					if !goFuncs[name] {
						t.Errorf("%s: no Go test %s", id, name)
					}
				case pyTest.MatchString(name):
					if !pyFuncs[name] {
						t.Errorf("%s: no Python test %s", id, name)
					}
				case !exists(name):
					t.Errorf("%s: no file or directory %s", id, name)
				}
			}
		}
	}
}

// definitions returns the names that files under dir ending in suffix
// define with keyword, such as "func " or "def ".
func definitions(t *testing.T, dir, suffix, keyword string) map[string]bool {
	t.Helper()
	def := regexp.MustCompile(`(?m)^` + keyword + `(\w+)\(`)
	names := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, suffix) {
			return err
		}
		src, err := os.ReadFile(path)
		for _, m := range def.FindAllSubmatch(src, -1) {
			names[string(m[1])] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// exists reports whether path names a file or directory, from the repo
// root, or from python/ or python/tests/ for Python modules and tests.
func exists(path string) bool {
	for _, dir := range []string{"", "python", "python/tests"} {
		if _, err := os.Stat(filepath.Join(root, dir, path)); err == nil {
			return true
		}
	}
	return false
}
