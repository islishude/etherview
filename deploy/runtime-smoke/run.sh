#!/bin/sh
set -eu

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_directory/../.." && pwd)
docker_command=${DOCKER:-docker}
image=${IMAGE:-etherview:local}
fixture_image=${RUNTIME_FIXTURE_IMAGE:-etherview-runtime-fixture:local}
loadtest_image=${RUNTIME_LOADTEST_IMAGE:-etherview-runtime-loadtest:local}
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/etherview-runtime-smoke.XXXXXX")
monolith_project="etherview-runtime-monolith-$$"
distributed_project="etherview-runtime-distributed-$$"
failed=true

compose() {
    "$docker_command" compose \
        -f "$repository_root/compose.yaml" \
        -f "$script_directory/compose.yaml" \
        "$@"
}

cleanup() {
    exit_code=$?
    trap - EXIT INT TERM
    if [ "$failed" = true ] || [ "$exit_code" -ne 0 ]; then
        echo "compose-runtime-smoke: failure logs (monolith)" >&2
        compose -p "$monolith_project" --profile monolith logs --no-color >&2 || true
        echo "compose-runtime-smoke: failure logs (distributed)" >&2
        compose -p "$distributed_project" --profile distributed logs --no-color >&2 || true
    fi
    compose -p "$monolith_project" --profile monolith --profile runtime-tools down --volumes --remove-orphans >/dev/null 2>&1 || true
    compose -p "$distributed_project" --profile distributed --profile runtime-tools down --volumes --remove-orphans >/dev/null 2>&1 || true
    if [ "${RUNTIME_SMOKE_KEEP_ARTIFACTS:-false}" = true ]; then
        echo "compose-runtime-smoke: retained artifacts at $temporary_directory" >&2
    else
        rm -r "$temporary_directory"
    fi
    exit "$exit_code"
}
trap cleanup EXIT INT TERM

for dependency in curl diff jq tar; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
        echo "compose-runtime-smoke: missing required command $dependency" >&2
        exit 1
    fi
done
if ! "$docker_command" compose version >/dev/null 2>&1; then
    echo "compose-runtime-smoke: Docker Compose v2 is required" >&2
    exit 1
fi
"$docker_command" image inspect "$image" >/dev/null

export ETHERVIEW_IMAGE=$image
export ETHERVIEW_RUNTIME_FIXTURE_IMAGE=$fixture_image
export ETHERVIEW_RUNTIME_LOADTEST_IMAGE=$loadtest_image
export ETHERVIEW_CONFIG_FILE=$repository_root/deploy/config.example.yaml
export ETHERVIEW_RPC_URLS=http://runtime-fixture:8545
export ETHERVIEW_CHAIN_ID=1
export ETHERVIEW_CHAIN_GENESIS_HASH=0x0000000000000000000000000000000000000000000000000000000000000100
export ETHERVIEW_API_KEY_PEPPER=
export ETHERVIEW_METADATA_IPFS_GATEWAY=
export ETHERVIEW_ADAPTER_NAMESPACE=runtime-smoke
export ETHERVIEW_NATS_URL=
export ETHERVIEW_REDIS_URL=
export ETHERVIEW_S3_ENDPOINT=
export ETHERVIEW_S3_BUCKET=
export ETHERVIEW_S3_PREFIX=runtime-smoke
export ETHERVIEW_S3_REGION=
export ETHERVIEW_S3_ACCESS_KEY=
export ETHERVIEW_S3_SECRET_KEY=
export ETHERVIEW_S3_SESSION_TOKEN=
export ETHERVIEW_S3_PATH_STYLE=false
export ETHERVIEW_OTLP_TRACE_ENDPOINT=
export ETHERVIEW_OTLP_TRACE_INSECURE=false
export OTEL_EXPORTER_OTLP_HEADERS=
export ETHERVIEW_PORT=0
export ETHERVIEW_METRICS_PORT=0
export POSTGRES_PASSWORD=etherview-runtime-smoke

