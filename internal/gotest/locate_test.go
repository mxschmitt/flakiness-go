package gotest

import (
	"path/filepath"
	"runtime"
	"testing"
)

// exampleModuleDir returns the absolute path to testdata/example, which is a
// self-contained Go module used as a fixture.
func exampleModuleDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/gotest/locate_test.go -> repo root is three levels up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "testdata", "example")
}

func TestSourceLocator_FindsTopLevelFunc(t *testing.T) {
	dir := exampleModuleDir(t)
	loc := NewSourceLocator(dir)
	got := loc.Locate("example.test/sample", "TestFails")
	if got == nil {
		t.Fatal("Locate returned nil for TestFails")
	}
	if got.File != "sample_test.go" {
		t.Errorf("File = %q, want sample_test.go", got.File)
	}
	if got.Line != 12 { // line of `func TestFails` in the fixture
		t.Errorf("Line = %d, want 12", got.Line)
	}
	if got.Column <= 0 {
		t.Errorf("Column = %d, want > 0", got.Column)
	}
}

func TestSourceLocator_ForwardSlashes(t *testing.T) {
	dir := exampleModuleDir(t)
	loc := NewSourceLocator(dir)
	got := loc.Locate("example.test/sample", "TestPasses")
	if got == nil {
		t.Fatal("nil location")
	}
	for _, r := range got.File {
		if r == '\\' {
			t.Errorf("path contains backslash: %q", got.File)
		}
	}
}

func TestSourceLocator_UnknownFunc(t *testing.T) {
	dir := exampleModuleDir(t)
	loc := NewSourceLocator(dir)
	if got := loc.Locate("example.test/sample", "TestDoesNotExist"); got != nil {
		t.Errorf("expected nil for unknown func, got %+v", got)
	}
}

func TestSourceLocator_Cached(t *testing.T) {
	dir := exampleModuleDir(t)
	loc := NewSourceLocator(dir)
	a := loc.Locate("example.test/sample", "TestPasses")
	b := loc.Locate("example.test/sample", "TestPasses")
	if a == nil || b == nil || *a != *b {
		t.Errorf("cached lookups disagree: %+v vs %+v", a, b)
	}
}
