package runner

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/mxschmitt/flakiness-go/internal/config"
	"github.com/mxschmitt/flakiness-go/internal/gotest"
	"github.com/mxschmitt/flakiness-go/report"
)

func TestTopLevelFunc(t *testing.T) {
	cases := map[string]string{
		"TestX":            "TestX",
		"TestX/sub":        "TestX",
		"TestX/sub/deeper": "TestX",
		"":                 "",
	}
	for in, want := range cases {
		if got := topLevelFunc(in); got != want {
			t.Errorf("topLevelFunc(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComposeRerunArgs(t *testing.T) {
	orig := []string{"-race", "-timeout=15m", "./..."}
	failed := []failedTest{
		{pkg: "ex/a", topFunc: "TestB"},
		{pkg: "ex/a", topFunc: "TestA"},
		{pkg: "ex/b", topFunc: "TestA"}, // duplicate top func across packages
	}
	got := composeRerunArgs(orig, failed)
	want := []string{"-race", "-timeout=15m", "./...", "-run", "^(TestA|TestB)$", "-count=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeRerunArgs:\n got %v\nwant %v", got, want)
	}
}

func TestComposeRerunArgs_DoesNotMutateOriginal(t *testing.T) {
	orig := []string{"./..."}
	_ = composeRerunArgs(orig, []failedTest{{pkg: "p", topFunc: "TestA"}})
	if len(orig) != 1 || orig[0] != "./..." {
		t.Errorf("original args mutated: %v", orig)
	}
}

// composeRerunArgs must insert -run/-count=1 BEFORE a test-binary separator so
// go test parses them as its own flags rather than forwarding them to the test
// binary (which would lose the failed-test scope and the cache-buster).
func TestComposeRerunArgs_InsertsBeforeArgsSeparator(t *testing.T) {
	failed := []failedTest{{pkg: "p", topFunc: "TestA"}}
	for _, sep := range []string{"-args", "--args", "--"} {
		orig := []string{"-race", "./...", sep, "-myflag"}
		got := composeRerunArgs(orig, failed)
		want := []string{"-race", "./...", "-run", "^(TestA)$", "-count=1", sep, "-myflag"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("separator %q:\n got %v\nwant %v", sep, got, want)
		}
	}
}

// streamFor builds a minimal `go test -json` stream for one (pkg, test) with the
// given terminal action, including the package-level fail when the test fails.
func streamFor(pkg, test, action string) []gotest.TestEvent {
	evs := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: pkg, Test: test},
		{Action: gotest.ActionOutput, Package: pkg, Test: test, Output: "=== RUN   " + test + "\n"},
		{Action: action, Package: pkg, Test: test, Elapsed: 0.01},
	}
	if action == gotest.ActionFail {
		evs = append(evs, gotest.TestEvent{Action: gotest.ActionFail, Package: pkg})
	} else {
		evs = append(evs, gotest.TestEvent{Action: gotest.ActionPass, Package: pkg})
	}
	return evs
}

// scriptedRunner wires a Runner whose rounds are served from a queue of
// (events, exitCode) pairs instead of a real go test subprocess.
type roundScript struct {
	events []gotest.TestEvent
	exit   int
}

func newScriptedRunner(t *testing.T, cfg *config.Config, rounds []roundScript) (*Runner, *[]([]string)) {
	t.Helper()
	var calls [][]string
	idx := 0
	r := &Runner{
		Cfg:     cfg,
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return nil },
	}
	r.runRound = func(args []string, onEvent func(gotest.TestEvent) error) (int, error) {
		calls = append(calls, args)
		if idx >= len(rounds) {
			t.Fatalf("unexpected extra round %d with args %v", idx, args)
		}
		rd := rounds[idx]
		idx++
		for _, ev := range rd.events {
			if err := onEvent(ev); err != nil {
				return 1, err
			}
		}
		return rd.exit, nil
	}
	return r, &calls
}

// runForReport drives the converter exactly as runWithReruns does and returns
// the resulting attempts for a given test title, plus the effective exit code.
func runWithRerunsForTest(t *testing.T, r *Runner) (int, *gotest.Converter) {
	t.Helper()
	conv := &gotest.Converter{}
	if r.runRound == nil {
		t.Fatal("scripted runRound required")
	}
	code, err := r.runWithReruns(conv)
	if err != nil {
		t.Fatalf("runWithReruns: %v", err)
	}
	return code, conv
}

