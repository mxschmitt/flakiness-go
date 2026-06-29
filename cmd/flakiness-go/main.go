// Command flakiness-go is the official Flakiness.io reporter for `go test`.
//
// Usage:
//
//	# Wrapper mode: runs `go test -json <args>` and reports the result.
//	flakiness-go ./...
//	flakiness-go --flakiness-project=my-org/my-proj ./pkg/...
//
//	# Stdin mode: consume an existing `go test -json` stream.
//	go test -json ./... | flakiness-go --stdin
//
// See PLAN.md and README.md for the full option list and behavior.
package main

import (
	"fmt"
	"os"

	"github.com/mxschmitt/flakiness-go/internal/config"
	"github.com/mxschmitt/flakiness-go/internal/runner"
)

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Flakiness] %v\n", err)
		os.Exit(2)
	}

	r := &runner.Runner{
		Cfg:    cfg,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	code, err := r.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Flakiness] %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
