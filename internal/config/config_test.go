package config

import "testing"

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
