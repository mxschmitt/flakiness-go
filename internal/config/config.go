// Package config resolves flakiness-go options with the precedence
// CLI flag > environment variable > built-in default.
//
// Unlike pytest-flakiness there is no project ini file tier: Go projects have
// no equivalent of pytest.ini, so resolution stops at env/default.
package config

import (
	"flag"
	"os"
	"strings"

	"github.com/mxschmitt/flakiness-go/internal/gitinfo"
)

// Config is a resolved, immutable snapshot of every option.
type Config struct {
	OutputDir     string
	CommitID      string
	Name          string
	GitRoot       string
	Title         string
	Project       string
	AccessToken   string
	Endpoint      string
	DisableUpload bool
	Stdin         bool

	// GoTestArgs are the arguments forwarded to `go test` in wrapper mode.
	GoTestArgs []string
}

// rawFlags holds the values parsed from the command line before precedence is
// applied. A nil string pointer means the flag was not supplied.
type rawFlags struct {
	outputDir     *string
	commitID      *string
	name          *string
	gitRoot       *string
	title         *string
	project       *string
	accessToken   *string
	endpoint      *string
	disableUpload bool
	stdin         bool
	goTestArgs    []string
}

// Parse parses args (excluding the program name) and resolves the config.
func Parse(args []string) (*Config, error) {
	raw, err := parseFlags(args)
	if err != nil {
		return nil, err
	}
	return resolve(raw, os.Getenv), nil
}

// flakinessFlags is the set of string-valued options owned by flakiness-go.
// Everything else on the command line is forwarded to `go test`.
var flakinessFlags = map[string]bool{
	"flakiness-output-dir":   true,
	"flakiness-commit-id":    true,
	"flakiness-name":         true,
	"flakiness-git-root":     true,
	"flakiness-title":        true,
	"flakiness-project":      true,
	"flakiness-access-token": true,
	"flakiness-endpoint":     true,
}

// flakinessBools is the set of boolean options owned by flakiness-go.
var flakinessBools = map[string]bool{
	"flakiness-disable-upload": true,
	"stdin":                    true,
}

// partition separates flakiness-go's own flags from arguments destined for
// `go test`. This lets flakiness-go act as a transparent wrapper:
//
//	flakiness-go --flakiness-project=org/p -run TestFoo ./... -count=2
//
// `--flakiness-project=org/p` is consumed here; `-run TestFoo ./... -count=2`
// is forwarded verbatim. An explicit `--` forces everything after it to
// `go test`. Both single- and double-dash forms are accepted for our flags.
func partition(args []string) (ours []string, goTest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			goTest = append(goTest, args[i+1:]...)
			break
		}
		name, hasValue := flagName(a)
		switch {
		case flakinessBools[name]:
			ours = append(ours, a)
		case flakinessFlags[name]:
			ours = append(ours, a)
			if !hasValue && i+1 < len(args) {
				// value is the next token (e.g. `--flakiness-title Foo`)
				i++
				ours = append(ours, args[i])
			}
		default:
			goTest = append(goTest, a)
		}
	}
	return ours, goTest
}

// flagName extracts the canonical flag name from a token, reporting whether the
// token already carries an inline `=value`.
func flagName(tok string) (name string, hasValue bool) {
	if len(tok) == 0 || tok[0] != '-' {
		return "", false
	}
	t := strings.TrimLeft(tok, "-")
	if eq := strings.IndexByte(t, '='); eq >= 0 {
		return t[:eq], true
	}
	return t, false
}

func parseFlags(args []string) (*rawFlags, error) {
	ours, goTestArgs := partition(args)

	fs := flag.NewFlagSet("flakiness-go", flag.ContinueOnError)
	raw := &rawFlags{}

	// Use optional strings so we can distinguish "set on CLI" from "absent".
	outputDir := fs.String("flakiness-output-dir", "", "Directory to write the JSON report (default \"flakiness-report\")")
	commitID := fs.String("flakiness-commit-id", "", "Commit ID under test (default: git HEAD)")
	name := fs.String("flakiness-name", "", "Environment name / report category (default \"go\")")
	gitRoot := fs.String("flakiness-git-root", "", "Root directory for path normalization (default: git toplevel)")
	title := fs.String("flakiness-title", "", "Optional human-readable report title")
	project := fs.String("flakiness-project", "", "Flakiness.io project identifier (org/project)")
	accessToken := fs.String("flakiness-access-token", "", "Flakiness.io access token for upload")
	endpoint := fs.String("flakiness-endpoint", "", "Flakiness.io endpoint (default \"https://flakiness.io\")")
	fs.BoolVar(&raw.disableUpload, "flakiness-disable-upload", false, "Write the report but do not upload it")
	fs.BoolVar(&raw.stdin, "stdin", false, "Read a `go test -json` stream from stdin instead of running go test")

	if err := fs.Parse(ours); err != nil {
		return nil, err
	}

	// Record which string flags were explicitly set.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	pick := func(name string, v *string) *string {
		if set[name] {
			return v
		}
		return nil
	}
	raw.outputDir = pick("flakiness-output-dir", outputDir)
	raw.commitID = pick("flakiness-commit-id", commitID)
	raw.name = pick("flakiness-name", name)
	raw.gitRoot = pick("flakiness-git-root", gitRoot)
	raw.title = pick("flakiness-title", title)
	raw.project = pick("flakiness-project", project)
	raw.accessToken = pick("flakiness-access-token", accessToken)
	raw.endpoint = pick("flakiness-endpoint", endpoint)
	raw.goTestArgs = goTestArgs
	return raw, nil
}

// resolve applies CLI > env > default precedence. getenv is injectable.
func resolve(raw *rawFlags, getenv func(string) string) *Config {
	c := &Config{
		OutputDir:     str(raw.outputDir, getenv("FLAKINESS_OUTPUT_DIR"), "flakiness-report"),
		CommitID:      str(raw.commitID, getenv("FLAKINESS_COMMIT_ID"), ""),
		Name:          str(raw.name, getenv("FLAKINESS_NAME"), "go"),
		GitRoot:       str(raw.gitRoot, getenv("FLAKINESS_GIT_ROOT"), ""),
		Title:         str(raw.title, getenv("FLAKINESS_TITLE"), ""),
		Project:       str(raw.project, getenv("FLAKINESS_PROJECT"), ""),
		AccessToken:   str(raw.accessToken, getenv("FLAKINESS_ACCESS_TOKEN"), ""),
		Endpoint:      str(raw.endpoint, getenv("FLAKINESS_ENDPOINT"), "https://flakiness.io"),
		DisableUpload: boolOpt(raw.disableUpload, getenv("FLAKINESS_DISABLE_UPLOAD"), false),
		Stdin:         raw.stdin,
		GoTestArgs:    raw.goTestArgs,
	}
	// Lazily fill git-derived defaults only when still empty, so we never run
	// git subprocesses if the user supplied explicit values.
	if c.CommitID == "" {
		c.CommitID = gitinfo.Commit()
	}
	if c.GitRoot == "" {
		c.GitRoot = gitinfo.Root()
	}
	return c
}

// str picks the first non-empty of: CLI value, env value, default.
func str(cli *string, env, def string) string {
	if cli != nil {
		return *cli
	}
	if env != "" {
		return env
	}
	return def
}

// boolOpt resolves a boolean flag: CLI (only when true, since absence is false)
// > env > default.
func boolOpt(cli bool, env string, def bool) bool {
	if cli {
		return true
	}
	if env != "" {
		return isTruthy(env)
	}
	return def
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
