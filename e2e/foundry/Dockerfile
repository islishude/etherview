FROM ghcr.io/foundry-rs/foundry:v1.9.0

USER root
RUN install -d -o foundry -g foundry /workspace

USER foundry
WORKDIR /workspace

COPY --chown=foundry:foundry foundry.toml ./
COPY --chown=foundry:foundry src ./src

# Install the exact Solidity release into Foundry's compiler cache during the
# image build, then prove the fixture can compile offline. Project build caching
# stays disabled because every verification command runs in a fresh container.
RUN forge --version | grep -F "forge Version: 1.9.0" \
    && forge --version | grep -F "Commit SHA: 6056c5845d34205a83cf7108042adb137325c9df" \
    && forge config --json | grep -F '"cache": false' \
    && forge build \
    && forge build --offline --force --silent

# The upstream image uses `sh -c`, which consumes only the first argument from
# an exec-form Compose command. `env` preserves every Forge argument without a
# wrapper script or shell interpolation.
ENTRYPOINT ["/usr/bin/env"]
CMD ["forge", "--version"]
