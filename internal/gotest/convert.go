package gotest

import (
	"strings"
	"time"

	"github.com/mxschmitt/flakiness-go/report"
)

// Locator resolves the source location of a top-level Go test function.
// Implementations are best-effort and may return nil.
type Locator interface {
	Locate(pkg, testFunc string) *report.Location
}

// attempt accumulates a single run of a test (run -> ... -> terminal).
type attempt struct {
	status     report.TestStatus
	start      time.Time
	elapsedSec float64
	output     strings.Builder
	skipReason string
}

// testAcc accumulates all attempts for one (package, test) pair.
type testAcc struct {
	pkg      string
	name     string // full go test name, e.g. "TestFoo/sub/case"
	attempts []*attempt
	cur      *attempt
}

func (ta *testAcc) lastAttempt() *attempt {
	if len(ta.attempts) == 0 {
		return nil
	}
	return ta.attempts[len(ta.attempts)-1]
}

// Converter turns a `go test -json` event stream into a report.Report.
//
// It is fed events via Process (in stream order) and produces the report with
// Build. The zero value is ready to use; set Locator to enrich tests with
// source locations.
type Converter struct {
	Locator Locator

	tests    map[string]*testAcc
	order    []string // test keys in first-seen order
	pkgOrder []string
	seenPkg  map[string]bool

	haveTime bool
	minTime  time.Time
	maxTime  time.Time
}

func key(pkg, test string) string { return pkg + "\x00" + test }

func (c *Converter) lazyInit() {
	if c.tests == nil {
		c.tests = map[string]*testAcc{}
		c.seenPkg = map[string]bool{}
	}
}

// Process consumes one event. Call once per event in stream order.
func (c *Converter) Process(ev TestEvent) error {
	c.lazyInit()

	if !ev.Time.IsZero() {
		if !c.haveTime || ev.Time.Before(c.minTime) {
			c.minTime = ev.Time
		}
		if !c.haveTime || ev.Time.After(c.maxTime) {
			c.maxTime = ev.Time
		}
		c.haveTime = true
	}

	if ev.Package != "" && !c.seenPkg[ev.Package] {
		c.seenPkg[ev.Package] = true
		c.pkgOrder = append(c.pkgOrder, ev.Package)
	}

	// Package-level events (no Test) carry build/setup output and the package
	// pass/fail/skip summary. We don't model packages as tests, so ignore them
	// here — individual test events already capture per-test results.
	if ev.Test == "" {
		return nil
	}

	k := key(ev.Package, ev.Test)
	ta := c.tests[k]
	if ta == nil {
		ta = &testAcc{pkg: ev.Package, name: ev.Test}
		c.tests[k] = ta
		c.order = append(c.order, k)
	}

	switch ev.Action {
	case ActionRun:
		ta.cur = &attempt{start: ev.Time}
		ta.attempts = append(ta.attempts, ta.cur)
	case ActionOutput:
		// Output normally arrives between `run` and the terminal event. Any
		// output that arrives *after* the terminal event (rare, but possible
		// for trailing summary lines) is appended to the just-finished attempt
		// rather than fabricating a spurious new one.
		dst := ta.cur
		if dst == nil {
			dst = ta.lastAttempt()
		}
		if dst == nil {
			dst = &attempt{start: ev.Time}
			ta.attempts = append(ta.attempts, dst)
			ta.cur = dst
		}
		dst.output.WriteString(ev.Output)
	case ActionPass, ActionFail, ActionSkip, ActionBench:
		if ta.cur == nil {
			ta.cur = &attempt{start: ev.Time}
			ta.attempts = append(ta.attempts, ta.cur)
		}
		ta.cur.elapsedSec = ev.Elapsed
		ta.cur.status = statusFor(ev.Action, ta.cur.output.String())
		if ev.Action == ActionSkip {
			ta.cur.skipReason = extractSkipReason(ta.cur.output.String())
		}
		ta.cur = nil
	case ActionPause, ActionCont:
		// Parallel-test scheduling markers; not attempt boundaries.
	}
	return nil
}

func statusFor(action, output string) report.TestStatus {
	switch action {
	case ActionPass, ActionBench:
		return report.StatusPassed
	case ActionSkip:
		return report.StatusSkipped
	case ActionFail:
		if isTimeout(output) {
			return report.StatusTimedOut
		}
		return report.StatusFailed
	default:
		return report.StatusInterrupted
	}
}

