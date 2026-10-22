# Top-level entry points. `make check` is the project's quality gate: it runs
# every linter and test, and the pre-push hook runs it before each push.
# (See docs/design/0002-local-quality-gate.md.)

.PHONY: all check hooks test lint fmt go-test go-lint py-test py-lint

all: check

check: lint test

# One-time setup per clone: tell git to use the hooks in .githooks/.
hooks:
	git config core.hooksPath .githooks

test: go-test py-test

lint: go-lint py-lint

fmt:
	cd go && gofmt -w .
	cd python && ruff format . && ruff check --fix .

go-test:
	cd go && go test -race ./...

go-lint:
	cd go && test -z "$$(gofmt -l .)" && go vet ./... && golangci-lint run ./...

py-test:
	cd python && python -m pytest -q

py-lint:
	cd python && ruff format --check . && ruff check .
