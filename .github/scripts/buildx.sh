#!/bin/sh
set -eu

docker_command=${DOCKER:-docker}

if "$docker_command" buildx version >/dev/null 2>&1; then
	exec "$docker_command" buildx "$@"
fi

if command -v docker-buildx >/dev/null 2>&1; then
	exec docker-buildx "$@"
fi

echo "buildx: neither '$docker_command buildx' nor 'docker-buildx' is available" >&2
exit 1