func attemptsFor(rep report.Report, title string) []report.RunAttempt {
	var found []report.RunAttempt
	var walk func(s report.Suite)
	walk = func(s report.Suite) {
		for _, tc := range s.Tests {
			if tc.Title == title {
				found = tc.Attempts
			}
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	for _, s := range rep.Suites {
		walk(s)
	}
	return found
}

func TestRerun_FailThenPass_GreenAndFlaky(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: streamFor("ex/pkg", "TestFlaky", gotest.ActionFail), exit: 1},
		{events: streamFor("ex/pkg", "TestFlaky", gotest.ActionPass), exit: 0},
	})
	code, conv := runWithRerunsForTest(t, r)

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (green-on-transient)", code)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 rounds, got %d: %v", len(*calls), *calls)
	}
	// Rerun args must scope to the failed test and force a fresh run.
	rerun := strings.Join((*calls)[1], " ")
	if !strings.Contains(rerun, "-run ^(TestFlaky)$") || !strings.Contains(rerun, "-count=1") {
		t.Errorf("rerun args = %q, want scoped -run + -count=1", rerun)
	}

	rep := conv.Build()
	att := attemptsFor(rep, "TestFlaky")
	if len(att) != 2 {
		t.Fatalf("TestFlaky attempts = %d, want 2 (flaky)", len(att))
	}
	// First failed, second passed. conv.Build() (unlike the serialized report)
	// yields the literal "passed", so assert it exactly — no "" fallback.
	if att[0].Status != report.StatusFailed {
		t.Errorf("attempt 0 status = %q, want failed", att[0].Status)
	}
	if att[1].Status != report.StatusPassed {
		t.Errorf("attempt 1 status = %q, want passed", att[1].Status)
	}
}

func TestRerun_FailsEveryAttempt_RealRegression(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: streamFor("ex/pkg", "TestBroken", gotest.ActionFail), exit: 1},
		{events: streamFor("ex/pkg", "TestBroken", gotest.ActionFail), exit: 1},
		{events: streamFor("ex/pkg", "TestBroken", gotest.ActionFail), exit: 1},
	})
	code, conv := runWithRerunsForTest(t, r)

	if code == 0 {
		t.Error("exit code = 0, want non-zero (fails every attempt)")
	}
	if len(*calls) != 3 {
		t.Fatalf("expected 3 rounds (1 + 2 reruns), got %d", len(*calls))
	}
	if att := attemptsFor(conv.Build(), "TestBroken"); len(att) != 3 {
		t.Errorf("TestBroken attempts = %d, want 3", len(att))
	}
}

func TestRerun_CleanPass_NoReruns(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: streamFor("ex/pkg", "TestOK", gotest.ActionPass), exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(*calls) != 1 {
		t.Errorf("expected exactly 1 round on a clean pass, got %d", len(*calls))
	}
}

func TestRerun_StopsEarlyWhenRecovered(t *testing.T) {
	// Two tests fail; both pass on the first rerun, so no second rerun happens
	// even though RerunFailed=3.
	cfg := &config.Config{RerunFailed: 3, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	round0 := append(streamFor("ex/pkg", "TestA", gotest.ActionFail), streamFor("ex/pkg", "TestB", gotest.ActionFail)...)
	round1 := append(streamFor("ex/pkg", "TestA", gotest.ActionPass), streamFor("ex/pkg", "TestB", gotest.ActionPass)...)
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(*calls) != 2 {
		t.Errorf("expected 2 rounds (stop once recovered), got %d", len(*calls))
	}
	// Both failed funcs must appear in the single rerun's -run alternation.
	rerun := strings.Join((*calls)[1], " ")
	if !strings.Contains(rerun, "^(TestA|TestB)$") {
		t.Errorf("rerun -run = %q, want both tests anchored", rerun)
	}
}

func TestRerun_MaxFailuresSkipsReruns(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 1, RerunAbortOnDataRace: true}
	round0 := append(streamFor("ex/pkg", "TestA", gotest.ActionFail), streamFor("ex/pkg", "TestB", gotest.ActionFail)...)
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code == 0 {
		t.Error("exit = 0, want non-zero (reruns skipped, failures stand)")
	}
	if len(*calls) != 1 {
		t.Errorf("expected no reruns above max-failures, got %d rounds", len(*calls))
	}
}

