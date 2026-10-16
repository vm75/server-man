.PHONY: setup dev api check test mcp-test ui-test build install clean

export GOCACHE ?= $(CURDIR)/.cache/go-build
export GOMODCACHE ?= $(CURDIR)/.cache/go-mod
export NPM_CONFIG_CACHE ?= $(CURDIR)/.cache/npm

setup:
	go mod download
	npm --prefix web ci --ignore-scripts
	npm --prefix tools/mcp ci --ignore-scripts
	npm --prefix tools/ui ci --ignore-scripts

api:
	go run ./cmd/serverman

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
	./tools/build.sh bin/serverman

PREFIX ?= /usr/local
DESTDIR ?=
install:
	test -x bin/serverman || { echo "run make build first" >&2; exit 1; }
	install -D -m 0755 bin/serverman "$(DESTDIR)$(PREFIX)/bin/serverman"
	install -D -m 0644 packaging/serverman.example.json "$(DESTDIR)$(PREFIX)/share/serverman/config.example.json"
	install -D -m 0644 packaging/systemd/serverman.service "$(DESTDIR)$(PREFIX)/lib/systemd/system/serverman.service"
	install -D -m 0644 packaging/systemd/user/serverman.service "$(DESTDIR)$(PREFIX)/lib/systemd/user/serverman.service"

clean:
	rm -rf bin web/dist internal/webui/assets/dist
