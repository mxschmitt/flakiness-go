package gotest

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

// TestEvent is a single record from the `go test -json` stream, matching the
// struct documented under `go doc cmd/test2json`.
type TestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"` // seconds
	Output  string    `json:"Output"`
}

// Action values emitted by test2json.
const (
	ActionStart  = "start"
	ActionRun    = "run"
	ActionPause  = "pause"
	ActionCont   = "cont"
	ActionPass   = "pass"
	ActionBench  = "bench"
	ActionFail   = "fail"
	ActionOutput = "output"
	ActionSkip   = "skip"
)

// DecodeStream reads a newline-delimited `go test -json` stream and invokes fn
// for each decoded event in order. Lines that are not valid JSON test events
// (for example interleaved build output) are skipped silently, mirroring how
// downstream tooling tolerates non-event lines.
func DecodeStream(r io.Reader, fn func(TestEvent) error) error {
	sc := bufio.NewScanner(r)
	// Test output lines can be long (large assertion diffs); grow the buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev TestEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Action == "" {
			continue
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return sc.Err()
}
