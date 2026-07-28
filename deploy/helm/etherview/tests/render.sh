#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
chart_dir=${1:-"$script_dir/.."}
helm_bin=${HELM:-helm}
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/etherview-helm-render.XXXXXX")
trap 'rm -rf "$temporary_dir"' EXIT INT TERM

fail() {
  echo "helm render test: $*" >&2
  exit 1
}

kind_count() {
  awk -v wanted="$2" '$0 ~ /^kind: / && $2 == wanted { count++ } END { print count + 0 }' "$1"
}

occurrence_count() {
  awk -v wanted="$2" 'index($0, wanted) != 0 { count++ } END { print count + 0 }' "$1"
}

assert_kind_count() {
  actual=$(kind_count "$1" "$2")
  [ "$actual" -eq "$3" ] || fail "$1 kind $2 count $actual, want $3"
}

assert_occurrences() {
  actual=$(occurrence_count "$1" "$2")
  [ "$actual" -eq "$3" ] || fail "$1 occurrence count for '$2' is $actual, want $3"
}

assert_component_occurrences() {
  actual=$(
    awk -v component="$2" -v wanted="$3" '
      /^---$/ {
        if (selected) {
          total += count
        }
        selected = 0
        count = 0
        next
      }
      $0 == "  name: etherview-" component {
        selected = 1
      }
      selected && index($0, wanted) != 0 {
        count++
      }
      END {
        if (selected) {
          total += count
        }
        print total + 0
      }
    ' "$1"
  )
  [ "$actual" -eq "$4" ] ||
    fail "$1 component $2 occurrence count for '$3' is $actual, want $4"
}

assert_resource_occurrences() {
  actual=$(
    awk -v resource="$2" -v wanted="$3" '
      /^---$/ {
        if (selected) {
          total += count
        }
        selected = 0
        count = 0
        next
      }
      $0 == "  name: " resource {
        selected = 1
      }
      selected && index($0, wanted) != 0 {
        count++
      }
      END {
        if (selected) {
          total += count
        }
        print total + 0
      }
    ' "$1"
  )
  [ "$actual" -eq "$4" ] ||
    fail "$1 resource $2 occurrence count for '$3' is $actual, want $4"
}

assert_contains() {
  grep -F -q -- "$2" "$1" || fail "$1 does not contain '$2'"
}

assert_not_contains() {
  if grep -F -q -- "$2" "$1"; then
    fail "$1 unexpectedly contains '$2'"
  fi
}

assert_all_alerts_scoped() {
  awk '
    /^[[:space:]]*expr:/ {
      count++
      if (index($0, "etherview_release=\"etherview\"") == 0 ||
          index($0, "etherview_namespace=\"explorer\"") == 0) {
        print "unscoped Prometheus expression: " $0 > "/dev/stderr"
        bad++
      }
    }
    END { exit count == 0 || bad != 0 }
  ' "$1" || fail "$1 contains an unscoped Prometheus expression"
}

expect_render_failure() {
  name=$1
  shift
  if "$helm_bin" template etherview "$chart_dir" "$@" >"$temporary_dir/$name.out" 2>"$temporary_dir/$name.err"; then
    fail "$name unexpectedly rendered successfully"
  fi
}

expect_template_failure() {
  name=$1
  shift
  if "$helm_bin" template etherview "$chart_dir" --skip-schema-validation "$@" >"$temporary_dir/$name.out" 2>"$temporary_dir/$name.err"; then
    fail "$name unexpectedly rendered successfully without schema validation"
  fi
}

monolith="$temporary_dir/monolith.yaml"
distributed="$temporary_dir/distributed.yaml"
reference_capacity="$temporary_dir/reference-capacity.yaml"
monolith_service="$temporary_dir/monolith-service.yaml"
distributed_service="$temporary_dir/distributed-service.yaml"
distributed_hpa="$temporary_dir/distributed-hpa.yaml"
monitor_one="$temporary_dir/monitor-one.yaml"
monitor_two="$temporary_dir/monitor-two.yaml"
genesis_monolith="$temporary_dir/genesis-monolith.yaml"
genesis_distributed="$temporary_dir/genesis-distributed.yaml"
genesis_url_monolith="$temporary_dir/genesis-url-monolith.yaml"
genesis_url_distributed="$temporary_dir/genesis-url-distributed.yaml"
genesis_url_without_sha="$temporary_dir/genesis-url-without-sha.yaml"

"$helm_bin" template etherview "$chart_dir" --namespace explorer >"$monolith"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-distributed.yaml" >"$distributed"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-reference-capacity.yaml" >"$reference_capacity"
"$helm_bin" template etherview "$chart_dir" --show-only templates/service.yaml >"$monolith_service"
"$helm_bin" template etherview "$chart_dir" -f "$chart_dir/values-distributed.yaml" \
  --show-only templates/service.yaml >"$distributed_service"
"$helm_bin" template etherview "$chart_dir" -f "$chart_dir/values-distributed.yaml" \
  --show-only templates/hpa.yaml >"$distributed_hpa"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set serviceMonitor.enabled=true --show-only templates/servicemonitor.yaml >"$monitor_one"
