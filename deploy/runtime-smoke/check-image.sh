#!/bin/sh
set -eu

docker_command=${DOCKER:-docker}
image=${IMAGE:-etherview:local}
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/etherview-image-check.XXXXXX")
container_name="etherview-image-check-$$"
container_created=false

cleanup() {
    exit_code=$?
    trap - EXIT INT TERM
    if [ "$container_created" = true ]; then
        "$docker_command" container rm "$container_name" >/dev/null 2>&1 || true
    fi
    rm -r "$temporary_directory"
    exit "$exit_code"
}
trap cleanup EXIT INT TERM

"$docker_command" image inspect "$image" >/dev/null
configured_user=$("$docker_command" image inspect --format '{{.Config.User}}' "$image")
if [ "$configured_user" != "65532:65532" ]; then
    echo "docker-image-check: production image user is ${configured_user:-<empty>}, want 65532:65532" >&2
    exit 1
fi

entrypoint=$("$docker_command" image inspect --format '{{json .Config.Entrypoint}}' "$image")
if [ "$entrypoint" != '["/etherview"]' ]; then
    echo "docker-image-check: unexpected entrypoint $entrypoint" >&2
    exit 1
fi

"$docker_command" run --rm \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    "$image" version >/dev/null

"$docker_command" create --name "$container_name" "$image" version >/dev/null
container_created=true
"$docker_command" export "$container_name" | tar -tf - >"$temporary_directory/rootfs.txt"

for required_path in LICENSE etherview; do
    if ! grep -Eq "^${required_path}$" "$temporary_directory/rootfs.txt"; then
        echo "docker-image-check: production image is missing /$required_path" >&2
        exit 1
    fi
done

forbidden_pattern='(^|/)(node|nodejs|npm|npx|corepack|pnpm|yarn|go|gofmt|solc|solcjs|vyper|vyper-json|compiler|compilers)(/|$)|(^|/)node_modules(/|$)|(^|/)(package.json|package-lock.json|yarn.lock|pnpm-lock.yaml)$|(^|/)(sh|bash|ash|dash|zsh|ksh|csh|tcsh|fish|busybox)$'
if grep -E -i "$forbidden_pattern" "$temporary_directory/rootfs.txt" >"$temporary_directory/forbidden.txt"; then
    echo "docker-image-check: forbidden runtime/build/compiler payload found:" >&2
    sed -n '1,40p' "$temporary_directory/forbidden.txt" >&2
    exit 1
fi

echo "docker-image-check: PASS (user=$configured_user, hardened execution and rootfs payload scan)"
