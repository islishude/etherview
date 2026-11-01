# syntax=docker/dockerfile:1
FROM golang:1.27.3 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY internal/facilitator ./internal/facilitator
COPY internal/db/gen ./internal/db/gen
COPY internal/jsonstrict ./internal/jsonstrict
COPY cmd/x402facilitator ./cmd/x402facilitator
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/x402facilitator ./cmd/x402facilitator
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/x402facilitator /x402facilitator
COPY licenses /licenses
ENTRYPOINT ["/x402facilitator"]
