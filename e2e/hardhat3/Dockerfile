FROM node:26.8.1-slim

WORKDIR /workspace

COPY .npmrc package.json package-lock.json ./
RUN npm ci --ignore-scripts

COPY hardhat.config.ts cache-inspect.mjs compile-yul.mjs constructor-args.mjs deploy.mjs tamper-slots.mjs transaction-sender.mjs upgrade.mjs ./
COPY contracts ./contracts

CMD ["npx", "hardhat"]
