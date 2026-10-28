FROM node:26.12.1
WORKDIR /app
# The named build context contains the checksum-verified official Rundler release.
COPY --from=rundler /rundler /usr/local/bin/rundler
COPY licenses/preview-rundler-* /app/licenses/
USER node
ENTRYPOINT ["node", "/app/src/preview-start.mjs"]
