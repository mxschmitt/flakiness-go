package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"

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
		GoTestArgs:    []string{"-bench=.", "./..."},
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
	// osName must follow the Flakiness convention, not Go's GOOS.
	if env.SystemData.OSName == "darwin" || env.SystemData.OSName == "windows" {
		t.Errorf("osName = %q, want normalized macos/win/linux", env.SystemData.OSName)
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
	// Reports strip default values (matching the Node SDK), so an omitted
	// status round-trips to "" and MEANS passed. Normalize that when counting.
	effStatus := func(s report.TestStatus) report.TestStatus {
		if s == "" {
			return report.StatusPassed
		}
		return s
	}
	statuses := map[report.TestStatus]int{}
	var walk func(report.Suite)
	walk = func(s report.Suite) {
		for _, tc := range s.Tests {
			for _, a := range tc.Attempts {
				statuses[effStatus(a.Status)]++
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

	// The benchmark (run via -bench, in its own all-passing package) must be
	// reported as passed, not interrupted — go test emits no per-test terminal
	// event for a passing benchmark.
	var bench *report.Test
	var findBench func(report.Suite)
	findBench = func(s report.Suite) {
		for i := range s.Tests {
			if s.Tests[i].Title == "BenchmarkAdd" {
				bench = &s.Tests[i]
			}
		}
		for _, sub := range s.Suites {
			findBench(sub)
		}
	}
	for _, s := range rep.Suites {
		findBench(s)
	}
	if bench == nil {
		t.Error("BenchmarkAdd missing from report")
	} else if effStatus(bench.Attempts[0].Status) != report.StatusPassed {
		t.Errorf("BenchmarkAdd status = %q, want passed", bench.Attempts[0].Status)
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

// fakeFlakinessServer implements the 4-step upload protocol and records the
// uploaded report.
func fakeFlakinessServer(t *testing.T, gotReport *report.Report) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/api/upload/start", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"uploadToken":        "utok",
			"presignedReportUrl": srv.URL + "/put",
			"webUrl":             "/org/proj/run/1",
		})
	})
	mux.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(brotli.NewReader(r.Body))
		json.Unmarshal(body, gotReport)
		w.WriteHeader(200)
	})
	mux.HandleFunc("/api/upload/finish", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	t.Cleanup(srv.Close)
	return srv
}

func runStdin(t *testing.T, cfg *config.Config, stream string) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cfg.Stdin = true
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	r := &Runner{
		Cfg:     cfg,
		Stdin:   strings.NewReader(stream),
		Stdout:  out,
		Stderr:  errb,
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return nil },
	}
	if _, err := r.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return r, out, errb
}

const sampleStream = `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestA"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestA","Elapsed":0.1}
`

func TestRunner_UploadsWithToken(t *testing.T) {
	var got report.Report
	srv := fakeFlakinessServer(t, &got)
	cfg := &config.Config{
		Name:        "go",
		CommitID:    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		AccessToken: "secret",
		Endpoint:    srv.URL,
		Project:     "max/flakiness-go",
		// no OutputDir: exercise upload-only
	}
	_, _, errb := runStdin(t, cfg, sampleStream)
	if got.CommitID != cfg.CommitID {
		t.Errorf("uploaded report commitId = %q, want %q", got.CommitID, cfg.CommitID)
	}
	if got.Category != "go" {
		t.Errorf("uploaded category = %q", got.Category)
	}
	if !strings.Contains(errb.String(), "uploaded") {
		t.Errorf("expected upload confirmation, stderr = %q", errb.String())
	}
}

func TestRunner_SkipsUploadWhenNoCommit(t *testing.T) {
	var got report.Report
	srv := fakeFlakinessServer(t, &got)
	cfg := &config.Config{
		Name:        "go",
		CommitID:    "", // unresolved
		AccessToken: "secret",
		Endpoint:    srv.URL,
	}
	_, _, errb := runStdin(t, cfg, sampleStream)
	if got.CommitID != "" {
		t.Errorf("should not have uploaded; server saw commitId %q", got.CommitID)
	}
	if !strings.Contains(errb.String(), "skipping upload") {
		t.Errorf("expected skip-upload warning, stderr = %q", errb.String())
	}
}

func TestRunner_DisableUploadWritesOnly(t *testing.T) {
	var got report.Report
	srv := fakeFlakinessServer(t, &got)
	outDir := filepath.Join(t.TempDir(), "rep")
	cfg := &config.Config{
		Name:          "go",
		CommitID:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		AccessToken:   "secret",
		Endpoint:      srv.URL,
		OutputDir:     outDir,
		DisableUpload: true,
	}
	runStdin(t, cfg, sampleStream)
	if got.CommitID != "" {
		t.Errorf("disable-upload should not upload; server saw %q", got.CommitID)
	}
	if _, err := os.Stat(filepath.Join(outDir, "report.json")); err != nil {
		t.Errorf("report.json should still be written: %v", err)
	}
}