"$helm_bin" template etherview-blue "$chart_dir" --namespace explorer \
  --set serviceMonitor.enabled=true --show-only templates/servicemonitor.yaml >"$monitor_two"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.existingClaim=etherview-genesis >"$genesis_monolith"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-distributed.yaml" \
  --set-string genesisState.existingClaim=etherview-genesis >"$genesis_distributed"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set-string genesisState.sha256=1111111111111111111111111111111111111111111111111111111111111111 \
  --set-string genesisState.fetchTimeout=90s >"$genesis_url_monolith"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-distributed.yaml" \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set-string genesisState.sha256=1111111111111111111111111111111111111111111111111111111111111111 \
  --set-string genesisState.fetchTimeout=90s >"$genesis_url_distributed"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.url=https://genesis.example/network.json >"$genesis_url_without_sha"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.fetchTimeout=1s >"$temporary_dir/genesis-timeout-minimum.yaml"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.fetchTimeout=1.5s >"$temporary_dir/genesis-timeout-fractional.yaml"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set-string genesisState.fetchTimeout=5m >"$temporary_dir/genesis-timeout-maximum.yaml"

assert_kind_count "$monolith" Deployment 1
assert_kind_count "$monolith" HorizontalPodAutoscaler 0
assert_kind_count "$monolith" PodDisruptionBudget 0
assert_kind_count "$monolith" Job 1
assert_kind_count "$monolith" NetworkPolicy 1
assert_contains "$monolith" "name: etherview-all"
assert_contains "$monolith" 'args: ["serve", "--config", "/etc/etherview/config.yaml", "--roles=all"]'
assert_contains "$monolith" "log_level: info"
assert_contains "$monolith" "log_format: json"
assert_contains "$monolith" "sync_progress_log_interval: 30s"
assert_occurrences "$monolith" "name: schema-compatibility" 1
assert_occurrences "$monolith" 'args: ["migrate", "status", "--config", "/etc/etherview/config.yaml"]' 1
assert_occurrences "$monolith" 'args: ["migrate", "up", "--config", "/etc/etherview/config.yaml"]' 1
assert_contains "$monolith" "ttlSecondsAfterFinished: 86400"
assert_contains "$monolith_service" "app.kubernetes.io/component: all"
assert_occurrences "$genesis_monolith" "name: ETHERVIEW_CHAIN_GENESIS_FILE" 1
assert_occurrences "$genesis_monolith" "name: genesis-state" 2
assert_contains "$genesis_monolith" 'mountPath: "/var/lib/etherview/genesis.json"'
assert_contains "$genesis_monolith" 'subPath: "genesis.json"'
assert_contains "$genesis_monolith" 'claimName: "etherview-genesis"'
assert_occurrences "$genesis_url_monolith" "name: ETHERVIEW_CHAIN_GENESIS_URL" 1
assert_occurrences "$genesis_url_monolith" "name: ETHERVIEW_CHAIN_GENESIS_SHA256" 1
assert_occurrences "$genesis_url_monolith" "name: ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT" 1
assert_contains "$genesis_url_monolith" 'value: "https://genesis.example/network.json"'
assert_contains "$genesis_url_monolith" 'value: "1111111111111111111111111111111111111111111111111111111111111111"'
assert_contains "$genesis_url_monolith" 'value: "90s"'
assert_not_contains "$genesis_url_monolith" "name: ETHERVIEW_CHAIN_GENESIS_FILE"
assert_not_contains "$genesis_url_monolith" "name: genesis-state"
assert_not_contains "$genesis_url_monolith" "claimName:"
assert_occurrences "$genesis_url_without_sha" "name: ETHERVIEW_CHAIN_GENESIS_URL" 1
assert_occurrences "$genesis_url_without_sha" "name: ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT" 1
assert_not_contains "$genesis_url_without_sha" "name: ETHERVIEW_CHAIN_GENESIS_SHA256"

