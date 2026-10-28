// Command mutate measures how well a package's tests catch bugs (#161). It
// makes one small change at a time to the named files: a comparison,
// arithmetic, bitwise or logical operator swapped, an integer constant one
// more, a boolean flipped, a negation dropped or an increment reversed. It then runs the package's tests on each changed
// file through go test -overlay, so the source tree is never modified. A
// mutant the tests fail on is killed; one they pass on survived, and
// either the tests miss a bug there or the change makes no difference.
//
//	go run ./tools/mutate [-timeout 2m] [-j N] ./internal/partition hash.go
//
// It prints each surviving mutant and the score, killed over killed plus
// survived. Mutants that do not compile are not counted. A mutant can make a
// test write where it should not, such as a sorter's runs into the package
// directory; files it leaves there are removed at the end.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// A mutant replaces Len bytes at Offset of File with New.
type mutant struct {
	File   string
	Line   int
	Offset int
	Len    int
	Old    string
	New    string
}

func (m mutant) String() string {
	return fmt.Sprintf("%s:%d: %s -> %s", filepath.Base(m.File), m.Line, m.Old, m.New)
}

// swaps are the operator mutations: each operator becomes the one listed.
var swaps = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL,
	token.LSS: token.LEQ, token.LEQ: token.LSS,
	token.GTR: token.GEQ, token.GEQ: token.GTR,
	token.ADD: token.SUB, token.SUB: token.ADD,
	token.MUL: token.QUO, token.QUO: token.MUL,
	token.LAND: token.LOR, token.LOR: token.LAND,
	token.SHL: token.SHR, token.SHR: token.SHL,
	token.AND: token.OR, token.OR: token.AND,
	token.INC: token.DEC, token.DEC: token.INC,
	token.REM: token.QUO, token.XOR: token.OR,
	token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN,
	token.MUL_ASSIGN: token.ADD_ASSIGN, token.XOR_ASSIGN: token.OR_ASSIGN,
	token.OR_ASSIGN: token.XOR_ASSIGN, token.AND_ASSIGN: token.OR_ASSIGN,
}

// mutants returns the mutants of a Go source file, in source order.
func mutants(name string, src []byte) ([]mutant, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	var ms []mutant
	add := func(pos token.Pos, old, new string) {
		p := fset.Position(pos)
		ms = append(ms, mutant{File: name, Line: p.Line, Offset: p.Offset, Len: len(old), Old: old, New: new})
	}
	op := func(pos token.Pos, tok token.Token) {
		if to, ok := swaps[tok]; ok {
			add(pos, tok.String(), to.String())
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			op(n.OpPos, n.Op)
		case *ast.AssignStmt:
			op(n.TokPos, n.Tok)
		case *ast.IncDecStmt:
			op(n.TokPos, n.Tok)
		case *ast.UnaryExpr:
			if n.Op == token.NOT {
				add(n.OpPos, "!", "")
			}
		case *ast.BasicLit: // an integer one more
			if n.Kind == token.INT {
				add(n.Pos(), n.Value, "("+n.Value+"+1)")
			}
		case *ast.Ident:
			switch n.Name {
			case "true":
				add(n.Pos(), "true", "false")
			case "false":
				add(n.Pos(), "false", "true")
			}
		}
		return true
	})
	return ms, nil
}

// apply returns src with m applied.
func apply(src []byte, m mutant) []byte {
	out := make([]byte, 0, len(src)+len(m.New))
	out = append(out, src[:m.Offset]...)
	out = append(out, m.New...)
	return append(out, src[m.Offset+m.Len:]...)
}

type result int

const (
	killed result = iota
	survived
	invalid // does not compile
)

// test runs pkg's tests with m applied through an overlay in dir.
func test(ctx context.Context, pkg, dir string, src []byte, m mutant, i int, timeout time.Duration) (result, error) {
	file := filepath.Join(dir, fmt.Sprintf("m%d.go", i))
	if err := os.WriteFile(file, apply(src, m), 0o644); err != nil {
		return 0, err
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {m.File: file}})
	if err != nil {
		return 0, err
	}
	ov := filepath.Join(dir, fmt.Sprintf("m%d.json", i))
	if err := os.WriteFile(ov, overlay, 0o644); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-overlay", ov, "-count=1", "-failfast", "-vet=off", pkg)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return survived, nil
	case ctx.Err() != nil: // hung, as when a loop's condition is flipped
		return killed, nil
	case bytes.Contains(out, []byte("[build failed]")) || bytes.Contains(out, []byte("[setup failed]")):
		return invalid, nil // a type error, such as - on strings
	case errors.As(err, &exit):
		return killed, nil
	}
	return 0, err
}

func main() {
	timeout := flag.Duration("timeout", 2*time.Minute, "longest a test run may take; one that takes longer kills the mutant")
	jobs := flag.Int("j", max(1, runtime.NumCPU()/2), "mutants tested at once")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: mutate [flags] package file.go...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Args()[1:], *timeout, *jobs); err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(1)
	}
}

func run(pkg string, files []string, timeout time.Duration, jobs int) error {
	dir, err := os.MkdirTemp("", "mutate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	before, err := os.ReadDir(pkg)
	if err != nil {
		return err
	}
	defer removeNew(pkg, before)
	srcs := map[string][]byte{}
	var all []mutant
	for _, f := range files {
		name, err := filepath.Abs(filepath.Join(pkg, f))
		if err != nil {
			return err
		}
		src, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		ms, err := mutants(name, src)
		if err != nil {
			return err
		}
		srcs[name] = src
		all = append(all, ms...)
	}

	results := make([]result, len(all))
	errs := make([]error, len(all))
	var wg sync.WaitGroup
	next := make(chan int)
	ctx := context.Background()
	for range jobs {
		wg.Go(func() {
			for i := range next {
				m := all[i]
				results[i], errs[i] = test(ctx, pkg, dir, srcs[m.File], m, i, timeout)
			}
		})
	}
	for i := range all {
		next <- i
	}
	close(next)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	var survivors []string
	counts := map[result]int{}
	for i, r := range results {
		counts[r]++
		if r == survived {
			survivors = append(survivors, all[i].String())
		}
	}
	sort.Strings(survivors)
	for _, s := range survivors {
		fmt.Println("survived:", s)
	}
	score := 100.0
	if n := counts[killed] + counts[survived]; n > 0 {
		score = 100 * float64(counts[killed]) / float64(n)
	}
	fmt.Printf("mutate: %s %s: %d killed, %d survived, %d invalid; score %.1f%%\n",
		pkg, strings.Join(files, " "), counts[killed], counts[survived], counts[invalid], score)
	return nil
}

// removeNew removes the entries of dir that are not among before.
func removeNew(dir string, before []os.DirEntry) {
	had := map[string]bool{}
	for _, e := range before {
		had[e.Name()] = true
	}
	now, _ := os.ReadDir(dir)
	for _, e := range now {
		if !had[e.Name()] {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}
