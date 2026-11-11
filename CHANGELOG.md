# Changelog
<!-- pyml disable md024 -->

Every notable change to MapReduce-Reloaded, newest first. The format follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and versions
follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).
Numbers like (#227) are the pull requests that made each change.

While the version is 0.y.z, the minor version goes up for any release that
adds or changes behavior, and the patch version for a release that only
fixes bugs or changes docs. AGENTS.md says how to add an entry and how to
cut a release.

## [Unreleased]

### Added

- Bench: `make figures-check` (`scripts/check-figures.py`) checks the
  evaluation archives against their `SHA256SUMS`, redraws every figure in
  `bench/evaluation/` from them in a temporary directory, and compares each
  with the committed PNG pixel by pixel. It needs matplotlib, so it runs on
  the experiment VM with `PLOT_PYTHON`, not in `make check` (#NUM).
- Lint: `make md-lint` runs `scripts/check-dark-charts.py`, which fails if
  a Markdown page shows a bench chart without its dark variant. It runs in
  the lint job, not the Python tests, so a Markdown-only change still runs
  only the lint job (#349).
- Bench figures: E7 (`E7.png` and `E7-dark.png`): job time on workers of
  mixed speeds with no backups, the paper's backup rule and LATE, each
  run's ring beside its median, the backups each rule launched and won, and
  the time each saves in the title (#343, #349).
- Agent tooling: four skills in `.claude/skills/`, listed in AGENTS.md:
  `saving-ci-credits` (`preflight.sh` checks identity, CHANGELOG and
  `[skip ci]` before a push), `interpreting-bench-results` (`summarize.py`
  reports medians, ranges, slow and invalid runs, and overlaps),
  `analyzing-bench-results` (`compare.py` compares a run with a published
  one) and `checking-campaign-status` (`status.sh` snapshots a running
  campaign).
- Bench: E0 starts every run with only its own program's input in the page
  cache. Before each warm-up and measured run, `bench/experiments.sh` drops
  every other input in the output volume, the other two E0 inputs and any
  word count inputs E1 and E6 left, with the new `bench/drop-cache.py`
  (`posix_fadvise`, run in a container, no root needed) and reads the
  run's own input. `E0_ISOLATE=0` restores the old order. In the reruns at
  `8f0ddb9`, E0's sort runs read 0.75–1.84 GiB (803–1,971 MB) from disk,
  machine-wide, against about 0.1 GiB for the same job in E2–E4 (#347,
  #349).
- Bench: `bench/evaluation/faults.png` (and `-dark`), the job time each
  disturbance in E2, E3 and E4 added, on one axis; its title says every
  disturbed run gave an undisturbed run's output only when every one was
  valid. The README's results lead with it (#342).
- Notebook: `examples/demo.ipynb` shows the combiner (the same output with
  `-combiner=false`, and the bytes shuffled), the 1 GiB sort surviving a
  coordinator restart with output identical to the worker-kill run, and the
  evaluation figures (#342).
- Docs: `docs/reviewing.md`, a reviewer's path through the code: a reading
  order, where each part lives, how to check each claim, and the known
  limits (#342).
- Docs/scripts: `scripts/capture-status-gifs.py` regenerates the README
  status-page GIFs from one real job's whole run, in the page's dark and
  light variants, with a provenance sidecar (commit, machine, job, input,
  timings) and honest time-lapse support for minutes-long jobs (#279).
- Status page (Go and Python): `/status.json` serves the status as data,
  and the page updates itself every two seconds from an inline script, with
  a pause button and a notice when the coordinator stops answering. Without
  scripts it still reloads with `<noscript>`. ADR 0022 records the change
  (#312).
- Status page (Go and Python): job cards link to themselves (`#job-<id>`),
  show host- and zone-local map counts, and the workers table shows zone,
  slots in use and heartbeat age. Badges carry a symbol as well as a color,
  the tables scroll on narrow screens, and tests check that both pages share
  one stylesheet, load nothing external, and keep text at WCAG AA contrast
  (#311).
- Python and Go coordinators: LATE speculation (Zaharia et al., ADR 0019) as
  an alternative to the paper's backups, selected with `-speculation=late`
  on the Go coordinator; the Python coordinator reports task progress in
  heartbeats (#284).
- Job and task event logging in the coordinator and worker, with a
  `-log-level` flag (#302).
- Bench: `bench/experiments.sh` saves the cluster's logs, one file per
  setting, program and run, as `bench/results/EXPERIMENT/logs-SETTING-PROGRAM-N.log`,
  and a registration timeout saves `logs-registration-timeout.log` (#301).
- Bench: `bench/run.sh` saves what the coordinator and workers logged during
  the run as `cluster.log` in its results directory (#301).
- The demo notebook, `examples/demo.ipynb`, and a test that every cell
  compiles (#252); the five-minute demo script and README badges (#255).
- The soak run, `make soak`: 1 GiB with the combiner off at a 256 MB worker
  limit, with a bounded heap (#251).
- Python coordinator tests for backups, bad-record skipping and counters
  (#256); e2e checks of the image and the job label (#249), and of
  `e2e.sh` with fakes and the local-mode quick start (#250).
- Regression tests for documentation claims and guide navigation (#283).
- The MIT license and a security policy, `SECURITY.md` (#291).
- Docs: the language-choice report and the Docker topology guide (#290); the
  Python coordinator explainer and the VM specification's source (#289); the
  status page during a chaos run (#258); test helper docs (#264).
- AGENTS.md, imported by CLAUDE.md: rules for coding agents on one CI run
  per PR (#296), commits as the owner only (#297) and when to open a PR
  (#298).
- `CHANGELOG.md`, backfilled for v0.1.0 to v0.3.0, with the workflow for
  adding entries and cutting releases in AGENTS.md and a layout test,
  `python/tests/test_changelog.py` (#305).

### Changed

- Agent tooling: the `interpreting-bench-results` skill states which
  experiments report wall time, which commit the reported E0 ran at, how
  each experiment orders its runs, and that `valid` is the only record of
  an output check; its `summarize.py` also flags a group whose bytes
  shuffled differ between runs and a setting with fewer valid runs than the
  rest. `analyzing-bench-results` documents `make figures-check`;
  `saving-ci-credits` describes the three-node `go` job and cold cache runs,
  and its `preflight.sh` names the PR's likely number so the CHANGELOG can
  cite it before the PR opens, as `AGENTS.md` now allows. `scripts/README.md`
  lists `check-dark-charts.py`, `check-figures.py`, `ci-go-job.sh` and
  `install-tool.sh` (#NUM).
- CI: the `go` job runs on three nodes at once (`parallelism: 3`), still
  one `go` check: `scripts/ci-go-job.sh` gives the race tests, the coverage
  tests, and sh-lint, go-lint and py-e2e a node each, the same make targets
  as before (`make go-test` is now `go-test-race` then `go-test-cover`).
  `make tools` keeps a cached generator that `scripts/install-tool.sh`
  finds was built from its pin, so `proto` no longer rebuilds buf on every
  run. The full pipeline should drop from about 400 s to about 220 s with
  no check removed; see docs/ci.md, Pipeline time, and ADR 0024 (#354).
- Bench: E7 records its workers' CPU limits in `cpus.txt`, which
  `bench/plot-experiments.py` names in the E7 subtitle; `DROP_CACHES=1`
  turns E0's input isolation off by default, and E0 refuses an explicit
  `E0_ISOLATE=1` with it, which the cache drop would undo;
  `bench/drop-cache.py` flushes each input before dropping it. The
  QUALITY tables label their columns `bk med` and `won sum` (#349).
- Bench: E3 (`bench/experiments.sh E3`) takes its kill times from
  `E3_CALIBRATE` runs (default 3) of a separate `calibrate` setting, which
  the figures leave out, then runs `normal`, `kill-25`, `kill-50` and
  `kill-75` in turn within each repeat, reversed on even repeats, as E2 and
  E7 do. Before, it ran each setting as a block, so drift in the machine
  could fall on one setting (#282, #348).
- Bench: E4 (`bench/experiments.sh E4`) does the same: `E4_CALIBRATE`
  runs (default 3) of a `calibrate` setting set the restart time, then
  `normal` and `restart` take turns. Every E4 run now starts with a fresh
  write-ahead log, so each restart replays only its own job instead of
  every job before it. Its restart cost is therefore a new measurement, not
  comparable with the published 1.6 s, which replayed three to five earlier
  jobs (#282, #348).
- Bench: E0 (`bench/experiments.sh E0`) first runs each program
  `E0_WARMUP` times (default 1) as a `warmup` setting, which the E0 figure
  leaves out, so no measured run is the first to read its input. Then all
  nine program and worker-count settings take turns within each repeat,
  reversed on even repeats; before, each ran as a block (#282, #348).
- Bench figures: E0 draws a run as a cross instead of a ring when the
  machine read at least a quarter of the input more from disk during it
  than during the median run of its program and worker count. The caption
  states only that measurement, not a cause: the counter in `cache.jsonl`
  counts the whole machine's reads, and sort's re-reads under the worker
  memory limit are one cause besides a cold input (#347, #349).
- Bench figures: one look across all of them. The fault-tolerance summary
  (`faults.png`) groups its bars by experiment, as each has its own
  undisturbed baseline, gives each bar its range, and colors backups on
  and off as E2 does instead of hatching. E0's legend is a row above the
  plot; E0, E1 and E6 draw each run's ring beside its median instead of
  behind it; E1 and E6's log axes drop their minor ticks. E2's job-time
  bars set the no-straggler runs apart with a gap instead of hatching, bars
  are thinner, text is at least 9 points and figures at most 10 inches wide
  so they stay legible scaled into a page, and the fault summary says which
  clock each experiment uses. Every figure is set in Inter, shipped in
  `bench/fonts/` under the SIL Open Font License, so they look the same
  whichever machine draws them (#349).
- Docs: every result in `README.md`, `bench/README.md` (now "Evaluation
  (E0–E7)"), `docs/evaluation.md`, `docs/report.md`, `docs/demo.md`,
  `design.md`, `docs/language-choice.md`, `docs/reviewing.md` and the demo
  notebook comes from the rerun at `8f0ddb9`: five runs per setting, taken
  in turn. E7's results replace "not yet run", the README and notebook show
  the E5 and E7 figures, and `docs/evaluation.md` records the isolated E0
  rerun and the worker memory limit's share of sort's disk reads (#343,
  #344, #346, #347, #349).
- Bench results: every figure in `bench/evaluation/` is drawn from the
  rerun at `8f0ddb9` in `bench/evaluation/rerun-8f0ddb9/`: E0–E7 at one
  commit, five interleaved repeats each, with their archives and harness
  logs. E0 comes from `E0-isolated/`, rerun so each program runs with only
  its own input in the page cache; `bench/plot-experiments.py` reads it in
  place of the first E0, which a `NOTE.md` marks superseded. The E0–E6
  results published before move to
  `bench/evaluation/superseded/before-8f0ddb9/` (#344, #347, #349).
- Bench figures: `bench/plot-rates.py OUT.png` also writes `OUT-dark.png`,
  and `docs/evaluation.md` shows the dark variant of the grep and sort rate
  plots and of E3. A test checks that every chart a page shows has a dark
  variant (#349).
- Bench figures: every title states its finding in one short line,
  computed from the CSVs (for example "E2: backups cut a straggler's job
  from 17.6 s to 11.2 s"), and every figure uses one scheme: blue is the system
  as it ships, orange the changed or disturbed setting. E0 draws each run
  as a ring instead of a range bar that one cold grep run stretched to
  0.35×, and names that run (#342).
- Bench figures: E1 and E6 are dumbbells instead of grouped bars, one row
  per group joining the combiner-on and combiner-off medians, with bytes on
  a log axis so a row's length is the factor saved; E3 draws each kill point
  as a stem from the no-failure baseline instead of a line joining separate
  settings. `bench/README.md` gives the reason for each figure's chart type
  (#342).
- Docs: `docs/demo.md` restarts the coordinator live, shows the combiner,
  maps each moment to the requirement it meets, and adds a checklist for
  the day and answers for questions; `docs/report.md` gains a "Process and
  review" section and follows the new demo (#342).
- Docs: the README's status-page GIFs (`docs/assets/status-*.gif`) show one
  whole 1 GiB word count, M = 32, R = 4, on four workers, from its first map
  task to the end state, replacing the 8-second seeded chaos loop; a
  provenance sidecar next to them records the commit, machine, job, input
  and capture settings, and the caption describes the recording honestly as
  a time-lapse (#277).
- CI: CircleCI runs only the jobs a PR implicates, through a setup config
  and a continuation config (#288, ADR 0020), selected by our own script
  with the mapping gaps closed (#293). Pushes to `main` run no jobs (#295,
  ADR 0021). Markdown lint runs in CI (#285), and the PR template requires
  green checks on the latest commit (#286). `docs/ci.md` records the single
  CircleCI trigger (#299).
- Evaluation graphs: E2-comparison regenerated with a disclosure and a dark
  variant; E5 labels, rate-plot titles and a runbook path fixed (#292).
- Docs: the README streamlined for a reviewer (#263), the status page and
  its README GIFs modernized (#262), coverage numbers refreshed (#257), and
  corpus and dataset descriptions corrected (#253).
- The demo notebook runs from any directory, with a job file and a worker
  kill step (#303).
- Docs: the chaos explainer says what `make chaos` covers and that dropped
  packets are out of scope (#287).
- Go coverage: each package's coverage counts every test in the module
  and the runs of the commands that `internal/e2e` builds, through
  `-coverpkg` and `GOCOVERDIR` in `make go-test` (#245).
- Docs: a stale-claim sweep. CI is described as six jobs with no run on
  pushes to `main`, the pull-request count and coverage floors are current,
  LATE speculation cites #284, and E0 to E7 ranges are corrected.
- Bench: each trial records the page cache's state in `cache.jsonl`, an
  optional `DROP_CACHES=1` makes trials cold, and `begin` says which older
  results it deletes (#310).
- `.gitignore` covers local secrets, scratch files and tool caches (#300).

### Removed

- The generated coverage counters and profiles under `go/.cover/` are no
  longer tracked in the repository; `make go-test` regenerates them locally
  and `.gitignore` already keeps the directory ignored (#331).
- The GitHub Actions workflow; CircleCI is the only CI, and a test fails if
  `.github/workflows/` comes back (#294).

### Fixed

- Docs: the E0-E7 rerun write-ups name `3a907de` wherever they say every
  experiment ran at `8f0ddb9`, say E5's settings rotate rather than reverse,
  compare E0's sort with E2-E4 on one clock, describe E3's cost and E1's
  disk reads as measured, and say that `runs.csv`'s `valid` column, not a
  stored checksum, records each output check (`README.md`, `design.md`,
  `docs/evaluation.md`, `bench/README.md`) (#355).
- Bench figures: the fault summary keeps zero in view when every
  disturbance sped its job up, and E7's title claims a cut only when no
  backup rule was slower (#349).
- Tests: `TestClusterReregistersOnHeartbeat` uses a 1 s worker timeout, so
  a heartbeat delayed on a loaded CI runner no longer costs the worker a
  third registration (#349).
- CI: `.circleci/mapping.txt` maps `.claude/` to the lint job, so the
  agent skills' Python scripts no longer fail `mapping-coverage` and
  `make check` with "unmapped file" (#352).
- CI: `make py-lint` and `make fmt` run ruff on the agent skills' Python
  scripts under `.claude/` too, and `.circleci/mapping.txt` maps a change
  to them to the Python jobs, which run `py-lint`. Before, a syntax error
  in one of them passed CI, as #351 reports (#352).
- Go coordinator: deleting a job's `_temporary` directory no longer gives
  up after 100 quick retries while attempts keep creating files in it. It
  now retries for up to 10 seconds, pausing a millisecond between tries
  after the first ten. On a loaded CI machine the old limit made
  `TestLocalDeleteAllConcurrentCreates` fail now and then (#352).
- Go coordinator: a succeeded job's output no longer keeps a stray
  `_temporary` directory when a losing backup reduce attempt creates its
  file while the job is cleaning up. The local file system's delete now
  retries until the directory is gone, after which no attempt can create
  it again (#345, #348).
- Tooling: `make go-lint`, and so `make check` and the pre-push hook, no
  longer fail when the `golangci-lint` on `PATH` was built by an older Go
  than `go/go.mod` needs. `scripts/golangci-lint.sh` uses the pinned version
  from `PATH` only if a new enough Go built it, and otherwise builds it into
  `.tools/bin` once, as CI does. The pre-push hook also finds Go and the
  tools in their usual install directories when a git client runs it
  without a login shell's `PATH` (#342).
- Notebook: the worker-kill cell no longer reads an unset reply when the
  kill fails; it stops the client and restores the worker (#342).
- Notebook: the setup cell removes the outputs and corpus that an earlier,
  interrupted run left in `data/out` and `data/input`, so retries after a
  failing cell no longer fill the disk (#342).
- Bench figures: E0's title names the largest worker count measured instead
  of always "four"; E1 and E6 label a combiner that costs time "slower", not
  "faster"; and `faults.png` draws a disturbance that sped a job up left of
  zero instead of clipping it (#342).

<!-- pyml disable md033 -->
<!-- coderabbit-ai-generated:v1 -->

<!-- coderabbit-ai-generated-section:v1:799b81130e48f9cd94b0adbbd0a0a99101cebc306996bb54bbae62494551351d -->

- Docs: `python/README.md` explains that `-keep-jobs=010` means 10 in
  Python and 8 in Go.

<!-- /coderabbit-ai-generated:v1 -->
<!-- pyml enable md033 -->

- Docs: a second stale-claim sweep of the numbers. `bench/README.md` gave the
  E6 crawled-English bytes ratio as 5.2% (it is 19.3%; 5.2 is the factor);
  `docs/testing.md` now has today's coverage figures and mutation scores
  (`tasktable.go` 91.9%, `task.go` 91.2%, down from 96.3% and 93.2% as
  LATE speculation and progress reports were added) and says that a Go
  package's coverage counts every test in the module and the e2e commands;
  `docs/report.md` counts 22 ADRs and 198 merged pull requests;
  `docs/evaluation.md` says E0 to E6 are the course evaluation; `design.md`
  no longer promises items marked "planned" (#341).
- Docs: commands and lists that no longer matched the code. `make bench`
  also takes `SCENARIO=book`; the Python coordinator has no `-log-level`;
  `scripts/README.md`, `go/README.md` and the roadmap's layout list the
  scripts and Go packages they omitted; the capture-GIF commands in
  `docs/demo.md` create the input directory; `README.md` shows what
  `mr submit -wait` prints and counts Phase 7 as done; `AGENTS.md`'s heading
  levels pass `make md-lint` again (#341).
- Build: `make` uses `python/.venv` when it exists, `make venv` creates it
  with the package and every dev tool, and `scripts/gen-proto.sh` says how to
  fix a Python that lacks `grpcio-tools`, instead of failing in
  `make check` on a system `python3` without the dev tools. `make ci-report`
  builds the clean copy's own venv when `python3` lacks them (#335).
- Go tests: the cluster tests that run a whole job (`runJob`) give workers a
  5 s timeout, not 100 ms, so a late heartbeat on a loaded CI runner no
  longer makes a worker re-register and fail `TestClusterWorkerMetrics`
  (#310).
- CI: the repository's second CircleCI project, made by the GitHub App, no
  longer runs the suite. Both projects built every push, doubling credit use
  and posting statuses that could disagree; the setup job now continues
  with the empty config when `pipeline.project.type` is `circleci`, so only
  `gh/Exios66/MapReduce-Reloaded` runs it (#318).
- CI: a push to `main` no longer ends in a config error. The continuation
  orb's `finish` command sends `version: 2`, which CircleCI no longer
  accepts, so `.circleci/config.yml` continues with the empty 2.1
  `.circleci/empty-continue-config.yml` instead (#313).
- The CircleCI badge URL (#261).
- Worker: a starting worker's sweep could delete a new worker's temporary
  directory, which made it fail with "already locked" (and flaked
  `internal/e2e`); the lock is now on the directory itself.
- Bench: `wait_workers` retries when the coordinator is unreachable, instead of
  exiting silently, and saves the logs on a registration timeout (#307).

## [0.3.0] - 2026-10-29

The Python coordinator reaches parity with Go: gRPC service, write-ahead log
and status page. Both coordinators survive log failures, and real datasets
and experiment E6 join the evaluation.

### Added

- Python coordinator: the gRPC service (#228), the write-ahead log and
  recovery (#229), and the status page, `/metrics` and command (#240).
- Python `split.py`: `plan_m` and `allocate`, as in `go/internal/split`
  (#227).
- Go coordinator: lock `-data-dir` against a second coordinator (#241).
- Real datasets for the example programs, fetched by `make real-data` and
  checked against an independent oracle (#246).
- The corpus gains *Die Verwandlung*, German in UTF-8 with LF line endings
  (#248).
- Experiment E6: the combiner on real text at 1 GiB (#247).
- Lint: shellcheck on every shell script (#243), and a check of every
  Markdown link and anchor (#244).
- CodeRabbit and CodeAnt reviewer configuration (#239).

### Changed

- CI: the go job runs the Python coordinator with the Go commands (#242).
- Docs brought up to date with the code (#231).

### Fixed

- Both coordinators stop while their log fails, and heal from the log
  (Python #235, Go #234).
- A coordinator served without a log gives each run its own IDs (Python
  #237, Go #236).
- Worker: survive a report for an unknown job; `TestCache` isolated (#233).
- Python matches Go on hidden inputs, split limits, open files and
  `termvector` (#232).
- `TestClusterMapOutputMatchesLocal` deflaked (#230).

## [0.2.0] - 2026-10-28

The paper's full system: the Compose cluster, backup tasks, locality, every
example program from the paper, monitoring, S3 storage, compression, job
files, the evaluation against §5, and the final report.

### Added

- Cluster: the Docker Compose cluster and e2e test (#83); the host client
  with `/data` shared storage, `mr upload` and `mr cat`, and an exact `-m`
  (#188); workers limited to one CPU each (#164).
- Scheduling: backup attempts of a phase's last tasks (#93), the
  longest-running task backed up first (#133); map tasks preferred near
  their split, with delay scheduling and a locality acceptance test (#102,
  #104), and host- and zone-local counts (#146).
- Fault handling: fail a job that would skip more records than its limit
  (#92), or after a fatal task failure or 8 failures of one task (#198);
  the write-ahead log on by default in Compose, compacted to the kept jobs
  (#189).
- Counters: byte and reduce-input counters whose totals match local mode
  under backups (#94); each job's duration from the coordinator (#132).
- Programs: `-param` job parameters (#109); distributed grep (#110), URL
  access frequency and the reverse web-link graph (#111), distributed sort
  (#112), the inverted index with `Context.Input` (#113) and term vector per
  host (#114); golden outputs for every example, Go and Python byte for
  byte (#203).
- Job files: `mr submit` of user Go job files interpreted by the workers
  (#171, ADR 0015), with compiled helpers `api.Words` and `api.Sum` (#199).
- `mr` flags: `-combiner=false` (#170) and `-input-format` (#142).
- Monitoring: the status page and `/metrics` (#106, #107), worker metrics
  with `-http` (#116, #139), a shared Prometheus text format (#115), attempt
  logs tagged with job and task and `-log-format json` (#117), and
  Prometheus and Grafana behind a monitoring profile (#118, #144).
- Storage: a storage backend interface (#125), an S3 backend (#126) that
  keeps late attempts out of S3 output (#127, ADR 0013), an S3 store in the
  Compose `s3` profile with `make e2e-s3` (#128), and one storage contract
  for every backend (#207).
- Compression: zstd and Snappy blocks in MRKV (#129) and per-job
  intermediate and output codecs (#130).
- Benchmarks: the codec benchmark (#131); grep, sort, Zipf, web-log and
  link-graph generators (#134, #148); `make bench` scenarios with rates over
  time (#140, #141, #143); benchmarks, memory and compression limits and
  `make perf` (#210).
- Evaluation: E0–E4 results from the course VM (#184), E1 (#170), E5
  compiled against job-file word count (#197), and the evaluation against
  the paper's §5 (#224), shown in the README with light and dark charts
  (#187, #192).
- Testing: the seeded chaos test in process (#101, #103, #105) and against the
  Compose cluster (#205), over every example and job option (#209), with
  storage faults and corrupted map output (#218); fuzzing, `make fuzz`
  (#202); a multi-process e2e test in `make check` (#206); a model-based
  test of the coordinator's RPCs with restarts (#208); goroutine leak
  checks and shuffled test order (#194); coverage floors per package (#211);
  mutation testing in Go (#220, #221) and Python (#222); command and
  deployment contract tests (#193, #196); cluster-vs-local checks for every
  example (#147); CodeRabbit's generated tests (#213, #217).
- Python coordinator: the task table and worker registry (#226).
- Docs: a checked paper-to-code table (#108); design walkthrough and ADRs
  0008–0012 (#122); the quick start, architecture and directory READMEs
  (#149); the assignment's design questions (#166) in `design.md` at the
  root (#168); the VM runbook (#169); `docs/testing.md` (#211); the final
  report (#225); GitHub templates (#215).
- CI: the quality gate on CircleCI Free (#216), after checks ran again on
  every PR (#165); ADR 0014 on locked GitHub Actions billing (#201).

### Changed

- Job input and output go through the storage backend (#125).
- Workers delete ended jobs' map output and bound disk, memory and logs
  (#172), delete dead workers' directories (#186), and keep disk room for
  each other in Compose (#204).
- A job's output is cleaned up only once the log holds its end (#191).
- The roadmap, proto comments and docs describe what was built (#120, #124,
  #136, #138, #223).

### Removed

- The `mr` binary is no longer tracked in git (#119).

### Fixed

- Lost and forgotten attempts stopped through the heartbeat reply (#123).
- A failed map attempt leaves none of its partitions (#200).
- Panics in gRPC handlers are recovered instead of stopping the process
  (#190).
- The coordinator rejects a task report with no outcome (#137).
- MRKV rejects S2 extensions in Snappy blocks (#135).
- Examples: a host ends at `?` or `#`, files in a set are deduped, and
  non-UTF-8 names are accepted (#145).
- Review findings applied (#84, #100, #195).

### Security

- CodeRabbit security findings F-001 to F-009 patched (#214).

## [0.1.0] - 2026-10-23

The first working MapReduce: the job API, file formats, external sort,
combiner, local mode, and a coordinator with workers that run map and
reduce tasks over gRPC and detect failed workers.

### Added

- Process: roadmap with project context (#61), a local pre-push quality
  gate in place of automatic GitHub Actions runs (#62), VM setup (#65).
- Design: ADR 0003 on the RPC contract (#63).
- Foundations: the file format (#64), the partition hash (#66), the job API
  (#67), the MRKV key-value format (#68), input and output formats (#69).
- Execution: external sort (#70), the combiner (#71) and local mode (#72).
- Distribution: input splits (#73), the task table and worker registry
  (#74), task execution (#75), wire conversions (#76), the coordinator
  server, workers and CLI (#77), map output that matches local mode (#78),
  and the shuffle server (#79).
- Fault tolerance: worker failure detection (#80).

### Fixed

- Review and CodeAnt findings (#81, #82).

[Unreleased]: https://github.com/Exios66/MapReduce-Reloaded/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/Exios66/MapReduce-Reloaded/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Exios66/MapReduce-Reloaded/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Exios66/MapReduce-Reloaded/releases/tag/v0.1.0
