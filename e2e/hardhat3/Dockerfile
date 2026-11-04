FROM node:26.13.0-slim

ENV PNPM_HOME=/pnpm
ENV PATH="${PNPM_HOME}/bin:${PATH}"
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://get.pnpm.io/install.sh -o /tmp/install-pnpm.sh \
    && env PNPM_VERSION=12 ENV=/root/.shrc SHELL=/bin/sh sh /tmp/install-pnpm.sh \
    && rm /tmp/install-pnpm.sh \
    && pnpm --version | grep -Eq '^12\.[0-9]+\.[0-9]+$'
WORKDIR /repo

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json ./web/
COPY api/package.json ./api/
COPY compiler/package.json ./compiler/
COPY e2e/hardhat3/package.json ./e2e/hardhat3/
RUN --mount=type=cache,id=etherview-pnpm,target=/pnpm-cache pnpm --filter ./e2e/hardhat3 install --frozen-lockfile --store-dir=/pnpm-cache

WORKDIR /repo/e2e/hardhat3
COPY e2e/hardhat3/hardhat.config.ts e2e/hardhat3/cache-inspect.mjs e2e/hardhat3/compile-yul.mjs e2e/hardhat3/constructor-args.mjs e2e/hardhat3/deploy.mjs e2e/hardhat3/tamper-slots.mjs e2e/hardhat3/transaction-sender.mjs e2e/hardhat3/upgrade.mjs ./
COPY e2e/hardhat3/contracts ./contracts

CMD ["pnpm", "exec", "hardhat"]
