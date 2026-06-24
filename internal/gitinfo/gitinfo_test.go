package gitinfo

import (
	"os/exec"
	"regexp"
	"testing"
)

// These tests run against flakiness-go's own git repository (the package is
// always built/tested from within it).

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func TestCommit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	c := Commit()
	if c == "" {
		t.Skip("not in a git repo (e.g. tarball build)")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(c) {
		t.Errorf("Commit() = %q, want a 40-char hex SHA", c)
	}
}

func TestRoot(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	r := Root()
	if r == "" {
		t.Skip("not in a git repo")
	}
	// The root should be an absolute path that exists.
	if r[0] != '/' && !regexp.MustCompile(`^[A-Za-z]:`).MatchString(r) {
		t.Errorf("Root() = %q, want an absolute path", r)
	}
}
