// Package gitinfo provides read-only git lookups used to populate the report's
// commitId and to normalize file paths against the repository root.
package gitinfo

import (
	"os/exec"
	"strings"
)

// safeEnv bypasses git's "dubious ownership" check (CVE-2022-24765) for our
// read-only calls without touching the user's global git config. This matters
// in CI containers where the repo is bind-mounted with a different UID.
var safeEnv = []string{
	"GIT_CONFIG_COUNT=1",
	"GIT_CONFIG_KEY_0=safe.directory",
	"GIT_CONFIG_VALUE_0=*",
}

func run(args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	// Append to (not replace) the inherited environment.
	cmd.Env = append(cmd.Environ(), safeEnv...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Commit returns the current HEAD commit SHA, or "" if unavailable.
func Commit() string {
	if v, ok := run("rev-parse", "HEAD"); ok {
		return v
	}
	return ""
}

// Root returns the absolute path of the git repository root, or "" if not in
// a git repository.
func Root() string {
	if v, ok := run("rev-parse", "--show-toplevel"); ok {
		return v
	}
	return ""
}
