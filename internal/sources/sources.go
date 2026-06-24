// Package sources embeds source-code excerpts into a report so the Flakiness.io
// viewer can show context around every referenced location (test definitions,
// errors, annotations). It mirrors the canonical Node SDK's collectSources.
package sources

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mxschmitt/flakiness-go/report"
)

// contextLines is the number of lines of context kept on each side of a
// referenced line, matching the SDK (collectSources.ts: { context: 5 }).
const contextLines = 5

// Collect scans every Location in the report, reads the referenced files
// relative to gitRoot, and populates report.Sources with ±contextLines
// excerpts (overlapping ranges in the same file are merged). Files that can't
// be read are skipped. It is a no-op when gitRoot is empty.
func Collect(rep *report.Report, gitRoot string) {
	if gitRoot == "" {
		return
	}

	// file path -> set of referenced 1-based line numbers.
	fileLines := map[string]map[int]bool{}
	add := func(loc *report.Location) {
		if loc == nil || loc.File == "" || loc.Line <= 0 {
			return
		}
		set := fileLines[loc.File]
		if set == nil {
			set = map[int]bool{}
			fileLines[loc.File] = set
		}
		set[loc.Line] = true
	}

	for i := range rep.UnattributedError {
		add(rep.UnattributedError[i].Location)
	}
	for i := range rep.Tests {
		collectTest(&rep.Tests[i], add)
	}
	for i := range rep.Suites {
		collectSuite(&rep.Suites[i], add)
	}

	// Stable file order for deterministic output.
	files := make([]string, 0, len(fileLines))
	for f := range fileLines {
		files = append(files, f)
	}
	sort.Strings(files)

	var sources []report.Source
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(gitRoot, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, chunk := range chunks(fileLines[file]) {
			from := chunk[0] - 1 // 0-based slice start
			if from < 0 {
				from = 0
			}
			to := chunk[1] // 1-based inclusive end == 0-based exclusive end
			if to > len(lines) {
				to = len(lines)
			}
			if from >= to {
				continue
			}
			s := report.Source{
				FilePath:    file,
				Text:        strings.Join(lines[from:to], "\n"),
				ContentType: "text/x-go",
			}
			if from != 0 {
				s.LineOffset = from + 1
			}
			sources = append(sources, s)
		}
	}
	rep.Sources = sources
}

func collectSuite(s *report.Suite, add func(*report.Location)) {
	add(s.Location)
	for i := range s.Suites {
		collectSuite(&s.Suites[i], add)
	}
	for i := range s.Tests {
		collectTest(&s.Tests[i], add)
	}
}

func collectTest(t *report.Test, add func(*report.Location)) {
	add(t.Location)
	for i := range t.Attempts {
		a := &t.Attempts[i]
		for j := range a.Annotations {
			add(a.Annotations[j].Location)
		}
		for j := range a.Errors {
			add(a.Errors[j].Location)
		}
	}
}

// chunks turns a set of line numbers into sorted, merged [start,end] ranges
// (1-based, inclusive), each expanded by contextLines on both sides. Adjacent
// or overlapping ranges are coalesced.
func chunks(lineSet map[int]bool) [][2]int {
	if len(lineSet) == 0 {
		return nil
	}
	nums := make([]int, 0, len(lineSet))
	for n := range lineSet {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var result [][2]int
	for _, ln := range nums {
		span := [2]int{ln - contextLines, ln + contextLines}
		if span[0] < 1 {
			span[0] = 1
		}
		// Merge into the previous range when it touches or overlaps (the SDK
		// merges when current.end + 1 >= span.start).
		if n := len(result); n > 0 && result[n-1][1]+1 >= span[0] {
			if span[1] > result[n-1][1] {
				result[n-1][1] = span[1]
			}
		} else {
			result = append(result, span)
		}
	}
	return result
}
