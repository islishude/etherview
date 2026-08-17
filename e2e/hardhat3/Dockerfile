FROM node:26.7.0-slim

WORKDIR /workspace

COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts

COPY hardhat.config.ts cache-inspect.mjs compile-yul.mjs constructor-args.mjs deploy.mjs tamper-slots.mjs transaction-sender.mjs upgrade.mjs ./
COPY contracts ./contracts

CMD ["npx", "hardhat"]
