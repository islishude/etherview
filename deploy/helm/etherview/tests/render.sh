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

monolith="$temporary_dir/monolith.yaml"
distributed="$temporary_dir/distributed.yaml"
monolith_service="$temporary_dir/monolith-service.yaml"
distributed_service="$temporary_dir/distributed-service.yaml"
distributed_hpa="$temporary_dir/distributed-hpa.yaml"
monitor_one="$temporary_dir/monitor-one.yaml"
monitor_two="$temporary_dir/monitor-two.yaml"

"$helm_bin" template etherview "$chart_dir" --namespace explorer >"$monolith"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  -f "$chart_dir/values-distributed.yaml" >"$distributed"
"$helm_bin" template etherview "$chart_dir" --show-only templates/service.yaml >"$monolith_service"
"$helm_bin" template etherview "$chart_dir" -f "$chart_dir/values-distributed.yaml" \
  --show-only templates/service.yaml >"$distributed_service"
"$helm_bin" template etherview "$chart_dir" -f "$chart_dir/values-distributed.yaml" \
  --show-only templates/hpa.yaml >"$distributed_hpa"
"$helm_bin" template etherview "$chart_dir" --namespace explorer \
  --set serviceMonitor.enabled=true --show-only templates/servicemonitor.yaml >"$monitor_one"
"$helm_bin" template etherview-blue "$chart_dir" --namespace explorer \
  --set serviceMonitor.enabled=true --show-only templates/servicemonitor.yaml >"$monitor_two"

assert_kind_count "$monolith" Deployment 1
assert_kind_count "$monolith" HorizontalPodAutoscaler 0
assert_kind_count "$monolith" Job 1
assert_kind_count "$monolith" NetworkPolicy 1
assert_contains "$monolith" "name: etherview-all"
assert_contains "$monolith" 'args: ["serve", "--config", "/etc/etherview/config.yaml", "--roles=all"]'
assert_occurrences "$monolith" "name: schema-compatibility" 1
assert_occurrences "$monolith" 'args: ["migrate", "status", "--config", "/etc/etherview/config.yaml"]' 1
assert_occurrences "$monolith" 'args: ["migrate", "up", "--config", "/etc/etherview/config.yaml"]' 1
assert_contains "$monolith" "ttlSecondsAfterFinished: 86400"
assert_contains "$monolith_service" "app.kubernetes.io/component: all"

assert_kind_count "$distributed" Deployment 7
assert_kind_count "$distributed" HorizontalPodAutoscaler 5
assert_kind_count "$distributed" Job 1
assert_kind_count "$distributed" NetworkPolicy 1
assert_contains "$distributed" "alert: EtherviewMetricsSnapshotStale"
assert_contains "$distributed" "alert: EtherviewRepairQueueStalled"
assert_contains "$distributed" "alert: EtherviewRepairExecutionFailures"
assert_contains "$distributed" "alert: EtherviewHTTPHandlerPanics"
assert_contains "$distributed" "targetLabel: etherview_release"
assert_contains "$distributed" 'replacement: "etherview"'
assert_contains "$distributed" "targetLabel: etherview_namespace"
assert_contains "$distributed" 'replacement: "explorer"'
assert_all_alerts_scoped "$distributed"
assert_occurrences "$distributed" 'etherview_release: "etherview"' 12
assert_occurrences "$distributed" 'etherview_namespace: "explorer"' 12
assert_occurrences "$distributed" 'chain_id: "1"' 12
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

# Sensitive runtime inputs are Secret references. The chart does not render a
# Kubernetes Secret containing operator values.
assert_kind_count "$monolith" Secret 0
assert_contains "$monolith" "name: ETHERVIEW_DATABASE_URL"
assert_contains "$monolith" "name: ETHERVIEW_OTLP_TRACE_ENDPOINT"
assert_occurrences "$distributed" "name: ETHERVIEW_OTLP_TRACE_ENDPOINT" 7
assert_contains "$monolith" "name: OTEL_EXPORTER_OTLP_HEADERS"
assert_occurrences "$distributed" "name: OTEL_EXPORTER_OTLP_HEADERS" 7
assert_contains "$monolith" 'name: "etherview"'
assert_contains "$monolith" 'key: "database-url"'
assert_contains "$monolith" 'key: "otlp-trace-endpoint"'
assert_contains "$monolith" 'key: "otlp-trace-headers"'
assert_contains "$monolith" "url: \"\""
assert_contains "$monolith" "api_key_pepper: \"\""
assert_contains "$monolith" "otlp_trace_endpoint: \"\""

external_secret="$temporary_dir/external-secret.yaml"
"$helm_bin" template etherview "$chart_dir" \
  --set externalSecret.enabled=true \
  --set-string externalSecret.natsURLRemoteKey=runtime/nats-url \
  --set-string externalSecret.redisURLRemoteKey=runtime/redis-url \
  --set-string externalSecret.s3AccessKeyRemoteKey=runtime/s3-access \
  --set-string externalSecret.s3SecretKeyRemoteKey=runtime/s3-secret \
  --set-string externalSecret.s3SessionTokenRemoteKey=runtime/s3-session \
  --set-string externalSecret.otlpTraceEndpointRemoteKey=runtime/otlp-trace-endpoint \
  --set-string externalSecret.otlpTraceHeadersRemoteKey=runtime/otlp-trace-headers \
  >"$external_secret"
assert_kind_count "$external_secret" ExternalSecret 1
for remote_key in runtime/nats-url runtime/redis-url runtime/s3-access runtime/s3-secret runtime/s3-session runtime/otlp-trace-endpoint runtime/otlp-trace-headers; do
  assert_contains "$external_secret" "key: \"$remote_key\""
done
for secret_key in nats-url redis-url s3-access-key s3-secret-key s3-session-token otlp-trace-endpoint otlp-trace-headers; do
  assert_contains "$external_secret" "secretKey: \"$secret_key\""
done

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
expect_render_failure inline-database-secret \
  --set-string config.database.url=postgres://inline.invalid/etherview
expect_render_failure inline-rpc-secret \
  --set-string 'config.rpc.endpoints[0].name=inline' \
  --set-string 'config.rpc.endpoints[0].url=https://credential.invalid' \
  --set-string 'config.rpc.endpoints[0].purposes[0]=all'
expect_render_failure inline-redis-secret \
  --set-string config.adapters.redis_url=redis://credential.invalid:6379
expect_render_failure inline-s3-secret \
  --set-string config.adapters.s3_secret_key=inline-secret
expect_render_failure inline-otlp-endpoint \
  --set-string config.observability.otlp_trace_endpoint=https://otel.invalid:4318
expect_render_failure incomplete-s3-external-secret \
  --set-string externalSecret.s3AccessKeyRemoteKey=runtime/s3-access
expect_render_failure alert-rules-without-scoped-monitor \
  --set prometheusRule.enabled=true \
  --set serviceMonitor.enabled=false

echo "helm render test: PASS"
