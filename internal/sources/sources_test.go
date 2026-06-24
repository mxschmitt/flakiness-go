package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxschmitt/flakiness-go/report"
)

func loc(file string, line int) *report.Location {
	return &report.Location{File: file, Line: line, Column: 1}
}

// writeFile creates a git-root with a numbered-line file and returns the root.
func writeFile(t *testing.T, name string, numLines int) string {
	t.Helper()
	root := t.TempDir()
	lines := make([]string, numLines)
	for i := range lines {
		lines[i] = "line" + itoa(i+1)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestCollect_SingleLocationContext(t *testing.T) {
	root := writeFile(t, "a_test.go", 40)
	rep := &report.Report{
		Suites: []report.Suite{{
			Type:  report.SuiteFile,
			Title: "pkg",
			Tests: []report.Test{{
				Title:    "TestX",
				Location: loc("a_test.go", 20),
				Attempts: []report.RunAttempt{{Status: report.StatusPassed}},
			}},
		}},
	}
	Collect(rep, root)

	if len(rep.Sources) != 1 {
		t.Fatalf("want 1 source, got %d", len(rep.Sources))
	}
	s := rep.Sources[0]
	// line 20 ± 5 -> lines 15..25, so lineOffset 15.
	if s.LineOffset != 15 {
		t.Errorf("lineOffset = %d, want 15", s.LineOffset)
	}
	gotLines := strings.Split(s.Text, "\n")
	if len(gotLines) != 11 {
		t.Errorf("excerpt has %d lines, want 11", len(gotLines))
	}
	if gotLines[0] != "line15" || gotLines[len(gotLines)-1] != "line25" {
		t.Errorf("excerpt = %q..%q, want line15..line25", gotLines[0], gotLines[len(gotLines)-1])
	}
	if s.FilePath != "a_test.go" {
		t.Errorf("filePath = %q", s.FilePath)
	}
	// ContentType is left unset so the viewer infers it from the path.
	if s.ContentType != "" {
		t.Errorf("contentType = %q, want empty (viewer infers from path)", s.ContentType)
	}
}

func TestCollect_MergesOverlappingChunks(t *testing.T) {
	root := writeFile(t, "a_test.go", 40)
	rep := &report.Report{
		Tests: []report.Test{
			{Title: "T1", Location: loc("a_test.go", 10), Attempts: []report.RunAttempt{{}}},
			{Title: "T2", Location: loc("a_test.go", 14), Attempts: []report.RunAttempt{{}}}, // within 5 of T1 -> merge
		},
	}
	Collect(rep, root)
	if len(rep.Sources) != 1 {
		t.Fatalf("overlapping lines should merge into 1 source, got %d", len(rep.Sources))
	}
	// 10-5=5 .. 14+5=19
	if rep.Sources[0].LineOffset != 5 {
		t.Errorf("lineOffset = %d, want 5", rep.Sources[0].LineOffset)
	}
}

func TestCollect_SeparateChunksNotMerged(t *testing.T) {
	root := writeFile(t, "a_test.go", 80)
	rep := &report.Report{
		Tests: []report.Test{
			{Title: "T1", Location: loc("a_test.go", 10), Attempts: []report.RunAttempt{{}}},
			{Title: "T2", Location: loc("a_test.go", 50), Attempts: []report.RunAttempt{{}}}, // far apart
		},
	}
	Collect(rep, root)
	if len(rep.Sources) != 2 {
		t.Fatalf("distant lines should produce 2 sources, got %d", len(rep.Sources))
	}
}

func TestCollect_LineOffsetOmittedAtStart(t *testing.T) {
	root := writeFile(t, "a_test.go", 40)
	rep := &report.Report{
		Tests: []report.Test{{Title: "T1", Location: loc("a_test.go", 3), Attempts: []report.RunAttempt{{}}}},
	}
	Collect(rep, root)
	if len(rep.Sources) != 1 {
		t.Fatalf("want 1 source")
	}
	// line 3 - 5 clamps to line 1, so lineOffset is omitted (0).
	if rep.Sources[0].LineOffset != 0 {
		t.Errorf("lineOffset = %d, want 0 (omitted) when excerpt starts at line 1", rep.Sources[0].LineOffset)
	}
}

func TestCollect_ErrorAndAnnotationLocations(t *testing.T) {
	root := writeFile(t, "a_test.go", 40)
	rep := &report.Report{
		Tests: []report.Test{{
			Title: "T1",
			Attempts: []report.RunAttempt{{
				Errors:      []report.ReportError{{Message: "boom", Location: loc("a_test.go", 30)}},
				Annotations: []report.Annotation{{Type: "skip", Location: loc("a_test.go", 31)}},
			}},
		}},
	}
	Collect(rep, root)
	if len(rep.Sources) != 1 {
		t.Fatalf("error+annotation locations should yield 1 merged source, got %d", len(rep.Sources))
	}
}

func TestCollect_MissingFileSkipped(t *testing.T) {
	root := t.TempDir() // no files
	rep := &report.Report{
		Tests: []report.Test{{Title: "T1", Location: loc("missing.go", 5), Attempts: []report.RunAttempt{{}}}},
	}
	Collect(rep, root)
	if len(rep.Sources) != 0 {
		t.Errorf("missing file should be skipped, got %d sources", len(rep.Sources))
	}
}

func TestCollect_NoGitRootNoop(t *testing.T) {
	rep := &report.Report{
		Tests: []report.Test{{Title: "T1", Location: loc("a_test.go", 5), Attempts: []report.RunAttempt{{}}}},
	}
	Collect(rep, "")
	if rep.Sources != nil {
		t.Errorf("no git root should be a no-op, got %+v", rep.Sources)
	}
}