assert_kind_count "$distributed" Deployment 7
assert_kind_count "$distributed" HorizontalPodAutoscaler 5
assert_kind_count "$distributed" PodDisruptionBudget 0
assert_kind_count "$distributed" Job 1
assert_kind_count "$distributed" NetworkPolicy 1
assert_contains "$distributed" "sync_progress_log_interval: 30s"
assert_contains "$distributed" "alert: EtherviewMetricsSnapshotStale"
assert_contains "$distributed" "alert: EtherviewRepairQueueStalled"
assert_contains "$distributed" "alert: EtherviewRepairExecutionFailures"
assert_contains "$distributed" "alert: EtherviewHTTPHandlerPanics"
assert_contains "$distributed" "alert: EtherviewX402FacilitatorUnavailable"
assert_contains "$distributed" "alert: EtherviewX402LedgerUnavailable"
assert_contains "$distributed" "alert: EtherviewX402SettlementReconciliationRequired"
assert_contains "$distributed" 'sum(increase(etherview_x402_requests_total{etherview_release="etherview",etherview_namespace="explorer",result="verify_unavailable"}[5m]))'
assert_contains "$distributed" 'sum(increase(etherview_x402_requests_total{etherview_release="etherview",etherview_namespace="explorer",result="ledger_unavailable"}[5m]))'
assert_contains "$distributed" 'max(etherview_x402_stale_settling_payments{etherview_release="etherview",etherview_namespace="explorer",reason=~"settlement_unknown|unmarked_after_timeout"})'
assert_contains "$distributed" "targetLabel: etherview_release"
assert_contains "$distributed" 'replacement: "etherview"'
assert_contains "$distributed" "targetLabel: etherview_namespace"
assert_contains "$distributed" 'replacement: "explorer"'
assert_all_alerts_scoped "$distributed"
assert_occurrences "$distributed" 'etherview_release: "etherview"' 15
assert_occurrences "$distributed" 'etherview_namespace: "explorer"' 15
assert_occurrences "$distributed" 'chain_id: "1"' 15
assert_contains "$monitor_one" "app.kubernetes.io/name: etherview"
assert_contains "$monitor_one" "app.kubernetes.io/instance: etherview"
assert_contains "$monitor_one" 'replacement: "etherview"'
assert_contains "$monitor_two" "app.kubernetes.io/instance: etherview-blue"
assert_contains "$monitor_two" 'replacement: "etherview-blue"'
assert_not_contains "$monitor_one" "app.kubernetes.io/instance: etherview-blue"
assert_occurrences "$distributed" "name: schema-compatibility" 7
assert_occurrences "$distributed" 'args: ["migrate", "status", "--config", "/etc/etherview/config.yaml"]' 7
assert_not_contains "$distributed" "name: etherview-all"
assert_contains "$distributed_service" "app.kubernetes.io/component: api"
for role in api sync enrich trace verify metadata maintenance; do
  assert_contains "$distributed" "name: etherview-$role"
  assert_contains "$distributed" "--roles=$role"
done
for role in api enrich trace verify metadata; do
  assert_contains "$distributed_hpa" "name: etherview-$role"
done
assert_not_contains "$distributed_hpa" "name: etherview-sync"
assert_not_contains "$distributed_hpa" "name: etherview-maintenance"
assert_occurrences "$genesis_distributed" "name: ETHERVIEW_CHAIN_GENESIS_FILE" 1
assert_occurrences "$genesis_distributed" "name: genesis-state" 2
assert_occurrences "$genesis_url_distributed" "name: ETHERVIEW_CHAIN_GENESIS_URL" 1
assert_occurrences "$genesis_url_distributed" "name: ETHERVIEW_CHAIN_GENESIS_SHA256" 1
assert_occurrences "$genesis_url_distributed" "name: ETHERVIEW_CHAIN_GENESIS_FETCH_TIMEOUT" 1
assert_not_contains "$genesis_url_distributed" "name: ETHERVIEW_CHAIN_GENESIS_FILE"
assert_not_contains "$genesis_url_distributed" "name: genesis-state"
assert_not_contains "$genesis_url_distributed" "claimName:"

# The reference profile is an HA/capacity starting point, not a result claim.
# It runs only core roles, retains one replica through voluntary disruption,
# hard-spreads each role across hosts, disables rollout surge, and caps the
# steady-state desired application DB pool at 216.
assert_kind_count "$reference_capacity" Deployment 4
assert_kind_count "$reference_capacity" HorizontalPodAutoscaler 2
assert_kind_count "$reference_capacity" PodDisruptionBudget 4
assert_contains "$reference_capacity" "sync_progress_log_interval: 30s"
assert_occurrences "$reference_capacity" "minAvailable: 1" 4
assert_occurrences "$reference_capacity" "minDomains: 2" 4
assert_occurrences "$reference_capacity" 'topologyKey: "kubernetes.io/hostname"' 4
assert_occurrences "$reference_capacity" "whenUnsatisfiable: DoNotSchedule" 4
assert_occurrences "$reference_capacity" 'maxSurge: "0"' 4
assert_occurrences "$reference_capacity" 'maxUnavailable: "1"' 4
assert_contains "$reference_capacity" "max_connections: 12"
assert_contains "$reference_capacity" "backfill_batch_blocks: 64"
for role in api sync enrich maintenance; do
  assert_contains "$reference_capacity" "name: etherview-$role"
done
for role in trace verify metadata; do
  assert_not_contains "$reference_capacity" "name: etherview-$role"
done

