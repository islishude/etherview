FROM alchemyplatform/rundler:v0.11.0 AS rundler

FROM node:26.13.0
WORKDIR /app
COPY --from=rundler /usr/local/bin/rundler /usr/local/bin/rundler
COPY licenses/preview-rundler-* /app/licenses/
USER node
ENTRYPOINT ["node", "/app/src/preview-start.mjs"]
