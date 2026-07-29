# ADR-0027: Process-Native API TLS

- Status: Accepted
- Date: 2026-07-29

## Context

Etherview's public API listener previously served only plain HTTP. Helm could
terminate client TLS at an Ingress, but direct Compose exposure, internal
service-to-service encryption, and deployments without a TLS terminator could
not make the Go server speak HTTPS. `server.public_url` describes the public
origin used by browser security and authentication; it does not configure the
listener protocol.

TLS private keys are role-scoped secrets. Migration, sync, enrichment, trace,
verification, metadata, maintenance, and the operations listener do not need
them. Certificate rotation must also preserve the existing lifecycle,
readiness, and monolith/split-role parity contracts.

## Decision

- `server.tls_cert_file` and `server.tls_key_file` are optional absolute file
  paths. Both empty keeps the public API listener on HTTP; both configured
  enables HTTPS. A partial pair, relative path, or HTTP `server.public_url`
  with listener TLS is invalid. An HTTPS public URL alone does not enable
  process-native TLS because an external terminator remains supported.
- The API service loads and validates one PEM certificate/private-key pair
  before binding its listener. Any read, parse, or key-match failure aborts
  startup without opening the port or falling back to HTTP.
- The server supports TLS 1.2 and 1.3 using Go's cipher defaults and HTTP/2
  integration. The existing request timeouts, middleware, readiness, stable
  internal logging, and graceful shutdown remain unchanged.
- One public address serves exactly one protocol. Etherview does not add a
  second HTTP listener, redirect, ACME client, mTLS policy, multi-certificate
  SNI selection, or certificate hot reload.
- `server.metrics_address` remains plain HTTP. It is an operator-controlled
  operations boundary and never receives the API private key.
- The base production Compose deployment remains HTTP and ships no TLS
  overlay. The full-stack Preview is the checked-in local HTTPS workflow: an
  explicit mkcert target installs local trust and generates an ignored
  localhost pair, which Preview mounts read-only only into `api`. Helm uses an
  independent, pre-existing `apiTLS.existingSecret`, mounts it only into the
  selected `all`/`api` main container, and marks the application Service and
  probes as HTTPS. `ingress.tls` continues to configure the
  client-to-Ingress hop; when both are enabled, the Ingress-to-Pod hop is
  HTTPS as well.
- Certificates are loaded once at process startup. Rotation is completed by a
  controlled process restart or Kubernetes rollout. The chart does not read,
  render, or checksum Secret contents.

## Consequences

Existing HTTP deployments remain compatible unless both TLS paths are
configured. Direct HTTPS deployments fail closed before readiness when
certificate material is missing or invalid. Operators remain responsible for
certificate issuance, trust chains, hostname coverage, expiry monitoring, and
rotation.

Ingress controllers that do not honor the Service's `appProtocol: https` must
receive their controller-specific HTTPS-backend annotation. The Helm
cluster-local test skips hostname verification because a public certificate
normally does not contain the internal Service DNS name; it proves TLS
reachability, while public-chain and hostname verification remain external
deployment checks.
