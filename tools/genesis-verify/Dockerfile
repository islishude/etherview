# syntax=docker/dockerfile:1
FROM golang:1.27.3 AS builder
WORKDIR /src
COPY go.mod ./
COPY tools/genesis-verify ./tools/genesis-verify
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /genesis-verify ./tools/genesis-verify

FROM scratch
COPY --from=builder /genesis-verify /genesis-verify
COPY licenses/LICENSE licenses/Go-LICENSE /licenses/
USER 65532:65532
ENTRYPOINT ["/genesis-verify"]
