#!/bin/sh
# The source tree and release archive share this entry point.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ ! -x "$root/bin/server-man-install" ] || [ -f "$root/go.mod" ]; then
    if [ "$(id -u)" = 0 ]; then
        echo 'Build as your normal account, then let this script invoke sudo for installation.' >&2
        exit 1
    fi
    make -C "$root" setup build
fi
case " $* " in
    *' --dry-run '*) exec "$root/bin/server-man-install" --bundle "$root" "$@" ;;
esac
if [ "$(id -u)" = 0 ]; then
    exec "$root/bin/server-man-install" --bundle "$root" "$@"
fi
exec sudo -- "$root/bin/server-man-install" --bundle "$root" "$@"
