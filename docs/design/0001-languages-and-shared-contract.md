# ADR 0001 — Go primary, Python reference, one shared contract

**Status:** accepted · **Phase:** 0

## Context

The 2004 system was written in C++ and ran on Google's internal stack (GFS,
custom RPC, a cluster scheduler). We are rebuilding it as a real distributed
system — a coordinator and N workers communicating over a network — that can
run on a laptop (Docker Compose) or a cluster (Kubernetes).

## Decision

1. **Go is the primary implementation.** Goroutines and channels map directly
   onto the master's concurrent bookkeeping and the workers' parallel I/O;
   gRPC is first-class; binaries are single static files that containerise
   trivially. Go is also the language of MIT 6.824's MapReduce lab, which
   gives a well-known point of comparison.
2. **Python is a second, reference implementation.** It is easier to
   read, easier to write map/reduce functions in, and lets us demonstrate that
   the design is language-independent.
3. **Both implementations share one contract**, defined once in this repo:
   - gRPC/protobuf service definitions in `proto/` (the "wire" contract);
   - an on-disk record format in `docs/file-format.md` (the "data" contract);
   - a language-neutral partition hash (FNV-1a 64), so a key lands in the same
     reduce partition no matter which language produced it.

## Alternatives considered

- **C++ (as in the paper):** most faithful, but slow to develop and not a
  language the author works in; fidelity is about *behaviour*, not language.
- **Python only:** fastest to write, but the GIL and interpreter overhead make
  it a poor fit for the performance experiments in §5 of the paper.
- **Rust:** best raw performance, but development speed would limit how many
  paper features we can cover.
- **Two independent implementations with no shared contract:** simpler at
  first, but loses the strongest correctness check we have — running the same
  job through Go and Python and comparing outputs byte-for-byte.

## Consequences

- Any protocol or format change must be made in `proto/` or
  `docs/file-format.md` first, then in both languages.
- Cross-implementation ("interop") tests become a core part of CI (Phase 5).
- Mixed clusters become possible: a Go coordinator driving Python workers, or
  Go workers running Python map/reduce functions. Python has the API, the
  file format and local mode today; its coordinator, worker and UDF host are
  Phase 5 stretch work (#35–#39).
