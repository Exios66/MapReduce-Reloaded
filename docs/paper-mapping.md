# Paper → Code Mapping

Traceability from each feature of Dean & Ghemawat, *MapReduce: Simplified Data
Processing on Large Clusters* (OSDI 2004), to where it lives in this repo and
the test that proves it. Rows are filled in as phases land. The Python
column lists the reference implementation and local mode. Python also has a
coordinator server that `make py-e2e` runs with Go workers (ADR 0001,
[explainer 25](explainers/25-python-coordinator.md)); it implements the
master's side of several coordinator rows, the F21 status page among them,
but no Python worker runs tasks, so a dash marks a feature with no Python
implementation outside that coordinator. A Python worker and a Python
user-function host are stretch work (#37, #38). `TestPaperMapping` checks
that every path and test named here exists. Where we chose differently from
the paper, the notes below the table say how.

Status: `planned` → `in progress` → `done`.

| ID  | Feature | Paper § | Phase | Go | Python | Test | Status |
|-----|---------|---------|-------|----|--------|------|--------|
| F1  | Map/Reduce programming model | §2 | 1 | `go/pkg/api` | `mapreduce_reloaded/api.py` | `Example_wordCount`, `TestWordCountGolden`, `test_api.py`, `test_wordcount_golden` | done |
| F2  | Input splitting (16–64 MB) | §3.1 | 2 | `go/internal/split` | `mapreduce_reloaded/split.py` | `TestPlanCoversInput`, `test_split.py` | done |
| F3  | Master + M map / R reduce tasks | §3.1 | 2 | `go/internal/coordinator`, `go/internal/worker` | — | `TestClusterWordCount`, `TestRequestTask`, `TestReportTask` | done |
| F4  | Partition `hash(key) mod R`, spill to local disk | §3.1 | 2 | `go/internal/partition`, `go/internal/task` | `mapreduce_reloaded/partition` | `TestVectors`, `test_partition_hash.py`, `TestClusterMapOutputMatchesLocal` | done |
| F5  | Remote fetch, merge, group, reduce | §3.1 | 2 | `go/internal/shuffle`, `go/internal/task` | — | `TestFetch`, `TestFetchResumes`, `TestClusterWordCount` | done |
| F6  | R output files | §3.1 | 2 | `go/internal/task` | `mapreduce_reloaded/local.py` | `TestWordCountGolden`, `test_wordcount_golden` | done |
| F7  | Master data structures | §3.2 | 2 | `go/internal/coordinator` | — | `TestInvariants`, `TestResetWorker` | done |
| F8  | Worker failure + map re-execution | §3.3 | 3 | `go/internal/coordinator`, `go/internal/worker` | — | `TestExpire`, `TestClusterSurvivesWorkerLoss`, `TestChaos` | done |
| F9  | Master checkpoint / recovery | §3.3 | 3 | `go/internal/wal`, `go/internal/coordinator` | — | `TestRecover`, `TestClusterSurvivesCoordinatorRestart`, `TestChaos` | done |
| F10 | Atomic commits | §3.3 | 2 | `go/internal/task`, `go/internal/storage`, `go/internal/coordinator` | `mapreduce_reloaded/local.py` | `TestFailedAttemptLeavesNoFiles`, `TestFirstCompletionWins`, `TestDuplicateAttemptsS3`, `test_failed_reduce_leaves_no_part` | done |
| F11 | Locality-aware scheduling | §3.4 | 4 | `go/internal/coordinator` | — | `TestLocality`, `TestLocalityWait`, `TestClusterLocality` | done |
| F12 | Task granularity (M, R ≫ workers) | §3.5 | 2 | `go/internal/split`, `go/internal/coordinator` | `mapreduce_reloaded/split.py` | `TestSizeFor`, `TestSubmitJob` | done |
| F13 | Backup tasks for stragglers | §3.6 | 3 | `go/internal/coordinator` | — | `TestBackups`, `TestBackupOldestFirst`, `TestRecoverBackup`, `TestClusterBackupTasks`, `TestChaos` | done |
| F14 | Custom partitioning function | §4.1 | 2 | `go/pkg/api`, `go/internal/task` | `mapreduce_reloaded/api.py` | `TestHashPartitioner`, `TestCustomPartitioner` | done |
| F15 | Sorted keys within a partition | §4.2 | 1 | `go/internal/extsort` | `mapreduce_reloaded/extsort.py` | `TestSort`, `TestGroups`, `test_extsort.py` | done |
| F16 | Combiner | §4.3 | 1 | `go/internal/combine` | `mapreduce_reloaded/combine.py` | `TestCombinerPreservesOutput`, `test_combine.py` | done |
| F17 | Pluggable input/output types | §4.4 | 1 | `go/internal/formats` | `mapreduce_reloaded/formats` | `FuzzTextSplits`, `test_formats.py` | done |
| F18 | Atomic side-effect files | §4.5 | 3 | `go/internal/task` | `mapreduce_reloaded/local.py` | `TestSideOutput`, `test_side_output` | done |
| F19 | Skipping bad records | §4.6 | 3 | `go/internal/task`, `go/internal/coordinator` | — | `TestBadRecords`, `TestFailWithCrash`, `TestClusterSkipsBadRecords` | done |
| F20 | Local execution mode | §4.7 | 1 | `go/internal/local`, `go/cmd/mr` | `mapreduce_reloaded/local.py`, `mapreduce_reloaded/__main__.py` | `TestWordCountGolden`, `test_local.py` | done |
| F21 | HTTP status pages | §4.8 | 4 | `go/internal/statusweb`, `go/cmd/coordinator` | — | `TestStatus`, `TestPage`, `TestMetrics`, `TestClusterReportsProgress`, `scripts/e2e.sh` | done |
| F22 | Counters (de-duplicated) | §4.9 | 3 | `go/internal/coordinator`, `go/internal/task` | `mapreduce_reloaded/local.py` | `TestClusterCountersMatchLocal`, `test_wordcount_golden`, `TestChaos` | done |
| F23 | Paper example programs | §2.3 | 4 | `go/examples` | `mapreduce_reloaded/examples` | `TestWordCountGolden`, `TestGrep`, `TestURLFreq`, `TestReverseLinks`, `TestSort`, `TestInvertedIndex`, `TestTermVector`, `test_wordcount_golden`, `test_grep`, `test_urlfreq`, `test_reverselinks`, `test_distsort`, `test_invertedindex`, `test_termvector` | done |
| F24 | Performance experiments (grep, sort, backups, killed workers) | §5 | 7 | — | — | `bench/run.sh`, `bench/experiments.sh`, `docs/evaluation.md`, `test_input_is_accessible_to_nonroot_workers`, `test_e2_draws_four_settings_in_two_colors` | done |

## Notes

- **F5.** Each map output partition is already sorted, so a reducer merges
  its inputs (`extsort.Merge`) instead of sorting them again.
- **F9.** There is no periodic checkpoint. With `-data-dir`, the
  coordinator logs each decision before it replies, and a restarted
  coordinator replays the log, so the job survives instead of being
  aborted (ADR 0007). Without it there is no log, and a restart loses the
  job.
- **F8, F21.** Workers' heartbeats (§3.3) also carry each running attempt's
  `fraction_done`: bytes of the split read for a map task, bytes of the map
  outputs read for a reduce task. The coordinator shows it on the status
  page (§4.8). The paper does not specify per-task fractions. The
  coordinator also uses them for LATE speculation (Zaharia et al., OSDI
  2008), an opt-in mode with `-speculation=late` that is not in the paper
  and has no F row; F13 is the paper's rule, unchanged (PR #284, issue #45; ADR 0009,
  0010, 0019).
- **F11.** The scheduler ranks tasks by host, then zone, and can wait for a
  local worker (`Config.Locate`, `LocalityWait`). Nothing in the shipped
  binaries labels splits yet: Compose workers share one volume, so there is
  no locality to exploit until a store that knows where data lives (S3 shipped in #41, but no `Locate` source is wired).
- **F24.** The benchmark harness is #57 and the §5 experiments #58; [evaluation](evaluation.md) compares each with the paper.