func isTimeout(output string) bool {
	return strings.Contains(output, "test timed out after") ||
		strings.Contains(output, "panic: test timed out")
}

// extractSkipReason pulls the message from a `t.Skip` line. Go prints skip
// reasons as "    file_test.go:12: <reason>" preceding the SKIP line.
func extractSkipReason(output string) string {
	lines := strings.Split(output, "\n")
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "=== ") {
			continue
		}
		// "file_test.go:12: reason"
		if i := strings.Index(trimmed, ": "); i >= 0 {
			if colon := strings.Index(trimmed, ":"); colon >= 0 && colon < i {
				return strings.TrimSpace(trimmed[i+2:])
			}
		}
	}
	return ""
}

// node is an intermediate tree node used while building the suite hierarchy.
type node struct {
	title    string
	pkg      string
	isPkg    bool
	children map[string]*node
	order    []string // child titles in insertion order
	test     *testAcc // set on leaf nodes that map to a test
	// own is the accumulator for a parent test that ALSO ran in its own right
	// (e.g. a `t.Run` group that itself fails via t.Error or panics). Such a
	// test becomes a suite for its subtests, but its own attempts must not be
	// lost — they are emitted as a leaf test inside the suite.
	own *testAcc
}

func newNode(title string) *node {
	return &node{title: title, children: map[string]*node{}}
}

func (n *node) child(title string) *node {
	if c, ok := n.children[title]; ok {
		return c
	}
	c := newNode(title)
	n.children[title] = c
	n.order = append(n.order, title)
	return c
}

// Build assembles the accumulated events into a report.Report. The returned
// report has Suites populated (one file-suite per package). category, commit,
// environment and other metadata are filled in by the caller.
func (c *Converter) Build() report.Report {
	c.lazyInit()

	// Identify which test names are "parents" (a prefix of a deeper test).
	parents := map[string]bool{}
	for _, k := range c.order {
		ta := c.tests[k]
		for _, k2 := range c.order {
			ta2 := c.tests[k2]
			if ta.pkg == ta2.pkg && ta.name != ta2.name && strings.HasPrefix(ta2.name, ta.name+"/") {
				parents[k] = true
				break
			}
		}
	}

	// Build a package -> suite tree. Leaf (non-parent) tests become Tests,
	// nested under suite segments derived from their "/"-separated name. A
	// parent test (one whose name prefixes a deeper test) becomes a suite; if
	// it also has its own attempts, they're preserved on node.own.
	root := newNode("")
	for _, k := range c.order {
		ta := c.tests[k]
		pkgNode := root.child(ta.pkg)
		pkgNode.isPkg = true
		pkgNode.pkg = ta.pkg

		segments := strings.Split(ta.name, "/")
		cur := pkgNode
		for _, seg := range segments[:len(segments)-1] {
			cur = cur.child(seg)
			cur.pkg = ta.pkg
		}
		leaf := cur.child(segments[len(segments)-1])
		leaf.pkg = ta.pkg
		if parents[k] {
			// The parent test maps to this suite node; keep its own run data.
			leaf.own = ta
		} else {
			leaf.test = ta
		}
	}

	var rep report.Report
	for _, pkgTitle := range root.order {
		pkgNode := root.children[pkgTitle]
		suite := c.buildSuite(pkgNode, report.SuiteFile)
		rep.Suites = append(rep.Suites, suite)
	}

	rep.StartTimestamp = c.startMillis()
	rep.Duration = c.durationMillis()
	return rep
}

func (c *Converter) buildSuite(n *node, typ report.SuiteType) report.Suite {
	s := report.Suite{Type: typ, Title: n.title}
	// The top-level test function (first segment) can be located in source.
	if !n.isPkg && c.Locator != nil {
		topFunc := topLevelFunc(n)
		if topFunc != "" {
			s.Location = c.Locator.Locate(n.pkg, topFunc)
		}
	}
	for _, childTitle := range n.order {
		child := n.children[childTitle]
		if len(child.children) == 0 && child.test != nil {
			s.Tests = append(s.Tests, c.buildTest(child))
		} else {
			s.Suites = append(s.Suites, c.buildSuite(child, report.SuiteNamed))
		}
	}
	// Preserve a parent test's own outcome (e.g. it failed via t.Error or
	// panicked) as a leaf test inside its suite, so it isn't lost. Skip it when
	// the parent merely passed as an aggregate of its subtests, to avoid noise.
	if n.own != nil && testCarriesSignal(n.own) {
		t := c.buildTestFrom(n.title, n.pkg, topLevelFunc(n), n.own)
		s.Tests = append(s.Tests, t)
	}
	return s
}

