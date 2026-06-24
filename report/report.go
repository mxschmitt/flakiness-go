// Package report defines the Flakiness JSON Report format as Go types.
//
// It is a faithful translation of the official specification published at
// https://github.com/flakiness/flakiness-report (TypeScript types + Zod
// schema). Only the fields meaningful to a Go reporter are modelled; the
// remaining optional fields are present so the types can round-trip any valid
// report.
//
// All file paths are POSIX, relative to the git root. Timestamps are Unix
// milliseconds. Durations are milliseconds.
package report

// Category constants mirror the CATEGORY_* values in the spec.
const (
	CategoryPlaywright = "playwright"
	CategoryPytest     = "pytest"
	CategoryJUnit      = "junit"
	// CategoryGo is the category emitted by this reporter.
	CategoryGo = "go"
)

// TestStatus is the outcome (or expected outcome) of a single run attempt.
type TestStatus string

const (
	StatusPassed      TestStatus = "passed"
	StatusFailed      TestStatus = "failed"
	StatusTimedOut    TestStatus = "timedOut"
	StatusSkipped     TestStatus = "skipped"
	StatusInterrupted TestStatus = "interrupted"
)

// SuiteType classifies a Suite node.
type SuiteType string

const (
	SuiteFile      SuiteType = "file"
	SuiteAnonymous SuiteType = "anonymous suite"
	SuiteNamed     SuiteType = "suite"
)

// Location is a position in a source file (1-based line/column).
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// NameVersion identifies a producer, test runner, or runtime.
type NameVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SystemData holds OS information collected automatically.
type SystemData struct {
	OSName    string `json:"osName,omitempty"`
	OSVersion string `json:"osVersion,omitempty"`
	OSArch    string `json:"osArch,omitempty"`
}

// Environment describes one execution context. At least one is required.
type Environment struct {
	Name       string         `json:"name"`
	SystemData *SystemData    `json:"systemData,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Annotation is metadata attached to a single run attempt (e.g. a skip reason).
type Annotation struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Location    *Location `json:"location,omitempty"`
}

// ReportError describes an error thrown during execution.
type ReportError struct {
	Location *Location `json:"location,omitempty"`
	Message  string    `json:"message,omitempty"`
	Stack    string    `json:"stack,omitempty"`
	Snippet  string    `json:"snippet,omitempty"`
	Value    string    `json:"value,omitempty"`
}

// STDIOEntry is a chunk of captured stdout/stderr. Text holds UTF-8 text;
// Buffer holds base64-encoded binary data (mutually exclusive).
type STDIOEntry struct {
	Text   string `json:"text,omitempty"`
	Buffer string `json:"buffer,omitempty"`
}

// Attachment references an artifact stored alongside the report by ID.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	ID          string `json:"id"`
}

// RunAttempt is a single execution of a test in one environment.
type RunAttempt struct {
	EnvironmentIdx int           `json:"environmentIdx"`
	ExpectedStatus TestStatus    `json:"expectedStatus,omitempty"`
	Status         TestStatus    `json:"status,omitempty"`
	StartTimestamp int64         `json:"startTimestamp"`
	Duration       int64         `json:"duration"`
	Timeout        int64         `json:"timeout,omitempty"`
	Annotations    []Annotation  `json:"annotations,omitempty"`
	Errors         []ReportError `json:"errors,omitempty"`
	ParallelIndex  *int          `json:"parallelIndex,omitempty"`
	Stdout         []STDIOEntry  `json:"stdout,omitempty"`
	Stderr         []STDIOEntry  `json:"stderr,omitempty"`
	Attachments    []Attachment  `json:"attachments,omitempty"`
}

// Test is a single test case: a named location that can be run one or more times.
type Test struct {
	Title    string       `json:"title"`
	Location *Location    `json:"location,omitempty"`
	Tags     []string     `json:"tags,omitempty"`
	Attempts []RunAttempt `json:"attempts"`
}

// Suite groups tests and/or nested suites.
type Suite struct {
	Type     SuiteType `json:"type"`
	Title    string    `json:"title"`
	Location *Location `json:"location,omitempty"`
	Suites   []Suite   `json:"suites,omitempty"`
	Tests    []Test    `json:"tests,omitempty"`
}

// Source is an embedded source-code excerpt that provides context for the
// Location references throughout the report.
type Source struct {
	// FilePath is the git-root-relative POSIX path; matches Location.File.
	FilePath string `json:"filePath"`
	// Text is the (possibly partial) file content for this excerpt.
	Text string `json:"text"`
	// LineOffset is the 1-based line number of Text's first line. Omitted when
	// the excerpt starts at line 1.
	LineOffset int `json:"lineOffset,omitempty"`
	// ContentType is an optional MIME hint for syntax highlighting.
	ContentType string `json:"contentType,omitempty"`
}

// TelemetryPoint is one [timestampOrDelta, value] tuple in a telemetry series.
// The first tuple in a series holds an absolute Unix-ms timestamp; subsequent
// tuples hold the millisecond delta from the previous sample. Value is a
// percentage 0–100.
type TelemetryPoint [2]float64

// Report is the root document written to report.json.
type Report struct {
	Sources           []Source         `json:"sources,omitempty"`
	FlakinessProject  string           `json:"flakinessProject,omitempty"`
	Title             string           `json:"title,omitempty"`
	Category          string           `json:"category"`
	CommitID          string           `json:"commitId"`
	RelatedCommitIDs  []string         `json:"relatedCommitIds,omitempty"`
	ConfigPath        string           `json:"configPath,omitempty"`
	URL               string           `json:"url,omitempty"`
	GeneratedBy       *NameVersion     `json:"generatedBy,omitempty"`
	TestRunner        *NameVersion     `json:"testRunner,omitempty"`
	Runtime           *NameVersion     `json:"runtime,omitempty"`
	Environments      []Environment    `json:"environments"`
	Suites            []Suite          `json:"suites,omitempty"`
	Tests             []Test           `json:"tests,omitempty"`
	UnattributedError []ReportError    `json:"unattributedErrors,omitempty"`
	StartTimestamp    int64            `json:"startTimestamp"`
	Duration          int64            `json:"duration"`
	CPUCount          int              `json:"cpuCount,omitempty"`
	CPUAvg            []TelemetryPoint `json:"cpuAvg,omitempty"`
	CPUMax            []TelemetryPoint `json:"cpuMax,omitempty"`
	RAM               []TelemetryPoint `json:"ram,omitempty"`
	RAMBytes          int64            `json:"ramBytes,omitempty"`
}
