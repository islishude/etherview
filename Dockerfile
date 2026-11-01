# syntax=docker/dockerfile:1

FROM gcr.io/distroless/base-debian13:nonroot AS production-base

FROM node:26.13.0 AS node-builder
ENV PNPM_HOME=/pnpm
ENV PATH="${PNPM_HOME}/bin:${PATH}"
RUN curl -fsSL https://get.pnpm.io/install.sh -o /tmp/install-pnpm.sh \
    && env PNPM_VERSION=12.15.0 ENV=/root/.shrc SHELL=/bin/sh sh /tmp/install-pnpm.sh \
    && rm /tmp/install-pnpm.sh \
    && test "$(pnpm --version)" = "12.15.0"

FROM node-builder AS web-builder
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json ./web/
COPY api/package.json ./api/
COPY compiler/package.json ./compiler/
COPY e2e/hardhat3/package.json ./e2e/hardhat3/
RUN --mount=type=cache,id=etherview-pnpm,target=/pnpm/store pnpm --filter . --filter ./web install --frozen-lockfile --store-dir=/pnpm/store
COPY web/index.html web/asset-budget.json web/tsconfig.json web/tsconfig.app.json web/tsconfig.node.json web/tsconfig.worker.json web/vite.config.ts ./web/
COPY web/scripts ./web/scripts
COPY web/src ./web/src
COPY web/test ./web/test
ARG ETHERVIEW_WEB_BRANDING_DIR=web/branding/default
COPY ["${ETHERVIEW_WEB_BRANDING_DIR}/", "/src/web-branding/"]
RUN ETHERVIEW_WEB_BRANDING_DIR=/src/web-branding pnpm --dir web run build

FROM node-builder AS compiler-builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends binutils pax-utils \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json ./web/
COPY api/package.json ./api/
COPY compiler/package.json ./compiler/
COPY e2e/hardhat3/package.json ./e2e/hardhat3/
RUN --mount=type=cache,id=etherview-pnpm,target=/pnpm/store \
    pnpm --filter ./compiler install --frozen-lockfile --store-dir=/pnpm/store
WORKDIR /src/compiler
COPY compiler/compile.mjs compiler/build-sea.mjs compiler/build-runtime.mjs compiler/check-licenses.mjs compiler/elf-runtime.mjs compiler/test-elf-runtime.mjs compiler/test-sea.mjs ./
COPY compiler/vyper/wasm ./vyper/wasm/
COPY licenses /licenses
COPY --from=production-base / /target-rootfs/
RUN node build-sea.mjs /opt/etherview/solcjs/etherview-solcjs \
    && node check-licenses.mjs /licenses/solcjs-runtime /usr/local/LICENSE \
    && node build-runtime.mjs \
       /opt/etherview/solcjs/etherview-solcjs \
       /target-rootfs \
       /licenses/solcjs-runtime \
    && node test-elf-runtime.mjs /target-rootfs \
    && install -d -m 1777 /target-rootfs/tmp \
    && node test-sea.mjs /target-rootfs "$(node -p 'require.resolve("solc/soljson.js")')" \
    && install -d -m 0755 /solcjs-runtime-copy \
    && cp -a /opt/etherview/solcjs /solcjs-runtime-copy/ \
    && node vyper/wasm/prepare.mjs /solcjs-runtime-copy/python-wasm \
    && node vyper/wasm/licenses.mjs /licenses/python-wasm \
    && find /solcjs-runtime-copy/python-wasm -type d -exec chmod 0555 {} + \
    && find /solcjs-runtime-copy/python-wasm -type f -exec chmod 0444 {} + \
    && install -d -m 0750 /var/lib/etherview/compilers/cache

FROM golang:1.27.3 AS go-builder
WORKDIR /src
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY compiler/vyper ./compiler/vyper
COPY web/webui.go ./web/webui.go
COPY api/openapi.yaml ./api/openapi.yaml
COPY --from=web-builder /src/web/dist ./web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go install -trimpath -ldflags="-s -w" ./cmd/...

FROM production-base AS production
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Etherview" \
    org.opencontainers.image.description="Ethereum execution-layer explorer" \
    org.opencontainers.image.source="https://github.com/islishude/etherview" \
    org.opencontainers.image.licenses="Apache-2.0 AND LGPL-3.0-or-later AND LGPL-3.0-only AND BSD-3-Clause AND BSD-2-Clause-FreeBSD AND MIT AND PSF-2.0 AND MPL-2.0" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.created="${CREATED}"
COPY --chown=nonroot:nonroot licenses /licenses
COPY --from=go-builder --chown=nonroot:nonroot /go/bin/etherview /etherview
COPY --from=go-builder --chown=nonroot:nonroot --chmod=0555 /go/bin/x402facilitator /x402facilitator
COPY --from=go-builder --chown=nonroot:nonroot --chmod=0555 /go/bin/etherview-geas-compiler /usr/local/bin/etherview-geas-compiler
COPY --from=compiler-builder --chown=nonroot:nonroot /solcjs-runtime-copy /opt/etherview
COPY --from=compiler-builder --chown=nonroot:nonroot --chmod=0750 /var/lib/etherview/compilers /var/lib/etherview/compilers
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/etherview"]
CMD ["serve", "--roles=all"]
