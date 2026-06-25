package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sampleReport() *Report {
	return &Report{
		Category:       "go",
		CommitID:       "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		Environments:   []Environment{{Name: "go"}},
		StartTimestamp: 1,
		Duration:       2,
	}
}

func TestWriteDir_CreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flakiness-report")
	if err := WriteDir(sampleReport(), dir); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("report.json not written: %v", err)
	}
	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("report.json invalid: %v", err)
	}
	if rep.Category != "go" {
		t.Errorf("category = %q", rep.Category)
	}
	if _, err := os.Stat(filepath.Join(dir, "attachments")); err != nil {
		t.Errorf("attachments dir missing: %v", err)
	}
}

func TestWriteDir_ClearsPriorReport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flakiness-report")
	// First write leaves a stale attachment behind.
	if err := WriteDir(sampleReport(), dir); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "attachments", "stale")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second write must clear it (the dir has report.json → safe to remove).
	if err := WriteDir(sampleReport(), dir); err != nil {
		t.Fatalf("second WriteDir: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale attachment should have been cleared, stat err = %v", err)
	}
}

func TestWriteDir_RefusesNonReportDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "important-stuff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(precious, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteDir(sampleReport(), dir)
	if err == nil {
		t.Fatal("expected WriteDir to refuse a non-empty non-report directory")
	}
	// The user's file must be untouched.
	if _, statErr := os.Stat(precious); statErr != nil {
		t.Errorf("precious file was deleted: %v", statErr)
	}
}

func TestWriteDir_EmptyDirOK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDir(sampleReport(), dir); err != nil {
		t.Errorf("WriteDir into empty dir should succeed: %v", err)
	}
}

func TestRunAttempt_MarshalStripsDefaults(t *testing.T) {
	// A passing attempt at env 0 with zero duration should strip status,
	// expectedStatus, environmentIdx, and duration — matching the Node SDK's
	// normalizeReport/cleanupAttempt.
	pass := RunAttempt{
		EnvironmentIdx: 0,
		ExpectedStatus: StatusPassed,
		Status:         StatusPassed,
		StartTimestamp: 100,
		Duration:       0,
	}
	data, err := json.Marshal(pass)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	for _, k := range []string{"status", "expectedStatus", "environmentIdx", "duration"} {
		if _, ok := m[k]; ok {
			t.Errorf("passing attempt should omit %q, got %s", k, data)
		}
	}
	if _, ok := m["startTimestamp"]; !ok {
		t.Errorf("startTimestamp must always be present: %s", data)
	}

	// A failing attempt keeps status and duration; still strips expectedStatus
	// (passed) and environmentIdx (0).
	fail := RunAttempt{Status: StatusFailed, ExpectedStatus: StatusPassed, StartTimestamp: 100, Duration: 1500}
	data, _ = json.Marshal(fail)
	json.Unmarshal(data, &m)
	if m["status"] != "failed" {
		t.Errorf("failed attempt must keep status: %s", data)
	}
	if m["duration"] != float64(1500) {
		t.Errorf("failed attempt must keep non-zero duration: %s", data)
	}
	if _, ok := m["expectedStatus"]; ok {
		t.Errorf("expectedStatus=passed should be stripped: %s", data)
	}
	if _, ok := m["environmentIdx"]; ok {
		t.Errorf("environmentIdx=0 should be stripped: %s", data)
	}

	// Non-default env index is preserved.
	envd := RunAttempt{EnvironmentIdx: 2, Status: StatusPassed, ExpectedStatus: StatusPassed, StartTimestamp: 1}
	data, _ = json.Marshal(envd)
	json.Unmarshal(data, &m)
	if m["environmentIdx"] != float64(2) {
		t.Errorf("non-zero environmentIdx must be kept: %s", data)
	}
}
