# README command verification

Checked on 2026-10-30. These are execution notes, not a claim that every
documented deployment or benchmark was rerun.

## Laptop: explicit local mode

- Executed the entire README local-mode shell block, including virtual
  environment creation, editable installation, `make mr`, Go and Python
  word count, output inspection, and `diff -r`. Exit status was zero;
  the outputs were identical and all nine counters matched.
- The setup GIFs render that actual command transcript. They are not a
  Docker deployment recording or a simulated successful terminal session.
  [Commands and captured output](assets/setup-terminal-provenance.json).
- Installed `python[dev]`, ran `make tools`, installed the documented
  `golangci-lint` version, and ran `make fmt` successfully.
- Shell syntax, Make target existence, linter version, both GIF variants,
  and capture provenance are covered by `python/tests/test_readme.py`.
- A missing `grpc_tools.protoc` during pre-push was reproduced as an
  interpreter/dependency mismatch. A Python 3.13 virtual environment in
  the original clone now has the dev dependencies; its protoc reports
  `libprotoc 35.1`. The README documents activation and an explicit
  `PYTHON="$PWD/.venv/bin/python" git push` invocation.

## CS544: read-only deployment checks

- SSH authentication, `docker info`, `docker compose version`, Compose
  configuration validation, and `bin/mr submit -h` succeeded on the VM.
- The VM checkout reported revision
  `6d82d1ea5840752e2609efa622507c61a736f522`, Docker 29.8.1 and Compose
  v5.5.1. This is an older deployed viewer, not the upcoming showcase UI.
- Opened the actual dashboard over an SSH forward and recorded eight
  dark/light capture pairs without submitting or stopping any job.
  [Per-capture timestamps and tools](assets/status-vm-provenance.json).
- `/status.json` returned 404 on that deployed revision. The GIF uses the
  deployed HTML page; it does not imply newer dashboard APIs are deployed.

## Still requiring an exclusive VM handoff

Two campaign runners were active, so no competing job or service changes
were started. These README commands are **not runtime-verified by this pass**:

- `make cluster-up`, `make upload-example`, and the quick-start
  `bin/mr submit` / `bin/mr cat` sequence on a fresh output directory.
- `make e2e`, `make monitoring-up`, and `make cluster-down`.
- `make bench SCENARIO=grep` (and `sort` / `zipf`), `make bench-codecs`,
  `make soak`, and `make perf`.
- The Linux full-suite quality gate and a successful pre-push run on the
  final committed tree. No hook or test was disabled to call this complete.

Full `make check` was also attempted locally. Missing per-worktree tools
were installed, and a changed quick-start heading that broke five existing
links was corrected. The final local run reached the Python suite:
1,083 passed, 11 skipped and 14 failed on macOS's Bash/GNU-tool differences
and refusal to create non-UTF-8 filenames. The Python suite's failing exit
means the overall quality gate did not pass. Linux validation remains
required before pushing. A syntax or configuration check alone is not an
end-to-end execution result.
