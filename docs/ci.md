# Continuous integration

Every pull request is checked by CircleCI's Free plan, because GitHub-hosted
Actions jobs on the owner's account fail within seconds on a billing lock
([ADR 0014](design/0014-ci-on-pull-requests.md)). CircleCI runs the same make
targets as `make check`, in four jobs. CircleCI is the project's only CI: the
Actions workflow is kept for a run by hand, and is disabled in the
repository's Actions settings (2026-10-27).

**What is committed and what is not.** This repository holds the
configuration: [`.circleci/config.yml`](../.circleci/config.yml),
[`scripts/ci-install-go.sh`](../scripts/ci-install-go.sh) and the tests that
keep them consistent. The service itself is turned on by the owner, in
CircleCI and in GitHub's settings (see [Setup](#setup-by-the-owner)). Until the
owner has done that, no CircleCI check appears on a PR, and nothing here has
run on CircleCI. [Status](#status) says what has been confirmed.

## The four jobs

| Check name | Make targets | Image, resource class |
|---|---|---|
| `proto` | `make proto-check` (buf lint, buf breaking against a fresh `origin/main`, generated code is current) | `cimg/python:3.13`, medium |
| `go` | `make go-lint go-test py-e2e` (gofmt, vet, golangci-lint, race tests, paper mapping, coverage floors, then the Python coordinator with the Go commands) | `cimg/python:3.13`, medium |
| `python-3.11` | `make py-lint py-test` | `cimg/python:3.11`, small |
| `python-3.13` | `make py-lint py-test` | `cimg/python:3.13`, small |

The names are set in the workflow, so they do not change when the job
definitions do. The jobs call make targets and hold no lint, test or coverage
logic of their own, so a CircleCI run and `make check` cannot disagree about
what a check means. Each job has its own checkout and runs in parallel, so
code generation in `proto` cannot change files `go` is testing.

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
   the PR's build. Confirm that all four checks (`proto`, `go`, `python-3.11`,
   `python-3.13`) pass on the latest commit.
3. **Check that it can fail.** Open a throwaway PR with a deliberate
   failure (a badly formatted Python file, say) and confirm CircleCI reports
   it red. Close it without merging.
4. **Require the checks.** In GitHub, Settings, Branches (or Rulesets), protect
   `main`: require a pull request, and require the four CircleCI checks.
   Pick them from the list GitHub offers after step 2, using the names it
   shows there (usually `ci/circleci: proto` and so on); do not type them in.
5. **Retire the Actions workflow.** Done on 2026-10-27: the triggers of
   `.github/workflows/ci.yml` are `workflow_dispatch` alone, and the workflow
   is disabled under Actions (`gh workflow disable CI`), since every run failed
   on the billing lock and notified the owner. Actions stays on for the
   repository (GitHub's own Dependency Graph workflow uses it). Remove any
   Actions check from the required list.
6. **Confirm the plan.** Open the CircleCI Plan page and confirm it still says
   Free.

## Required checks

| Branch protection | Source |
|---|---|
| Pull request required, `main` | GitHub |
| `proto`, `go`, `python-3.11`, `python-3.13` | CircleCI, as GitHub names them |
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
  different Go. Bump `go-toolchain-v1` in `.circleci/config.yml`.
- **`go` fails in `golangci-lint`.** Run `make go-lint` locally with
  golangci-lint built by the Go in `go/go.mod`. A linter built by an older Go
  cannot lint newer code.
- **Race detector missing.** `CGO_ENABLED=1` and a C compiler are required;
  the `medium` executor sets the first, the image has the second.
- **A job fails to start with a credits message.** The monthly allowance is
  spent. Run `make ci-report` for the PR, or wait for the reset.
- **A stale cache.** Bump the `-v1-` in its key.

## Rollback

If CircleCI has to go: remove the four checks from branch protection, enable
the workflow again (`gh workflow enable CI`), set the triggers of
`.github/workflows/ci.yml` back to `pull_request`, `push` to `main` and
`workflow_dispatch`, and (if the Actions account lock persists) use
`CI_RUNNER` or `make ci-report` as in ADR 0014. Delete `.circleci/` when no PR
needs it. Nothing else depends on it.

## Status

Live since 2026-10-27. The project is connected through the CircleCI GitHub
App, with triggers for pull requests (opened and pushed) and for pushes to
`main`, and GitHub shows the checks as `ci/circleci: proto`,
`ci/circleci: go`, `ci/circleci: python-3.11` and `ci/circleci: python-3.13`.
The first runs found two problems, both fixed: buf v1.74.0 needs a newer Go
than go.mod's (the generator install now allows `GOTOOLCHAIN=auto`), and buf
breaking cannot clone a blobless checkout (`proto` checks out in full). The
Actions workflow is disabled and runs only by hand (Setup, step 5).

CircleCI reads this configuration from each branch, so a branch made before
it reached `main` gets no checks until `main` is merged into it.
