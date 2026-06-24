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

func TestIsFullSHA(t *testing.T) {
	valid := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if !IsFullSHA(valid) {
		t.Errorf("IsFullSHA(%q) = false, want true", valid)
	}
	for _, bad := range []string{
		"",
		"deadbeef", // short
		"DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEADBEEF", // uppercase
		"v1.2.3", // tag
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefx", // 41 / non-hex
		"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",  // non-hex
	} {
		if IsFullSHA(bad) {
			t.Errorf("IsFullSHA(%q) = true, want false", bad)
		}
	}
}

func TestExpandCommit(t *testing.T) {
	if !hasGit() || Commit() == "" {
		t.Skip("not in a git repo")
	}
	full := Commit()
	// A short prefix of HEAD should expand back to the full SHA.
	short := full[:8]
	if got := ExpandCommit(short); got != full {
		t.Errorf("ExpandCommit(%q) = %q, want %q", short, got, full)
	}
	if got := ExpandCommit("definitely-not-a-ref-xyz"); got != "" {
		t.Errorf("ExpandCommit(bogus) = %q, want empty", got)
	}
}
