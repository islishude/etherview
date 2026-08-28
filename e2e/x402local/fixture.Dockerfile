FROM ghcr.io/foundry-rs/foundry:v1.7.1

USER root
RUN install -d -o foundry -g foundry /workspace
USER foundry
WORKDIR /workspace

COPY --chown=foundry:foundry foundry.toml ./
COPY --chown=foundry:foundry contracts ./contracts
RUN forge build && forge build --offline --force --silent

ENTRYPOINT ["sh", "-ec"]
