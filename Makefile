# Top-level entry points. Every CI step is a make target, so anything CI runs
# can be reproduced locally with the same command.

.PHONY: all test lint fmt go-test go-lint py-test py-lint

all: lint test

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
