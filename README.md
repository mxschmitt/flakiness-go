# flakiness-go

[![CI](https://github.com/mxschmitt/flakiness-go/actions/workflows/ci.yml/badge.svg)](https://github.com/mxschmitt/flakiness-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mxschmitt/flakiness-go.svg)](https://pkg.go.dev/github.com/mxschmitt/flakiness-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/mxschmitt/flakiness-go)](https://goreportcard.com/report/github.com/mxschmitt/flakiness-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

A [Flakiness.io](https://flakiness.io) reporter for **Go's `go test`**.

It wraps `go test -json`, turns the result into a
[Flakiness JSON Report](https://github.com/flakiness/flakiness-report), and
(optionally) uploads it to Flakiness.io — the same report format used by the
official [Playwright](https://github.com/flakiness/playwright),
[Jest](https://github.com/flakiness/jest),
[Vitest](https://github.com/flakiness/vitest), and
[pytest](https://github.com/flakiness/pytest-flakiness) reporters.

> Built to slot directly into the `flakiness/*` reporter family: it follows the
> same configuration surface, the same upload protocol (incl. GitHub OIDC
> keyless auth and mandatory brotli report compression), and ships a
> [`features.md`](./features.md) keyed to the official feature checklist. Its
> only dependency is a pure-Go brotli codec.

## Install

```bash
go install github.com/mxschmitt/flakiness-go/cmd/flakiness-go@latest
```

## Usage

### Wrapper mode (recommended)

Prefix your normal `go test` invocation. `flakiness-go` runs
`go test -json` for you, forwards every `go test` flag verbatim, and exits with
the same status code — so it's a drop-in CI wrapper.

```bash
flakiness-go ./...
flakiness-go -run TestFoo ./pkg/... -count=2
```

A `flakiness-report/` directory with `report.json` is written by default. View
it locally with the [Flakiness CLI](https://flakiness.io/docs/cli/):

```bash
flakiness show ./flakiness-report
```

> [!TIP]
> Add `flakiness-report/` to your `.gitignore`.

### Stdin mode

If you'd rather control the `go test` invocation yourself, pipe a
`go test -json` stream in:

```bash
go test -json ./... | flakiness-go --stdin
```

## Retrying flaky tests

`--rerun-failed` brings upstream-Playwright retry semantics to `go test`: **stay
green on a transient flake, but still surface it as flaky** on Flakiness.io.

```bash
flakiness-go --rerun-failed -timeout 15m --race ./...   # bare flag → 2 reruns
flakiness-go --rerun-failed=3 ./...                     # up to 3 reruns
```

After the first pass, `flakiness-go` re-invokes `go test` on **only the tests
that failed** (scoped with an anchored `-run` regex and `-count=1`), up to N more
times. Every attempt is fed to the same converter, so:

- ✅ a test that **fails then passes** exits **0** (CI green) and is recorded as
  multiple `RunAttempt`s — Flakiness.io flags it **flaky**, not hidden;
- ✅ a real regression that **fails every attempt** still exits non-zero;
- ✅ cost is ~1× plus only-the-failed-tests, not 2× the whole suite.

Only failed tests rerun, so the whole suite isn't re-executed. Reruns are scoped
to the **top-level test function** (rerunning `TestX` re-executes all of
`TestX/...`, including subtests that already passed — those extra passing
attempts are harmless). The job's verdict is **per test**: it goes green only
once every test that failed in the first run has passed on some attempt.

**Safeguards** (borrowed from [gotestsum](https://github.com/gotestyourself/gotestsum)):

- `--rerun-failed-max-failures=N` (default `10`) — when the first run has more
  than N distinct failures, reruns are skipped so a broad breakage isn't papered
  over by per-test retries.
- `--rerun-failed-abort-on-data-race` (default `true`) — a data race is a real
  bug; reruns are skipped when the race detector fires.

**Notes**

- Wrapper-mode only. With `--stdin`, `flakiness-go` doesn't drive `go test`, so
  reruns can't apply (use [`gotestsum --rerun-fails`](https://github.com/gotestyourself/gotestsum)
  upstream of the pipe instead).
- Build/compile failures and `TestMain`/`init` panics are never masked — they
  fail the job immediately without reruns.
- Benchmarks aren't rerun (they need `-bench`, not `-run`); a failing benchmark
  fails the job.
- A `-run` / `-count` you pass is overridden on rerun rounds (Go's flag parsing
  is last-wins) so the failed-test scope and a cache-busting `-count=1` take
  effect. The rerun flags are inserted before any `-args` / `--` separator so
  they reach `go test`, not the test binary.

## Uploading to Flakiness.io

### GitHub Actions (OIDC — no secrets)

With `id-token: write` permission and a `FLAKINESS_PROJECT` set, uploads
authenticate via GitHub's OIDC token — no access token needed. The project must
be bound to the repository in your Flakiness.io project settings.

```yaml
permissions:
  id-token: write
steps:
  - uses: actions/setup-go@v5
    with: { go-version: "1.24" }
  - run: go install github.com/mxschmitt/flakiness-go/cmd/flakiness-go@latest
  - run: flakiness-go ./...
    env:
      FLAKINESS_PROJECT: my-org/my-project
```

See [`.github/workflows/flakiness.yml`](./.github/workflows/flakiness.yml) for
this repo dogfooding itself.

### Access token

```bash
export FLAKINESS_ACCESS_TOKEN="flakiness-io-..."
flakiness-go ./...
```

## Configuration

Each option resolves with the precedence **CLI flag > environment variable >
default**. Any flag not listed here is forwarded to `go test`.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--flakiness-output-dir` | `FLAKINESS_OUTPUT_DIR` | `flakiness-report` | Directory for the JSON report |
| `--flakiness-commit-id` | `FLAKINESS_COMMIT_ID` | git `HEAD` | Commit under test |
| `--flakiness-name` | `FLAKINESS_NAME` | `go` | Environment name / report category |
| `--flakiness-git-root` | `FLAKINESS_GIT_ROOT` | git toplevel | Root for path normalization |
| `--flakiness-title` | `FLAKINESS_TITLE` | — | Human-readable report title |
| `--flakiness-project` | `FLAKINESS_PROJECT` | — | `org/project`, required for GitHub OIDC |
| `--flakiness-access-token` | `FLAKINESS_ACCESS_TOKEN` | — | Upload token |
| `--flakiness-endpoint` | `FLAKINESS_ENDPOINT` | `https://flakiness.io` | Service endpoint |
| `--flakiness-disable-upload` | `FLAKINESS_DISABLE_UPLOAD` | `false` | Write the report but don't upload |
| `--stdin` | — | `false` | Read `go test -json` from stdin |
| `--rerun-failed[=N]` | `FLAKINESS_RERUN_FAILED` | `0` (off) | Rerun only failed tests up to N times (bare flag → 2). See [Retrying flaky tests](#retrying-flaky-tests) |
| `--rerun-failed-max-failures` | `FLAKINESS_RERUN_FAILED_MAX_FAILURES` | `10` | Skip reruns when the first run has more than this many distinct failures (`0` = unlimited) |
| `--rerun-failed-abort-on-data-race` | `FLAKINESS_RERUN_FAILED_ABORT_ON_DATA_RACE` | `true` | Don't rerun when the race detector fires |

### Custom environment data

`FK_ENV_*` variables are recorded into the report's environment metadata (prefix
stripped and key + value lowercased/trimmed, matching the other Flakiness
reporters): `FK_ENV_GPU_TYPE=H100` becomes `gpu_type: "h100"`.

## How it maps `go test` to the report

| `go test` concept | Report |
|---|---|
| package (import path) | `file` suite |
| `func TestXxx` | a test (source-located via `go/ast`) |
| subtest `TestXxx/a/b` | nested `suite` per segment, leaf is a test |
| repeated run (`-count=N`) or `--rerun-failed` | additional `RunAttempt`s on the test |
| `pass` / `fail` / `skip` | attempt status; `panic: test timed out` → `timedOut` |
| `t.Skip(reason)` | `skip` annotation with the reason |

Full feature coverage and intentional `N/A`s are documented in
[`features.md`](./features.md). Design rationale lives in [`PLAN.md`](./PLAN.md).

## Development

```bash
go test ./...      # run the suite
go vet ./...
go build ./cmd/flakiness-go
```

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

[MIT](./LICENSE)
