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

- Python and Go coordinators: LATE speculation (Zaharia et al., ADR 0019) as
  an alternative to the paper's backups, selected with `-speculation=late`
  on the Go coordinator; the Python coordinator reports task progress in
  heartbeats (#284).
- Job and task event logging in the coordinator and worker, with a
  `-log-level` flag (#302).
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
- `.gitignore` covers local secrets, scratch files and tool caches (#300).

### Removed

- The GitHub Actions workflow; CircleCI is the only CI, and a test fails if
  `.github/workflows/` comes back (#294).

### Fixed

- The CircleCI badge URL (#261).

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
