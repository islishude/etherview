# syntax=docker/dockerfile:1

FROM gcr.io/distroless/base-debian13:nonroot AS production-base

FROM node:26.7.0-slim AS web-builder
WORKDIR /src
COPY web/package.json web/package-lock.json web/.npmrc ./web/
RUN --mount=type=cache,target=/root/.npm npm --prefix web ci
COPY ./api ./api
RUN --mount=type=cache,target=/root/.npm npm --prefix api ci
COPY web/index.html web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/vite.config.ts ./web/
COPY web/src ./web/src
RUN npm --prefix api run generate:api && npm --prefix web run build

FROM node:26.7.0-slim AS compiler-builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends binutils pax-utils \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src/compiler
COPY compiler/package.json compiler/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --ignore-scripts --no-audit --no-fund
COPY compiler/compile.mjs compiler/build-sea.mjs compiler/build-runtime.mjs compiler/elf-runtime.mjs compiler/test-elf-runtime.mjs compiler/test-sea.mjs ./
COPY --from=production-base / /target-rootfs/
RUN node build-sea.mjs /opt/etherview/solcjs/etherview-solcjs \
    && install -d -m 0755 /opt/etherview/licenses/solcjs-runtime \
    && test -f /usr/local/LICENSE \
    && cp /usr/local/LICENSE /opt/etherview/licenses/solcjs-runtime/node-LICENSE.txt \
    && find node_modules -mindepth 2 -maxdepth 2 -type f \
       \( -iname 'license' -o -iname 'license.*' -o -iname 'copying' -o -iname 'copying.*' \) \
       -exec sh -c 'package="$(basename "$(dirname "$1")")"; extension="$(basename "$1")"; cp "$1" "/opt/etherview/licenses/solcjs-runtime/${package}-${extension}"' _ {} \; \
    && node build-runtime.mjs \
       /opt/etherview/solcjs/etherview-solcjs \
       /target-rootfs \
       /opt/etherview/licenses/solcjs-runtime \
    && node test-elf-runtime.mjs /target-rootfs \
    && install -d -m 1777 /target-rootfs/tmp \
    && node test-sea.mjs /target-rootfs node_modules/solc/soljson.js \
    && install -d -m 0755 /solcjs-runtime-copy \
    && cp -a /opt/etherview/solcjs /solcjs-runtime-copy/ \
    && install -d -m 0750 /var/lib/etherview/compilers/cache

FROM golang:1.27.0 AS go-builder
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
    org.opencontainers.image.licenses="Apache-2.0 AND LGPL-3.0-or-later AND LGPL-3.0-only AND BSD-3-Clause AND BSD-2-Clause-FreeBSD AND MIT" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.created="${CREATED}"
COPY --chown=nonroot:nonroot LICENSE /LICENSE
COPY --chown=nonroot:nonroot THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md
COPY --from=go-builder --chown=nonroot:nonroot /licenses /licenses
COPY --chown=nonroot:nonroot licenses /licenses
COPY --from=go-builder --chown=nonroot:nonroot /go/bin/etherview /etherview
COPY --from=go-builder --chown=nonroot:nonroot --chmod=0555 /go/bin/etherview-geas-compiler /usr/local/bin/etherview-geas-compiler
COPY --from=compiler-builder --chown=nonroot:nonroot /solcjs-runtime-copy /opt/etherview
COPY --from=compiler-builder --chown=nonroot:nonroot /opt/etherview/licenses/solcjs-runtime /licenses/solcjs-runtime
COPY --from=compiler-builder --chown=nonroot:nonroot --chmod=0750 /var/lib/etherview/compilers /var/lib/etherview/compilers
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