# Sensitive runtime inputs are Secret references. The chart does not render a
# Kubernetes Secret containing operator values.
assert_kind_count "$monolith" Secret 0
assert_contains "$monolith" "name: ETHERVIEW_DATABASE_URL"
assert_occurrences "$monolith" "name: ETHERVIEW_DATABASE_READ_URL" 1
assert_contains "$monolith" "name: ETHERVIEW_OTLP_TRACE_ENDPOINT"
assert_occurrences "$distributed" "name: ETHERVIEW_DATABASE_READ_URL" 1
assert_occurrences "$distributed" "name: ETHERVIEW_OTLP_TRACE_ENDPOINT" 7
assert_contains "$monolith" "name: OTEL_EXPORTER_OTLP_HEADERS"
assert_occurrences "$distributed" "name: OTEL_EXPORTER_OTLP_HEADERS" 7
assert_contains "$monolith" 'name: "etherview"'
assert_contains "$monolith" 'key: "database-url"'
assert_contains "$monolith" 'key: "database-read-url"'
assert_contains "$monolith" 'key: "otlp-trace-endpoint"'
assert_contains "$monolith" 'key: "otlp-trace-headers"'
assert_contains "$monolith" "url: \"\""
assert_contains "$monolith" "read_url: \"\""
assert_contains "$monolith" "read_max_connections: 0"
assert_contains "$monolith" "read_min_connections: 0"
assert_contains "$monolith" "api_key_pepper: \"\""
assert_contains "$monolith" "otlp_trace_endpoint: \"\""
assert_not_contains "$monolith" "checksum/secret"
assert_contains "$monolith" "user_auth: false"
assert_not_contains "$monolith" "ETHERVIEW_SESSION_PEPPER"
assert_not_contains "$distributed" "ETHERVIEW_SESSION_PEPPER"
assert_contains "$monolith" "x402_billing: false"
assert_not_contains "$monolith" "ETHERVIEW_X402_FINGERPRINT_PEPPER"
assert_not_contains "$monolith" "ETHERVIEW_X402_FACILITATOR_HEADERS"
assert_not_contains "$distributed" "ETHERVIEW_X402_FINGERPRINT_PEPPER"
assert_not_contains "$distributed" "ETHERVIEW_X402_FACILITATOR_HEADERS"
assert_not_contains "$monolith" "etherview-x402-facilitator"

external_secret="$temporary_dir/external-secret.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set externalSecret.enabled=true \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com \
  --set-string externalSecret.databaseReadURLRemoteKey=runtime/database-read-url \
  --set-string externalSecret.sessionPepperRemoteKey=runtime/session-pepper \
  --set-string externalSecret.natsURLRemoteKey=runtime/nats-url \
  --set-string externalSecret.redisURLRemoteKey=runtime/redis-url \
  --set-string externalSecret.s3AccessKeyRemoteKey=runtime/s3-access \
  --set-string externalSecret.s3SecretKeyRemoteKey=runtime/s3-secret \
  --set-string externalSecret.s3SessionTokenRemoteKey=runtime/s3-session \
  --set-string externalSecret.otlpTraceEndpointRemoteKey=runtime/otlp-trace-endpoint \
  --set-string externalSecret.otlpTraceHeadersRemoteKey=runtime/otlp-trace-headers \
  >"$external_secret"
assert_kind_count "$external_secret" ExternalSecret 1
for remote_key in runtime/database-read-url runtime/session-pepper runtime/nats-url runtime/redis-url runtime/s3-access runtime/s3-secret runtime/s3-session runtime/otlp-trace-endpoint runtime/otlp-trace-headers; do
  assert_contains "$external_secret" "key: \"$remote_key\""
done
for secret_key in database-read-url session-pepper nats-url redis-url s3-access-key s3-secret-key s3-session-token otlp-trace-endpoint otlp-trace-headers; do
  assert_contains "$external_secret" "secretKey: \"$secret_key\""
done
assert_occurrences "$external_secret" "name: ETHERVIEW_SESSION_PEPPER" 1
assert_component_occurrences "$external_secret" all "name: ETHERVIEW_SESSION_PEPPER" 1

external_secret_without_reader="$temporary_dir/external-secret-without-reader.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set externalSecret.enabled=true \
  --set-string externalSecret.sessionPepperRemoteKey=runtime/session-pepper \
  --set-string externalSecret.x402FingerprintPepperRemoteKey=runtime/x402-fingerprint \
  --set-string externalSecret.x402FacilitatorHeadersRemoteKey=runtime/x402-headers \
  >"$external_secret_without_reader"
assert_kind_count "$external_secret_without_reader" ExternalSecret 1
assert_not_contains "$external_secret_without_reader" 'secretKey: "database-read-url"'
assert_not_contains "$external_secret_without_reader" 'secretKey: "session-pepper"'
assert_not_contains "$external_secret_without_reader" 'secretKey: "x402-fingerprint-pepper"'
assert_not_contains "$external_secret_without_reader" 'secretKey: "x402-facilitator-headers"'

auth_monolith="$temporary_dir/auth-monolith.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com \
  >"$auth_monolith"
assert_occurrences "$auth_monolith" "name: ETHERVIEW_SESSION_PEPPER" 1
assert_component_occurrences "$auth_monolith" all "name: ETHERVIEW_SESSION_PEPPER" 1

auth_loopback="$temporary_dir/auth-loopback.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=http://localhost:8080 \
  >"$auth_loopback"
assert_occurrences "$auth_loopback" "name: ETHERVIEW_SESSION_PEPPER" 1

auth_distributed="$temporary_dir/auth-distributed.yaml"
"$helm_bin" template etherview "$chart_dir" \
  -f "$chart_dir/values-distributed.yaml" \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com \
  >"$auth_distributed"
