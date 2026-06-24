package gotest

import (
	"strings"
	"testing"

	"github.com/mxschmitt/flakiness-go/report"
)

// decode runs a raw go test -json stream through a Converter and builds it.
func decode(t *testing.T, stream string) report.Report {
	t.Helper()
	conv := &Converter{}
	if err := DecodeStream(strings.NewReader(stream), conv.Process); err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	return conv.Build()
}

// findSuite returns the file-suite for the given package import path.
func findSuite(t *testing.T, rep report.Report, pkg string) report.Suite {
	t.Helper()
	for _, s := range rep.Suites {
		if s.Title == pkg {
			return s
		}
	}
	t.Fatalf("suite for package %q not found in %+v", pkg, rep.Suites)
	return report.Suite{}
}

func findTest(s report.Suite, title string) *report.Test {
	for i := range s.Tests {
		if s.Tests[i].Title == title {
			return &s.Tests[i]
		}
	}
	return nil
}

func TestConvert_PassFailSkip(t *testing.T) {
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"ex/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"ex/pkg","Test":"TestPass"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestPass","Output":"=== RUN   TestPass\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"ex/pkg","Test":"TestPass","Elapsed":1.5}
{"Time":"2024-01-01T00:00:02Z","Action":"run","Package":"ex/pkg","Test":"TestFail"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"ex/pkg","Test":"TestFail","Output":"    foo_test.go:10: want 1 got 2\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"fail","Package":"ex/pkg","Test":"TestFail","Elapsed":0.2}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"ex/pkg","Test":"TestSkip"}
{"Time":"2024-01-01T00:00:03Z","Action":"output","Package":"ex/pkg","Test":"TestSkip","Output":"    foo_test.go:20: not on windows\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"skip","Package":"ex/pkg","Test":"TestSkip","Elapsed":0}
{"Time":"2024-01-01T00:00:03Z","Action":"fail","Package":"ex/pkg","Elapsed":2.5}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	if suite.Type != report.SuiteFile {
		t.Errorf("package suite type = %q, want file", suite.Type)
	}
	if len(suite.Tests) != 3 {
		t.Fatalf("got %d tests, want 3", len(suite.Tests))
	}

	pass := findTest(suite, "TestPass")
	if pass == nil || pass.Attempts[0].Status != report.StatusPassed {
		t.Errorf("TestPass status = %+v, want passed", pass)
	}
	if got := pass.Attempts[0].Duration; got != 1500 {
		t.Errorf("TestPass duration = %d ms, want 1500", got)
	}
	if pass.Attempts[0].ExpectedStatus != report.StatusPassed {
		t.Errorf("expectedStatus = %q, want passed", pass.Attempts[0].ExpectedStatus)
	}

	fail := findTest(suite, "TestFail")
	if fail == nil || fail.Attempts[0].Status != report.StatusFailed {
		t.Fatalf("TestFail status wrong: %+v", fail)
	}
	if len(fail.Attempts[0].Errors) != 1 || !strings.Contains(fail.Attempts[0].Errors[0].Message, "want 1 got 2") {
		t.Errorf("TestFail error not captured: %+v", fail.Attempts[0].Errors)
	}

	skip := findTest(suite, "TestSkip")
	if skip == nil || skip.Attempts[0].Status != report.StatusSkipped {
		t.Fatalf("TestSkip status wrong: %+v", skip)
	}
	anns := skip.Attempts[0].Annotations
	if len(anns) != 1 || anns[0].Type != "skip" || anns[0].Description != "not on windows" {
		t.Errorf("TestSkip annotation = %+v, want skip/'not on windows'", anns)
	}

	if rep.StartTimestamp == 0 {
		t.Error("startTimestamp not set")
	}
	if rep.Duration != 3000 { // 00:00:00 -> 00:00:03
		t.Errorf("duration = %d ms, want 3000", rep.Duration)
	}
}

func TestConvert_Subtests(t *testing.T) {
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestGroup"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestGroup/sub_a"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestGroup/sub_a","Elapsed":0.1}
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"ex/pkg","Test":"TestGroup/sub_b"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestGroup/sub_b","Output":"    x_test.go:5: boom\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"ex/pkg","Test":"TestGroup/sub_b","Elapsed":0.2}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"ex/pkg","Test":"TestGroup","Elapsed":2}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	// TestGroup is a parent -> becomes a named suite, not a test.
	if len(suite.Tests) != 0 {
		t.Errorf("package suite should have no direct tests, got %d", len(suite.Tests))
	}
	if len(suite.Suites) != 1 || suite.Suites[0].Title != "TestGroup" {
		t.Fatalf("expected nested TestGroup suite, got %+v", suite.Suites)
	}
	group := suite.Suites[0]
	if group.Type != report.SuiteNamed {
		t.Errorf("subtest suite type = %q, want suite", group.Type)
	}
	// Two subtests, plus TestGroup's own failing attempt preserved as a leaf.
	if len(group.Tests) != 3 {
		t.Fatalf("TestGroup should have 3 leaf tests (2 subtests + own), got %d", len(group.Tests))
	}
	a := findTest(group, "sub_a")
	b := findTest(group, "sub_b")
	if a == nil || a.Attempts[0].Status != report.StatusPassed {
		t.Errorf("sub_a wrong: %+v", a)
	}
	if b == nil || b.Attempts[0].Status != report.StatusFailed {
		t.Errorf("sub_b wrong: %+v", b)
	}
	// The parent's own direct failure must not be lost.
	own := findTest(group, "TestGroup")
	if own == nil || own.Attempts[0].Status != report.StatusFailed {
		t.Errorf("TestGroup's own failing attempt should be preserved: %+v", own)
	}
}

