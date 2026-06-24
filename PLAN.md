# flakiness-go — Design & Implementation Plan

The official [Flakiness.io](https://flakiness.io) reporter for **Go's `go test`**.

This document records the investigation and the plan that produced this package.
It is the Go counterpart to [`pytest-flakiness`](https://github.com/flakiness/pytest-flakiness).

## 1. Background: how the other reporters work

A Flakiness reporter has exactly one job: take whatever a test runner emits and
turn it into a single **Flakiness JSON Report** (see
[`flakiness-report`](https://github.com/flakiness/flakiness-report)), optionally
writing it to a directory and/or uploading it to flakiness.io.

The report is a JSON document (`report.json`) with an optional sibling
`attachments/` directory. Its shape (abridged, see `report/report.go` for the
full set of types):

```
Report {
  category        string         // "go" for us
  commitId        string         // 40-char git SHA
  environments    Environment[]  // >= 1
  suites?         Suite[]        // tree: file/suite/anonymous suite
  tests?          Test[]         // top-level tests not in a suite
  startTimestamp  unixMillis
  duration        ms
  title? url? generatedBy? testRunner? runtime? ...
}
Test    { title, location?, tags?, attempts: RunAttempt[] }
Suite   { type, title, location?, suites?, tests? }
RunAttempt { environmentIdx?, expectedStatus?, status?, startTimestamp,
             duration?, errors?, stdout?, stderr?, annotations?, attachments? }
```

Key concepts reused from the spec:

- **Test** = a location in source that can be run; **Suite** = grouping of
  tests; **RunAttempt** = one execution of a test in one environment. Retries
  become multiple attempts on the same test.
- **Status** is one of `passed | failed | timedOut | skipped | interrupted`,
  each attempt also carries an `expectedStatus`.
- All file paths are POSIX, **relative to the git root**.
- `FK_ENV_*` environment variables are recorded (prefix stripped, lowercased)
  into `environments[].metadata`.

The `pytest-flakiness` reference splits cleanly into: report types, a reporter
that accumulates results, options resolution (CLI > env > ini > default), git
helpers, CI-URL detection, GitHub OIDC, and an uploader. We mirror that
structure in idiomatic Go.

## 2. The Go side: `go test -json`

Go has no plugin system like pytest, but it has a stable machine-readable
output: `go test -json` (documented under `go doc cmd/test2json`). It emits a
newline-delimited stream of `TestEvent` objects:

```go
type TestEvent struct {
    Time    time.Time // RFC3339; omitted for cached results
    Action  string    // start|run|pause|cont|pass|bench|fail|output|skip
    Package string    // import path
    Test    string    // test func, "TestX/sub/case" for subtests; empty = package-level
    Elapsed float64   // seconds, set on pass/fail
    Output  string     // chunk of merged stdout+stderr
}
```

This is the integration point. Two ways to obtain the stream, both supported:

1. **Wrapper mode** (default): `flakiness-go [go-test-args...]` runs
   `go test -json <args>` itself, tees the stream to its own stdout (so the
   developer still sees normal output via a re-render) and to the parser.
2. **Stdin mode**: `go test -json ./... | flakiness-go --stdin`. Useful when the
   user wants full control over the `go test` invocation, or in CI.

### Mapping `go test -json` → report

| go test concept                     | report concept                                  |
|-------------------------------------|-------------------------------------------------|
| package (import path)               | `Suite{ type: "file", title: <import path> }`   |
| top-level `TestFoo`                 | `Test` inside that package suite                |
| subtest `TestFoo/sub/case`          | nested `Suite{ type: "suite" }` per path segment, leaf is a `Test` |
| `run` event                         | start a new attempt (start ts = event time)     |
| `pass`/`fail`/`skip` event          | close attempt, set status + duration (Elapsed)  |
| repeated run of same name (`-count`)| additional `RunAttempt` on the same `Test`      |
| `output` events                     | appended to the attempt's `stdout`              |
| `fail` with collected output        | a `ReportError{ message }` synthesized from output |
| package without tests (`skip`)      | suite with no tests (dropped)                   |

Status mapping:

- `pass` → `passed`
- `skip` → `skipped` (Go marks `t.Skip` and also "no test files"; the latter is
  a package-level skip and is dropped)
- `fail` → `failed`, unless the test's output contains `panic:`/`test timed out`
  → `timedOut`. (Go has no distinct timeout status; the `-timeout` killing a
  test produces a `panic: test timed out` line we detect.)
- A test that started (`run`) but never produced a terminal event (binary
  crashed) → `interrupted`.

`expectedStatus` is always `passed` for Go — Go has no `xfail` equivalent.

### Things Go does *not* give us (documented N/A, like pytest's features.md)

- **Source locations**: test2json carries no file/line. We recover the location
  of each top-level `TestFoo` by parsing the package's `*_test.go` files with
  `go/ast` and finding the `func TestFoo(t *testing.T)` declaration. Subtests
  are created dynamically at runtime, so they get no location (best effort).
- **Per-attempt timeout**, **steps**, **step attachments**, **timed stdio**,
  **soft multi-errors**: no native concept → omitted.
- **Tags / annotations**: Go has no test markers. We support **`FK_ENV_*`**
  env metadata, and skip messages become a `skip` annotation with the reason.
- **Attachments**: no native concept → not populated.
- **parallelIndex**: `t.Parallel()` exists but test2json doesn't expose a worker
  index → omitted.

## 3. Package layout

```
flakiness-go/
├── go.mod                         # module github.com/mxschmitt/flakiness-go, no deps
├── README.md
├── PLAN.md                        # this file
├── report/
│   └── report.go                  # report types + JSON tags (the schema, in Go)
├── internal/
│   ├── gotest/
│   │   ├── event.go               # TestEvent + stream decoder
│   │   ├── convert.go             # TestEvent stream -> report.Report  (the core)
│   │   └── locate.go              # go/ast source-location lookup
│   ├── config/
│   │   └── config.go              # option resolution CLI > env > default
│   ├── gitinfo/
│   │   └── gitinfo.go             # git root + commit (safe.directory workaround)
│   ├── ci/
│   │   └── ci.go                  # CI run-URL detection (GH Actions, Azure, GitLab, Jenkins)
│   ├── oidc/
│   │   └── oidc.go                # GitHub Actions OIDC token fetch
│   └── upload/
│       └── upload.go              # start/put/attachments/finish upload protocol
└── cmd/
    └── flakiness-go/
        └── main.go                # CLI wiring
```

### Dependencies

**Standard library only.** pytest's uploader brotli-compresses the report and
text attachments before the presigned PUT; Go's stdlib has no brotli encoder, so
we upload **uncompressed** (omit `Content-Encoding`) — the spec explicitly says
reporters must not compress attachments themselves and the server applies its
own compression, so this is safe and keeps the module dependency-free.

## 4. Configuration (mirrors pytest precedence: CLI flag > env > default)

| Flag                       | Env                       | Default            | Meaning |
|----------------------------|---------------------------|--------------------|---------|
| `--flakiness-output-dir`   | `FLAKINESS_OUTPUT_DIR`    | `flakiness-report` | report dir |
| `--flakiness-commit-id`    | `FLAKINESS_COMMIT_ID`     | git HEAD           | commit under test |
| `--flakiness-name`         | `FLAKINESS_NAME`          | `go`               | environment name / category |
| `--flakiness-git-root`     | `FLAKINESS_GIT_ROOT`      | git toplevel       | path-normalization root |
| `--flakiness-title`        | `FLAKINESS_TITLE`         | —                  | report title |
| `--flakiness-project`      | `FLAKINESS_PROJECT`       | —                  | `org/project`, needed for OIDC |
| `--flakiness-access-token` | `FLAKINESS_ACCESS_TOKEN`  | —                  | upload token (no ini equiv by design) |
| `--flakiness-endpoint`     | `FLAKINESS_ENDPOINT`      | `https://flakiness.io` | service endpoint |
| `--flakiness-disable-upload`| `FLAKINESS_DISABLE_UPLOAD`| `false`           | write only, no upload |
| `--stdin`                  | —                         | `false`            | read `go test -json` from stdin |

Go has no project ini file like `pytest.ini`, so we stop at CLI > env > default
(one tier shorter than pytest). Everything after `--` (or any non-flakiness
flag) is forwarded to `go test`.

## 5. Upload protocol (mirrors `uploader.py`)

1. `POST {endpoint}/api/upload/start` with `Authorization: Bearer <token>` →
   `{ uploadToken, presignedReportUrl, webUrl }`.
2. `PUT presignedReportUrl` with the report JSON (`Content-Type: application/json`).
3. If attachments: `POST {endpoint}/api/upload/attachments` with `{attachmentIds}`
   using the `uploadToken`, then `PUT` each presigned URL. (No attachments are
   produced today, but the path is implemented for parity.)
4. `POST {endpoint}/api/upload/finish` with the `uploadToken`.

Auth token resolution: explicit `--flakiness-access-token`/env first; otherwise,
if running in GitHub Actions (`ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN` present) and a
`flakinessProject` is set, fetch a GitHub OIDC token with the project as audience.

Retries with backoff on 5xx for the POST/PUT calls, like the reference.

## 6. Testing strategy

- **`internal/gotest` (table-driven):** feed canned `go test -json` streams
  (captured from real runs) and assert the produced `report.Report` — statuses,
  suite nesting for subtests, multiple attempts under `-count`, stdout capture,
  failure → error, panic → `timedOut`, skipped tests + skip annotation.
- **`locate` test:** point at a fixture `*_test.go` and assert file/line.
- **`config` test:** assert CLI > env > default precedence for each option.
- **`ci` test:** set GH Actions / Azure / GitLab / Jenkins env and assert URL.
- **`upload` test:** spin up an `httptest.Server` implementing the 4-step
  protocol and assert the reporter walks it correctly (incl. retry on 503).
- **End-to-end:** a `testdata` Go module with passing/failing/skipped/subtests;
  run the built CLI in wrapper mode against it, parse `report.json`, assert.

`go test ./...` must be green and `go vet ./...` clean.

## 7. Out of scope (v1)

Attachments production, test steps, CPU/RAM telemetry, `relatedCommitIds`,
`sources[]` embedding. All are optional in the spec and have no natural Go
`go test` source — documented in `features.md`.