compose --profile monolith --profile runtime-tools build runtime-fixture runtime-loadtest

database_query() {
    project=$1
    query=$2
    compose -p "$project" exec -T postgres \
        psql -X -qAt -v ON_ERROR_STOP=1 -U etherview -d etherview -c "$query"
}

assert_service_replicas() {
    project=$1
    service=$2
    expected_replicas=$3
    container_ids=$(compose -p "$project" ps -q "$service")
    actual_replicas=$(printf '%s\n' "$container_ids" | sed '/^$/d' | wc -l | tr -d ' ')
    if [ "$actual_replicas" != "$expected_replicas" ]; then
        echo "compose-runtime-smoke: $project service $service has $actual_replicas running replicas, want $expected_replicas" >&2
        return 1
    fi
    for container_id in $container_ids; do
        state=$("$docker_command" container inspect --format '{{.State.Status}} {{.RestartCount}}' "$container_id")
        if [ "$state" != "running 0" ]; then
            echo "compose-runtime-smoke: $project service $service state is $state" >&2
            return 1
        fi
    done
}

assert_migration_completed() {
    project=$1
    container_id=$(compose -p "$project" ps -a -q migration)
    if [ -z "$container_id" ]; then
        echo "compose-runtime-smoke: $project migration has no container" >&2
        return 1
    fi
    state=$("$docker_command" container inspect --format '{{.State.Status}} {{.State.ExitCode}}' "$container_id")
    if [ "$state" != "exited 0" ]; then
        echo "compose-runtime-smoke: $project migration state is $state" >&2
        return 1
    fi
}

public_port() {
    project=$1
    service=$2
    binding=$(compose -p "$project" port "$service" 8080 | sed -n '1p')
    port=${binding##*:}
    case "$port" in
        "" | *[!0-9]*)
            echo "compose-runtime-smoke: cannot resolve $project $service public port from $binding" >&2
            return 1
            ;;
    esac
    printf '%s\n' "$port"
}

wait_for_ready() {
    ready_url=$1
    ready_attempt=0
    while [ "$ready_attempt" -lt 150 ]; do
        if curl --fail --silent --show-error --max-time 2 "$ready_url/health/ready" >/dev/null 2>&1; then
            return 0
        fi
        ready_attempt=$((ready_attempt + 1))
        sleep 1
    done
    echo "compose-runtime-smoke: $ready_url did not become ready" >&2
    return 1
}

wait_for_service_operational() {
    operational_project=$1
    operational_service=$2
    operational_expected_replicas=$3
    assert_service_replicas "$operational_project" "$operational_service" "$operational_expected_replicas"
    operational_container_ids=$(compose -p "$operational_project" ps -q "$operational_service")
    for operational_container_id in $operational_container_ids; do
        operational_binding=$("$docker_command" container port "$operational_container_id" 9090/tcp | sed -n '1p')
        operational_port=${operational_binding##*:}
        case "$operational_port" in
            "" | *[!0-9]*)
                echo "compose-runtime-smoke: cannot resolve operational port for $operational_service container $operational_container_id from $operational_binding" >&2
                return 1
                ;;
        esac
        wait_for_ready "http://127.0.0.1:$operational_port"
    done
}

