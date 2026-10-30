# Continuous integration

Every pull request is checked by CircleCI's Free plan, because GitHub-hosted
Actions jobs on the owner's account fail within seconds on a billing lock
([ADR 0014](design/0014-ci-on-pull-requests.md)). CircleCI runs the same make
targets as `make check`, in six jobs, but only the jobs a PR's files
implicate (see [Implicated-only CI](#implicated-only-ci)). CircleCI is the project's only CI: the
repository has no GitHub Actions workflow (the last one was deleted on
2026-10-29; see [Setup](#setup-by-the-owner), step 5).

**What is committed and what is not.** This repository holds the
configuration: [`.circleci/config.yml`](../.circleci/config.yml) (the setup
config), [`.circleci/continue-config.yml`](../.circleci/continue-config.yml)
(the jobs), [`.circleci/mapping.txt`](../.circleci/mapping.txt) (which
files run which jobs), [`scripts/ci-params.py`](../scripts/ci-params.py)
(the job selection and its check) and [`scripts/ci-install-go.sh`](../scripts/ci-install-go.sh) and the tests that
keep them consistent. The service itself is turned on by the owner, in
CircleCI and in GitHub's settings (see [Setup](#setup-by-the-owner)). Until the
owner has done that, no CircleCI check appears on a PR, and nothing here has
run on CircleCI. [Status](#status) says what has been confirmed.

## The jobs

| Check name | Make targets | Image, resource class |
|---|---|---|
| `proto` | `make proto-check` (buf lint, buf breaking against a fresh `origin/main`, generated code is current) | `cimg/python:3.13`, medium |
| `go` | `make go-lint go-test py-e2e` (gofmt, vet, golangci-lint, race tests, paper mapping, coverage floors, then the Python coordinator with the Go commands) | `cimg/python:3.13`, medium |
| `python-3.11` | `make py-lint py-test` (ruff, the Python tests and coverage floors) | `cimg/python:3.11`, small |
| `python-3.13` | `make py-lint py-test` | `cimg/python:3.13`, small |
| `lint` | `make md-lint` (the Markdown link and style checks) | `cimg/python:3.11`, small |
| `mapping-coverage` | `scripts/ci-params.py --check` (every changed file is mapped, and the jobs received the selection) | `cimg/python:3.11`, small |

The names are set in the workflow, so they do not change when the job
definitions do. The jobs call make targets and hold no lint, test or coverage
logic of their own, so a CircleCI run and `make check` cannot disagree about
what a check means. Each job has its own checkout and runs in parallel, so
code generation in `proto` cannot change files `go` is testing.

## Implicated-only CI

CircleCI dynamic configuration (ADR 0020) runs only the jobs a PR's files
implicate. `.circleci/config.yml` is the setup config: its one `select` job
runs `scripts/ci-params.py`, which diffs the branch against `origin/main` at
the merge base, turns the changed files into six boolean pipeline
parameters, and continues with `.circleci/continue-config.yml`, which holds
the jobs.

Filtering applies to PR branches only. A push to `main` runs no CI jobs
(ADR 0021): there the `select` job continues with the empty
`.circleci/empty-continue-config.yml` instead of selecting, so the pipeline ends after that one small job.
Branch protection merges a PR only when its six checks passed on a branch up
to date with `main`, so the merged tree was already tested. Should a `main`
pipeline ever continue, the script selects `run-all` there and each job's
condition also holds when `pipeline.git.branch` is `main`, so it would run
everything.

[`.circleci/mapping.txt`](../.circleci/mapping.txt) is the mapping: each
line is `<regex> <parameter>`, and a changed file sets every parameter whose
regex matches it. Three rules keep the selection conservative:

- **A deleted file runs everything.** Other directories may name it: the Go
  paper-mapping test checks that every file `docs/paper-mapping.md` cites
  exists, and the link check follows Markdown links. The diff runs with
  `--no-renames`, so a moved file is a deletion and an addition, and the job
  that lost the file runs too. (The path-filtering orb this replaced listed
  a moved file only at its new path, so moving a file out of `go/` skipped
  `go`.)
- **An unmapped file runs everything**, and `mapping-coverage` fails until it
  has a line.
- **`mapping-coverage` checks what the jobs received.** It runs the
  selection again and fails if the parameters the pipeline actually got
  (passed to it as `MR_CI_PARAMS`) lack one the changes need, so a broken
  setup step cannot skip a job silently.

| Changed files | Jobs run |
|---|---|
| `.circleci/**`, `Makefile`, `python/pyproject.toml`, `scripts/**`, `bin/**`, `go/go.mod`, `.gitignore` | all (run-all) |
| any deleted file, any unmapped file | all (run-all) |
| `go/**`, `*.sh`, `.githooks/**` | go (sh-lint lives in the go job) |
| `python/**` | python-3.11, python-3.13, and the go job's narrow mode (run-go-python: `make go-python-test py-e2e`) |
| `proto/**`, `buf.yaml`, `go/gen/**`, `python/mapreduce_reloaded/gen/**` | proto (it regenerates the code and fails if the committed copy differs) |
| `testdata/**` | go, python-3.11, python-3.13 |
| `*.md`, `docs/**`, `bench/**`, `examples/**`, `deploy/**`, `.github/**`, `LICENSE*`, root dotfiles | lint |
| files outside `python/` the Python tests read: `README.md`, `design.md`, `docs/README.md`, `docs/paper-mapping.md`, `docs/roadmap.md`, `docs/roadmap-explained.md`, `docs/report.md`, `docs/ci.md`, `docs/cluster-vm.md`, `docs/design/0001-*`, `docs/design/0014-*`, `bench/README.md`, `bench/*.py`, `bench/*.sh`, `deploy/compose/*.yaml`, `deploy/compose/README.md`, `deploy/compose/monitoring/**`, `examples/**`, `go/examples/**`, `.coderabbit.yaml`, `.codeant/**` | python-3.11, python-3.13, and lint where the file is docs |
| files outside `go/` the Go tests read: `README.md`, `docs/paper-mapping.md`, `examples/**`, `bench/jobs/**`, `deploy/compose/monitoring/**` | go, and lint where the file is docs |

The cross-directory rows come from tracing every file the Go and Python test
suites open. The Python side checks itself: `python/tests/conftest.py`
records every tracked file outside `python/` that a test opens, and the
test run fails, naming the file, if the mapping does not route it to the
Python jobs. (The whole-repository link sweep is marked `lint_inputs`: it
reads every Markdown file, which the lint job checks.) For the Go tests, a
new read of a file outside `go/` needs its line added by hand, and
`python/tests/test_ci_params.py` lists the known ones.

Echo-skip semantics: every job always exists in the workflow, so every
required check always reports. A job whose inputs did not change runs only
its skip note and reports SUCCESS after seconds, without a checkout or any
install. That is sound because a job's result is a function of its inputs:
if none of them changed, the result from `main` stands, and `main` cannot
merge red. There is deliberately no aggregator/gate job: CircleCI
`requires` is not satisfied by a job skipped with `when`, so a gate
requiring skipped jobs would never run.

When adding a new top-level area to the repo, add its lines to
`.circleci/mapping.txt` in the same PR; `mapping-coverage` enforces it, and
`test_every_tracked_file_is_mapped` fails locally first.

How each job meets its requirements:

- **Go version.** `scripts/ci-install-go.sh` reads it from `go/go.mod` (the
  `toolchain` line wins over `go`, as for the go command), downloads that
  release, and checks the archive's SHA-256 against go.dev's release list. The
  jobs set `GOTOOLCHAIN=local`, so a Go that does not match fails the job
  rather than being downloaded behind its back. The one exception is
  `make tools` in `proto`: some generator pins need a newer Go than go.mod's
  (buf v1.74.0 needs 1.26.7), so that step allows `GOTOOLCHAIN=auto`, as
  `scripts/vm-setup.sh` does, and the go command fetches that toolchain,
  checked against the Go checksum database.
- **Race detector.** `go test -race` needs cgo, so the Go jobs set
  `CGO_ENABLED=1`; `cimg/python` images include a C compiler.
- **golangci-lint.** Built from source by the job's Go, at the version in
  `scripts/vm-setup.sh` (`GOLANGCI_VERSION`, currently v2.14.0). The workflow
  and the VM setup use the same pin, and a test fails if they drift.
- **shellcheck.** The `go` job installs the release pinned in
  `scripts/vm-setup.sh` (`--shellcheck`, which checks its SHA-256 sum) and
  runs `make sh-lint` with `MR_REQUIRE_SHELLCHECK=1`. Locally, `make lint`
  skips shellcheck with a message if it is not installed; in CI a missing
  shellcheck fails the job.
- **Python in the Go job.** The Go golden tests run the Python reference
  implementation, so the `go` job installs `python[dev]` too. With both
  installed, it also runs `make py-e2e`: the Python coordinator with the Go
  worker and `mr` commands. The Python jobs have no Go and skip those
  tests; `py-e2e` sets `MR_REQUIRE_GO`, which turns the skip into a failure.
- **Breaking-change check.** The `proto` job fetches `origin/main` fresh and
  runs `make proto-check PROTO_FETCH=false`. If the fetch fails, the job
  fails; the check is never skipped. A fork's build has no deploy key, so the
  job falls back to the public HTTPS URL.
- **Narrow Go mode.** A PR whose Go-side inputs are only `python/` runs
  `make go-python-test py-e2e` in the go job instead of the whole Go suite:
  `cmd/mr` (its golden tests compare the Go examples with Python's),
  `papermap` (it reads the Python test names) and `py-e2e`. Every other Go
  package passes with `python/` deleted and no Python on the PATH, so its
  result from `main` stands. `TestOnlyGoPythonTestPackagesReadPython` fails
  if another Go test starts to read `python/` or run Python. Any other
  implicated file, `run-all`, and `main` run the full mode.
- **Go build cache.** The go job restores Go's build cache and
  golangci-lint's analysis cache, so compiled packages (the race-built
  standard library above all) are not rebuilt on every run. Both caches are
  keyed by content, so an entry is reused only for identical input, and the
  job sets `GOFLAGS=-count=1`, so test results are never cached: every test
  runs every time. The key changes with the Go release, `go.sum` and the ISO
  week, so the cache is saved about once a week, not on every run.
- **Artifacts.** Only `go/cover.out` and `python/cover.json` are kept.

## Test results and artifacts

The `go` and `python` jobs set `JUNIT_DIR=/tmp/test-results`, so `make`
writes JUnit XML there: `gotestsum` runs the Go tests (`go-test.xml`), and
pytest gets `--junitxml` (`py-test.xml`, `py-e2e.xml`). The jobs hand the
directory to CircleCI's `store_test_results`, even when a test failed, so
the Tests tab lists every test with its time and failures, and CircleCI's
flaky-test and timing insights work. The same files are artifacts under
`test-results/`, with `go/cover.out` and `python/cover.json`. Without
`JUNIT_DIR`, as in `make check`, the tests run as before.

## Caching

Caches only save time; none of them replaces a check.

| Cache | Key | What a hit skips |
|---|---|---|
| pip downloads | Python version, `python/pyproject.toml` | Downloads. `pip install` still runs. |
| Go toolchain | architecture, Go version | The download. The script runs the restored `go` and rejects a wrong version. |
| Go modules | `go/go.sum` | Downloads. `go mod download` still runs. |
| Generators in `.tools/bin` | the `@v` pins in the `Makefile`, Go version | Compiling. `make tools` still runs. |
| golangci-lint | the pin, Go version | Compiling. The job rebuilds a binary whose version or Go build differs. |

Bump a `-v1-` in a key to throw a cache away.

## Triggers

The project has one pipeline, `quality-gate` (a GitHub App pipeline), with
exactly one trigger: the preset "PR opened or pushed to, default branch and
tag pushes" (Project Settings, Triggers). Each event starts one pipeline:

- Opening a PR, and each push to a PR branch, runs the selected jobs.
  `[skip ci]` in the pushed head commit skips a push's pipeline, but not the
  one that opening a PR starts.
- A push to `main` runs only the `select` job, which ends the pipeline
  (ADR 0021).
- A tag push runs `select`, which compares the tagged commit with `main` at
  their merge base. A tag on a commit already in `main` changes no files, so
  five jobs skip in seconds and `mapping-coverage` checks that selection. A
  tag on a commit not yet in `main` runs the jobs its changes select.

Do not add a second trigger. Until 2026-10-29 a separate "Pushes to default
branch" trigger also matched `main`, so every push to `main` started two
pipelines. The trigger lives in the CircleCI settings, not in this
repository, so this section is its record.

The pipeline needs no secrets, so a build triggered by a fork's PR
does not see any. Whether CircleCI builds fork PRs at all is a project
setting (Project Settings, Advanced, "Build fork pull requests"). Leave it on
only while no secret is added to the project. The checks need none.

"Auto-cancel redundant workflows" is on (Project Settings, Advanced): a new
push to a PR branch cancels that branch's run still in progress, which saves
Free-plan credits. CircleCI never auto-cancels on `main`, which is why
`main` is skipped in the config instead.

Branch protection requires a PR to be up to date with `main`. A PR that
passed before another one merged must merge `main` in (a merge commit, not
a rebase) and pass again, so two PRs that each pass alone, but break when
combined, cannot both land.

## Free plan

The setup uses only what the Free plan includes: Docker executors with the
`small` and `medium` resource classes. It does not use remote Docker (`setup_remote_docker`), Docker
layer caching, machine executors, larger resource classes, or a paid plan, and
there is no payment method on the account.

The Free plan is not unlimited. At the time of writing CircleCI gives a
monthly credit allowance (30,000 credits on the Free plan, and up to 400,000
for a public open-source project on Linux). The plans change, so check
[CircleCI's pricing page](https://circleci.com/pricing/) and the project's
Plan page before relying on a figure. A job that has used up the credits
fails to start; that shows on the PR as a CircleCI failure, and the
fallbacks below still apply. The Compose end-to-end test, the S3 gateway
test, the long chaos, fuzz and flaky runs, and the benchmarks are not in CircleCI: they need a Docker daemon or
many minutes, and stay on `make e2e`, `make e2e-s3`, `make chaos`,
`make fuzz`, `make flaky` and `make perf` (see [Testing](testing.md)).

## Setup by the owner

Only the owner of the GitHub repository and the CircleCI organization can do
these. Do them in order. Each step is safe to stop after.

1. **Connect the project.** Sign in to CircleCI with GitHub, create an
   organization on the **Free** plan (do not add a payment method), and set up
   this repository (Projects, Set Up Project, use the existing
   `.circleci/config.yml`).
2. **Run the branch.** Push a branch with this configuration, or re-run
   the PR's build. Confirm that all six checks (`proto`, `go`, `python-3.11`,
   `python-3.13`, `lint`, `mapping-coverage`) pass on the latest commit.
3. **Check that it can fail.** Open a throwaway PR with a deliberate
   failure (a badly formatted Python file, say) and confirm CircleCI reports
   it red. Close it without merging.
4. **Require the checks.** In GitHub, Settings, Branches (or Rulesets), protect
   `main`: require a pull request, and require the six CircleCI checks.
   Pick them from the list GitHub offers after step 2, using the names it
   shows there (usually `ci/circleci: proto` and so on); do not type them in.
5. **Retire the Actions workflow.** Done: on 2026-10-27 the workflow was
   limited to `workflow_dispatch` and disabled (`gh workflow disable CI`),
   since every run failed on the billing lock and notified the owner, and on
   2026-10-29 `.github/workflows/ci.yml` was deleted, so no check depends on
   Actions. A test fails if `.github/workflows/` comes back. No Actions check
   is in the required list. GitHub's automatic dependency submission (the
   "Dependency Graph" runs under Actions) is a repository setting, not a
   file here: Settings, Code security, Dependency graph.
6. **Confirm the plan.** Open the CircleCI Plan page and confirm it still says
   Free.

## Required checks

| Branch protection | Source |
|---|---|
| Pull request required, `main` | GitHub |
| `proto`, `go`, `python-3.11`, `python-3.13`, `lint`, `mapping-coverage` | CircleCI, as GitHub names them |
| Branch up to date with `main` before merging | GitHub (strict status checks, on since 2026-10-28) |
| `local-ci/make-check` | optional, from `make ci-report` |

`make check`, the pre-push hook and `make ci-report` are unchanged. The hook
catches a failure before the push, and `make ci-report` still posts a status
that does not depend on any hosted service.

## Troubleshooting

- **No CircleCI check on a PR.** The project is not connected (Setup, step 1),
  or the PR is from a fork and fork builds are off.
- **`proto` fails fetching `origin/main`.** The checkout lost access to the
  repository. Check the project's deploy key under Project Settings, SSH Keys.
- **`proto` fails in buf breaking with `could not fetch ... from promisor
  remote`.** The checkout is a blobless partial clone, which buf cannot
  clone. The `proto` job uses `checkout: method: full`; keep it.
- **`go` fails with `checksum mismatch`.** go.dev's release list and the
  archive disagree. Re-run once; if it persists, do not work around the
  check, and open an issue.
- **`go` fails with a Go version mismatch.** A cache or the image supplied a
  different Go. Bump `go-toolchain-v1` in `.circleci/continue-config.yml`.
- **`go` fails in `golangci-lint`.** Run `make go-lint` locally with
  golangci-lint built by the Go in `go/go.mod`. A linter built by an older Go
  cannot lint newer code.
- **Race detector missing.** `CGO_ENABLED=1` and a C compiler are required;
  the `medium` executor sets the first, the image has the second.
- **A job fails to start with a credits message.** The monthly allowance is
  spent. Run `make ci-report` for the PR, or wait for the reset.
- **A stale cache.** Bump the `-v1-` in its key.
- **A job succeeds in seconds without running.** Expected when the PR touches
  none of that job's inputs: the job ran only its skip note (see
  [Implicated-only CI](#implicated-only-ci)). If the job should have run,
  its inputs are mapped wrong — fix the mapping, not the job.
- **`mapping-coverage` fails.** A changed file matches no mapping pattern;
  the failure lists it. Add a mapping line for it in `.circleci/mapping.txt`.

## Rollback

If CircleCI has to go, remove the six checks from branch protection, then
restore `.github/workflows/ci.yml` from git history:

```sh
mkdir -p .github/workflows
git show 1717257:.github/workflows/ci.yml > .github/workflows/ci.yml
```

Set its triggers back to `pull_request`, `push` to `main` and
`workflow_dispatch`, enable it (`gh workflow enable CI`), and (if the Actions
account lock persists) use `CI_RUNNER` or `make ci-report` as in ADR 0014.
Delete `.circleci/` when no PR needs it. Nothing else depends on it.

## Status

Live since 2026-10-27. The project is connected through the CircleCI GitHub
App, with one trigger, the preset "PR opened or pushed to, default branch and
tag pushes" (see Triggers); a push to `main` runs no CI jobs (ADR 0021),
and GitHub shows the checks as `ci/circleci: proto`,
`ci/circleci: go`, `ci/circleci: python-3.11`, `ci/circleci: python-3.13`,
`ci/circleci: lint` and `ci/circleci: mapping-coverage`.
The first runs found two problems, both fixed: buf v1.74.0 needs a newer Go
than go.mod's (the generator install now allows `GOTOOLCHAIN=auto`), and buf
breaking cannot clone a blobless checkout (`proto` checks out in full). There
is no Actions workflow (Setup, step 5).

CircleCI reads this configuration from each branch, so a branch made before
it reached `main` gets no checks until `main` is merged into it.
