# One image holds the coordinator, the worker and the mr CLI. The build
# context is the Go module: docker build -f deploy/docker/Dockerfile go
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /dist/ ./cmd/... && mkdir /dist/out

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /dist/coordinator /dist/worker /dist/mr /usr/local/bin/
# A new named volume mounted here copies this directory's owner, so the
# nonroot user can write job output to it.
COPY --from=build --chown=nonroot /dist/out /out
