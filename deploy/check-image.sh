#!/bin/sh
set -eu

# This intentionally stays a small image-boundary probe; service lifecycle and
# behavioral assertions belong to the Go E2E suites.
docker_command=${DOCKER:-docker}
image=${IMAGE:-etherview:local}
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/etherview-image-check.XXXXXX")
container_name="etherview-image-check-$$"
container_created=false

if ! grep -Fxq '/.local/' .dockerignore; then
    echo "docker-image-check: local Preview artifacts must be excluded from the build context" >&2
    exit 1
fi

cleanup() {
    exit_code=$?
    trap - EXIT INT TERM
    if [ "$container_created" = true ]; then
        "$docker_command" container rm "$container_name" >/dev/null 2>&1 || true
    fi
    chmod -R u+w "$temporary_directory" >/dev/null 2>&1 || true
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

sea_self_test=$("$docker_command" run --rm \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m,mode=0700 \
    --env HOME=/nonexistent \
    --env TMPDIR=/tmp \
    --env LD_LIBRARY_PATH=/opt/etherview/solcjs/lib \
    --entrypoint /opt/etherview/solcjs/etherview-solcjs \
    "$image" \
    --self-test)
if [ "$sea_self_test" != '{"schema":"etherview-solcjs-sea-self-test-v1","sea":true,"node_version":"v26.7.0","wrapper_package":"solc@0.8.36","exec_argv":["--permission","--disable-sigusr1","--no-addons","--no-global-search-paths","--max-old-space-size=384"],"permissions":"restricted","write_denied":true}' ]; then
    echo "docker-image-check: unexpected solc-js SEA self-test: $sea_self_test" >&2
    exit 1
fi

"$docker_command" run --rm \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m,mode=0700 \
    --env HOME=/nonexistent \
    --env TMPDIR=/tmp \
    --env GOMEMLIMIT=384MiB \
    --entrypoint /usr/local/bin/etherview-geas-compiler \
    "$image" --self-test >/dev/null

normalize_architecture() {
    case "$1" in
        amd64|x86_64|x86-64) echo amd64 ;;
        arm64|aarch64) echo arm64 ;;
        *) echo "$1" ;;
    esac
}

image_architecture=$(
    "$docker_command" image inspect --format '{{.Architecture}}' "$image"
)
host_architecture=$(
    "$docker_command" info --format '{{.Architecture}}'
)
image_architecture=$(normalize_architecture "$image_architecture")
host_architecture=$(normalize_architecture "$host_architecture")
if [ -z "$image_architecture" ] || [ "$image_architecture" != "$host_architecture" ]; then
    echo "docker-image-check: image architecture $image_architecture does not match Docker host $host_architecture" >&2
    exit 1
fi

"$docker_command" create --name "$container_name" "$image" version >/dev/null
container_created=true
"$docker_command" export "$container_name" >"$temporary_directory/rootfs.tar"
tar -tf "$temporary_directory/rootfs.tar" >"$temporary_directory/rootfs.txt"
tar -xf "$temporary_directory/rootfs.tar" \
    -C "$temporary_directory" \
    opt/etherview/solcjs licenses/solcjs-runtime
node .github/scripts/solcjs-runtime-image-check.mjs \
    "$temporary_directory/opt/etherview/solcjs" \
    "$temporary_directory/rootfs.txt" \
    "$temporary_directory/licenses/solcjs-runtime"

for required_path in \
    LICENSE \
    THIRD_PARTY_NOTICES.md \
    etherview \
    usr/local/bin/etherview-geas-compiler \
    opt/etherview/solcjs/etherview-solcjs \
    opt/etherview/solcjs/runtime-manifest.json \
    licenses/solcjs-runtime/node-LICENSE.txt \
    licenses/solcjs-runtime/solc-LICENSE \
    licenses/solcjs-runtime/esbuild-LICENSE.md \
    licenses/go-ethereum-LGPL-3.0-or-later.txt \
    licenses/go-ethereum-crypto-bn256-BSD-3-Clause.txt \
    licenses/go-ethereum-crypto-keccak-BSD-3-Clause.txt \
    licenses/go-ethereum-crypto-secp256k1-BSD-3-Clause.txt \
    licenses/go-ethereum-metrics-BSD-2-Clause-FreeBSD.txt \
    licenses/geas-LGPL-3.0.txt \
    licenses/holiman-bloomfilter-MIT.txt \
    licenses/libsecp256k1-MIT.txt
