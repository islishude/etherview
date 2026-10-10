# syntax=docker/dockerfile:1
FROM golang:1.27.2 AS builder
WORKDIR /src
COPY go.mod ./
COPY cmd/ipfs-gateway ./cmd/ipfs-gateway
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ipfs-gateway ./cmd/ipfs-gateway

# Root can read the existing owner-only (0600) bind-mounted Preview key.
# No shell, package manager, application runtime or Kubo management client.
FROM scratch
LABEL org.opencontainers.image.title="Etherview IPFS Gateway" \
    org.opencontainers.image.licenses="Apache-2.0 AND BSD-3-Clause"
COPY LICENSE /LICENSE
COPY --from=builder /usr/local/go/LICENSE /licenses/Go-LICENSE
COPY --from=builder /ipfs-gateway /ipfs-gateway
EXPOSE 8443
ENTRYPOINT ["/ipfs-gateway"]
CMD ["serve"]