assert_occurrences "$auth_distributed" "name: ETHERVIEW_SESSION_PEPPER" 1
assert_component_occurrences "$auth_distributed" api "name: ETHERVIEW_SESSION_PEPPER" 1
for role in sync enrich trace verify metadata maintenance; do
  assert_component_occurrences "$auth_distributed" "$role" "name: ETHERVIEW_SESSION_PEPPER" 0
done

billing_monolith="$temporary_dir/billing-monolith.yaml"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set config.features.x402_billing=true \
  --set-string config.server.public_url=https://explorer.example.com \
  --set-string config.billing.facilitator_url=https://facilitator.example.com \
  --set-string 'config.billing.facilitator_allowed_cidrs[0]=203.0.113.0/24' \
  --set-string config.billing.network=eip155:84532 \
  --set-string config.billing.asset=0x1111111111111111111111111111111111111111 \
  --set config.billing.asset_decimals=6 \
  --set-string config.billing.asset_eip712_name=USDC \
  --set-string config.billing.asset_eip712_version=2 \
  --set-string config.billing.recipient=0x2222222222222222222222222222222222222222 \
  --set-string config.billing.routes.listBlocks.access=x402 \
  --set-string config.billing.routes.listBlocks.amount_atomic=1000 \
  --set networkPolicy.allowExternalHTTPS=false \
  --set-string 'networkPolicy.runtimeHTTPSCIDRs[0]=198.51.100.0/24' \
  >"$billing_monolith"
assert_kind_count "$billing_monolith" NetworkPolicy 2
assert_contains "$billing_monolith" "name: etherview-x402-facilitator"
assert_resource_occurrences "$billing_monolith" etherview-x402-facilitator 'cidr: "203.0.113.0/24"' 1
assert_resource_occurrences "$billing_monolith" etherview-x402-facilitator "app.kubernetes.io/component: all" 1
assert_resource_occurrences "$billing_monolith" etherview-x402-facilitator "protocol: TCP" 1
assert_resource_occurrences "$billing_monolith" etherview-x402-facilitator "port: 443" 1
assert_resource_occurrences "$billing_monolith" etherview 'cidr: "198.51.100.0/24"' 1
assert_resource_occurrences "$billing_monolith" etherview 'cidr: "203.0.113.0/24"' 0
assert_resource_occurrences "$billing_monolith" etherview-x402-facilitator 'cidr: "198.51.100.0/24"' 0
assert_occurrences "$billing_monolith" "name: ETHERVIEW_X402_FINGERPRINT_PEPPER" 1
assert_occurrences "$billing_monolith" "name: ETHERVIEW_X402_FACILITATOR_HEADERS" 1
assert_component_occurrences "$billing_monolith" all "name: ETHERVIEW_X402_FINGERPRINT_PEPPER" 1
assert_component_occurrences "$billing_monolith" all "name: ETHERVIEW_X402_FACILITATOR_HEADERS" 1

billing_distributed="$temporary_dir/billing-distributed.yaml"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-distributed.yaml" \
  --set config.features.x402_billing=true \
  --set-string config.server.public_url=https://explorer.example.com \
  --set-string config.billing.facilitator_url=https://facilitator.example.com \
  --set-string 'config.billing.facilitator_allowed_cidrs[0]=203.0.113.0/24' \
  --set-string config.billing.network=eip155:84532 \
  --set-string config.billing.asset=0x1111111111111111111111111111111111111111 \
  --set config.billing.asset_decimals=6 \
  --set-string config.billing.asset_eip712_name=USDC \
  --set-string config.billing.asset_eip712_version=2 \
  --set-string config.billing.recipient=0x2222222222222222222222222222222222222222 \
  --set-string config.billing.routes.listBlocks.access=api_key_or_x402 \
  --set-string config.billing.routes.listBlocks.amount_atomic=1000 \
  --set networkPolicy.allowExternalHTTPS=false \
  --set-string 'networkPolicy.runtimeHTTPSCIDRs[0]=198.51.100.0/24' \
  >"$billing_distributed"
assert_kind_count "$billing_distributed" NetworkPolicy 2
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator "app.kubernetes.io/component: api" 1
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator "app.kubernetes.io/component: sync" 0
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator 'cidr: "203.0.113.0/24"' 1
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator "protocol: TCP" 1
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator "port: 443" 1
assert_resource_occurrences "$billing_distributed" etherview 'cidr: "198.51.100.0/24"' 1
assert_resource_occurrences "$billing_distributed" etherview 'cidr: "203.0.113.0/24"' 0
assert_resource_occurrences "$billing_distributed" etherview-x402-facilitator 'cidr: "198.51.100.0/24"' 0
assert_occurrences "$billing_distributed" "name: ETHERVIEW_X402_FINGERPRINT_PEPPER" 1
assert_occurrences "$billing_distributed" "name: ETHERVIEW_X402_FACILITATOR_HEADERS" 1
assert_component_occurrences "$billing_distributed" api "name: ETHERVIEW_X402_FINGERPRINT_PEPPER" 1
assert_component_occurrences "$billing_distributed" api "name: ETHERVIEW_X402_FACILITATOR_HEADERS" 1
for role in sync enrich trace verify metadata maintenance; do
  assert_component_occurrences "$billing_distributed" "$role" "name: ETHERVIEW_X402_FINGERPRINT_PEPPER" 0
  assert_component_occurrences "$billing_distributed" "$role" "name: ETHERVIEW_X402_FACILITATOR_HEADERS" 0
