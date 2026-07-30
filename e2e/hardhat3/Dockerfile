FROM node:24.18.0-alpine

WORKDIR /workspace

RUN npm install --global npm@11.16.0

COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts

COPY hardhat.config.ts compile-yul.mjs constructor-args.mjs deploy.mjs upgrade.mjs ./
COPY contracts ./contracts

CMD ["npx", "hardhat"]
