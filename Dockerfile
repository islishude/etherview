# syntax=docker/dockerfile:1.7

FROM node:26.5.0-alpine AS web-builder
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
COPY cmd ./cmd
COPY internal ./internal
COPY web/webui.go ./web/webui.go
COPY api/openapi.yaml ./api/openapi.yaml
COPY --from=web-builder /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install -trimpath -ldflags="-s -w" ./cmd/... \
    && geth_module_dir="$(go list -m -f '{{.Dir}}' github.com/ethereum/go-ethereum)" \
    && mkdir -p /licenses \
    && cp "$geth_module_dir/COPYING.LESSER" /licenses/go-ethereum-LGPL-3.0-or-later.txt \
    && cp "$geth_module_dir/crypto/bn256/LICENSE" /licenses/go-ethereum-crypto-bn256-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/keccak/LICENSE" /licenses/go-ethereum-crypto-keccak-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/secp256k1/LICENSE" /licenses/go-ethereum-crypto-secp256k1-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/secp256k1/libsecp256k1/COPYING" /licenses/libsecp256k1-MIT.txt \
    && cp "$geth_module_dir/metrics/LICENSE" /licenses/go-ethereum-metrics-BSD-2-Clause-FreeBSD.txt

# Generic verifier runner. Build and publish this target separately, pin its
# digest in verification.runner_image, and pre-pull it on verify-role hosts.
# Compiler binaries and Standard JSON enter only through the bounded frame on
# stdin and are materialized in the container's tmpfs by compiler-runner.
FROM gcr.io/distroless/base-debian13:nonroot AS compiler-runner
COPY --from=go-builder --chown=nonroot:nonroot /go/bin/compiler-runner /compiler-runner
USER 65532:65532
ENTRYPOINT ["/compiler-runner"]
CMD []

# Keep production last so an unqualified `docker build .` still emits the
# deployable Etherview image rather than a test-only tool.
FROM gcr.io/distroless/base-debian13:nonroot AS production
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Etherview" \
    org.opencontainers.image.description="Ethereum execution-layer explorer" \
    org.opencontainers.image.source="https://github.com/islishude/etherview" \
    org.opencontainers.image.licenses="Apache-2.0 AND LGPL-3.0-or-later AND BSD-3-Clause AND BSD-2-Clause-FreeBSD AND MIT" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.created="${CREATED}"
COPY --chown=nonroot:nonroot LICENSE /LICENSE
COPY --chown=nonroot:nonroot THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
COPY --from=go-builder --chown=nonroot:nonroot /licenses /licenses
COPY --chown=nonroot:nonroot licenses /licenses
COPY --from=go-builder --chown=nonroot:nonroot /go/bin/etherview /etherview
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
