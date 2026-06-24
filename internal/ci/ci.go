// Package ci detects the URL of the CI job that produced a test run.
package ci

import (
	"net/url"
	"os"
)

// Getenv is the environment lookup used for detection; overridable in tests.
var Getenv = os.Getenv

// RunURL returns the best-effort URL of the current CI run, or "" when not
// running in a recognized CI system. Mirrors the detection in pytest-flakiness.
func RunURL() string {
	if u := githubActionsURL(); u != "" {
		return u
	}
	if u := azureDevOpsURL(); u != "" {
		return u
	}
	if u := Getenv("CI_JOB_URL"); u != "" { // GitLab CI
		return u
	}
	if u := Getenv("BUILD_URL"); u != "" { // Jenkins
		return u
	}
	return ""
}

func githubActionsURL() string {
	repo := Getenv("GITHUB_REPOSITORY")
	runID := Getenv("GITHUB_RUN_ID")
	if repo == "" || runID == "" {
		return ""
	}
	server := Getenv("GITHUB_SERVER_URL")
	if server == "" {
		server = "https://github.com"
	}
	u := server + "/" + repo + "/actions/runs/" + runID
	query := ""
	if attempt := Getenv("GITHUB_RUN_ATTEMPT"); attempt != "" {
		query = "attempt=" + attempt + "&"
	}
	query += "check_suite_focus=true"
	return u + "?" + query
}

func azureDevOpsURL() string {
	collection := Getenv("SYSTEM_TEAMFOUNDATIONCOLLECTIONURI")
	project := Getenv("SYSTEM_TEAMPROJECT")
	buildID := Getenv("BUILD_BUILDID")
	if collection == "" || project == "" || buildID == "" {
		return ""
	}
	base := collection
	if base[len(base)-1] != '/' {
		base += "/"
	}
	return base + url.PathEscape(project) + "/_build/results?buildId=" + buildID
}
