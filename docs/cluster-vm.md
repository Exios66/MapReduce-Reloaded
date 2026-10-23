# Cluster VM: where the distributed cluster runs

The distributed cluster (#21 onward) runs on one Linux VM with **8 GB of RAM**:
the VM provided for CS544. Every component runs as a Docker container on that
VM: the coordinator, the workers and MinIO. The containers talk to each other
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
| MinIO (#41) | 512 MB | Object storage for input and output |
| **Total** | **≈ 3.8 GB** | Leaves about 4 GB for the OS, Docker and coursework containers |

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
make cluster-down    # stop and remove only this project's containers and volume
```

Run any `mr` command on the cluster with
`docker compose -f deploy/compose/compose.yaml run --rm mr ...`, for example
`mr status -coordinator coordinator:7070 job-1`. Every container sees the
test corpus at `/corpus` and job output in the `output` volume at `/out`.
MinIO joins the Compose file with #41.

Web pages (the status UI from #32, MinIO's console) are reached through an
SSH tunnel, so no port is opened to the network:

```sh
ssh -L 18080:localhost:18080 -L 18900:localhost:18900 mr-vm
# then open http://localhost:18080 (status UI) or http://localhost:18900 (MinIO)
```

## Living alongside CS544 coursework

- The Compose project is named `mapreduce`. `make cluster-down` therefore only
  removes containers, networks and volumes with that name, and never touches
  course containers.
- Published ports use the 18xxx range (for example, status UI 18080 and MinIO
  18900), away from the common 8080/9000-style ports that course work uses.
- Check memory use at any time with `docker stats`. The #21 end-to-end test
  also checks that every service stays under its limit.
