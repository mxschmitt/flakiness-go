package gotest

import "regexp"

// ansiRegex matches ANSI/VT escape sequences. It is a direct port of the Node
// SDK's stripAnsi regex (nodejs-sdk/src/stripAnsi.ts): the leading class is
// ESC (\x1b) / CSI (\x9b), and the first alternative terminates on BEL (\x07).
// Stripping these keeps report error messages clean, matching the other
// reporters (playwright/jest/vitest all stripAnsi error text).
var ansiRegex = regexp.MustCompile(
	"[\\x1b\\x9b][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[-a-zA-Z\\d/#&.:=?%@~_]*)*)?\\x07)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PR-TZcf-ntqry=><~]))",
)

// stripANSI removes ANSI escape codes from s. Go test output is usually plain,
// but t.Log / assertion libraries can emit color codes; the SDK strips them
// from error messages and stacks, so we do too for parity.
func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiRegex.ReplaceAllString(s, "")
}
