FROM ghcr.io/foundry-rs/foundry:v1.8.6 AS foundry
FROM node:24.22.1-bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=foundry /usr/local/bin/forge /usr/local/bin/forge
WORKDIR /app
# Alto version 0.0.21; revision pinned below
# Exact upstream tree also pins the contract submodules and package lock.
RUN git init && git remote add origin https://github.com/pimlicolabs/alto.git && \
    git fetch --depth=1 origin 3413697d806785a5b57f7aefea1967ffd4fe618e && \
    git checkout --detach FETCH_HEAD
RUN for attempt in 1 2 3; do git submodule update --init --depth=1 -- contracts/lib && exit 0; done; exit 1
RUN corepack enable && pnpm install --frozen-lockfile
RUN for attempt in 1 2 3; do pnpm run build:contracts && exit 0; done; exit 1
COPY deploy/preview/alto-safe-validation.patch /tmp/alto-safe-validation.patch
RUN git apply --check /tmp/alto-safe-validation.patch && git apply /tmp/alto-safe-validation.patch
RUN pnpm build
FROM node:24.22.1-bookworm-slim
WORKDIR /app
COPY --from=build /app /app
COPY --from=build /tmp/alto-safe-validation.patch /app/preview-safe-validation.patch
USER node
ENTRYPOINT ["node", "src/esm/cli/alto.js"]