done

billing_external_secret="$temporary_dir/billing-external-secret.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set externalSecret.enabled=true \
  --set-string externalSecret.x402FingerprintPepperRemoteKey=runtime/x402-fingerprint \
  --set-string externalSecret.x402FacilitatorHeadersRemoteKey=runtime/x402-headers \
  --set config.features.x402_billing=true \
  --set-string config.server.public_url=https://explorer.example.com \
  --set-string config.billing.facilitator_url=https://facilitator.example.com \
  --set-string 'config.billing.facilitator_allowed_cidrs[0]=203.0.113.0/24' \
  --set-string config.billing.network=eip155:84532 \
  --set-string config.billing.asset=0x1111111111111111111111111111111111111111 \
  --set config.billing.asset_decimals=6 \
  --set-string config.billing.asset_eip712_name=USDC \
  --set-string config.billing.asset_eip712_version=2 \
  --set-string config.billing.recipient=0x2222222222222222222222222222222222222222 \
  --set networkPolicy.allowExternalHTTPS=false \
  --set-string 'networkPolicy.runtimeHTTPSCIDRs[0]=198.51.100.0/24' \
  >"$billing_external_secret"
assert_contains "$billing_external_secret" 'secretKey: "x402-fingerprint-pepper"'
assert_contains "$billing_external_secret" 'secretKey: "x402-facilitator-headers"'
assert_contains "$billing_external_secret" 'key: "runtime/x402-fingerprint"'
assert_contains "$billing_external_secret" 'key: "runtime/x402-headers"'

billing_restricted_egress="$temporary_dir/billing-restricted-egress.yaml"
"$helm_bin" template etherview "$chart_dir" \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"protocol":"TCP","port":4222}]},{"to":[{"ipBlock":{"cidr":"198.51.100.0/24"}}],"ports":[{"protocol":"TCP","port":8443}]}]' \
  >"$billing_restricted_egress"
assert_resource_occurrences "$billing_restricted_egress" etherview "port: 4222" 1
assert_resource_occurrences "$billing_restricted_egress" etherview 'cidr: 198.51.100.0/24' 1
assert_resource_occurrences "$billing_restricted_egress" etherview "port: 8443" 1

# Default policy carries only DNS, PostgreSQL, and optional HTTPS egress. A
# release can append accelerator or private endpoint rules without replacing
# those correctness-critical entries.
assert_contains "$monolith" "policyTypes: [Ingress, Egress]"
assert_contains "$monolith" "port: 53"
assert_contains "$monolith" "port: 5432"
assert_contains "$monolith" "port: 443"
network_custom="$temporary_dir/network-custom.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set 'networkPolicy.additionalEgress[0].ports[0].protocol=TCP' \
  --set 'networkPolicy.additionalEgress[0].ports[0].port=4222' \
  >"$network_custom"
assert_contains "$network_custom" "port: 4222"
network_disabled="$temporary_dir/network-disabled.yaml"
"$helm_bin" template etherview "$chart_dir" --set networkPolicy.enabled=false >"$network_disabled"
assert_kind_count "$network_disabled" NetworkPolicy 0

# Reject configurations that would leave the Service without its selected
# role, make an HPA internally inconsistent, or put credentials in a ConfigMap.
expect_render_failure monolith-without-all --set roles.all.enabled=false
expect_render_failure distributed-without-api -f "$chart_dir/values-distributed.yaml" \
  --set roles.api.enabled=false
expect_render_failure invalid-hpa --set roles.api.autoscaling.minReplicas=5 \
  --set roles.api.autoscaling.maxReplicas=2
expect_render_failure invalid-pdb --set podDisruptionBudget.enabled=true \
  --set podDisruptionBudget.minAvailable=0
expect_render_failure invalid-role-topology-spread \
  --set roleTopologySpread.enabled=true --set roleTopologySpread.minDomains=1
expect_render_failure soft-role-topology-spread \
  --set roleTopologySpread.enabled=true \
  --set roleTopologySpread.whenUnsatisfiable=ScheduleAnyway
expect_render_failure duplicate-topology-spread-inputs \
  --set roleTopologySpread.enabled=true \
  --set 'topologySpreadConstraints[0].maxSkew=1'
expect_render_failure zero-capacity-deployment-strategy \
  --set-string deploymentStrategy.maxSurge=0 \
  --set-string deploymentStrategy.maxUnavailable=0
expect_render_failure inline-database-secret \
  --set-string config.database.url=postgres://inline.invalid/etherview
expect_render_failure inline-genesis-path \
  --set-string config.chain.genesis_file=/var/lib/etherview/genesis.json
