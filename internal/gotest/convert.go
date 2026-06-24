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
		if ta.cur == nil {
			ta.cur = &attempt{start: ev.Time}
			ta.attempts = append(ta.attempts, ta.cur)
		}
		ta.cur.output.WriteString(ev.Output)
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
	// nested under suite segments derived from their "/"-separated name.
	root := newNode("")
	for _, k := range c.order {
		ta := c.tests[k]
		if parents[k] {
			continue // parent aggregate test is represented by its child suite
		}
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
		leaf.test = ta
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
		if child.test != nil && len(child.children) == 0 {
			s.Tests = append(s.Tests, c.buildTest(child))
		} else {
			s.Suites = append(s.Suites, c.buildSuite(child, report.SuiteNamed))
		}
	}
	return s
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
	ta := n.test
	t := report.Test{Title: n.title}
	if c.Locator != nil {
		if fn := topLevelFunc(n); fn != "" {
			t.Location = c.Locator.Locate(ta.pkg, fn)
		}
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
		status = report.StatusInterrupted
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
