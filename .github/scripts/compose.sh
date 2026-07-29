#!/bin/sh
set -eu

docker_command=${DOCKER:-docker}

if "$docker_command" compose version >/dev/null 2>&1; then
	exec "$docker_command" compose "$@"
fi

if command -v docker-compose >/dev/null 2>&1; then
	exec docker-compose "$@"
fi

echo "compose: neither '$docker_command compose' nor 'docker-compose' is available" >&2
exit 1
