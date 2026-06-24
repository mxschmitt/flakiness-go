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
> keyless auth), and ships a [`features.md`](./features.md) keyed to the
> official feature checklist. Stdlib-only, zero third-party dependencies.

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

### Custom environment data

`FK_ENV_*` variables are recorded into the report's environment metadata (prefix
stripped, lowercased): `FK_ENV_GPU_TYPE=H100` becomes `gpu_type: "H100"`.

## How it maps `go test` to the report

| `go test` concept | Report |
|---|---|
| package (import path) | `file` suite |
| `func TestXxx` | a test (source-located via `go/ast`) |
| subtest `TestXxx/a/b` | nested `suite` per segment, leaf is a test |
| repeated run (`-count=N`) | additional `RunAttempt`s on the test |
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
