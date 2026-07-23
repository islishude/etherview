# syntax=docker/dockerfile:1.7

FROM node:24.18.0-alpine AS web-builder
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./web/
RUN --mount=type=cache,target=/root/.npm npm --prefix web ci
COPY ./api ./api
RUN --mount=type=cache,target=/root/.npm npm --prefix api ci
COPY web/index.html web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/vite.config.ts ./web/
COPY web/src ./web/src
RUN npm --prefix api run generate:api && npm --prefix web run build

FROM golang:1.26.5 AS go-builder
WORKDIR /src
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY web/webui.go ./web/webui.go
COPY --from=web-builder /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -ldflags="-s -w" -o /out/etherview ./cmd/etherview

# The deterministic JSON-RPC fixture is a test-only target used by the
# Compose runtime parity smoke. Nothing from this stage enters production.
FROM golang:1.26.5 AS runtime-fixture-builder
WORKDIR /src
COPY go.mod ./
COPY cmd/runtimefixture ./cmd/runtimefixture
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/runtimefixture ./cmd/runtimefixture

FROM gcr.io/distroless/base-debian13:nonroot AS runtime-fixture
COPY --from=runtime-fixture-builder --chown=nonroot:nonroot /out/runtimefixture /runtimefixture
USER 65532:65532
EXPOSE 8545
ENTRYPOINT ["/runtimefixture"]
CMD ["serve"]

# The bounded public-API load driver is another test-only target. It is kept
# separate from go-builder so smoke runs do not rebuild the embedded SPA and
# nothing from this stage enters production.
FROM golang:1.26.5 AS runtime-loadtest-builder
WORKDIR /src
COPY go.mod ./
COPY cmd/loadtest ./cmd/loadtest
COPY internal/loadtest ./internal/loadtest
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loadtest ./cmd/loadtest

FROM gcr.io/distroless/base-debian13:nonroot AS runtime-loadtest
COPY --from=runtime-loadtest-builder --chown=nonroot:nonroot /out/loadtest /loadtest
USER 65532:65532
ENTRYPOINT ["/loadtest"]

# Keep production last so an unqualified `docker build .` still emits the
# deployable Etherview image rather than a test-only tool.
FROM gcr.io/distroless/base-debian13:nonroot AS production
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Etherview" \
    org.opencontainers.image.description="Ethereum execution-layer explorer" \
    org.opencontainers.image.source="https://github.com/islishude/etherview" \
    org.opencontainers.image.licenses="Apache-2.0" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.created="${CREATED}"
COPY --chown=nonroot:nonroot LICENSE /LICENSE
COPY --from=go-builder --chown=nonroot:nonroot /out/etherview /etherview
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
