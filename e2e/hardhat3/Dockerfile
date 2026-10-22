FROM node:26.12.1-slim

WORKDIR /repo

COPY package.json package-lock.json ./
COPY web/package.json ./web/
COPY api/package.json ./api/
COPY compiler/package.json ./compiler/
COPY e2e/hardhat3/package.json ./e2e/hardhat3/
RUN npm ci --engine-strict --ignore-scripts --legacy-peer-deps --no-audit --no-fund --workspace e2e/hardhat3

WORKDIR /repo/e2e/hardhat3
COPY e2e/hardhat3/hardhat.config.ts e2e/hardhat3/cache-inspect.mjs e2e/hardhat3/compile-yul.mjs e2e/hardhat3/constructor-args.mjs e2e/hardhat3/deploy.mjs e2e/hardhat3/tamper-slots.mjs e2e/hardhat3/transaction-sender.mjs e2e/hardhat3/upgrade.mjs ./
COPY e2e/hardhat3/contracts ./contracts

CMD ["npx", "hardhat"]
