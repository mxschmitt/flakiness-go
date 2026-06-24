package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteDir writes the report as <dir>/report.json alongside an <dir>/attachments
// directory, matching the on-disk layout in the spec. A pre-existing report
// directory is cleared first so stale attachments don't linger.
//
// To avoid clobbering an unrelated directory the user may have pointed at by
// mistake, WriteDir refuses to delete a non-empty directory that does not look
// like a report directory (i.e. has no report.json). In that case it returns an
// error rather than removing the contents.
func WriteDir(rep *Report, dir string) error {
	if err := clearReportDir(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.json"), data, 0o644)
}

// clearReportDir removes dir if it is safe to do so: it must not exist, be
// empty, or already be a report directory (contain report.json).
func clearReportDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil // nothing to clear
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.RemoveAll(dir)
	}
	for _, e := range entries {
		if e.Name() == "report.json" {
			return os.RemoveAll(dir) // looks like a prior report dir
		}
	}
	return fmt.Errorf("refusing to overwrite %q: it is not empty and does not look like a report directory (no report.json). Choose a different --flakiness-output-dir", dir)
}
