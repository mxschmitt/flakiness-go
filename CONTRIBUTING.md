# Contributing to flakiness-go

Thanks for your interest in improving the Go reporter for [Flakiness.io](https://flakiness.io)!

## Prerequisites

- Go 1.22 or newer.
- No third-party dependencies: this module is intentionally stdlib-only. Please
  keep it that way unless there's a compelling reason to add one.

## Development

```bash
go build ./...        # build everything
go vet ./...          # static analysis
go test ./...         # run the full suite
go test -race ./...   # what CI runs
gofmt -l .            # must print nothing
```

The end-to-end test runs a real `go test -json` against the fixture module in
`testdata/example`. Skip it with `go test -short ./...` if you don't have a Go
toolchain handy on the path (you almost always do).

## Project layout

```
cmd/flakiness-go/   CLI entrypoint
report/             the Flakiness report format as Go types + on-disk writer
internal/gotest/    go test -json decoding, conversion, go/ast source locator
internal/config/    option resolution (CLI > env > default)
internal/runner/    orchestration: run/ingest -> convert -> write -> upload
internal/gitinfo/   git commit & root lookups
internal/ci/        CI run-URL detection
internal/oidc/      GitHub Actions OIDC token fetch
internal/upload/    Flakiness.io upload protocol
testdata/example/   fixture Go module used by tests
```

See [`PLAN.md`](./PLAN.md) for the design rationale and
[`features.md`](./features.md) for feature coverage against the
[official checklist](https://github.com/flakiness/flakiness-report/blob/main/features.md).

## Adding a feature

1. Check the [feature checklist](https://github.com/flakiness/flakiness-report/blob/main/features.md)
   and update `features.md` when the status of a row changes.
2. Add table-driven tests — for converter changes, the cleanest approach is a
   canned `go test -json` stream in `internal/gotest/convert_test.go`.
3. Keep `report/report.go` in sync with the upstream spec; prefer additive,
   optional fields.

## Pull requests

- Keep the suite green (`go test -race ./...`) and `gofmt`-clean.
- Describe the behavior change and link the relevant feature-checklist row.
