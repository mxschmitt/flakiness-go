package runner

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mxschmitt/flakiness-go/internal/config"
	"github.com/mxschmitt/flakiness-go/report"
)

func exampleModuleDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "testdata", "example")
}

// TestRunner_EndToEnd runs the real `go test -json` against the fixture module
// and asserts the produced report.json.
func TestRunner_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end go test invocation in -short mode")
	}
	moduleDir := exampleModuleDir(t)
	outDir := filepath.Join(t.TempDir(), "flakiness-report")

	// Run go test from within the fixture module dir.
	cwd, _ := os.Getwd()
	if err := os.Chdir(moduleDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	cfg := &config.Config{
		OutputDir:     outDir,
		Name:          "go",
		CommitID:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		GitRoot:       moduleDir,
		DisableUpload: true,
		GoTestArgs:    []string{"./..."},
	}
	r := &Runner{
		Cfg:     cfg,
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return []string{"FK_ENV_SHARD=3"} },
	}
	// go test exits non-zero because the fixture has failing tests; that's fine.
	if _, err := r.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse report: %v", err)
	}

	if rep.Category != "go" {
		t.Errorf("category = %q, want go", rep.Category)
	}
	if rep.CommitID != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("commitId = %q", rep.CommitID)
	}
	if rep.GeneratedBy == nil || rep.GeneratedBy.Name != "flakiness-go" {
		t.Errorf("generatedBy = %+v", rep.GeneratedBy)
	}
	if rep.TestRunner == nil || rep.TestRunner.Name != "go test" {
		t.Errorf("testRunner = %+v", rep.TestRunner)
	}
	if rep.Runtime == nil || rep.Runtime.Name != "go" {
		t.Errorf("runtime = %+v", rep.Runtime)
	}
	if len(rep.Environments) != 1 {
		t.Fatalf("environments = %d, want 1", len(rep.Environments))
	}
	env := rep.Environments[0]
	if env.Metadata["shard"] != "3" {
		t.Errorf("FK_ENV_SHARD not propagated: %+v", env.Metadata)
	}
	if env.SystemData == nil || env.SystemData.OSName == "" {
		t.Errorf("systemData missing: %+v", env.SystemData)
	}

	// Find the package suite and assert the mix of statuses.
	if len(rep.Suites) == 0 {
		t.Fatal("no suites in report")
	}
	var pkg report.Suite
	for _, s := range rep.Suites {
		if len(s.Tests) > 0 || len(s.Suites) > 0 {
			pkg = s
			break
		}
	}
	statuses := map[report.TestStatus]int{}
	var walk func(report.Suite)
	walk = func(s report.Suite) {
		for _, tc := range s.Tests {
			for _, a := range tc.Attempts {
				statuses[a.Status]++
			}
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(pkg)

	if statuses[report.StatusPassed] < 1 {
		t.Errorf("expected at least one passed test, got %+v", statuses)
	}
	if statuses[report.StatusFailed] < 1 {
		t.Errorf("expected at least one failed test, got %+v", statuses)
	}
	if statuses[report.StatusSkipped] < 1 {
		t.Errorf("expected at least one skipped test, got %+v", statuses)
	}

	// Source location should be resolved for a top-level test.
	var located bool
	var findLoc func(report.Suite)
	findLoc = func(s report.Suite) {
		for _, tc := range s.Tests {
			if tc.Location != nil && tc.Location.File == "sample_test.go" {
				located = true
			}
		}
		for _, sub := range s.Suites {
			findLoc(sub)
		}
	}
	findLoc(pkg)
	if !located {
		t.Error("expected at least one test with a resolved source location")
	}
}
