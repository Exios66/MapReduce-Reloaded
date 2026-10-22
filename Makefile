# Top-level entry points. `make check` is the project's quality gate: it runs
# every linter and test, and the pre-push hook runs it before each push.
# (See docs/design/0002-local-quality-gate.md.)

.PHONY: all check hooks tools proto proto-lint proto-breaking proto-check test lint fmt go-test go-lint py-test py-lint

# Python interpreter; many systems only have python3. Override with PYTHON=...
PYTHON ?= python3

all: check

check: proto-check lint test

# One-time setup per clone: tell git to use the hooks in .githooks/.
hooks:
	git config core.hooksPath .githooks

test: go-test py-test

lint: go-lint py-lint

fmt:
	cd go && gofmt -w .
	cd python && ruff format . && ruff check --fix .

# Pinned code-generation tools, installed into .tools/bin (git-ignored).
TOOLS := $(CURDIR)/.tools/bin
tools:
	GOBIN=$(TOOLS) go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	GOBIN=$(TOOLS) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
	GOBIN=$(TOOLS) go install github.com/bufbuild/buf/cmd/buf@v1.74.0

# Regenerate Go and Python code from proto/.
proto:
	scripts/gen-proto.sh

proto-lint:
	$(TOOLS)/buf lint

# Fail if a .proto change would break existing clients, compared with main.
# Skipped while main has no .proto files yet.
proto-breaking:
	@if git ls-tree -r --name-only main -- proto | grep -q '\.proto$$'; then \
		$(TOOLS)/buf breaking --against '.git#branch=main'; \
	else echo "proto-breaking: main has no .proto files yet, skipping"; fi

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
	cd python && ruff format --check . && ruff check .
