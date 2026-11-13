# A/B comparison: distsort with the system at 4b5a235 (old) and 8f0ddb9 (new)

- Both arms ran 8f0ddb9's bench harness (`E2_SETTINGS=normal REPEAT=1 bench/experiments.sh E2`, 1 GiB, M = 32, R = 4, four workers); the old arm's tree is 8f0ddb9 with `go/` and `deploy/` replaced by 4b5a235's, so its `machine.json` says dirty.
- Ten pairs, taking turns, the first arm swapped every pair, after one 64 MiB smoke run per arm (`smoke/`).
- The old arm runs without the coordinator's write-ahead log (4b5a235's Compose file has none); the new arm logs every job.
- `checksums.csv` is not evidence: `bench/experiments.sh` deletes each run's output after checking it, so every line hashes nothing (the SHA-256 of empty input). Output equality across the two arms was therefore not checked; both arms shuffled the same 1,084,682,711 bytes and every run SUCCEEDED.
