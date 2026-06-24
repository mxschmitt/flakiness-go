package report

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteDir writes the report as <dir>/report.json and creates an empty
// <dir>/attachments directory, matching the on-disk layout in the spec. Any
// pre-existing directory contents are removed first.
func WriteDir(rep *Report, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
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
