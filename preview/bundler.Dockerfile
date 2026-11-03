FROM alchemyplatform/rundler:v0.11.0 AS rundler

FROM node:26.13.0-trixie-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=rundler /usr/local/bin/rundler /usr/local/bin/rundler
COPY licenses/preview-rundler-* /app/licenses/
USER node
RUN rundler --help >/dev/null && test -s /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["node", "/app/src/preview-start.mjs"]
