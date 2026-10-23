#!/usr/bin/env bash

set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
CHECKER="$SCRIPT_DIR/toolchain-check.sh"
FIXTURE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/etherview-toolchain-check.XXXXXX")"
trap 'rm -rf "$FIXTURE_ROOT"' EXIT INT TERM

make_fake() {
  local name="$1"
  local version="$2"
  local executable="$FIXTURE_ROOT/$name"

  {
    printf '%s\n' '#!/usr/bin/env bash'
    printf "printf '%%s\\n' %q\n" "$version"
  } >"$executable"
  chmod 0700 "$executable"
  printf '%s\n' "$executable"
}

make_failing_fake() {
  local name="$1"
  local executable="$FIXTURE_ROOT/$name"

  printf '%s\n' '#!/usr/bin/env bash' 'exit 9' >"$executable"
  chmod 0700 "$executable"
  printf '%s\n' "$executable"
}

run_case() {
  local name="$1"
  local expectation="$2"
  local go_version="$3"
  local node_version="$4"
  local pnpm_version="$5"
  local expected_message="${6:-}"
  local go_bin node_bin pnpm_bin output

  go_bin="$(make_fake "$name-go" "$go_version")"
  node_bin="$(make_fake "$name-node" "$node_version")"
  pnpm_bin="$(make_fake "$name-pnpm" "$pnpm_version")"

  if output="$(GO="$go_bin" NODE="$node_bin" PNPM="$pnpm_bin" "$CHECKER" 2>&1)"; then
    if [ "$expectation" != success ]; then
      echo "toolchain-check test $name: expected failure"
      return 1
    fi
    return 0
  fi

  if [ "$expectation" = success ]; then
    echo "toolchain-check test $name: expected success: $output"
    return 1
  fi

  case "$output" in
    *"$expected_message"*) ;;
    *)
      echo "toolchain-check test $name: missing expected message: $expected_message"
      echo "$output"
      return 1
      ;;
  esac
}

run_case minimums success go1.27.3 v26.12.1 12.15.0
run_case patch-newer success go1.27.4 v26.12.1 12.15.0
run_case node-patch-newer success go1.27.3 v26.12.2 12.15.0
run_case go-newer success go1.28.0 v26.12.1 12.15.0
run_case minor-newer success go1.27.3 v26.13.0 12.15.0
run_case major-newer success go1.27.3 v26.12.1 13.0.0
run_case go-patch-older failure go1.27.2 v26.12.1 12.15.0 "Go version 1.27.2 is below minimum"
run_case go-older failure go1.26.99 v26.12.1 12.15.0 "Go version 1.26.99 is below minimum"
run_case node-older failure go1.27.3 v26.12.0 12.15.0 "Node version 26.12.0 is below minimum"
run_case pnpm-older failure go1.27.3 v26.12.1 12.14.9 "pnpm version 12.14.9 is below minimum"
run_case go-prerelease failure go1.28rc1 v26.12.1 12.15.0 "unsupported Go version format"
run_case node-prerelease failure go1.27.3 v27.0.0-rc.1 12.15.0 "unsupported Node version format"
run_case pnpm-malformed failure go1.27.3 v26.12.1 latest "unsupported pnpm version format"
run_case node-leading-zero failure go1.27.3 v026.12.1 12.15.0 "unsupported Node version format"

go_bin="$(make_failing_fake failing-go)"
node_bin="$(make_fake failing-go-node v26.12.1)"
pnpm_bin="$(make_fake failing-go-pnpm 12.15.0)"
if output="$(GO="$go_bin" NODE="$node_bin" PNPM="$pnpm_bin" "$CHECKER" 2>&1)"; then
  echo "toolchain-check test failing-go: expected failure"
  exit 1
fi
case "$output" in
  *"failed to read current Go version"*) ;;
  *)
    echo "toolchain-check test failing-go: missing expected message"
    echo "$output"
    exit 1
    ;;
esac

go_bin="$(make_fake missing-pnpm-go go1.27.3)"
node_bin="$(make_fake missing-pnpm-node v26.12.1)"
missing_pnpm="$FIXTURE_ROOT/missing-pnpm"
if output="$(GO="$go_bin" NODE="$node_bin" PNPM="$missing_pnpm" "$CHECKER" 2>&1)"; then
  echo "toolchain-check test missing-pnpm: expected failure"
  exit 1
fi
case "$output" in
  *"pnpm executable not found"*) ;;
  *)
    echo "toolchain-check test missing-pnpm: missing expected message"
    echo "$output"
    exit 1
    ;;
esac

echo "toolchain-check tests: passed"
