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
  local npm_version="$5"
  local expected_message="${6:-}"
  local go_bin node_bin npm_bin output

  go_bin="$(make_fake "$name-go" "$go_version")"
  node_bin="$(make_fake "$name-node" "$node_version")"
  npm_bin="$(make_fake "$name-npm" "$npm_version")"

  if output="$(GO="$go_bin" NODE="$node_bin" NPM="$npm_bin" "$CHECKER" 2>&1)"; then
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

run_case minimums success go1.26.5 v24.18.0 11.16.0
run_case patch-newer success go1.26.6 v24.18.0 11.16.0
run_case minor-newer success go1.26.5 v24.19.0 11.16.0
run_case major-newer success go1.26.5 v24.18.0 12.0.0
run_case go-older failure go1.26.4 v24.18.0 11.16.0 "Go version 1.26.4 is below minimum"
run_case node-older failure go1.26.5 v24.17.9 11.16.0 "Node version 24.17.9 is below minimum"
run_case npm-older failure go1.26.5 v24.18.0 11.15.9 "npm version 11.15.9 is below minimum"
run_case go-prerelease failure go1.27.0rc1 v24.18.0 11.16.0 "unsupported Go version format"
run_case node-prerelease failure go1.26.5 v25.0.0-rc.1 11.16.0 "unsupported Node version format"
run_case npm-malformed failure go1.26.5 v24.18.0 latest "unsupported npm version format"
run_case node-leading-zero failure go1.26.5 v024.18.0 11.16.0 "unsupported Node version format"

go_bin="$(make_failing_fake failing-go)"
node_bin="$(make_fake failing-go-node v24.18.0)"
npm_bin="$(make_fake failing-go-npm 11.16.0)"
if output="$(GO="$go_bin" NODE="$node_bin" NPM="$npm_bin" "$CHECKER" 2>&1)"; then
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

go_bin="$(make_fake missing-npm-go go1.26.5)"
node_bin="$(make_fake missing-npm-node v24.18.0)"
missing_npm="$FIXTURE_ROOT/missing-npm"
if output="$(GO="$go_bin" NODE="$node_bin" NPM="$missing_npm" "$CHECKER" 2>&1)"; then
  echo "toolchain-check test missing-npm: expected failure"
  exit 1
fi
case "$output" in
  *"npm executable not found"*) ;;
  *)
    echo "toolchain-check test missing-npm: missing expected message"
    echo "$output"
    exit 1
    ;;
esac

echo "toolchain-check tests: passed"
