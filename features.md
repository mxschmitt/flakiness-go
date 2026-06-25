# Reporter Features — go test

Status of [Flakiness Report Features](https://github.com/flakiness/flakiness-report/blob/main/features.md)
as implemented by this `flakiness-go` reporter. Many `N/A` rows reflect that
Go's `go test` (and its `-json` event stream) has no native concept for the
feature — they are not gaps in this reporter so much as in the runner's output.

| # | Feature | Status | Notes |
|---|---------|--------|-------|
| 1 | Report metadata | ✅ | `commitId` from git HEAD, CI run URL auto-detected (GitHub Actions / Azure DevOps / GitLab `CI_JOB_URL` / Jenkins `BUILD_URL`), start time & duration from the event stream. `generatedBy` = `flakiness-go`, `testRunner` = `go test`, `runtime` = `go`. `configPath` N/A (Go has no single test config file). `relatedCommitIds` not populated. |
| 2 | Environment metadata | ✅ | `name`, `osName` (Flakiness convention, matching the Node SDK: `macos`, `win`, or the distro `NAME` from `/etc/os-release` on Linux — e.g. `ubuntu`), `osVersion` (macOS `sw_vers -productVersion`, Linux `VERSION_ID`, Windows `ver`; without it Flakiness.io renders the OS as "unknown"), `osArch` (`uname -m` on Unix — e.g. `x86_64`/`aarch64` — `GOARCH` on Windows, matching the SDK), `go_version`. All so FQL filters and environment dedup match the other reporters. |
| 3 | Multiple environments | N/A | `go test` runs one toolchain/GOOS/GOARCH per invocation; a single `environments[]` entry is emitted. Matrix shards are separate runs/reports. |
| 4 | Custom environments (`FK_ENV_*`) | ✅ | `FK_ENV_*` variables are parsed into `environment.metadata`: prefix matched case-insensitively, key lowercased, value trimmed + lowercased (matching the Node SDK so environments hash-dedup and FQL-match across reporters). |
| 5 | Test hierarchy / suites | ✅ | Each package → a `file` suite (titled by import path). Subtests (`TestX/a/b`) → nested `suite` nodes per path segment; leaves are tests. A parent test that itself fails/panics (not just its subtests) keeps its own attempt as a leaf test inside its suite, so its error is not lost. |
| 6 | Per-attempt reporting (retries) | ✅ | Re-runs of the same test (e.g. `-count=N`) each emit their own `RunAttempt` with independent status/duration/errors. |
| 7 | Per-attempt timeout | N/A | `go test -timeout` is a single whole-binary deadline, not exposed as a per-test value in the `-json` stream, so `RunAttempt.timeout` is not set. (A test killed by that deadline is still *classified* as `timedOut` — see #status mapping below.) |
| 8 | Test steps | N/A | Go has no native step concept. |
| 9 | Expected status (`expectedStatus`) | N/A | Go has no "expected to fail" mechanism; `expectedStatus` is always `passed`. |
| 10 | Attachments | N/A | Go has no native attachment mechanism. The upload path supports attachments for future use. |
| 11 | Step-level attachments | N/A | No steps, no attachments. |
| 12 | Timed StdIO | ❌ | The `-json` stream interleaves stdout/stderr as `output` events but doesn't separate streams or expose reliable per-write deltas; captured output is stored as `stdout` text. |
| 13 | Annotations | ✅ | `skip` annotations (with the `t.Skip` reason) are emitted. Go has no native tag/owner markers. |
| 14 | Tags | N/A | Go tests have no native tagging mechanism. (Build tags select files, not individual tests.) |
| 15 | `parallelIndex` | N/A | `t.Parallel()` exists but the worker index is not exposed by `go test -json`. |
| 16 | `FLAKINESS_TITLE` | ✅ | Honored via `--flakiness-title` / `FLAKINESS_TITLE`. |
| 17 | `FLAKINESS_OUTPUT_DIR` | ✅ | Honored via `--flakiness-output-dir` / `FLAKINESS_OUTPUT_DIR`, defaults to `flakiness-report`. |
| 18 | Sources | ✅ | Top-level `sources[]` is populated with ±5-line excerpts around every referenced location (test definitions, errors, skip annotations), read from the git checkout. Overlapping ranges in a file are merged; `lineOffset` is set when an excerpt doesn't start at line 1. |
| 19 | Error snippets | ❌ | `go test` emits plain failure text without ANSI-highlighted code excerpts. |
| 20 | Errors support | ✅ | Failures are captured as a `ReportError` with a message synthesized from the test's output. Go surfaces a single aggregate failure per test, so one error per attempt. |
| 21 | Unattributed errors | ✅ | A package that fails with no test results — compile/build failure (the `build-output`/`FailedBuild` diagnostics are captured), `init()`/`TestMain` panic, or setup failure — becomes a report-level `unattributedError` with the failure output, instead of vanishing. |
| 22 | Source locations | ✅ | Top-level `TestXxx`/`Example`/`Benchmark`/`Fuzz` functions are located by parsing `*_test.go` with `go/ast`. Subtests are created at runtime and have no static location. |
| 23 | Auto-upload | ✅ | GitHub OIDC (via `FLAKINESS_PROJECT`), `FLAKINESS_ACCESS_TOKEN`, and `FLAKINESS_DISABLE_UPLOAD` / `--flakiness-disable-upload` opt-out. |
| 24 | CPU / RAM telemetry | ✅ | In wrapper mode, a 1s background sampler records system CPU (`cpuAvg`/`cpuMax` across cores) and RAM utilization while `go test` runs, plus `cpuCount`/`ramBytes`. Series are flat-region-coalesced and delta-encoded like the SDK. Sampling reads `/proc` (Linux, where CI runs); on other platforms it's a documented no-op. Not collected in `--stdin` mode (tests ran elsewhere). |
| 25 | Duplicate-name handling | ⚠️ | `go test` auto-disambiguates duplicate subtest names (a second `t.Run("x", …)` becomes `x#01`), so in-package collisions can't reach the converter. Cross-package names are disambiguated by the package (file) suite. Re-runs are intentionally merged as attempts (feature 6). |

## Status mapping

`go test -json` actions map to attempt statuses as follows:

| go test signal | status |
|---|---|
| `pass`, `bench` | `passed` |
| `skip` | `skipped` (with a `skip` annotation carrying the `t.Skip` reason) |
| `fail` | `failed` |
| test killed by `-timeout` (panic banner, no per-test terminal event) | `timedOut` |
| passing benchmark (no per-test terminal event, only a package-level `pass`) | `passed` |
| `run` with no terminal event (binary crashed/aborted) | `interrupted` |

Tests, subtests (`TestX/a/b`, arbitrary depth), examples (`func Example…`),
fuzz targets run as tests (`func Fuzz…`), and benchmarks (`func Benchmark…`) are
all handled. Benchmarks are a special case: per the test2json contract only a
*failing* benchmark emits a per-test `bench`/`fail` event — a passing one
produces just a package-level `pass` — so an un-terminated benchmark attempt is
treated as passed rather than interrupted.

When `go test -timeout` fires, the runner prints a `panic: test timed out`
banner attributed to the hung test but emits **no** per-test `pass`/`fail`
event (only the package fails). The converter detects that banner on an
otherwise-unterminated attempt and reports `timedOut` rather than `interrupted`.