func TestRerun_DataRaceAborts(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	// A failed test whose output carries the race-detector banner.
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestRacy"},
		{Action: gotest.ActionOutput, Package: "ex/pkg", Test: "TestRacy", Output: "==================\nWARNING: DATA RACE\n"},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestRacy", Elapsed: 0.01},
		{Action: gotest.ActionFail, Package: "ex/pkg"},
	}
	r, calls := newScriptedRunner(t, cfg, []roundScript{{events: round0, exit: 1}})
	code, _ := runWithRerunsForTest(t, r)
	if code == 0 {
		t.Error("exit = 0, want non-zero (race not retried)")
	}
	if len(*calls) != 1 {
		t.Errorf("data race must skip reruns, got %d rounds", len(*calls))
	}
}

func TestRerun_BuildFailureNotRetried(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	// A package-level fail with no test event = build/setup failure.
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionOutput, Package: "ex/pkg", Output: "build failed\n"},
		{Action: gotest.ActionFail, Package: "ex/pkg"},
	}
	r, calls := newScriptedRunner(t, cfg, []roundScript{{events: round0, exit: 1}})
	code, _ := runWithRerunsForTest(t, r)
	if code == 0 {
		t.Error("exit = 0, want non-zero (build failure must not be masked)")
	}
	if len(*calls) != 1 {
		t.Errorf("build failure must not trigger reruns, got %d rounds", len(*calls))
	}
}

func TestRerun_DisabledPreservesExitCode(t *testing.T) {
	cfg := &config.Config{RerunFailed: 0}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: streamFor("ex/pkg", "TestX", gotest.ActionFail), exit: 1},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (rerun disabled, pass through)", code)
	}
	if len(*calls) != 1 {
		t.Errorf("expected 1 round with reruns off, got %d", len(*calls))
	}
}

func TestRerun_SubtestRerunsParentFunc(t *testing.T) {
	cfg := &config.Config{RerunFailed: 1, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestParent"},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestParent/subFail"},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestParent/subFail", Elapsed: 0.01},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestParent", Elapsed: 0.01},
		{Action: gotest.ActionFail, Package: "ex/pkg"},
	}
	// A real rerun of `-run ^(TestParent)$` re-executes the subtest, so its
	// terminal event reappears (now passing) alongside the parent's.
	round1 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestParent"},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestParent/subFail"},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestParent/subFail", Elapsed: 0.01},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestParent", Elapsed: 0.01},
		{Action: gotest.ActionPass, Package: "ex/pkg"},
	}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	// The rerun must anchor to the parent func, not the subtest path.
	rerun := strings.Join((*calls)[1], " ")
	if !strings.Contains(rerun, "^(TestParent)$") {
		t.Errorf("rerun -run = %q, want ^(TestParent)$ (whole parent reruns)", rerun)
	}
}

// nonzeroExitNoFailures models a go-test usage error (bad flag): exit != 0 but
// no test or package fail events. Reruns must not run, and the exit code must be
// preserved rather than masked to 0.
func TestRerun_NonzeroExitWithoutFailuresPreserved(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: nil, exit: 2},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage error preserved, not masked)", code)
	}
	if len(*calls) != 1 {
		t.Errorf("expected 1 round, got %d", len(*calls))
	}
}

// A passing sibling subtest is re-executed when its failing sibling's parent
// func reruns. Its (passing) outcome must NOT gate the job: once the failing
// subtest recovers, the build is green.
func TestRerun_CollateralPassingSiblingDoesNotGate(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX"},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subA"},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestX/subA", Elapsed: 0.01},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subB"},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestX/subB", Elapsed: 0.01},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestX", Elapsed: 0.02},
		{Action: gotest.ActionFail, Package: "ex/pkg"},
	}
	// Rerun re-executes both subtests; subA now passes, subB passes again.
	round1 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX"},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subA"},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestX/subA", Elapsed: 0.01},
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subB"},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestX/subB", Elapsed: 0.01},
		{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestX", Elapsed: 0.02},
		{Action: gotest.ActionPass, Package: "ex/pkg"},
	}
	r, _ := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (failing subtest recovered; passing sibling must not gate)", code)
	}
}

