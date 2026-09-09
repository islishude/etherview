# syntax=docker/dockerfile:1

FROM gcr.io/distroless/base-debian13:nonroot AS production-base

FROM node:26.8.1-slim AS web-builder
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./web/
RUN --mount=type=cache,target=/root/.npm npm --prefix web ci
COPY ./api ./api
RUN --mount=type=cache,target=/root/.npm npm --prefix api ci
COPY web/index.html web/asset-budget.json web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/vite.config.ts ./web/
COPY web/scripts ./web/scripts
COPY web/src ./web/src
RUN npm --prefix api run generate:api && npm --prefix web run build

FROM golang:1.27.1 AS wasm-tools
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/etherview-wasm ./cmd/etherview-wasm
COPY cmd/wasmpack ./cmd/wasmpack
COPY cmd/wasmpython-build ./cmd/wasmpython-build
COPY internal/wasmcompiler ./internal/wasmcompiler
COPY internal/compilerbundle ./internal/compilerbundle
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /wasm-tools/etherview-wasm ./cmd/etherview-wasm \
    && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /wasm-tools/wasmpack ./cmd/wasmpack \
    && CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /wasm-tools/wasmpython-build ./cmd/wasmpython-build \
    && mkdir -p /wasm-tools/licenses \
    && cp "$(go env GOROOT)/LICENSE" /wasm-tools/licenses/Go-LICENSE.txt \
    && for entry in wazero=github.com/tetratelabs/wazero wabin=github.com/tetratelabs/wabin lz4=github.com/pierrec/lz4/v4 x-crypto=golang.org/x/crypto x-sys=golang.org/x/sys; do \
         name="${entry%%=*}"; module="${entry#*=}"; directory="$(go list -m -f '{{.Dir}}' "$module")"; \
         cp "$directory/LICENSE" "/wasm-tools/licenses/go-${name}-LICENSE.txt"; \
       done

FROM python:3.13.15-slim-trixie AS wasm-builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential curl pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY --from=wasm-tools /wasm-tools /wasm-tools
COPY compiler/wasm ./compiler/wasm
RUN --mount=type=cache,target=/wasm-cache/downloads \
    python compiler/wasm/build.py --cache /wasm-cache --output /runtime --tools-directory /wasm-tools \
    && install -d -m 0750 /var/lib/etherview/compilers/cache \
    && mkdir /runtime-copy \
    && cp -a /runtime /runtime-copy/wasm

FROM golang:1.27.1 AS go-builder
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
    go install -trimpath -ldflags="-s -w" ./cmd/...
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    geth_module_dir="$(go list -m -f '{{.Dir}}' github.com/ethereum/go-ethereum)" \
    && geas_module_dir="$(go list -m -f '{{.Dir}}' github.com/fjl/geas)" \
    && mkdir -p /licenses \
    && cp "$geth_module_dir/COPYING.LESSER" /licenses/go-ethereum-LGPL-3.0-or-later.txt \
    && cp "$geth_module_dir/crypto/bn256/LICENSE" /licenses/go-ethereum-crypto-bn256-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/keccak/LICENSE" /licenses/go-ethereum-crypto-keccak-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/secp256k1/LICENSE" /licenses/go-ethereum-crypto-secp256k1-BSD-3-Clause.txt \
    && cp "$geth_module_dir/crypto/secp256k1/libsecp256k1/COPYING" /licenses/libsecp256k1-MIT.txt \
    && cp "$geth_module_dir/metrics/LICENSE" /licenses/go-ethereum-metrics-BSD-2-Clause-FreeBSD.txt \
    && cp "$geas_module_dir/LICENSE" /licenses/geas-LGPL-3.0.txt

FROM production-base AS production
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Etherview" \
    org.opencontainers.image.description="Ethereum execution-layer explorer" \
    org.opencontainers.image.source="https://github.com/islishude/etherview" \
    org.opencontainers.image.licenses="Apache-2.0 AND LGPL-3.0-or-later AND LGPL-3.0-only AND BSD-3-Clause AND BSD-2-Clause-FreeBSD AND MIT AND PSF-2.0 AND BSD-2-Clause AND (Apache-2.0 WITH LLVM-exception)" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.created="${CREATED}"
COPY --chown=nonroot:nonroot LICENSE /LICENSE
COPY --chown=nonroot:nonroot THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
COPY --from=go-builder --chown=nonroot:nonroot /licenses /licenses
COPY --chown=nonroot:nonroot licenses /licenses
COPY --from=go-builder --chown=nonroot:nonroot /go/bin/etherview /etherview
COPY --from=go-builder --chown=nonroot:nonroot --chmod=0555 /go/bin/etherview-geas-compiler /usr/local/bin/etherview-geas-compiler
COPY --from=wasm-builder --chown=nonroot:nonroot /runtime-copy /opt/etherview
COPY --from=wasm-builder --chown=nonroot:nonroot /runtime/licenses /licenses/wasm-runtime
COPY --from=wasm-builder --chown=nonroot:nonroot --chmod=0750 /var/lib/etherview/compilers /var/lib/etherview/compilers
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