func TestConvert_PassingParentNotDuplicated(t *testing.T) {
	// A parent that only passes (aggregate of subtests) should NOT be emitted
	// as its own leaf test — that would be noise.
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestGroup"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestGroup/sub"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestGroup/sub","Elapsed":0.1}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestGroup","Elapsed":0.1}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	group := suite.Suites[0]
	if len(group.Tests) != 1 || group.Tests[0].Title != "sub" {
		t.Fatalf("passing parent should yield only the subtest, got %+v", group.Tests)
	}
}

func TestConvert_PrefixCollisionNotParent(t *testing.T) {
	// TestFoo must not be treated as a parent of TestFooBar (no slash boundary).
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestFoo","Elapsed":0.1}
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"ex/pkg","Test":"TestFooBar"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"ex/pkg","Test":"TestFooBar","Elapsed":0.1}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	if len(suite.Tests) != 2 {
		t.Fatalf("both tests should be leaves, got %d tests / %d suites", len(suite.Tests), len(suite.Suites))
	}
	if findTest(suite, "TestFoo") == nil || findTest(suite, "TestFooBar") == nil {
		t.Errorf("expected both TestFoo and TestFooBar as leaf tests")
	}
}

func TestConvert_MultipleAttempts(t *testing.T) {
	// Simulates `go test -count=2`: two run/pass cycles for the same test.
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestFlaky"}
{"Time":"2024-01-01T00:00:01Z","Action":"fail","Package":"ex/pkg","Test":"TestFlaky","Elapsed":0.1}
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"ex/pkg","Test":"TestFlaky"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"ex/pkg","Test":"TestFlaky","Elapsed":0.1}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	tc := findTest(suite, "TestFlaky")
	if tc == nil || len(tc.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %+v", tc)
	}
	if tc.Attempts[0].Status != report.StatusFailed || tc.Attempts[1].Status != report.StatusPassed {
		t.Errorf("attempt statuses = %q,%q want failed,passed", tc.Attempts[0].Status, tc.Attempts[1].Status)
	}
}

func TestConvert_Timeout(t *testing.T) {
	// Mirrors REAL `go test -timeout` output: the runner panics with a
	// "test timed out" banner attributed to the hung test, then the PACKAGE
	// fails — there is no per-test terminal event. The test must still be
	// classified as timedOut (not interrupted).
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestSlow"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestSlow","Output":"=== RUN   TestSlow\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestSlow","Output":"panic: test timed out after 1s\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestSlow","Output":"\trunning tests:\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Output":"FAIL\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"fail","Package":"ex/pkg","Elapsed":1.2}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	tc := findTest(suite, "TestSlow")
	if tc == nil || tc.Attempts[0].Status != report.StatusTimedOut {
		t.Errorf("want timedOut, got %+v", tc)
	}
}

func TestConvert_OutputAfterTerminal(t *testing.T) {
	// Defensive: trailing output after a terminal event must attach to the
	// finished attempt, NOT fabricate a spurious extra (interrupted) attempt.
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestX"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestX","Elapsed":0.1}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"ex/pkg","Test":"TestX","Output":"trailing line\n"}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	tc := findTest(suite, "TestX")
	if tc == nil || len(tc.Attempts) != 1 {
		t.Fatalf("want exactly 1 attempt (no phantom), got %+v", tc)
	}
	if tc.Attempts[0].Status != report.StatusPassed {
		t.Errorf("status = %q, want passed", tc.Attempts[0].Status)
	}
}

func TestConvert_StdoutCaptured(t *testing.T) {
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestOut"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"ex/pkg","Test":"TestOut","Output":"hello from test\n"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestOut","Elapsed":0.1}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	tc := findTest(suite, "TestOut")
	if tc == nil || len(tc.Attempts[0].Stdout) == 0 {
		t.Fatalf("stdout not captured: %+v", tc)
	}
	if !strings.Contains(tc.Attempts[0].Stdout[0].Text, "hello from test") {
		t.Errorf("stdout missing content: %q", tc.Attempts[0].Stdout[0].Text)
	}
}

func TestConvert_NonEventLinesIgnored(t *testing.T) {
	// Build output and blank lines must not break decoding.
	stream := `
# ex/pkg
some plain build log line
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestOK"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"ex/pkg","Test":"TestOK","Elapsed":0.1}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	if findTest(suite, "TestOK") == nil {
		t.Error("TestOK not parsed when interleaved with non-event lines")
	}
}

func TestConvert_Interrupted(t *testing.T) {
	// A test that runs but never reaches a terminal event (binary crashed).
	stream := `
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"ex/pkg","Test":"TestCrash"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"ex/pkg","Test":"TestCrash","Output":"started\n"}
`
	rep := decode(t, stream)
	suite := findSuite(t, rep, "ex/pkg")
	tc := findTest(suite, "TestCrash")
	if tc == nil || tc.Attempts[0].Status != report.StatusInterrupted {
		t.Errorf("want interrupted, got %+v", tc)
	}
}
