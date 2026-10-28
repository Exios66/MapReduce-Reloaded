# Top-level entry points. `make check` is the project's quality gate: it runs
# every linter and test, and the pre-push hook runs it before each push.
# (See docs/design/0002-local-quality-gate.md.)

.PHONY: all check flaky fuzz mutate mutate-py hooks vm-check data mr upload-example cluster-up cluster-down clean-output monitoring-up e2e e2e-s3 chaos bench bench-codecs perf cover tools proto proto-lint proto-breaking proto-check test lint fmt ci-report go-test go-lint py-test py-lint

# Python interpreter; many systems only have python3. Override with PYTHON=...
PYTHON ?= python3

all: check

# Run the three stages one after another, even under `make -j`: code
# generation rewrites go/gen and python/.../gen, which lint and test read.
check:
	$(MAKE) proto-check
	$(MAKE) lint
	$(MAKE) test

# Run `make check` on a clean copy of HEAD and post the result as a GitHub
# commit status (needs GITHUB_TOKEN; docs/design/0014-ci-on-pull-requests.md).
ci-report:
	scripts/ci-report.sh

# One-time setup per clone: tell git to use the hooks in .githooks/.
hooks:
	git config core.hooksPath .githooks

# Report whether this machine can build and run the cluster (docs/cluster-vm.md).
vm-check:
	scripts/vm-setup.sh --check

# The Compose cluster (deploy/compose, docs/cluster-vm.md). Its project is
# named mapreduce, so cluster-down removes only this project's containers.
COMPOSE = docker compose -p mapreduce -f deploy/compose/compose.yaml

# The shared storage mounted at /data in every container (ADR 0016).
# Distroless workers run as UID 65532 and need to write output here.
data:
	mkdir -p data/input data/out
	chmod a+rwx data/out

# The host client, bin/mr (ADR 0016), built with the host's Go if it has
# one, otherwise in the same golang image the cluster's image is built from.
mr:
	@mkdir -p bin
	if command -v go >/dev/null; then cd go && go build -o ../bin/mr ./cmd/mr; \
	else docker run --rm -u "$$(id -u):$$(id -g)" -e HOME=/tmp -e CGO_ENABLED=0 \
		-v "$(CURDIR)/go:/src:ro" -v "$(CURDIR)/bin:/bin-out" -w /src \
		golang:1.26-alpine go build -buildvcs=false -o /bin-out/mr ./cmd/mr; fi

# Example input in shared storage: the test corpus at /data/input/corpus and
# a 10 MB web log at /data/input/weblog/access.log.
upload-example: data mr
	bin/mr upload 'testdata/corpus/*.txt' /data/input/corpus
	tmp=$$(mktemp -d) && $(PYTHON) bench/gen-weblog.py 10000000 > $$tmp/access.log && \
		bin/mr upload $$tmp/access.log /data/input/weblog; status=$$?; rm -rf $$tmp; exit $$status

cluster-up: data
	$(COMPOSE) up -d --build

cluster-down:
	$(COMPOSE) --profile monitoring --profile s3 down -v --remove-orphans

# Select completed job directory names explicitly, e.g.
# make clean-output CLEAN_OUTPUTS='sort-kill-123 exp-E0-w4-1'
# Never select an active job. Inputs, the log, and unselected outputs stay.
export CLEAN_OUTPUTS
clean-output:
	@test -n "$$CLEAN_OUTPUTS" || { echo 'select completed output directories with CLEAN_OUTPUTS' >&2; exit 1; }
	set -f; docker run --rm -v mapreduce_output:/out golang:1.26-alpine \
		sh -eu -c 'for name do case "$$name" in \
			wal|exp-input-*|sort-1g.txt|*[!a-zA-Z0-9_.-]*|""|.|..|[-_]*) echo "invalid output directory: $$name" >&2; exit 1;; \
		esac; done; for name do find /out -mindepth 1 -maxdepth 1 -type d -name "$$name" -exec rm -rf {} +; done' \
		sh $$CLEAN_OUTPUTS
	@df -h /
	@docker system df

# Prometheus at 127.0.0.1:19090 and Grafana at 127.0.0.1:13000.
monitoring-up:
	$(COMPOSE) --profile monitoring up -d prometheus grafana

e2e: cluster-up mr
	scripts/e2e.sh

# Seeded chaos on the cluster (#29): worker kills and pauses and coordinator
# restarts during word count jobs, which must all give the same output.
# RUNS (default 5) runs from SEED (default 1); see scripts/chaos.sh.
chaos: cluster-up mr
	scripts/chaos.sh $(or $(RUNS),5) $(or $(SEED),1)

# The same job with input and output in the s3 profile's store.
e2e-s3: cluster-up
	$(COMPOSE) --profile s3 up -d --wait s3
	scripts/e2e.sh s3
	cd go && go test -count=1 ./internal/storage -run TestGatewayContract -s3.endpoint=http://127.0.0.1:18790

# One of the paper's benchmarks, with its data rates over time
# (bench/README.md): make bench SCENARIO=grep|sort|zipf.
SCENARIO ?= grep
bench:
	bench/run.sh $(SCENARIO)

# Bytes shuffled and job time per intermediate codec (bench/README.md).
bench-codecs: cluster-up
	bench/codecs.sh

# The Go benchmarks against bench/baseline.txt, failing on a slowdown of
# more than THRESHOLD percent (default 20); PERF_UPDATE=1 rewrites the
# baseline (#160). See scripts/perf.sh.
perf:
	PYTHON=$(PYTHON) scripts/perf.sh

test: go-test py-test

