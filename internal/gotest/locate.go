package gotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mxschmitt/flakiness-go/report"
)

// SourceLocator resolves the source location of top-level test functions by
// parsing the package's *_test.go files with go/ast. Results are git-root
// relative POSIX paths. It is safe for concurrent use.
type SourceLocator struct {
	gitRoot string

	mu sync.Mutex
	// dirCache maps an import path to the absolute directory of its sources,
	// resolved lazily via `go list`. An empty string means "not found".
	dirCache map[string]string
	// funcCache maps "<importPath>\x00<func>" to a resolved location (or nil).
	funcCache map[string]*report.Location
}

// NewSourceLocator creates a locator that normalizes paths against gitRoot.
func NewSourceLocator(gitRoot string) *SourceLocator {
	return &SourceLocator{
		gitRoot:   gitRoot,
		dirCache:  map[string]string{},
		funcCache: map[string]*report.Location{},
	}
}

// Locate implements Locator.
func (l *SourceLocator) Locate(pkg, testFunc string) *report.Location {
	if l == nil || pkg == "" || testFunc == "" {
		return nil
	}
	ck := pkg + "\x00" + testFunc
	l.mu.Lock()
	if loc, ok := l.funcCache[ck]; ok {
		l.mu.Unlock()
		return loc
	}
	l.mu.Unlock()

	loc := l.resolve(pkg, testFunc)

	l.mu.Lock()
	l.funcCache[ck] = loc
	l.mu.Unlock()
	return loc
}

func (l *SourceLocator) resolve(pkg, testFunc string) *report.Location {
	dir := l.packageDir(pkg)
	if dir == "" {
		return nil
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != testFunc {
				continue
			}
			pos := fset.Position(fn.Pos())
			return &report.Location{
				File:   l.normalize(pos.Filename),
				Line:   pos.Line,
				Column: pos.Column,
			}
		}
	}
	return nil
}

// packageDir resolves an import path to its source directory using `go list`.
func (l *SourceLocator) packageDir(pkg string) string {
	l.mu.Lock()
	if dir, ok := l.dirCache[pkg]; ok {
		l.mu.Unlock()
		return dir
	}
	l.mu.Unlock()

	dir := ""
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", pkg)
	if l.gitRoot != "" {
		cmd.Dir = l.gitRoot
	}
	if out, err := cmd.Output(); err == nil {
		dir = strings.TrimSpace(string(out))
	}

	l.mu.Lock()
	l.dirCache[pkg] = dir
	l.mu.Unlock()
	return dir
}

// normalize converts an absolute file path to a git-root-relative POSIX path.
func (l *SourceLocator) normalize(absPath string) string {
	p := absPath
	if l.gitRoot != "" {
		if rel, err := filepath.Rel(l.gitRoot, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
	}
	return filepath.ToSlash(p)
}
