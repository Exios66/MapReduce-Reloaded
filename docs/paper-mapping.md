# Paper → Code Mapping

Traceability from each feature of Dean & Ghemawat, *MapReduce: Simplified Data
Processing on Large Clusters* (OSDI 2004), to where it lives in this repo and
the test that proves it. Rows are filled in as phases land.

Status: `planned` → `in progress` → `done`.

| ID  | Feature | Paper § | Phase | Go | Python | Test | Status |
|-----|---------|---------|-------|----|--------|------|--------|
| F1  | Map/Reduce programming model | §2 | 1 | `go/pkg/api` | `mapreduce_reloaded/api.py` | | planned |
| F2  | Input splitting (16–64 MB) | §3.1 | 2 | | | | planned |
| F3  | Master + M map / R reduce tasks | §3.1 | 2 | | | | planned |
| F4  | Partition `hash(key) mod R`, spill to local disk | §3.1 | 2 | | | | planned |
| F5  | Remote fetch, sort, group, reduce | §3.1 | 2 | | | | planned |
| F6  | R output files | §3.1 | 2 | | | | planned |
| F7  | Master data structures | §3.2 | 2 | | | | planned |
| F8  | Worker failure + map re-execution | §3.3 | 3 | | | | planned |
| F9  | Master checkpoint / recovery | §3.3 | 3 | | | | planned |
| F10 | Atomic commits | §3.3 | 2 | | | | planned |
| F11 | Locality-aware scheduling | §3.4 | 4 | | | | planned |
| F12 | Task granularity (M, R ≫ workers) | §3.5 | 2 | | | | planned |
| F13 | Backup tasks for stragglers | §3.6 | 3 | | | | planned |
| F14 | Custom partitioning function | §4.1 | 2 | | | | planned |
| F15 | Sorted keys within a partition | §4.2 | 1 | | | | planned |
| F16 | Combiner | §4.3 | 1 | | | | planned |
| F17 | Pluggable input/output types | §4.4 | 1 | | | | planned |
| F18 | Atomic side-effect files | §4.5 | 3 | | | | planned |
| F19 | Skipping bad records | §4.6 | 3 | | | | planned |
| F20 | Local execution mode | §4.7 | 1 | | | | planned |
| F21 | HTTP status pages | §4.8 | 4 | | | | planned |
| F22 | Counters (de-duplicated) | §4.9 | 3 | | | | planned |
| F23 | Paper example programs | §2.3, §5 | 4 | | | | planned |
