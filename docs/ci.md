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
(the jobs), [`scripts/check-ci-mapping.py`](../scripts/check-ci-mapping.py)
(the mapping guard) and [`scripts/ci-install-go.sh`](../scripts/ci-install-go.sh) and the tests that
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
| `mapping-coverage` | `scripts/check-ci-mapping.py` (every changed file matches a CI mapping) | `cimg/python:3.11`, small |

The names are set in the workflow, so they do not change when the job
definitions do. The jobs call make targets and hold no lint, test or coverage
logic of their own, so a CircleCI run and `make check` cannot disagree about
what a check means. Each job has its own checkout and runs in parallel, so
code generation in `proto` cannot change files `go` is testing.

## Implicated-only CI

CircleCI dynamic configuration (ADR 0020) runs only the jobs a PR's files
implicate. `.circleci/config.yml` is the setup config: one setup workflow
runs the `path-filtering` orb against `main`, sets five boolean pipeline
parameters from the changed files, and continues with
`.circleci/continue-config.yml`, which holds the jobs.

Filtering applies to PR branches only. On `main` every job runs in full,
whatever the push changed: each job's condition also holds when
`pipeline.git.branch` is `main`, so `main` keeps the coverage it had before
filtering, and the credits are saved on PR pushes, where most runs happen.

| Changed files | Jobs run |
|---|---|
| `.circleci/**`, `Makefile`, `python/pyproject.toml`, `scripts/**`, `bin/**`, `go/go.mod` | all (run-all) |
| `go/**` | go |
| `python/**` | python-3.11, python-3.13, go (the go job runs py-e2e) |
| `proto/**` | proto |
| `*.sh` | go (sh-lint lives in the go job) |
| `*.md`, `docs/**`, `bench/**`, `examples/**`, `deploy/**`, `.github/**`, `LICENSE*`, root dotfiles | lint |
| docs files Python tests assert on (`docs/paper-mapping.md`, `docs/roadmap.md`, `docs/roadmap-explained.md`, `docs/report.md`, `docs/ci.md`, `docs/cluster-vm.md`, `design.md`, `README.md`, `docs/design/0001-*`, `docs/design/0014-*`) | python-3.11, python-3.13, lint |
| `docs/paper-mapping.md` additionally | go (the Go paper-mapping test reads it) |
| `bench/README.md`, `bench/*.py` | python-3.11, python-3.13, lint (Python tests execute the generators and assert on the README) |
| `deploy/compose/*.yaml`, `deploy/compose/README.md` | python-3.11, python-3.13, lint (deploy tests validate the compose files and the guide) |
| `deploy/compose/monitoring/**` | go, python-3.11, python-3.13, lint (the Go dashboard test reads the Grafana dashboard; the Python deploy tests check the Prometheus config, the Grafana provisioning and the dashboard) |
| `examples/README.md`, `go/examples/**` | python-3.11, python-3.13, lint (the Python examples-guide test reads both) |
| anything else | the mapping-coverage job fails; the author adds a mapping |

Setup image pin: the filter job passes `tag: "3.12"`, not the orb default.
Orb 1.1.0 defaults the tag to 3.8, and the get-pip.py bootstrap its
Set-parameters step downloads now requires Python 3.10 or newer, so the
first setup run failed on the default image (job #1111). The pin is a
major.minor tag, not rolling `current`, so the image cannot silently
regress; any 3.10+ tag works. (Bumping within 1.x would not fix it: even
1.3.0 defaults to 3.9. If the orb is ever bumped to 2.x+, re-check its
default tag against that floor before dropping the pin.)

Echo-skip semantics: every job always exists in the workflow, so every
required check always reports. A job whose inputs did not change runs only
its skip note and reports SUCCESS after seconds, without a checkout or any
install. That is sound because a job's result is a function of its inputs:
if none of them changed, the result from `main` stands, and `main` cannot
merge red. There is deliberately no aggregator/gate job: CircleCI
`requires` is not satisfied by a job skipped with `when`, so a gate
requiring skipped jobs would never run.

The mapping-coverage job always runs and fails the pipeline if any changed
file matches no mapping pattern, so an unmapped file can never silently
skip every job. When adding a new top-level area to the repo, add its
mapping in the same PR; the guard enforces it. The guard script parses the
mapping block out of `.circleci/config.yml`, so each mapping line must stay
in the form `<regex> <parameter> true`, with no comments inside the block.

Transition: before this lands, branch protection requires the original four
checks. This change itself touches `.circleci/**`, so it maps to run-all
and runs the full suite under the old protection, merging normally with no
admin bypass. After merge, the owner adds `lint` and `mapping-coverage` to
the required checks (a separate action, not part of the PR); until then
those two report but are not required.

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
- **Artifacts.** Only `go/cover.out` and `python/cover.json` are kept.

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

The workflow runs when a PR is opened, on each new commit to a PR branch, and
on pushes to `main`. It needs no secrets, so a build triggered by a fork's PR
does not see any. Whether CircleCI builds fork PRs at all is a project
setting (Project Settings, Advanced, "Build fork pull requests"). Leave it on
only while no secret is added to the project. The checks need none.

"Auto-cancel redundant workflows" is on (Project Settings, Advanced): a new
push to a PR branch cancels that branch's run still in progress, which saves
Free-plan credits. CircleCI never auto-cancels on `main`.

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
  the failure lists it. Add a mapping line for it in `.circleci/config.yml`.

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
App, with triggers for pull requests (opened and pushed) and for pushes to
`main`, and GitHub shows the checks as `ci/circleci: proto`,
`ci/circleci: go`, `ci/circleci: python-3.11`, `ci/circleci: python-3.13`,
`ci/circleci: lint` and `ci/circleci: mapping-coverage`.
The first runs found two problems, both fixed: buf v1.74.0 needs a newer Go
than go.mod's (the generator install now allows `GOTOOLCHAIN=auto`), and buf
breaking cannot clone a blobless checkout (`proto` checks out in full). There
is no Actions workflow (Setup, step 5).

CircleCI reads this configuration from each branch, so a branch made before
it reached `main` gets no checks until `main` is merged into it.
