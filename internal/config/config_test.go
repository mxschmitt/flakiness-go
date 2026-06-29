package config

import (
	"strings"
	"testing"
)

// env builds a getenv func from a map.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve_Defaults(t *testing.T) {
	raw, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	if c.OutputDir != "flakiness-report" {
		t.Errorf("OutputDir default = %q", c.OutputDir)
	}
	if c.Name != "go" {
		t.Errorf("Name default = %q", c.Name)
	}
	if c.Endpoint != "https://flakiness.io" {
		t.Errorf("Endpoint default = %q", c.Endpoint)
	}
	if c.DisableUpload {
		t.Error("DisableUpload should default false")
	}
}

func TestResolve_EnvOverridesDefault(t *testing.T) {
	raw, _ := parseFlags(nil)
	c := resolve(raw, env(map[string]string{
		"FLAKINESS_NAME":     "from-env",
		"FLAKINESS_TITLE":    "Env Title",
		"FLAKINESS_ENDPOINT": "https://example.test",
	}))
	if c.Name != "from-env" {
		t.Errorf("Name = %q, want from-env", c.Name)
	}
	if c.Title != "Env Title" {
		t.Errorf("Title = %q", c.Title)
	}
	if c.Endpoint != "https://example.test" {
		t.Errorf("Endpoint = %q", c.Endpoint)
	}
}

