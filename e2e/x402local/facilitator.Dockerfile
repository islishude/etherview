# syntax=docker/dockerfile:1

FROM golang:1.27.0 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/x402localfacilitator ./cmd/x402localfacilitator
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/x402localfacilitator ./cmd/x402localfacilitator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/x402localfacilitator /x402localfacilitator
ENTRYPOINT ["/x402localfacilitator"]