func TestBuildEnvironment_OSNameAndFKEnv(t *testing.T) {
	r := &Runner{
		Cfg: &config.Config{Name: "go"},
		Environ: func() []string {
			return []string{
				"FK_ENV_GPU_TYPE=H100",      // value must be lowercased
				"fk_env_Region=  US-East  ", // case-insensitive prefix; value trimmed+lowercased
				"PATH=/usr/bin",             // ignored
			}
		},
	}
	env := r.buildEnvironment()

	// osName normalized per platform (matching the Node SDK).
	switch runtime.GOOS {
	case "darwin":
		if env.SystemData.OSName != "macos" {
			t.Errorf("darwin -> osName = %q, want macos", env.SystemData.OSName)
		}
	case "windows":
		if env.SystemData.OSName != "win" {
			t.Errorf("windows -> osName = %q, want win", env.SystemData.OSName)
		}
	case "linux":
		// The SDK uses the distro NAME from /etc/os-release (e.g. "ubuntu"),
		// falling back to "linux". Either is acceptable; never raw GOOS-only
		// when os-release exists. Just require it to be non-empty and lowercase.
		n := env.SystemData.OSName
		if n == "" {
			t.Error("linux -> osName is empty")
		}
		if n != strings.ToLower(n) {
			t.Errorf("linux -> osName = %q, want lowercase", n)
		}
	default:
		if env.SystemData.OSName != runtime.GOOS {
			t.Errorf("osName = %q, want %q", env.SystemData.OSName, runtime.GOOS)
		}
	}

	// osVersion must be populated on the major platforms — its absence is what
	// made Flakiness.io render the environment OS as "unknown".
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		if env.SystemData.OSVersion == "" {
			t.Errorf("osVersion is empty on %s; Flakiness.io shows OS as 'unknown' without it", runtime.GOOS)
		}
	}

	if got := env.Metadata["gpu_type"]; got != "h100" {
		t.Errorf("FK_ENV_GPU_TYPE -> %q, want lowercased h100", got)
	}
	if got := env.Metadata["region"]; got != "us-east" {
		t.Errorf("case-insensitive prefix + trim/lowercase -> %q, want us-east", got)
	}
	if _, ok := env.Metadata["path"]; ok {
		t.Errorf("non FK_ENV_ var leaked into metadata: %+v", env.Metadata)
	}
}

func TestBuildEnvironment_ExplicitMetadataWinsOverFKEnv(t *testing.T) {
	// The SDK merges { ...FK_ENV_*, ...explicitMetadata }, so a colliding
	// FK_ENV_GO_VERSION must NOT override the real go_version we set.
	r := &Runner{
		Cfg:     &config.Config{Name: "go"},
		Environ: func() []string { return []string{"FK_ENV_GO_VERSION=999.dont.win"} },
	}
	env := r.buildEnvironment()
	gv, _ := env.Metadata["go_version"].(string)
	if gv == "999.dont.win" || gv == "" {
		t.Errorf("go_version = %q; real Go version must win over FK_ENV_GO_VERSION", gv)
	}
}

func TestRunner_SkipsUploadOnInvalidCommit(t *testing.T) {
	var got report.Report
	srv := fakeFlakinessServer(t, &got)
	cfg := &config.Config{
		Name:        "go",
		CommitID:    "v1.2.3", // not a SHA, and not resolvable as a ref here
		AccessToken: "secret",
		Endpoint:    srv.URL,
	}
	_, _, errb := runStdin(t, cfg, sampleStream)
	if got.CommitID != "" {
		t.Errorf("should not have uploaded an invalid-commit report; server saw %q", got.CommitID)
	}
	if !strings.Contains(errb.String(), "not a 40-char SHA") {
		t.Errorf("expected invalid-SHA warning, stderr = %q", errb.String())
	}
}

func TestParseOSRelease(t *testing.T) {
	cases := []struct{ content, key, want string }{
		{"NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\n", "version_id", "24.04"}, // quoted
		{"NAME=Fedora\nVERSION_ID=40\n", "version_id", "40"},               // unquoted
		{"VERSION_ID=\"13\"", "version_id", "13"},                          // no trailing newline
		{"ID=alpine\nPRETTY_NAME=\"Alpine\"\n", "version_id", ""},          // missing key
		{"  VERSION_ID = nope\n", "version_id", ""},                        // spaces around = -> no match
		{"NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\n", "name", "ubuntu"},      // NAME lowercased
		{"NAME=Fedora Linux\n", "name", "fedora linux"},                    // value lowercased
	}
	for _, c := range cases {
		if got := parseOSRelease(c.content, c.key); got != c.want {
			t.Errorf("parseOSRelease(%q, %q) = %q, want %q", c.content, c.key, got, c.want)
		}
	}
}
