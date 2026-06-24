// Package runner orchestrates a flakiness-go invocation: obtain a
// `go test -json` stream (by running go test or reading stdin), convert it to a
// report, enrich it with metadata, write it to disk, and optionally upload it.
package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mxschmitt/flakiness-go/internal/ci"
	"github.com/mxschmitt/flakiness-go/internal/config"
	"github.com/mxschmitt/flakiness-go/internal/gotest"
	"github.com/mxschmitt/flakiness-go/internal/oidc"
	"github.com/mxschmitt/flakiness-go/internal/upload"
	"github.com/mxschmitt/flakiness-go/report"
)

// Version is the reporter version, stamped into generatedBy.
const Version = "0.1.0"

// Runner holds the resolved configuration and IO streams.
type Runner struct {
	Cfg    *config.Config
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Getenv is used for FK_ENV_* discovery; defaults to os.Getenv.
	Getenv func(string) string
	// Environ lists environment entries; defaults to os.Environ.
	Environ func() []string
}

// Run executes the reporter and returns the process exit code. The exit code
// reflects the underlying `go test` result in wrapper mode so flakiness-go is a
// drop-in prefix in CI.
func (r *Runner) Run() (int, error) {
	if r.Getenv == nil {
		r.Getenv = os.Getenv
	}
	if r.Environ == nil {
		r.Environ = os.Environ
	}

	conv := &gotest.Converter{}
	if r.Cfg.GitRoot != "" {
		conv.Locator = gotest.NewSourceLocator(r.Cfg.GitRoot)
	}

	testExit := 0
	var err error
	if r.Cfg.Stdin {
		err = gotest.DecodeStream(r.Stdin, conv.Process)
	} else {
		testExit, err = r.runGoTest(conv)
	}
	if err != nil {
		return 1, err
	}

	rep := conv.Build()
	r.fillMetadata(&rep)

	if r.Cfg.OutputDir != "" {
		if err := report.WriteDir(&rep, r.Cfg.OutputDir); err != nil {
			return testExit, fmt.Errorf("writing report: %w", err)
		}
		fmt.Fprintf(r.Stderr, "[Flakiness] Report written to %s\n", r.Cfg.OutputDir)
	}

	if !r.Cfg.DisableUpload {
		// A report with an empty commitId is rejected by the spec (commitId is
		// required and must be a 40-char SHA), so don't upload one — but the
		// local report is still written above for inspection.
		if r.Cfg.CommitID == "" {
			fmt.Fprintln(r.Stderr, "[Flakiness] Warning: no commit id resolved; skipping upload (set --flakiness-commit-id or run inside a git repo)")
		} else {
			r.maybeUpload(&rep)
		}
	}

	return testExit, nil
}

// runGoTest runs `go test -json <args>`, feeds the event stream into the
// converter, and returns go test's exit code. Each decoded `output` event is
// re-emitted to stdout so the developer still sees normal, human-readable
// `go test` output (the concatenation of all output events is exactly the
// original test output) rather than a silent run or raw JSON.
func (r *Runner) runGoTest(conv *gotest.Converter) (int, error) {
	args := append([]string{"test", "-json"}, r.Cfg.GoTestArgs...)
	cmd := exec.Command("go", args...)
	cmd.Stderr = r.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	if err := cmd.Start(); err != nil {
		return 1, err
	}

	decodeErr := gotest.DecodeStream(stdout, func(ev gotest.TestEvent) error {
		if ev.Action == gotest.ActionOutput && r.Stdout != nil {
			io.WriteString(r.Stdout, ev.Output)
		}
		return conv.Process(ev)
	})

	waitErr := cmd.Wait()
	if decodeErr != nil {
		return 1, decodeErr
	}
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, waitErr
	}
	return 0, nil
}

func (r *Runner) fillMetadata(rep *report.Report) {
	rep.Category = r.Cfg.Name
	rep.CommitID = r.Cfg.CommitID
	rep.Title = r.Cfg.Title
	if r.Cfg.Project != "" {
		rep.FlakinessProject = r.Cfg.Project
	}
	if u := ci.RunURL(); u != "" {
		rep.URL = u
	}
	rep.GeneratedBy = &report.NameVersion{Name: "flakiness-go", Version: Version}
	rep.TestRunner = &report.NameVersion{Name: "go test", Version: goToolVersion()}
	rep.Runtime = &report.NameVersion{Name: "go", Version: strings.TrimPrefix(runtime.Version(), "go")}
	rep.Environments = []report.Environment{r.buildEnvironment()}
}

func (r *Runner) buildEnvironment() report.Environment {
	env := report.Environment{
		Name: r.Cfg.Name,
		SystemData: &report.SystemData{
			OSName: runtime.GOOS,
			OSArch: runtime.GOARCH,
		},
		Metadata: map[string]any{
			"go_version": strings.TrimPrefix(runtime.Version(), "go"),
		},
	}
	const prefix = "FK_ENV_"
	for _, kv := range r.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		if strings.HasPrefix(k, prefix) {
			env.Metadata[strings.ToLower(strings.TrimPrefix(k, prefix))] = v
		}
	}
	return env
}

func (r *Runner) maybeUpload(rep *report.Report) {
	token := r.Cfg.AccessToken
	if token == "" {
		if gh := oidc.FromEnv(); gh != nil {
			if r.Cfg.Project == "" {
				if r.Getenv("CI") != "" {
					fmt.Fprintln(r.Stderr, "[Flakiness] Warning: skipping upload — flakinessProject is not configured for GitHub OIDC")
				}
				return
			}
			t, err := gh.FetchToken(r.Cfg.Project)
			if err != nil {
				fmt.Fprintf(r.Stderr, "[Flakiness] Error fetching GitHub OIDC token: %v\n", err)
				return
			}
			token = t
		}
	}
	if token == "" {
		return
	}
	client := upload.New(r.Cfg.Endpoint)
	url, err := client.Upload(rep, nil, token)
	if err != nil {
		fmt.Fprintf(r.Stderr, "[Flakiness] Upload failed: %v\n", err)
		return
	}
	fmt.Fprintf(r.Stderr, "[Flakiness] Report uploaded: %s\n", url)
}

func goToolVersion() string {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return strings.TrimPrefix(runtime.Version(), "go")
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "go")
}