// testCarriesSignal reports whether a parent test's own attempts contain
// anything worth surfacing (a non-passing outcome). A parent that only passed
// is an aggregate of its subtests and adds no information of its own.
func testCarriesSignal(ta *testAcc) bool {
	for _, a := range ta.attempts {
		if a.status != "" && a.status != report.StatusPassed {
			return true
		}
	}
	return false
}

// topLevelFunc returns the top-level test function name for a suite node, used
// for source location. Only suites that correspond to a `func TestXxx` (i.e.
// not deeper subtests) resolve to a real function.
func topLevelFunc(n *node) string {
	if strings.HasPrefix(n.title, "Test") || strings.HasPrefix(n.title, "Example") ||
		strings.HasPrefix(n.title, "Benchmark") || strings.HasPrefix(n.title, "Fuzz") {
		return n.title
	}
	return ""
}

func (c *Converter) buildTest(n *node) report.Test {
	return c.buildTestFrom(n.title, n.pkg, topLevelFunc(n), n.test)
}

// buildTestFrom builds a report.Test from an accumulator. locFunc, when
// non-empty, is the top-level test function name used to resolve a source
// location.
func (c *Converter) buildTestFrom(title, pkg, locFunc string, ta *testAcc) report.Test {
	t := report.Test{Title: title}
	if c.Locator != nil && locFunc != "" {
		t.Location = c.Locator.Locate(pkg, locFunc)
	}
	for _, a := range ta.attempts {
		t.Attempts = append(t.Attempts, c.buildAttempt(a))
	}
	if len(t.Attempts) == 0 {
		t.Attempts = []report.RunAttempt{{
			EnvironmentIdx: 0,
			ExpectedStatus: report.StatusPassed,
			Status:         report.StatusInterrupted,
			StartTimestamp: c.startMillis(),
		}}
	}
	return t
}

func (c *Converter) buildAttempt(a *attempt) report.RunAttempt {
	status := a.status
	if status == "" {
		// No terminal event was seen for this attempt. When `go test -timeout`
		// kills a hung test, the runner panics with a "test timed out" banner
		// (attributed to the test) but emits no per-test `fail` event — only
		// the package fails. Detect that here so the attempt is reported as
		// timedOut rather than a generic interruption.
		if isTimeout(a.output.String()) {
			status = report.StatusTimedOut
		} else {
			status = report.StatusInterrupted
		}
	}
	ra := report.RunAttempt{
		EnvironmentIdx: 0,
		ExpectedStatus: report.StatusPassed,
		Status:         status,
		StartTimestamp: toMillis(a.start, c.startMillis()),
		Duration:       int64(a.elapsedSec * 1000),
	}
	if out := a.output.String(); out != "" {
		ra.Stdout = []report.STDIOEntry{{Text: out}}
	}
	switch status {
	case report.StatusFailed, report.StatusTimedOut:
		ra.Errors = []report.ReportError{{Message: failureMessage(a.output.String())}}
	case report.StatusSkipped:
		ann := report.Annotation{Type: "skip"}
		if a.skipReason != "" {
			ann.Description = a.skipReason
		}
		ra.Annotations = []report.Annotation{ann}
	}
	return ra
}

// failureMessage produces a concise error message from a failed test's output,
// preferring the first meaningful assertion/error line.
func failureMessage(output string) string {
	lines := strings.Split(output, "\n")
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "=== ") || strings.HasPrefix(trimmed, "--- ") {
			continue
		}
		return trimmed
	}
	return strings.TrimSpace(output)
}

func (c *Converter) startMillis() int64 {
	if c.haveTime {
		return c.minTime.UnixMilli()
	}
	return 0
}

func (c *Converter) durationMillis() int64 {
	if c.haveTime {
		d := c.maxTime.Sub(c.minTime).Milliseconds()
		if d < 0 {
			return 0
		}
		return d
	}
	// Cached results have no timestamps; fall back to summing attempt durations.
	var total float64
	for _, k := range c.order {
		for _, a := range c.tests[k].attempts {
			total += a.elapsedSec
		}
	}
	return int64(total * 1000)
}

func toMillis(t time.Time, fallback int64) int64 {
	if t.IsZero() {
		return fallback
	}
	return t.UnixMilli()
}
