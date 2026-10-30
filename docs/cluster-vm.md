# Cluster VM: where the distributed cluster runs

The distributed cluster (#21 onward) runs on one Linux VM with **4 vCPUs,
8 GB of RAM and a 35 GB disk**, the VM the course provides. Every component runs as a Docker
container on that VM: the coordinator, the workers and an S3-compatible store. The containers talk to each other
over a Docker network exactly as they would across machines, so the code
does not know or care that it is one host.

This page covers the VM's specifications, the CPU, memory and disk budgets,
one-time setup, and daily use. The [runbook](runbook.md) is the short
version: from SSH to a full demo, in order.

This page is the single source of truth for the VM's specification. Other
pages that state it for planning ([design](../design.md), the
[roadmap](roadmap.md), the [report](report.md), the
[evaluation](evaluation.md)) link here; when a figure differs, this page is
right.

## The VM

Measured on the course VM on 2026-10-26 (`lscpu`, `/proc/meminfo`,
`lsblk`, `df`):

| | |
|---|---|
| CPU | 4 vCPUs, Intel Xeon Silver 4516Y+, 1 thread per core, KVM guest |
| Memory | 8 GB: `MemTotal` 8,130,756 kB (7,940 MB), no swap |
| Disk | 35 GB (`/dev/sda`), with a 33 GB root filesystem (`/dev/sda1`) holding `$HOME`, Docker and job data |
| OS | Ubuntu 24.04.4 LTS, kernel 6.8.0-139-generic, x86_64 |
| Docker | Engine 29.8.1, Compose 5.5.1; the user is in the `docker` group |
| Tools | git 2.43.0, Python 3.12.3; Go 1.26.0 is installed by `scripts/vm-setup.sh --install`; `make` and `gcc` are not installed by default (`sudo apt-get install -y make gcc`) |
| Network | Inter-worker traffic runs over the host-local Docker network (service-name DNS); it was never measured, and the paper's gigabit figure must not be imported for it. |

The budgets below are planned against these totals. The VM is shared with
other coursework, so what is free at any moment is less;
`scripts/vm-setup.sh` reports it at the start of each session.

Re-verify at freeze: before the project is submitted, run the commands above
again on the course VM and update this table and its date. The other pages
link here and need no change.

## CPU budget

The assignment asks for four worker containers, each limited to one CPU, so
that a job runs faster when its tasks are spread over all four. Compose sets
`cpus: 1` on the `worker` service, which Docker enforces as a CFS quota of one
CPU per container.

| Service | CPU limit | Why |
|---|---|---|
| worker (× 4) | 1 CPU each | One worker per vCPU: a job that keeps all four busy uses the whole VM, and one that runs on fewer workers is measurably slower |
| coordinator | none | It only schedules and stores locations (§3.2), and it is idle most of the time |
| mr, s3, prometheus, grafana | none | Short-lived or optional; the benchmarks record whether they ran |

- A worker runs one task at a time, so one CPU per worker means one task per
  CPU. Go's runtime reads the quota (`GOMAXPROCS` follows the container's CPU
  limit since Go 1.25), so a worker does not start more threads than it can
  run.
- Scaling past four workers (`--scale worker=N`) oversubscribes the VM.
  Benchmarks that compare 1, 2 and 4 workers stay within it.

## Memory budget

The VM has 8 GB of RAM and also runs other coursework, so the cluster
keeps well under it. Each service gets a hard memory limit (`mem_limit` in
[`deploy/compose/compose.yaml`](../deploy/compose/compose.yaml)). A container that exceeds its limit is killed instead of
slowing down the whole VM.