expect_render_failure inline-genesis-url \
  --set-string config.chain.genesis_url=https://genesis.example/network.json
expect_render_failure inline-genesis-sha256 \
  --set-string config.chain.genesis_sha256=1111111111111111111111111111111111111111111111111111111111111111
expect_render_failure genesis-with-nonzero-start \
  --set-string genesisState.existingClaim=etherview-genesis \
  --set config.chain.start_block=1
expect_render_failure genesis-source-conflict \
  --set-string genesisState.existingClaim=etherview-genesis \
  --set-string genesisState.url=https://genesis.example/network.json
expect_render_failure genesis-url-with-nonzero-start \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set config.chain.start_block=1
expect_render_failure genesis-sha-without-url \
  --set-string genesisState.sha256=1111111111111111111111111111111111111111111111111111111111111111
expect_render_failure zero-genesis-sha \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set-string genesisState.sha256=0000000000000000000000000000000000000000000000000000000000000000
expect_render_failure uppercase-genesis-sha \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set-string genesisState.sha256=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
expect_render_failure short-genesis-sha \
  --set-string genesisState.url=https://genesis.example/network.json \
  --set-string genesisState.sha256=111111111111111111111111111111111111111111111111111111111111111
expect_render_failure insecure-genesis-url \
  --set-string genesisState.url=http://genesis.example/network.json
expect_render_failure genesis-url-with-query \
  --set-string 'genesisState.url=https://genesis.example/network.json?token=secret'
expect_render_failure genesis-url-with-fragment \
  --set-string 'genesisState.url=https://genesis.example/network.json#sha256'
expect_render_failure genesis-url-with-userinfo \
  --set-string genesisState.url=https://user:pass@genesis.example/network.json
expect_render_failure genesis-url-with-nondefault-port \
  --set-string genesisState.url=https://genesis.example:8443/network.json
expect_render_failure genesis-url-with-traversal \
  --set-string genesisState.url=https://genesis.example/../network.json
expect_render_failure invalid-genesis-fetch-timeout \
  --set-string genesisState.fetchTimeout=soon
expect_render_failure short-genesis-fetch-timeout \
  --set-string genesisState.fetchTimeout=999ms
expect_render_failure zero-genesis-fetch-timeout \
  --set-string genesisState.fetchTimeout=0s
expect_render_failure long-genesis-fetch-timeout \
  --set-string genesisState.fetchTimeout=5m1s
expect_render_failure above-maximum-genesis-fetch-timeout \
  --set-string genesisState.fetchTimeout=300.1s
expect_render_failure invalid-inline-genesis-fetch-timeout \
  --set-string config.chain.genesis_fetch_timeout=6m
expect_render_failure relative-genesis-mount \
  --set-string genesisState.existingClaim=etherview-genesis \
  --set-string genesisState.mountPath=genesis.json
expect_render_failure inline-database-read-secret \
  --set-string config.database.read_url=postgres://inline-read.invalid/etherview
expect_render_failure invalid-writer-connection-bounds \
  --set config.database.max_connections=1 \
  --set config.database.min_connections=2
expect_render_failure invalid-effective-reader-inherited-min \
  --set config.database.read_max_connections=1
expect_render_failure invalid-effective-reader-inherited-max \
  --set config.database.read_min_connections=21
expect_render_failure invalid-explicit-reader-connection-bounds \
  --set config.database.read_max_connections=2 \
  --set config.database.read_min_connections=3
expect_render_failure reader-max-int32-overflow \
  --set config.database.read_max_connections=2147483648
expect_render_failure reader-min-int32-overflow \
  --set config.database.read_min_connections=2147483648
expect_render_failure writer-max-int32-overflow \
  --set config.database.max_connections=2147483648
expect_render_failure inline-rpc-secret \
  --set-string 'config.rpc.endpoints[0].name=inline' \
  --set-string 'config.rpc.endpoints[0].url=https://credential.invalid' \
  --set-string 'config.rpc.endpoints[0].purposes[0]=all'
expect_render_failure inline-redis-secret \
  --set-string config.adapters.redis_url=redis://credential.invalid:6379
expect_render_failure inline-s3-secret \
  --set-string config.adapters.s3_secret_key=inline-secret
expect_render_failure inline-session-pepper \
  --set-string config.user_auth.session_pepper=inline-secret
expect_render_failure inline-x402-fingerprint-pepper \
  --set-string config.billing.fingerprint_pepper=inline-secret
expect_render_failure inline-x402-facilitator-headers \
  --set-string config.billing.facilitator_headers.Authorization=inline-secret
expect_render_failure inline-otlp-endpoint \
  --set-string config.observability.otlp_trace_endpoint=https://otel.invalid:4318
expect_render_failure invalid-log-level \
  --set-string config.observability.log_level=INFO
expect_render_failure invalid-log-format \
  --set-string config.observability.log_format=console
expect_render_failure incomplete-s3-external-secret \
  --set-string externalSecret.s3AccessKeyRemoteKey=runtime/s3-access
expect_render_failure auth-without-public-origin \
  --set config.features.user_auth=true