wait_for_publications() {
    project=$1
    expected_publications=$2
    expected_head=$3
    expected_hash=$4
    expected_state="0:$expected_publications:5:0:0:$expected_head:$expected_hash:true:$expected_head:$expected_head:$expected_head:true:true"
    attempt=0
    while [ "$attempt" -lt 180 ]; do
        state=$(database_query "$project" "
            SELECT
                (SELECT count(*) FROM durable_jobs WHERE status IN ('queued', 'leased'))::text || ':' ||
                (SELECT count(*) FROM published_block_stage_results
                  WHERE chain_id = 1
                    AND state = 'complete'
                    AND (stage, stage_version) IN (
                        ('proxy', 1), ('abi', 1), ('token', 1), ('stats', 2), ('trace', 1)
                    ))::text || ':' ||
                (SELECT count(*) FROM published_block_stage_results
                  WHERE chain_id = 1
                    AND block_number = $expected_head
                    AND state = 'complete'
                    AND (stage, stage_version) IN (
                        ('proxy', 1), ('abi', 1), ('token', 1), ('stats', 2), ('trace', 1)
                    ))::text || ':' ||
                (SELECT count(*) FROM durable_jobs WHERE status IN ('failed', 'cancelled'))::text || ':' ||
                (SELECT count(*) FROM transactional_outbox WHERE published_at IS NULL)::text || ':' ||
                COALESCE((SELECT contiguous_through::text FROM index_checkpoints
                           WHERE chain_id = 1 AND stage = 'core'), '') || ':' ||
                COALESCE((SELECT encode(block_hash, 'hex') FROM index_checkpoints
                           WHERE chain_id = 1 AND stage = 'core'), '') || ':' ||
                (SELECT (count(*) = 1 AND min(range_start) = 0 AND max(range_end) = $expected_head)::text
                   FROM core_coverage_ranges WHERE chain_id = 1) || ':' ||
                COALESCE((SELECT latest_number::text FROM sync_runtime_status WHERE chain_id = 1), '') || ':' ||
                COALESCE((SELECT indexed_number::text FROM sync_runtime_status WHERE chain_id = 1), '') || ':' ||
                COALESCE((SELECT highest_covered_number::text FROM sync_runtime_status WHERE chain_id = 1), '') || ':' ||
                COALESCE((SELECT ready::text FROM sync_runtime_status WHERE chain_id = 1), 'false') || ':' ||
                COALESCE((SELECT backfill_complete::text FROM sync_runtime_status WHERE chain_id = 1), 'false');
        ")
        if [ "$state" = "$expected_state" ]; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    echo "compose-runtime-smoke: $project did not reach head $expected_head with $expected_publications complete publications, zero lag, and a drained outbox (last=$state)" >&2
    return 1
}

advance_fixture() {
    project=$1
    compose -p "$project" exec -T runtime-fixture /runtimefixture advance
}

capture_state() {
    project=$1
    output=$2
    compose -p "$project" exec -T postgres \
        psql -X -qAt -v ON_ERROR_STOP=1 -U etherview -d etherview \
        <"$script_directory/state-digest.sql" >"$output"
}

capture_api() {
    base_url=$1
    output=$2
    spa_output=$3

    : >"$output"
    capture_endpoint "$base_url" config "/api/v1/config" \
        'del(.meta.request_id)' "$output"
    capture_endpoint "$base_url" status "/api/v1/status" \
        'del(.meta.request_id)' "$output"
    capture_endpoint "$base_url" blocks "/api/v1/blocks?limit=10" \
        'del(.meta.request_id, .meta.next_cursor)' "$output"
    capture_endpoint "$base_url" transactions "/api/v1/transactions?limit=10" \
        'del(.meta.request_id, .meta.next_cursor)' "$output"
    capture_endpoint "$base_url" address "/api/v1/addresses/$from_address" \
        'del(.meta.request_id)' "$output"
    capture_endpoint "$base_url" trace "/api/v1/transactions/$transaction_hash/trace" \
        'del(.meta.request_id)' "$output"
    capture_endpoint "$base_url" pending "/api/v1/pending?limit=10" \
        'del(.meta.request_id, .meta.snapshot_at, .meta.expires_at, .meta.snapshot_id, .meta.next_cursor)
         | .data |= map(del(.first_seen_at, .last_seen_at, .expires_at))' "$output"
    curl --fail --silent --show-error --max-time 5 "$base_url/" >"$spa_output"

    config=$(curl --fail --silent --show-error --max-time 5 "$base_url/api/v1/config")
    printf '%s' "$config" | jq -e '
        .data.features.trace == true and
        .data.features.mempool == true and
        .data.features.historical_state == true and
        .data.features.nft_metadata == true and
        .data.features.verification == false and
        .data.features.sourcify == false and
        .data.features.pricing == false
    ' >/dev/null
    pending=$(curl --fail --silent --show-error --max-time 5 "$base_url/api/v1/pending?limit=10")
    printf '%s' "$pending" | jq -e --arg hash "$pending_hash" \
        '.data | length == 1 and .[0].hash == $hash' >/dev/null
    trace=$(curl --fail --silent --show-error --max-time 5 "$base_url/api/v1/transactions/$transaction_hash/trace")
    printf '%s' "$trace" | jq -e \
        '.data.state == "complete" and (.data.frames | length) == 1' >/dev/null
    address=$(curl --fail --silent --show-error --max-time 5 "$base_url/api/v1/addresses/$from_address")
    printf '%s' "$address" | jq -e --arg block "$final_block_hash" '
        .data.balance == "5" and
        .data.nonce == "1" and
        .data.type == "eoa" and
        .data.at_block == $block and
        .data.completeness.state == "complete"
    ' >/dev/null
}

capture_endpoint() {
    base_url=$1
    label=$2
    path=$3
    filter=$4
    output=$5
    body=$(curl --fail --silent --show-error --max-time 5 "$base_url$path")
    normalized=$(printf '%s' "$body" | jq -cS "$filter")
    printf '%s\t%s\n' "$label" "$normalized" >>"$output"
}

verify_distributed_replica_survival() {
    project=$1
    base_url=$2
    wait_for_service_operational "$project" sync 2
    wait_for_service_operational "$project" enrich 2
    sync_container=$(compose -p "$project" ps -q sync | sed -n '1p')
    enrich_container=$(compose -p "$project" ps -q enrich | sed -n '1p')
    if [ -z "$sync_container" ] || [ -z "$enrich_container" ]; then
        echo "compose-runtime-smoke: cannot select distributed replicas for failover check" >&2
        return 1
    fi
    "$docker_command" container stop --time 10 "$sync_container" "$enrich_container" >/dev/null
    wait_for_ready "$base_url"
    assert_service_replicas "$project" api 1
    wait_for_service_operational "$project" sync 1
    wait_for_service_operational "$project" enrich 1
    advance_fixture "$project"
    wait_for_publications "$project" 15 2 "$block_two_hash_hex"
    wait_for_ready "$base_url"
    wait_for_service_operational "$project" sync 1
    wait_for_service_operational "$project" enrich 1
    echo "compose-runtime-smoke: surviving sync and enrich replicas processed block 2 after peer shutdown"
}

start_config_only_role_first() {
    project=$1
    compose -p "$project" --profile distributed up -d --wait --wait-timeout 90 verify
    assert_migration_completed "$project"
    assert_service_replicas "$project" verify 1
    assert_service_replicas "$project" runtime-fixture 1
    assert_service_replicas "$project" postgres 1

    identity_attempt=0
    identity=
    while [ "$identity_attempt" -lt 20 ]; do
        identity=$(database_query "$project" "
            SELECT chain_id::text || ':' || encode(genesis_hash, 'hex')
            FROM chains
            WHERE chain_id = 1;
        ")
        if [ "$identity" = "1:$genesis_hash_hex" ]; then
            assert_service_replicas "$project" verify 1
            echo "compose-runtime-smoke: config-only verify role bound the identity before any RPC-backed role"
            return 0
        fi
        identity_attempt=$((identity_attempt + 1))
        sleep 1
    done
    echo "compose-runtime-smoke: config-only verify role did not bind the fresh database identity (last=$identity)" >&2
    return 1
}

run_short_load() {
    project=$1
    mode=$2
    api_service=$3
    output=$temporary_directory/$mode-load.json

    compose -p "$project" --profile "$mode" --profile runtime-tools run \
        --rm --no-deps runtime-loadtest \
        -base-url "http://$api_service:8080" \
        -rate 40 \
        -duration 3s \
        -concurrency 16 \
        -request-timeout 2s \
        -max-p95 1s \
        -max-error-rate 0 \
        -min-throughput-ratio 0.8 \
        -max-lag 0 \
        -profile "p60-runtime-$mode" \
        -revision working-tree \
        -dataset deterministic-three-block-runtime-fixture \
        -hardware docker-compose-runner \
        -rpc-behavior deterministic-local >"$output"
    jq -e '
        .requests == 120 and
        .errors == 0 and
        .core_lag_blocks == 0 and
        .core_ready == true and
        .backfill_complete == true and
        .p95_ms <= 1000 and
        .throughput_rps >= 32
    ' "$output" >/dev/null
    echo "compose-runtime-smoke: $mode short load report"
    jq -cS . "$output"
}

assert_final_topology() {
    mode=$1
    project=$2
    assert_migration_completed "$project"
    assert_service_replicas "$project" runtime-fixture 1
    assert_service_replicas "$project" postgres 1
    if [ "$mode" = distributed ]; then
        set -- api:1 sync:1 enrich:1 trace:1 verify:1 metadata:1 maintenance:1
    else
        set -- etherview:1
    fi
    for service_specification in "$@"; do
        service_name=${service_specification%%:*}
        replica_count=${service_specification##*:}
        assert_service_replicas "$project" "$service_name" "$replica_count"
    done
}

from_address=0x00000000000000000000000000000000000000a1
genesis_hash_hex=0000000000000000000000000000000000000000000000000000000000000100
block_one_hash_hex=0000000000000000000000000000000000000000000000000000000000000101
block_two_hash_hex=0000000000000000000000000000000000000000000000000000000000000102
final_block_hash=0x$block_two_hash_hex
transaction_hash=0x0000000000000000000000000000000000000000000000000000000000000201
pending_hash=0x0000000000000000000000000000000000000000000000000000000000000301

run_mode() {
    mode=$1
    project=$2
    api_service=$3
    output_prefix=$4
    shift 4

    echo "compose-runtime-smoke: starting $mode"
    if [ "$mode" = distributed ]; then
        start_config_only_role_first "$project"
        compose -p "$project" --profile "$mode" up -d --wait --wait-timeout 90 \
            --scale sync=2 --scale enrich=2
    else
        compose -p "$project" --profile "$mode" up -d --wait --wait-timeout 90
    fi
    assert_migration_completed "$project"
    assert_service_replicas "$project" runtime-fixture 1
    assert_service_replicas "$project" postgres 1
    for service_specification in "$@"; do
        service_name=${service_specification%%:*}
        replica_count=${service_specification##*:}
        assert_service_replicas "$project" "$service_name" "$replica_count"
    done

    port=$(public_port "$project" "$api_service")
    base_url="http://127.0.0.1:$port"
    wait_for_ready "$base_url"
    wait_for_publications "$project" 10 1 "$block_one_hash_hex"
    if [ "$mode" = distributed ]; then
        verify_distributed_replica_survival "$project" "$base_url"
    else
        advance_fixture "$project"
        wait_for_publications "$project" 15 2 "$block_two_hash_hex"
    fi
    run_short_load "$project" "$mode" "$api_service"
    capture_state "$project" "$temporary_directory/$output_prefix-state.txt"
    capture_api "$base_url" "$temporary_directory/$output_prefix-api.txt" \
        "$temporary_directory/$output_prefix-spa.html"
    assert_final_topology "$mode" "$project"

    compose -p "$project" --profile "$mode" down --volumes --remove-orphans
}

run_mode monolith "$monolith_project" etherview monolith etherview:1
run_mode distributed "$distributed_project" api distributed \
    api:1 sync:2 enrich:2 trace:1 verify:1 metadata:1 maintenance:1

diff -u "$temporary_directory/monolith-state.txt" "$temporary_directory/distributed-state.txt"
diff -u "$temporary_directory/monolith-api.txt" "$temporary_directory/distributed-api.txt"
diff -u "$temporary_directory/monolith-spa.html" "$temporary_directory/distributed-spa.html"

failed=false
echo "compose-runtime-smoke: PASS (fresh monolith/distributed state, API, and embedded SPA parity)"
