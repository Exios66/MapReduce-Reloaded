# Top-level entry points. `make check` is the project's quality gate: it runs
# every linter and test, and the pre-push hook runs it before each push.
# (See docs/design/0002-local-quality-gate.md.)

.PHONY: all check hooks vm-check cluster-up cluster-down e2e tools proto proto-lint proto-breaking proto-check test lint fmt go-test go-lint py-test py-lint

# Python interpreter; many systems only have python3. Override with PYTHON=...
PYTHON ?= python3

all: check

# Run the three stages one after another, even under `make -j`: code
# generation rewrites go/gen and python/.../gen, which lint and test read.
check:
	$(MAKE) proto-check
	$(MAKE) lint
	$(MAKE) test

# One-time setup per clone: tell git to use the hooks in .githooks/.
hooks:
	git config core.hooksPath .githooks

# Report whether this machine can build and run the cluster (docs/cluster-vm.md).
vm-check:
	scripts/vm-setup.sh --check

# The Compose cluster (deploy/compose, docs/cluster-vm.md). Its project is
# named mapreduce, so cluster-down removes only this project's containers.
COMPOSE = docker compose -p mapreduce -f deploy/compose/compose.yaml

cluster-up:
	$(COMPOSE) up -d --build

cluster-down:
	$(COMPOSE) down -v --remove-orphans

e2e: cluster-up
	scripts/e2e.sh

test: go-test py-test

lint: go-lint py-lint

fmt:
	cd go && gofmt -w .
	cd python && $(PYTHON) -m ruff format . ../scripts && $(PYTHON) -m ruff check --fix . ../scripts

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
proto-breaking:
	@git fetch --quiet origin main
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

go-test:
	cd go && go test -race ./...

go-lint:
	cd go && test -z "$$(gofmt -l .)" && go vet ./... && golangci-lint run ./...

py-test:
	cd python && $(PYTHON) -m pytest -q

py-lint:
	cd python && $(PYTHON) -m ruff format --check . ../scripts && $(PYTHON) -m ruff check . ../scripts
