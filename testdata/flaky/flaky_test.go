// Package flaky is a fixture for the --rerun-failed integration test. It models
// the three outcomes the rerun loop must distinguish:
//
//   - TestFlaky fails on its first attempt then passes (recovers on rerun) —
//     the job should stay green while the flake is still surfaced as two
//     attempts in the report.
//   - TestStable always passes and must not be rerun.
//
// TestFlaky tracks attempts across process invocations via a marker file whose
// path is read from FLAKY_MARKER_DIR, so each `go test` re-invocation by the
// rerun loop sees the incremented count. -count=1 (added by the rerun loop)
// keeps the cache from short-circuiting the re-execution.
package flaky

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestStable(t *testing.T) {}

func TestFlaky(t *testing.T) {
	dir := os.Getenv("FLAKY_MARKER_DIR")
	if dir == "" {
		t.Skip("FLAKY_MARKER_DIR not set; only meaningful under the rerun integration test")
	}
	marker := filepath.Join(dir, "attempts")

	n := 0
	if b, err := os.ReadFile(marker); err == nil {
		n, _ = strconv.Atoi(string(b))
	}
	n++
	if err := os.WriteFile(marker, []byte(strconv.Itoa(n)), 0o644); err != nil {
		t.Fatalf("writing marker: %v", err)
	}

	if n == 1 {
		t.Fatalf("transient failure on attempt %d", n)
	}
}
