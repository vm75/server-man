#!/bin/sh
set -eu

output=${1:-bin/server-man}
version=${SERVER_MAN_VERSION:-}
if [ -z "$version" ]; then
	if git describe --tags --always --dirty >/dev/null 2>&1; then
		version=$(git describe --tags --always --dirty)
	else
		version=dev
	fi
fi
case "$version" in
	''|*[!A-Za-z0-9._+-]*) echo "invalid SERVER_MAN_VERSION: $version" >&2; exit 2 ;;
esac
mkdir -p "$(dirname "$output")"
go build -trimpath -ldflags "-s -w -X server-man/internal/buildinfo.Version=$version" -o "$output" ./cmd/server-man
