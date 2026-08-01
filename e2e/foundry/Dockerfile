FROM ghcr.io/foundry-rs/foundry:v1.7.1

USER root
RUN install -d -o foundry -g foundry /workspace

USER foundry
WORKDIR /workspace

COPY --chown=foundry:foundry foundry.toml ./
COPY --chown=foundry:foundry src ./src

# Cache the exact Solidity release during the image build, then prove the
# fixture can compile from that cache without resolving another toolchain.
RUN forge --version | grep -F "forge Version: 1.7.1" \
    && forge --version | grep -F "Commit SHA: 4072e48705af9d93e3c0f6e29e93b5e9a40caed8" \
    && forge build \
    && forge build --offline --force --silent

# The upstream image uses `sh -c`, which consumes only the first argument from
# an exec-form Compose command. `env` preserves every Forge argument without a
# wrapper script or shell interpolation.
ENTRYPOINT ["/usr/bin/env"]
CMD ["forge", "--version"]