expect_render_failure auth-with-non-root-public-url \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com/nested
expect_render_failure auth-with-plaintext-public-url \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=http://explorer.example.com
expect_render_failure auth-with-empty-host \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://:
expect_render_failure auth-with-nonnumeric-port \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com:bad
expect_render_failure auth-with-overflow-port \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com:65536
expect_render_failure auth-with-zero-port \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com:0
expect_render_failure auth-with-unclosed-ipv6 \
  --set config.features.user_auth=true \
  --set-string 'config.server.public_url=https://[::1'
expect_render_failure auth-with-trailing-dot \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com.
expect_render_failure external-auth-without-session-pepper \
  --set externalSecret.enabled=true \
  --set config.features.user_auth=true \
  --set-string config.server.public_url=https://explorer.example.com
expect_render_failure billing-without-public-origin \
  -f "$script_dir/values-x402.yaml" \
  --set-string config.server.public_url=
expect_render_failure billing-without-facilitator-cidr \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'config.billing.facilitator_allowed_cidrs=[]'
expect_render_failure billing-with-network-policy-disabled \
  -f "$script_dir/values-x402.yaml" \
  --set networkPolicy.enabled=false
expect_render_failure billing-with-broad-https \
  -f "$script_dir/values-x402.yaml" \
  --set networkPolicy.allowExternalHTTPS=true
expect_template_failure billing-with-broad-runtime-https \
  -f "$script_dir/values-x402.yaml" \
  --set-string 'networkPolicy.runtimeHTTPSCIDRs[0]=0.0.0.0/0'
assert_contains "$temporary_dir/billing-with-broad-runtime-https.err" \
  "networkPolicy.runtimeHTTPSCIDRs must not contain an internet-wide CIDR"
expect_template_failure billing-with-facilitator-runtime-overlap \
  -f "$script_dir/values-x402.yaml" \
  --set-string 'networkPolicy.runtimeHTTPSCIDRs[0]=203.0.113.0/24'
assert_contains "$temporary_dir/billing-with-facilitator-runtime-overlap.err" \
  "networkPolicy.runtimeHTTPSCIDRs must not repeat a facilitator CIDR"
expect_render_failure billing-with-additional-tcp-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"protocol":"TCP","port":443}]}]'
assert_contains "$temporary_dir/billing-with-additional-tcp-443.err" \
  "/networkPolicy/additionalEgress"
expect_template_failure billing-template-with-additional-tcp-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"protocol":"TCP","port":443}]}]'
assert_contains "$temporary_dir/billing-template-with-additional-tcp-443.err" \
  "networkPolicy.additionalEgress must explicitly exclude TCP/443"
expect_render_failure billing-with-additional-empty-to-tcp-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"to":[],"ports":[{"protocol":"TCP","port":443}]}]'
expect_render_failure billing-with-additional-ipv4-any-tcp-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"to":[{"ipBlock":{"cidr":"0.0.0.0/0"}}],"ports":[{"protocol":"TCP","port":443}]}]'
expect_render_failure billing-with-additional-ipv6-any-tcp-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"to":[{"ipBlock":{"cidr":"::/0"}}],"ports":[{"protocol":"TCP","port":443}]}]'
expect_render_failure billing-with-additional-implicit-ports \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"to":[{"ipBlock":{"cidr":"198.51.100.0/24"}}]}]'
expect_render_failure billing-with-additional-default-protocol-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"port":443}]}]'
expect_render_failure billing-with-additional-named-tcp-port \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"protocol":"TCP","port":"https"}]}]'
expect_render_failure billing-with-additional-tcp-range-spanning-443 \
  -f "$script_dir/values-x402.yaml" \
  --set-json 'networkPolicy.additionalEgress=[{"ports":[{"protocol":"TCP","port":400,"endPort":500}]}]'
expect_render_failure billing-with-non-root-facilitator \
  -f "$script_dir/values-x402.yaml" \
  --set-string config.billing.facilitator_url=https://facilitator.example.com/nested
expect_render_failure billing-with-non-443-facilitator \
  -f "$script_dir/values-x402.yaml" \
  --set-string config.billing.facilitator_url=https://facilitator.example.com:8443
expect_render_failure external-billing-without-fingerprint-pepper \
  -f "$script_dir/values-x402.yaml" \
  --set externalSecret.enabled=true \
  --set-string externalSecret.x402FingerprintPepperRemoteKey=
expect_render_failure invalid-x402-operation \
  --set-string config.billing.routes.notEligible.access=x402 \
  --set-string config.billing.routes.notEligible.amount_atomic=1
expect_render_failure invalid-x402-access \
  --set-string config.billing.routes.listBlocks.access=free \
  --set-string config.billing.routes.listBlocks.amount_atomic=1
expect_render_failure invalid-x402-amount \
  --set-string config.billing.routes.listBlocks.access=x402 \
  --set-string config.billing.routes.listBlocks.amount_atomic=01
expect_render_failure alert-rules-without-scoped-monitor \
  --set prometheusRule.enabled=true \
  --set serviceMonitor.enabled=false

echo "helm render test: PASS"