// A subtest that keeps failing must fail the job even though its sibling passes
// every round — recovery is tracked per full test path, not per parent func.
func TestRerun_FailingSubtestNotMaskedByPassingSibling(t *testing.T) {
	cfg := &config.Config{RerunFailed: 1, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	mk := func(subAStatus string) []gotest.TestEvent {
		return []gotest.TestEvent{
			{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX"},
			{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subA"},
			{Action: subAStatus, Package: "ex/pkg", Test: "TestX/subA", Elapsed: 0.01},
			{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestX/subB"},
			{Action: gotest.ActionPass, Package: "ex/pkg", Test: "TestX/subB", Elapsed: 0.01},
			{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestX", Elapsed: 0.02},
			{Action: gotest.ActionFail, Package: "ex/pkg"},
		}
	}
	r, _ := newScriptedRunner(t, cfg, []roundScript{
		{events: mk(gotest.ActionFail), exit: 1},
		{events: mk(gotest.ActionFail), exit: 1},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code == 0 {
		t.Error("exit = 0, want non-zero (subA fails every attempt)")
	}
}

func TestRerun_DataRaceNotAbortedWhenFlagFalse(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: false}
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestRacy"},
		{Action: gotest.ActionOutput, Package: "ex/pkg", Test: "TestRacy", Output: "WARNING: DATA RACE\n"},
		{Action: gotest.ActionFail, Package: "ex/pkg", Test: "TestRacy", Elapsed: 0.01},
		{Action: gotest.ActionFail, Package: "ex/pkg"},
	}
	round1 := streamFor("ex/pkg", "TestRacy", gotest.ActionPass)
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (abort-on-data-race=false lets the race retry and recover)", code)
	}
	if len(*calls) != 2 {
		t.Errorf("expected a rerun when abort-on-data-race=false, got %d rounds", len(*calls))
	}
}

func TestRerun_MaxFailuresZeroMeansUnlimited(t *testing.T) {
	cfg := &config.Config{RerunFailed: 1, RerunMaxFailures: 0, RerunAbortOnDataRace: true}
	var round0 []gotest.TestEvent
	var round1 []gotest.TestEvent
	for _, n := range []string{"TestA", "TestB", "TestC", "TestD", "TestE"} {
		round0 = append(round0, streamFor("ex/pkg", n, gotest.ActionFail)...)
		round1 = append(round1, streamFor("ex/pkg", n, gotest.ActionPass)...)
	}
	r, calls := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 0},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(*calls) != 2 {
		t.Errorf("max-failures=0 must not cap reruns, got %d rounds", len(*calls))
	}
}

// An explicit build-fail event (compile error) is a hard failure: no reruns,
// exit code preserved.
func TestRerun_BuildFailEventNotRetried(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	round0 := []gotest.TestEvent{
		{Action: gotest.ActionBuildOutput, ImportPath: "ex/pkg.test", Output: "./x.go:1:1: syntax error\n"},
		{Action: gotest.ActionBuildFail, ImportPath: "ex/pkg.test"},
		{Action: gotest.ActionFail, Package: "ex/pkg", FailedBuild: "ex/pkg.test"},
	}
	r, calls := newScriptedRunner(t, cfg, []roundScript{{events: round0, exit: 2}})
	code, _ := runWithRerunsForTest(t, r)
	if code != 2 {
		t.Errorf("exit = %d, want 2 (build failure preserved, not masked)", code)
	}
	if len(*calls) != 1 {
		t.Errorf("build-fail event must not trigger reruns, got %d rounds", len(*calls))
	}
}

// A rerun round that exits non-zero but emits no failed-test and no build/setup
// signal (e.g. a panic/timeout that killed the test with no terminal `fail`, or
// a usage error from the composed args) must not be masked as green.
func TestRerun_UnexplainedRerunFailureNotMasked(t *testing.T) {
	cfg := &config.Config{RerunFailed: 2, RerunMaxFailures: 10, RerunAbortOnDataRace: true}
	round0 := streamFor("ex/pkg", "TestFlaky", gotest.ActionFail)
	// Rerun: the previously-failing test is killed by a panic/timeout — only a
	// `run` event, no terminal pass/fail — and the process exits non-zero.
	round1 := []gotest.TestEvent{
		{Action: gotest.ActionRun, Package: "ex/pkg", Test: "TestFlaky"},
		{Action: gotest.ActionOutput, Package: "ex/pkg", Test: "TestFlaky", Output: "panic: boom\n"},
	}
	r, _ := newScriptedRunner(t, cfg, []roundScript{
		{events: round0, exit: 1},
		{events: round1, exit: 2},
	})
	code, _ := runWithRerunsForTest(t, r)
	if code == 0 {
		t.Error("exit = 0, want non-zero (unexplained rerun failure must not be masked green)")
	}
}
