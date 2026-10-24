# Cluster VM: where the distributed cluster runs

The distributed cluster (#21 onward) runs on one Linux VM with **8 GB of RAM**:
the VM provided for CS544. Every component runs as a Docker container on that
VM: the coordinator, the workers and an S3-compatible store. The containers talk to each other
over a Docker network exactly as they would across machines, so the code
does not know or care that it is one host.

This page covers the memory budget, one-time setup, and daily use.

## Memory budget

The VM is shared with CS544 coursework, so the cluster has to fit in about
half of it. Each service gets a hard memory limit (`mem_limit` in
[`deploy/compose/compose.yaml`](../deploy/compose/compose.yaml)). A container that exceeds its limit is killed instead of
slowing down the whole VM.

| Service | Limit | Why this much |
|---|---|---|
| coordinator | 256 MB | Holds only task state and partition locations, never data (§3.2) |
| worker (× 4) | 768 MB each | 256 MB sort buffer (#10 spills beyond that), the rest for the Go runtime, gRPC and file buffers |
| mr (on demand) | 128 MB | Submits jobs and polls status; workers process the data |
| s3 (#41, profile `s3`) | 256 MB | [VersityGW](https://github.com/versity/versitygw), an S3-compatible store for input and output |
| **Total** | **≈ 3.7 GB** | Leaves about 4 GB for the OS, Docker and coursework containers |

- Scaling up (`docker compose up --scale worker=N`) costs 768 MB per worker.
  About 6 workers is the practical ceiling.
- The task-level numbers (sort buffer size, how many tasks a worker runs at
  once) are set in #10 and #16 to stay inside these limits.

## One-time setup

### 1. On your laptop: an SSH alias

Add this to `~/.ssh/config` so every command below can say `mr-vm` instead of
the full host name. Fill in your own values: the real host and user name stay
out of this public repository.

```
Host mr-vm
    HostName <your-vm-hostname>
    User <your-username>
```

Check it with `ssh mr-vm hostname`.

### 2. On the VM: clone the repo and run the setup script

```sh
ssh mr-vm
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
make cluster-down    # stop and remove only this project's containers and volume
```

Run any `mr` command on the cluster with
`docker compose -p mapreduce -f deploy/compose/compose.yaml run --rm mr ...`, for example
`mr status -coordinator coordinator:7070 job-1`. Every container sees the
test corpus at `/corpus` and job output in the `output` volume at `/out`.
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


## Living alongside CS544 coursework

- The Compose project is named `mapreduce`. `make cluster-down` therefore only
  removes containers, networks and volumes with that name, and never touches
  course containers.
- Published ports use the 18xxx range (for example, the status page at
  18780), away from the common 8080/9000-style ports that course work uses.
- Check memory use at any time with `docker stats`. The #21 end-to-end test
  also checks that every service stays under its limit.
