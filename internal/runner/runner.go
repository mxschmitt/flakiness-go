// Package runner orchestrates a flakiness-go invocation: obtain a
// `go test -json` stream (by running go test or reading stdin), convert it to a
// report, enrich it with metadata, write it to disk, and optionally upload it.
package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mxschmitt/flakiness-go/internal/ci"
	"github.com/mxschmitt/flakiness-go/internal/config"
	"github.com/mxschmitt/flakiness-go/internal/gitinfo"
	"github.com/mxschmitt/flakiness-go/internal/gotest"
	"github.com/mxschmitt/flakiness-go/internal/oidc"
	"github.com/mxschmitt/flakiness-go/internal/sources"
	"github.com/mxschmitt/flakiness-go/internal/telemetry"
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
	var sampler *telemetry.Sampler
	if r.Cfg.Stdin {
		// In stdin mode the tests already ran elsewhere, so sampling this
		// process's resource use would be meaningless.
		err = gotest.DecodeStream(r.Stdin, conv.Process)
	} else {
		// Wrapper mode: sample system CPU/RAM while `go test` runs.
		sampler = telemetry.NewSampler()
		sampler.Start()
		testExit, err = r.runGoTest(conv)
	}
	if err != nil {
		return 1, err
	}

	rep := conv.Build()
	r.fillMetadata(&rep)
	if sampler != nil {
		sampler.Stop(&rep, time.Now().UnixMilli())
	}

	// Embed source excerpts for every referenced location so the viewer can
	// show context. Best-effort: a no-op without a git root.
	sources.Collect(&rep, r.Cfg.GitRoot)

	if r.Cfg.OutputDir != "" {
		if err := report.WriteDir(&rep, r.Cfg.OutputDir); err != nil {
			return testExit, fmt.Errorf("writing report: %w", err)
		}
		fmt.Fprintf(r.Stderr, "[Flakiness] Report written to %s\n", r.Cfg.OutputDir)
	}

	if !r.Cfg.DisableUpload {
		// The spec requires commitId to be a full 40-char SHA. Don't upload a
		// report that would be rejected — but the local report is still written
		// above for inspection.
		if !gitinfo.IsFullSHA(rep.CommitID) {
			fmt.Fprintf(r.Stderr, "[Flakiness] Warning: commit id %q is not a 40-char SHA; skipping upload (set --flakiness-commit-id or run inside a git repo)\n", rep.CommitID)
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
	rep.CommitID = r.normalizedCommit()
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
			OSName:    osName(),
			OSVersion: osVersion(),
			OSArch:    osArch(),
		},
		Metadata: map[string]any{},
	}
	// Merge order mirrors the Node SDK (createEnvironment.ts):
	// `{ ...FK_ENV_*, ...explicitMetadata }`, i.e. FK_ENV_* are applied first
	// and reporter-supplied metadata (here `go_version`) wins on collision.
	const prefix = "FK_ENV_"
	for _, kv := range r.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		if strings.HasPrefix(strings.ToUpper(k), prefix) {
			// Match the SDK: key has its prefix stripped and is lowercased, and
			// the VALUE is trimmed and lowercased too. Keeping these in lockstep
			// matters because the server dedups environments by a hash of the
			// whole object and FQL queries match on these normalized values.
			key := strings.ToLower(k[len(prefix):])
			env.Metadata[key] = strings.ToLower(strings.TrimSpace(v))
		}
	}
	env.Metadata["go_version"] = strings.TrimPrefix(runtime.Version(), "go")
	return env
}

// normalizedCommit returns the best available commit SHA: a full 40-char SHA is
// used as-is; anything else (short SHA, ref, tag) is expanded via git when
// possible. The raw value is returned otherwise so it is still recorded locally
// and the upload gate can warn that it isn't a valid SHA.
func (r *Runner) normalizedCommit() string {
	c := r.Cfg.CommitID
	if c == "" || gitinfo.IsFullSHA(c) {
		return c
	}
	if full := gitinfo.ExpandCommit(c); full != "" {
		return full
	}
	return c
}

// osName matches the Flakiness.io osName convention used by the other reporters
// (createEnvironment.ts): macOS → "macos", Windows → "win", Linux → the distro
// NAME from /etc/os-release (lowercased, e.g. "ubuntu"), falling back to the
// GOOS. Matching these keeps FQL filters and environment dedup consistent
// across reporters.
func osName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "win"
	case "linux":
		if n := linuxOSReleaseName(); n != "" {
			return n
		}
		return "linux"
	default:
		return runtime.GOOS
	}
}

// osArch matches the SDK's osArch, which on Unix is `uname -m` (e.g. "x86_64",
// "aarch64"/"arm64") rather than Go's GOARCH ("amd64"/"arm64"). On platforms
// without uname (Windows) it falls back to GOARCH, like the SDK uses
// process.arch there.
func osArch() string {
	if runtime.GOOS == "windows" {
		return runtime.GOARCH
	}
	if out, err := exec.Command("uname", "-m").Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return runtime.GOARCH
}

// osVersion returns the OS version string, matching how the Node SDK populates
// systemData.osVersion (createEnvironment.ts): macOS via `sw_vers
// -productVersion`, Linux via VERSION_ID in /etc/os-release, Windows via the
// kernel version. Returns "" when it can't be determined (the field is then
// omitted). Without it, Flakiness.io shows the environment OS as "unknown".
func osVersion() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		if v := linuxOSReleaseVersionID(); v != "" {
			return v
		}
	case "windows":
		// The Node SDK uses os.release(), which yields a bare kernel version
		// like "10.0.26100". `cmd /c ver` prints the decorated banner
		// "Microsoft Windows [Version 10.0.26100.32995]"; extract just the
		// version number so we match the SDK's clean value rather than the
		// banner.
		if out, err := exec.Command("cmd", "/c", "ver").Output(); err == nil {
			return parseWindowsVer(strings.TrimSpace(string(out)))
		}
	}
	return ""
}

// windowsVerRe extracts a dotted version (e.g. 10.0.26100.32995) from the
// `cmd /c ver` banner.
var windowsVerRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// parseWindowsVer pulls the bare version number out of the `ver` banner,
// falling back to the trimmed banner if no version is found.
func parseWindowsVer(banner string) string {
	if m := windowsVerRe.FindString(banner); m != "" {
		return m
	}
	return banner
}

// linuxOSReleaseVersionID reads VERSION_ID (e.g. "24.04") from /etc/os-release.
func linuxOSReleaseVersionID() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return parseOSRelease(string(data), "version_id")
}

// linuxOSReleaseName reads NAME (e.g. "ubuntu") from /etc/os-release.
func linuxOSReleaseName() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return parseOSRelease(string(data), "name")
}

// parseOSRelease extracts a key from /etc/os-release content. The SDK
// (readLinuxOSRelease) lowercases the whole file before parsing, so both the
// key and value come back lowercased; surrounding quotes are stripped
// (e.g. `NAME="Ubuntu"` with key "name" -> "ubuntu").
func parseOSRelease(content, key string) string {
	content = strings.ToLower(content)
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
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
