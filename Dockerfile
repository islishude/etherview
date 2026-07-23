# syntax=docker/dockerfile:1.7

FROM node:24.18.0-alpine AS web-builder
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./web/
RUN --mount=type=cache,target=/root/.npm npm --prefix web ci --ignore-scripts
COPY api/openapi.yaml ./api/openapi.yaml
COPY web/index.html web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/vite.config.ts ./web/
COPY web/src ./web/src
RUN npm --prefix web run generate:api && npm --prefix web run build

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
COPY --from=web-builder /src/web/webui.go ./web/webui.go
COPY --from=web-builder /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -trimpath -ldflags="-s -w" -o /out/etherview ./cmd/etherview

FROM gcr.io/distroless/static-debian12:nonroot AS production
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
COPY --chown=nonroot:nonroot LICENSE /licenses/LICENSE
COPY --from=go-builder --chown=nonroot:nonroot /out/etherview /etherview
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