| Service | Limit | Why this much |
|---|---|---|
| coordinator | 256 MB | Holds only task state and partition locations, never data (§3.2) |
| worker (× 4) | 768 MB each | 256 MB sort buffer (#10 spills beyond that), the rest for the Go runtime, gRPC and file buffers |
| mr (on demand) | 128 MB | Submits jobs and polls status; workers process the data |
| s3 (#41, profile `s3`) | 256 MB | [VersityGW](https://github.com/versity/versitygw), an S3-compatible store for input and output |
| prometheus, grafana (profile `monitoring`) | 256 MB each | Optional metrics and dashboards |
| **Core: coordinator + 4 workers** | **3,328 MB (≈ 3.3 GB)** | Always running |
| **Core + mr + s3** | **3,712 MB (≈ 3.7 GB)** | A job run against the S3 store |
| **Everything, including monitoring** | **4,224 MB (≈ 4.2 GB)** | Leaves about 3.8 GB of the 8 GB for the OS, Docker and anything else |

These are ceilings: a container may use less. The optional `s3` and
`monitoring` profiles both fit, but with all of them running only about half
of the 8 GB is left, so close other memory-hungry work before a benchmark.
The kernel reports a little under 8 GB as total, and the OS and the Docker
daemon take some of the remainder.

- Scaling up (`docker compose up --scale worker=N`) costs 768 MB and one CPU
  per worker. Each extra worker eats into the 8 GB, and the 4 vCPUs already make
  four the sensible ceiling (see the CPU budget above).
- The task-level numbers (sort buffer size, how many tasks a worker runs at
  once) are set in #10 and #16 to stay inside these limits.

## Disk budget

The disk is 35 GB, with a 33 GB root filesystem shared by the OS, other
coursework, Docker images, the Go toolchain, the Python venv, and job data.
This project's own share is about 2.4 GB that stays (2 GB of images, of
which Grafana is 960 MB, and 0.4 GB for Go and the venv), plus up to 3 GB
of job data while a 1 GiB experiment runs. `scripts/vm-setup.sh` warns
when less than 5 GB is free in `$HOME`. A job's data exists several times at
once: input, intermediate files (about the input's size; the `distsort`
benchmark shuffled 68 MB for a 64 MB input) and output. The experiments
(E0–E7) use 1 GiB inputs; keep any larger input to a few GB at most, and
delete old output and `bench/results/` files as you go.

Workers delete a job's map output once it ends and stop taking tasks under
2 GiB free (`-min-free-mib 2048` in Compose; the worker's default is 1 GiB); logs rotate and Prometheus has a retention target
([ADR 0017](design/0017-disk-and-memory-bounds.md)). `make clean-output`
requires explicit completed directories, e.g.
`make clean-output CLEAN_OUTPUTS='sort-kill-123'`, and prints `df` and
`docker system df`. Keep active job outputs unselected.

## One-time setup

### 1. On your laptop: an SSH alias

Add this to `~/.ssh/config` so every command below can say `mr-vm` instead of
the full host name. Fill in your own values: the real host and user name stay
out of this public repository.

```text
Host mr-vm
    HostName <your-vm-hostname>
    User <your-username>
```

Check it with `ssh mr-vm hostname`.

### 2. On the VM: clone the repo and run the setup script

```sh
ssh mr-vm
sudo apt-get install -y make gcc   # not on the course VM by default; gcc is for go test -race
git clone https://github.com/Exios66/MapReduce-Reloaded.git
cd MapReduce-Reloaded
scripts/vm-setup.sh            # check only: reports what is missing, changes nothing
scripts/vm-setup.sh --install  # installs what is missing, all inside $HOME
```

What `--install` does, and what it does not do:

- **Go:** it downloads the version named in `go/go.mod` from go.dev and
  checks the download's SHA-256 against go.dev's published list. Then it
  unpacks it to `~/.local/go`. It prints the `PATH` line for you to add to
  `~/.bashrc`; it does not edit your shell files.
- **Python:** it creates a virtual environment at `python/.venv` and installs
  the project into it. Activate it with `source python/.venv/bin/activate`.
- **Tools for `make check`:** it installs golangci-lint (the version CI
  uses) into `~/.local/bin` and runs `make tools` for buf and the protobuf
  generators in `.tools/bin`. The cluster runs without them, so they are
  reported as `WARN`, not `MISSING`, as is a missing C compiler, which
  `go test -race` needs.
- **Docker:** it is never installed or reconfigured. If your user cannot run
  `docker` without `sudo`, the script prints the one command that fixes it
  (`sudo usermod -aG docker $USER`). After running it, log out and back in.

Both modes end with a summary block. Run `scripts/vm-setup.sh` again until
every line says `OK`.

## Daily use

```sh
ssh mr-vm
cd MapReduce-Reloaded && git pull
make cluster-up      # build the image, start the coordinator and 4 workers
make e2e             # run word count and compare with the golden output
make e2e-s3          # the same, with input and output in the S3 store
make bench-codecs     # bytes shuffled and job time per codec (bench/README.md)
make cluster-down    # stop and remove only this project's containers and volume
```

Run `mr` on the VM itself: `make mr` builds it into `bin/mr`, and
`bin/mr upload`, `bin/mr submit`, `bin/mr status` and `bin/mr cat` cover
the workflow (README, [quick start](../README.md#quick-start)). Every
container sees the repository's `data/` directory at `/data`, the shared
storage for job input and output (ADR 0016), the test corpus at `/corpus`,
and the experiments' `output` volume at `/out`. On a machine without Go,
`docker compose -p mapreduce -f deploy/compose/compose.yaml run --rm mr
submit -coordinator coordinator:7070 ...` runs `mr` in a container instead.
In the `s3` profile, the store serves the corpus as `s3://corpus` and keeps
output in `s3://out`; every node has its address and credentials in the
`AWS_*` variables (ADR 0013).

The coordinator is also available to a host CLI at `127.0.0.1:18770`, bound
only to loopback. For example, on the VM run
`mr status -coordinator 127.0.0.1:18770 job-1`. To use a local CLI from your
laptop, forward the same address through SSH:

```sh
ssh -L 18770:127.0.0.1:18770 mr-vm
# in another terminal: mr status -coordinator 127.0.0.1:18770 job-1
```

Containers continue to use `coordinator:7070`.

The status page and Prometheus metrics are at `127.0.0.1:18780` (`/` and
`/metrics`), also bound only to loopback; forward them the same way with
`ssh -L 18780:127.0.0.1:18780 mr-vm`. After `make monitoring-up`, Grafana
is at `127.0.0.1:13000` and Prometheus at `127.0.0.1:19090`:
`ssh -L 13000:127.0.0.1:13000 mr-vm`.

## Living alongside other coursework

- The Compose project is named `mapreduce`. `make cluster-down` therefore only
  removes containers, networks and volumes with that name, and never touches
  course containers.
- Published ports use the 18xxx range (for example, the status page at
  18780), away from the common 8080/9000-style ports that course work uses.
- Check memory use at any time with `docker stats`. The #21 end-to-end test
  also checks that every service stays under its limit.
