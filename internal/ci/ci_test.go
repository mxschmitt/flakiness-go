package ci

import "testing"

// withEnv temporarily replaces Getenv with a map-backed lookup.
func withEnv(t *testing.T, m map[string]string) {
	t.Helper()
	prev := Getenv
	Getenv = func(k string) string { return m[k] }
	t.Cleanup(func() { Getenv = prev })
}

func TestRunURL_GithubActions(t *testing.T) {
	withEnv(t, map[string]string{
		"GITHUB_REPOSITORY":  "octo/repo",
		"GITHUB_RUN_ID":      "42",
		"GITHUB_RUN_ATTEMPT": "2",
	})
	got := RunURL()
	want := "https://github.com/octo/repo/actions/runs/42?attempt=2&check_suite_focus=true"
	if got != want {
		t.Errorf("RunURL = %q, want %q", got, want)
	}
}

func TestRunURL_GithubActionsCustomServer(t *testing.T) {
	withEnv(t, map[string]string{
		"GITHUB_REPOSITORY": "octo/repo",
		"GITHUB_RUN_ID":     "42",
		"GITHUB_SERVER_URL": "https://ghe.example.com",
	})
	got := RunURL()
	want := "https://ghe.example.com/octo/repo/actions/runs/42?check_suite_focus=true"
	if got != want {
		t.Errorf("RunURL = %q, want %q", got, want)
	}
}

func TestRunURL_AzureDevOps(t *testing.T) {
	withEnv(t, map[string]string{
		"SYSTEM_TEAMFOUNDATIONCOLLECTIONURI": "https://dev.azure.com/org",
		"SYSTEM_TEAMPROJECT":                 "My Project",
		"BUILD_BUILDID":                      "99",
	})
	got := RunURL()
	want := "https://dev.azure.com/org/My%20Project/_build/results?buildId=99"
	if got != want {
		t.Errorf("RunURL = %q, want %q", got, want)
	}
}

func TestRunURL_GitLab(t *testing.T) {
	withEnv(t, map[string]string{"CI_JOB_URL": "https://gitlab.com/x/-/jobs/7"})
	if got := RunURL(); got != "https://gitlab.com/x/-/jobs/7" {
		t.Errorf("RunURL = %q", got)
	}
}

func TestRunURL_Jenkins(t *testing.T) {
	withEnv(t, map[string]string{"BUILD_URL": "https://jenkins.example/job/1/"})
	if got := RunURL(); got != "https://jenkins.example/job/1/" {
		t.Errorf("RunURL = %q", got)
	}
}

func TestRunURL_None(t *testing.T) {
	withEnv(t, map[string]string{})
	if got := RunURL(); got != "" {
		t.Errorf("RunURL = %q, want empty", got)
	}
}

func TestRunURL_Precedence(t *testing.T) {
	// GitHub Actions wins over GitLab/Jenkins when several are present.
	withEnv(t, map[string]string{
		"GITHUB_REPOSITORY": "octo/repo",
		"GITHUB_RUN_ID":     "1",
		"CI_JOB_URL":        "https://gitlab.com/x",
		"BUILD_URL":         "https://jenkins/x",
	})
	got := RunURL()
	if got != "https://github.com/octo/repo/actions/runs/1?check_suite_focus=true" {
		t.Errorf("precedence wrong: %q", got)
	}
}
