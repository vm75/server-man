#!/bin/sh
set -eu

output=${1:-bin/server-man}
version=${SERVER_MAN_VERSION:-}
if [ -z "$version" ]; then
	base_version=$(cat VERSION)
	if ! printf '%s\n' "$base_version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$'; then
		echo "invalid VERSION: $base_version" >&2
		exit 2
	fi
	version="${base_version}+source"
	if revision=$(git rev-parse --verify --short=12 HEAD 2>/dev/null); then
		dirty=
		if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
			dirty=.dirty
		fi
		if [ -z "$dirty" ] && git tag --points-at HEAD | grep -Fxq "v$base_version"; then
			version=$base_version
		else
			version="${base_version}+g${revision}${dirty}"
		fi
	fi
fi
case "$version" in
	''|*[!A-Za-z0-9._+-]*) echo "invalid SERVER_MAN_VERSION: $version" >&2; exit 2 ;;
esac
mkdir -p "$(dirname "$output")"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X server-man/internal/buildinfo.Version=$version" -o "$output" ./cmd/server-man
