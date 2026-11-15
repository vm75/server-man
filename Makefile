.PHONY: setup dev api check test mcp-test ui-test build install release clean

export GOCACHE ?= $(CURDIR)/.cache/go-build
export GOMODCACHE ?= $(CURDIR)/.cache/go-mod
export NPM_CONFIG_CACHE ?= $(CURDIR)/.cache/npm

setup:
	go mod download
	npm --prefix web ci --ignore-scripts
	npm --prefix tools/mcp ci --ignore-scripts
	npm --prefix tools/ui ci --ignore-scripts

api:
	go run ./cmd/server-man

dev:
	npm --prefix web run dev

check:
	go vet ./...
	npm --prefix web run check

test:
	go test -race ./...

mcp-test: build
	go build -o bin/mcp-http-bridge ./tools/mcp/bridge
	npm --prefix tools/mcp run verify

ui-test: build
	go build -o bin/mcp-http-bridge ./tools/mcp/bridge
	npm --prefix tools/ui run verify

build:
	npm --prefix web run build
	./tools/build.sh bin/server-man
	CGO_ENABLED=0 go build -trimpath -o bin/server-man-admin ./cmd/server-man-admin
	CGO_ENABLED=0 go build -trimpath -o bin/server-man-install ./cmd/server-man-install

release: build
	./tools/release.sh

PREFIX ?= /usr/local
DESTDIR ?=
install:
	test -x bin/server-man-install
	test -x bin/server-man-admin
	test -x bin/server-man || { echo "run make build first" >&2; exit 1; }
	install -D -m 0755 bin/server-man "$(DESTDIR)$(PREFIX)/bin/server-man"
	install -D -m 0755 bin/server-man-admin "$(DESTDIR)$(PREFIX)/libexec/server-man-admin"
	install -D -m 0755 bin/server-man-install "$(DESTDIR)$(PREFIX)/sbin/server-man-install"
	install -D -m 0644 packaging/server-man.sudoers.example "$(DESTDIR)$(PREFIX)/share/server-man/sudoers.example"
	install -D -m 0644 packaging/server-man.example.json "$(DESTDIR)$(PREFIX)/share/server-man/config.example.json"
	install -D -m 0644 packaging/systemd/server-man.service "$(DESTDIR)$(PREFIX)/lib/systemd/system/server-man.service"
	install -D -m 0644 packaging/systemd/user/server-man.service "$(DESTDIR)$(PREFIX)/lib/systemd/user/server-man.service"

clean:
	rm -rf bin web/dist internal/webui/assets/dist
