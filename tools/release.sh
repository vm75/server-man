#!/bin/sh
set -eu
# Native release packaging: run on the target Linux architecture.
version=$(cat VERSION)
case "$version" in ''|*[!A-Za-z0-9._+-]*) echo 'invalid VERSION' >&2; exit 1 ;; esac
arch=$(go env GOARCH)
name="server-man-${version}-linux-${arch}"
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/$name/bin" "$stage/$name/docs"
for binary in server-man server-man-admin server-man-install server-man-firecracker-runtime; do
    install -m 0755 "bin/$binary" "$stage/$name/bin/$binary"
done
install -m 0755 install.sh "$stage/$name/install.sh"
install -m 0644 VERSION "$stage/$name/VERSION"
install -m 0644 docs/installation.md "$stage/$name/docs/installation.md"
install -m 0644 packaging/RELEASE-README.md "$stage/$name/README.md"
tar -C "$stage" -czf "dist/$name.tar.gz" "$name"
(cd dist && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
printf 'Created dist/%s.tar.gz and checksum\n' "$name"
