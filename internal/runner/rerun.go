package runner

import (
	"io"
	"sort"
	"strings"

	"github.com/mxschmitt/flakiness-go/internal/gotest"
)

// testKey identifies a single test by package and full go test name
// ("TestX/sub/case"), so reruns track recovery per individual test.
type testKey struct{ pkg, name string }

// roundObserver watches one `go test -json` round: it forwards every event to
// the shared converter (so attempts accumulate across rounds), echoes output to
// the developer's stdout, and records each test's terminal outcome this round
// so the caller knows which originally-failed tests have since recovered.
type roundObserver struct {
	conv   *gotest.Converter
	stdout io.Writer

	// failed/passed record the terminal outcome of each test this round.
	failed map[testKey]bool
	passed map[testKey]bool
	// hadTest records packages that emitted at least one per-test event, so a
	// package-level `fail` (which always accompanies a failed test) isn't
	// mistaken for a build/setup failure.
	hadTest map[string]bool
	// pkgFailed records packages that emitted a package-level `fail`. A package
	// that fails with no per-test event is a build error / TestMain or init
	// panic — a failure reruns can't recover from.
	pkgFailed map[string]bool
	// buildFail is set on an explicit build-fail event (compile error).
	buildFail bool
	dataRace  bool
}

func newRoundObserver(conv *gotest.Converter, stdout io.Writer) *roundObserver {
	return &roundObserver{
		conv:      conv,
		stdout:    stdout,
		failed:    map[testKey]bool{},
		passed:    map[testKey]bool{},
		hadTest:   map[string]bool{},
		pkgFailed: map[string]bool{},
	}
}

// process is the per-event callback handed to a run round.
func (o *roundObserver) process(ev gotest.TestEvent) error {
	if ev.Action == gotest.ActionOutput {
		if o.stdout != nil {
			io.WriteString(o.stdout, ev.Output)
		}
		// The race detector always prints the full "WARNING: DATA RACE" banner;
		// matching the prefix (not the bare words) avoids a false positive from a
		// test that merely logs the phrase "DATA RACE" itself.
		if strings.Contains(ev.Output, "WARNING: DATA RACE") {
			o.dataRace = true
		}
	}

	if ev.Test != "" {
		o.hadTest[ev.Package] = true
	}

	switch ev.Action {
	case gotest.ActionPass:
		if ev.Test != "" {
			o.passed[testKey{ev.Package, ev.Test}] = true
		}
	case gotest.ActionFail:
		if ev.Test != "" {
			o.failed[testKey{ev.Package, ev.Test}] = true
		} else {
			o.pkgFailed[ev.Package] = true
		}
	case gotest.ActionBuildFail:
		o.buildFail = true
	}

	return o.conv.Process(ev)
}

// failedTest identifies a test that failed, by package and full name. topFunc is
// its top-level function (the rerun `-run` granularity); rerunnable is false for
// failures reruns can't address (benchmarks, which need -bench not -run).
type failedTest struct {
	pkg        string
	name       string
	topFunc    string
	rerunnable bool
}

// hardFailure reports whether this round contained a failure reruns cannot
// recover: a build/compile error, or a package that failed without running any
// test (a TestMain or init panic).
func (o *roundObserver) hardFailure() bool {
	if o.buildFail {
		return true
	}
	for pkg := range o.pkgFailed {
		if !o.hadTest[pkg] {
			return true
		}
	}
	return false
}

// failures returns the tests that produced a terminal `fail` this round, in
// deterministic order so composed `-run` regexes and logs are stable.
func (o *roundObserver) failures() []failedTest {
	out := make([]failedTest, 0, len(o.failed))
	for k := range o.failed {
		top := topLevelFunc(k.name)
		out = append(out, failedTest{
			pkg:        k.pkg,
			name:       k.name,
			topFunc:    top,
			rerunnable: isRerunnableFunc(top),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pkg != out[j].pkg {
			return out[i].pkg < out[j].pkg
		}
		return out[i].name < out[j].name
	})
	return out
}

// didPass reports whether a specific test reached a terminal pass this round.
func (o *roundObserver) didPass(k testKey) bool { return o.passed[k] }

// topLevelFunc returns the top-level test function of a go test name: the first
// "/"-separated segment ("TestX/sub/case" -> "TestX").
func topLevelFunc(testName string) string {
	if i := strings.IndexByte(testName, '/'); i >= 0 {
		return testName[:i]
	}
	return testName
}

// isRerunnableFunc reports whether a top-level test function can be re-executed
// with a scoped `-run` regex. Test/Example/Fuzz targets all run under `-run`;
// benchmarks need `-bench`, so a failed benchmark is treated as a hard failure
// rather than something we falsely claim to retry.
func isRerunnableFunc(top string) bool {
	return strings.HasPrefix(top, "Test") ||
		strings.HasPrefix(top, "Example") ||
		strings.HasPrefix(top, "Fuzz")
}

// composeRerunArgs builds the `go test` args for a rerun round: the original
// args, plus a `-run` regex scoped to exactly the failed top-level functions and
// `-count=1`. Go matches `-run` per "/"-separated test-name segment, so
// anchoring the top function with ^...$ reruns that whole test (all its
// subtests) — a deliberate, documented granularity.
//
// The rerun flags are inserted *before* any test-binary separator (`-args` or a
// bare `--`): go test forwards everything after such a separator to the test
// binary, so appending at the very end would route our `-run`/`-count=1` to the
// binary instead of go test (losing both the failed-test scope and the
// cache-busting `-count=1`). Inserting before the separator keeps them as
// go-test flags while still landing after any user-supplied `-run`/`-count` so
// last-wins parsing makes them take effect.
//
// Test function names are Go identifiers (letters, digits, underscore), none of
// which are regexp metacharacters, so they need no escaping in the alternation.
func composeRerunArgs(orig []string, failed []failedTest) []string {
	names := make([]string, 0, len(failed))
	seen := map[string]bool{}
	for _, f := range failed {
		if seen[f.topFunc] {
			continue
		}
		seen[f.topFunc] = true
		names = append(names, f.topFunc)
	}
	sort.Strings(names)
	runRegex := "^(" + strings.Join(names, "|") + ")$"

	// Find the first test-binary separator; our flags must precede it.
	split := len(orig)
	for i, a := range orig {
		if a == "-args" || a == "--args" || a == "--" {
			split = i
			break
		}
	}

	args := make([]string, 0, len(orig)+3)
	args = append(args, orig[:split]...)
	args = append(args, "-run", runRegex, "-count=1")
	args = append(args, orig[split:]...)
	return args
}