func TestResolve_CLIOverridesEnv(t *testing.T) {
	raw, err := parseFlags([]string{
		"--flakiness-name=from-cli",
		"--flakiness-title=CLI Title",
		"./...",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(map[string]string{
		"FLAKINESS_NAME":  "from-env",
		"FLAKINESS_TITLE": "Env Title",
	}))
	if c.Name != "from-cli" {
		t.Errorf("Name = %q, want from-cli (CLI wins)", c.Name)
	}
	if c.Title != "CLI Title" {
		t.Errorf("Title = %q, want CLI Title", c.Title)
	}
	if len(c.GoTestArgs) != 1 || c.GoTestArgs[0] != "./..." {
		t.Errorf("GoTestArgs = %v, want [./...]", c.GoTestArgs)
	}
}

func TestResolve_DisableUploadEnvTruthy(t *testing.T) {
	raw, _ := parseFlags(nil)
	for _, v := range []string{"1", "true", "YES", "on"} {
		c := resolve(raw, env(map[string]string{"FLAKINESS_DISABLE_UPLOAD": v}))
		if !c.DisableUpload {
			t.Errorf("DisableUpload for %q = false, want true", v)
		}
	}
	c := resolve(raw, env(map[string]string{"FLAKINESS_DISABLE_UPLOAD": "false"}))
	if c.DisableUpload {
		t.Error("DisableUpload for 'false' = true, want false")
	}
}

func TestResolve_CLIDisableUploadWins(t *testing.T) {
	raw, err := parseFlags([]string{"--flakiness-disable-upload"})
	if err != nil {
		t.Fatal(err)
	}
	// Even with env saying false, the explicit CLI flag forces true.
	c := resolve(raw, env(map[string]string{"FLAKINESS_DISABLE_UPLOAD": "false"}))
	if !c.DisableUpload {
		t.Error("explicit --flakiness-disable-upload should win over env false")
	}
}

func TestResolve_GoTestArgsForwarded(t *testing.T) {
	raw, err := parseFlags([]string{"-run", "TestFoo", "./pkg/...", "-count=2"})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	want := []string{"-run", "TestFoo", "./pkg/...", "-count=2"}
	if len(c.GoTestArgs) != len(want) {
		t.Fatalf("GoTestArgs = %v, want %v", c.GoTestArgs, want)
	}
	for i := range want {
		if c.GoTestArgs[i] != want[i] {
			t.Errorf("GoTestArgs[%d] = %q, want %q", i, c.GoTestArgs[i], want[i])
		}
	}
}

func TestResolve_StdinFlag(t *testing.T) {
	raw, err := parseFlags([]string{"--stdin"})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	if !c.Stdin {
		t.Error("Stdin flag not parsed")
	}
}

func TestResolve_RerunFailedDefaults(t *testing.T) {
	raw, _ := parseFlags(nil)
	c := resolve(raw, env(nil))
	if c.RerunFailed != 0 {
		t.Errorf("RerunFailed default = %d, want 0 (off)", c.RerunFailed)
	}
	if c.RerunMaxFailures != defaultRerunMaxFailures {
		t.Errorf("RerunMaxFailures default = %d, want %d", c.RerunMaxFailures, defaultRerunMaxFailures)
	}
	if !c.RerunAbortOnDataRace {
		t.Error("RerunAbortOnDataRace should default true")
	}
}

func TestResolve_RerunFailedBareUsesDefault(t *testing.T) {
	// `--rerun-failed` with no value resolves to defaultRerunFailed and must NOT
	// consume the following package list as its value.
	raw, err := parseFlags([]string{"--rerun-failed", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	if c.RerunFailed != defaultRerunFailed {
		t.Errorf("bare --rerun-failed = %d, want %d", c.RerunFailed, defaultRerunFailed)
	}
	if len(c.GoTestArgs) != 1 || c.GoTestArgs[0] != "./..." {
		t.Errorf("GoTestArgs = %v, want [./...] (package list must not be swallowed)", c.GoTestArgs)
	}
}

func TestResolve_RerunFailedExplicitValue(t *testing.T) {
	raw, err := parseFlags([]string{"--rerun-failed=3", "-race", "./pkg/..."})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	if c.RerunFailed != 3 {
		t.Errorf("RerunFailed = %d, want 3", c.RerunFailed)
	}
	want := []string{"-race", "./pkg/..."}
	if len(c.GoTestArgs) != len(want) {
		t.Fatalf("GoTestArgs = %v, want %v", c.GoTestArgs, want)
	}
}

func TestResolve_RerunFailedRejectsNegative(t *testing.T) {
	if _, err := parseFlags([]string{"--rerun-failed=-1"}); err == nil {
		t.Error("--rerun-failed=-1 should be rejected")
	}
	if _, err := parseFlags([]string{"--rerun-failed=abc"}); err == nil {
		t.Error("--rerun-failed=abc should be rejected")
	}
}

func TestResolve_RerunFailedSpaceSeparatedValueRejected(t *testing.T) {
	// `--rerun-failed 3 ./...` is a mistake: the flag takes its value inline, so
	// the bare `3` would otherwise be misrouted to go test. Reject with guidance.
	_, err := parseFlags([]string{"--rerun-failed", "3", "./..."})
	if err == nil {
		t.Fatal("--rerun-failed 3 (space-separated) should be rejected with guidance")
	}
	if !strings.Contains(err.Error(), "--rerun-failed=3") {
		t.Errorf("error should suggest the = form, got %q", err.Error())
	}
}

func TestResolve_RerunFailedBareBeforeFlagOK(t *testing.T) {
	// A bare --rerun-failed followed by a non-integer (a real go test flag or
	// package) is fine and must not be flagged as the space-separated mistake.
	raw, err := parseFlags([]string{"--rerun-failed", "-race", "./..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := resolve(raw, env(nil))
	if c.RerunFailed != defaultRerunFailed {
		t.Errorf("RerunFailed = %d, want %d", c.RerunFailed, defaultRerunFailed)
	}
	if len(c.GoTestArgs) != 2 || c.GoTestArgs[0] != "-race" {
		t.Errorf("GoTestArgs = %v, want [-race ./...]", c.GoTestArgs)
	}
}

func TestResolve_RerunFailedEnv(t *testing.T) {
	raw, _ := parseFlags(nil)
	c := resolve(raw, env(map[string]string{
		"FLAKINESS_RERUN_FAILED":              "4",
		"FLAKINESS_RERUN_FAILED_MAX_FAILURES": "20",
	}))
	if c.RerunFailed != 4 {
		t.Errorf("RerunFailed from env = %d, want 4", c.RerunFailed)
	}
	if c.RerunMaxFailures != 20 {
		t.Errorf("RerunMaxFailures from env = %d, want 20", c.RerunMaxFailures)
	}
}

func TestResolve_RerunFailedCLIOverridesEnv(t *testing.T) {
	raw, err := parseFlags([]string{"--rerun-failed=1"})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(map[string]string{"FLAKINESS_RERUN_FAILED": "9"}))
	if c.RerunFailed != 1 {
		t.Errorf("RerunFailed = %d, want 1 (CLI wins over env)", c.RerunFailed)
	}
}

func TestResolve_RerunAbortOnDataRaceExplicitFalse(t *testing.T) {
	raw, err := parseFlags([]string{"--rerun-failed-abort-on-data-race=false"})
	if err != nil {
		t.Fatal(err)
	}
	c := resolve(raw, env(nil))
	if c.RerunAbortOnDataRace {
		t.Error("explicit --rerun-failed-abort-on-data-race=false should win over the true default")
	}
}

func TestResolve_MaxFailuresEnvZeroMeansUnlimited(t *testing.T) {
	raw, _ := parseFlags(nil)
	c := resolve(raw, env(map[string]string{"FLAKINESS_RERUN_FAILED_MAX_FAILURES": "0"}))
	if c.RerunMaxFailures != 0 {
		t.Errorf("RerunMaxFailures = %d, want 0 (unlimited)", c.RerunMaxFailures)
	}
}