lint: go-lint py-lint

fmt:
	cd go && gofmt -w .
	cd python && $(PYTHON) -m ruff format . ../scripts ../bench && $(PYTHON) -m ruff check --fix . ../scripts ../bench

# Pinned code-generation tools, installed into .tools/bin (git-ignored).
TOOLS := $(CURDIR)/.tools/bin
tools:
	GOBIN=$(TOOLS) go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
	GOBIN=$(TOOLS) go install github.com/bufbuild/buf/cmd/buf@v1.74.0

# Regenerate Go and Python code from proto/.
proto:
	PYTHON=$(PYTHON) scripts/gen-proto.sh

proto-lint:
	$(TOOLS)/buf lint

# Fail if a .proto change would break existing clients, compared with main on
# GitHub. We fetch it first so a missing or stale local main cannot hide a
# break. Skipped only while main has no .proto files yet.
PROTO_BASE := origin/main
# CI's full checkout already fetched main before removing credentials.
PROTO_FETCH ?= true
proto-breaking:
ifeq ($(PROTO_FETCH),true)
	@git fetch --quiet origin main
endif
	@files=$$(git ls-tree -r --name-only $(PROTO_BASE) -- proto) || exit 1; \
	if echo "$$files" | grep -q '\.proto$$'; then \
		$(TOOLS)/buf breaking --against '.git#ref=$(PROTO_BASE)'; \
	else echo "proto-breaking: $(PROTO_BASE) has no .proto files yet, skipping"; fi

# Fail if regenerating changes anything, i.e. the generated code in git (or
# staged for commit) is out of date with proto/.
proto-check: proto-lint proto-breaking proto
	@git diff --exit-code --stat -- go/gen python/mapreduce_reloaded/gen || \
		(echo "generated code is stale: run 'make proto' and commit the result" && exit 1)
	@test -z "$$(git ls-files --others --exclude-standard -- go/gen python/mapreduce_reloaded/gen)" || \
		(echo "generated code has new files: git add them" && exit 1)

# papermap and statusweb read docs, Python tests and deployment files outside
# the Go module, which the test cache does not track; -count=1 runs them every
# time. -shuffle=on runs each package's tests in a random order, so that no
# test depends on another; a failure prints the seed, and -shuffle=SEED
# repeats the order.
# MR_PYTHON lets go/cmd/mr compare the Go examples with Python's (#153); a
# relative PYTHON is a command name, looked up on the PATH.
# Each package's coverage must stay at or over its floor in go/cover-floors.txt
# and python/cover-floors.txt (scripts/cover.py, #161). A Go package's
# coverage counts every test in the module (-coverpkg), and the runs of the
# commands that internal/e2e builds, which write their counters to
# GO_COVERDIR; go/cover.out holds both.
GO_COVERDIR = $(CURDIR)/go/.cover
go-test:
	rm -rf $(GO_COVERDIR) && mkdir -p $(GO_COVERDIR)
	cd go && MR_E2E_GOCOVERDIR=$(GO_COVERDIR) MR_PYTHON=$(PYTHON) go test -race -shuffle=on -coverpkg=./... -coverprofile=cover.out ./... && go test -count=1 ./internal/papermap ./internal/statusweb
	if ls $(GO_COVERDIR)/covcounters.* >/dev/null 2>&1; then \
		cd go && go tool covdata textfmt -i=$(GO_COVERDIR) -o $(GO_COVERDIR)/e2e.out && tail -n +2 $(GO_COVERDIR)/e2e.out >> cover.out; \
	fi
	$(PYTHON) scripts/cover.py go go/cover.out go/cover-floors.txt

# Run the coordinator, worker and command tests 20 times in random orders,
# by hand before a release, to find flaky tests (#157). It takes a while.
flaky:
	cd go && go test -race -shuffle=on -count=20 -timeout 90m ./internal/coordinator ./internal/worker ./internal/shuffle ./cmd/...

# Run every Go fuzz target for FUZZTIME (default 20s) each, by hand: make
# check runs only their stored seeds (#152). See scripts/fuzz.sh.
fuzz:
	scripts/fuzz.sh

# Mutation testing of the critical packages, by hand (#161): each change to
# the code that the tests miss, and each package's score. It takes a while.
# See go/tools/mutate.
mutate:
	cd go && go run ./tools/mutate ./internal/partition hash.go
	cd go && go run ./tools/mutate ./internal/extsort merge.go sorter.go
	cd go && go run ./tools/mutate ./internal/coordinator tasktable.go
	cd go && go run ./tools/mutate ./internal/task task.go

# The same for the Python MRKV format and partition function, with mutmut
# (pip install -e 'python[dev,mutation]'); the files and tests are set in
# python/pyproject.toml. It needs fork, so Linux or macOS.
mutate-py:
	cd python && $(PYTHON) -m mutmut run && $(PYTHON) -m mutmut results

go-lint:
	cd go && test -z "$$(gofmt -l .)" && go vet ./... && golangci-lint run ./...

py-test:
	cd python && $(PYTHON) -m coverage run -m pytest -q && $(PYTHON) -m coverage json -q -o cover.json
	$(PYTHON) scripts/cover.py python python/cover.json python/cover-floors.txt

# The tests, then each package's coverage against its floor.
cover: test
	$(PYTHON) scripts/cover.py go go/cover.out go/cover-floors.txt --report
	$(PYTHON) scripts/cover.py python python/cover.json python/cover-floors.txt --report

py-lint:
	cd python && $(PYTHON) -m ruff format --check . ../scripts ../bench && $(PYTHON) -m ruff check . ../scripts ../bench