do
    if ! grep -Eq "^${required_path}$" "$temporary_directory/rootfs.txt"; then
        echo "docker-image-check: production image is missing /$required_path" >&2
        exit 1
    fi
done

grep -Ev '^opt/etherview/solcjs(/|$)' \
    "$temporary_directory/rootfs.txt" >"$temporary_directory/non-compiler-rootfs.txt"
forbidden_pattern='(^|/)(node|nodejs|npm|npx|corepack|pnpm|yarn|go|gofmt|solc|solcjs|vyper|vyper-json|docker|podman|containerd|nerdctl|runc)(/|$)|(^|/)node_modules(/|$)|(^|/)(package.json|package-lock.json|yarn.lock|pnpm-lock.yaml)$|(^|/)(sh|bash|ash|dash|zsh|ksh|csh|tcsh|fish|busybox)$'
if grep -E -i "$forbidden_pattern" "$temporary_directory/non-compiler-rootfs.txt" >"$temporary_directory/forbidden.txt"; then
    echo "docker-image-check: forbidden runtime/build/compiler payload found:" >&2
    sed -n '1,40p' "$temporary_directory/forbidden.txt" >&2
    exit 1
fi

tar --numeric-owner -tvf "$temporary_directory/rootfs.tar" \
    >"$temporary_directory/rootfs-verbose.txt"
if awk '$NF == "opt/etherview/solcjs/etherview-solcjs" && ($1 ~ /w/ || $1 !~ /x/) { found = 1 } END { exit !found }' \
    "$temporary_directory/rootfs-verbose.txt"; then
    echo "docker-image-check: bundled SEA is writable or not executable" >&2
    exit 1
fi
if awk '$NF == "usr/local/bin/etherview-geas-compiler" && ($1 ~ /w/ || $1 !~ /x/) { found = 1 } END { exit !found }' \
    "$temporary_directory/rootfs-verbose.txt"; then
    echo "docker-image-check: bundled Geas helper is writable or not executable" >&2
    exit 1
fi
if awk '$1 ~ /^l/ && $NF ~ /^opt\/etherview\/solcjs(\/|$)/ { found = 1 } END { exit !found }' \
    "$temporary_directory/rootfs-verbose.txt"; then
    echo "docker-image-check: compiler runtime contains a symbolic link" >&2
    exit 1
fi
if awk '$1 ~ /^d/ && $1 ~ /w/ && $NF ~ /^opt\/etherview\/solcjs(\/|$)/ { found = 1 } END { exit !found }' \
    "$temporary_directory/rootfs-verbose.txt"; then
    echo "docker-image-check: compiler runtime contains a writable directory" >&2
    exit 1
fi
if awk '$1 ~ /^-/ && $NF ~ /^opt\/etherview\/solcjs\/lib\// && ($1 ~ /w/ || $1 ~ /x/) { found = 1 } END { exit !found }' \
    "$temporary_directory/rootfs-verbose.txt"; then
    echo "docker-image-check: private SEA library is writable or executable" >&2
    exit 1
fi

for cache_path in var/lib/etherview/compilers var/lib/etherview/compilers/cache
do
    cache_line=$(awk -v path="$cache_path" '$NF == path || $NF == path "/" { print; exit }' \
        "$temporary_directory/rootfs-verbose.txt")
    if [ -z "$cache_line" ] || ! printf '%s\n' "$cache_line" | grep -Eq '^drwxr-x---'; then
        echo "docker-image-check: unsafe compiler cache mode for /$cache_path" >&2
        exit 1
    fi
    if ! printf '%s\n' "$cache_line" | grep -Eq '(^|[[:space:]])65532[/[:space:]]+65532([[:space:]]|$)'; then
        echo "docker-image-check: unsafe compiler cache owner for /$cache_path" >&2
        exit 1
    fi
done

echo "docker-image-check: PASS (user=$configured_user, architecture=$image_architecture, SEA=Node-v26.7.0, Geas=0.3.3, hardened rootfs)"
