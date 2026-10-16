.PHONY: setup dev api check test mcp-test build clean

export GOCACHE ?= $(CURDIR)/.cache/go-build
export GOMODCACHE ?= $(CURDIR)/.cache/go-mod
export NPM_CONFIG_CACHE ?= $(CURDIR)/.cache/npm

setup:
	go mod download
	npm --prefix web ci --ignore-scripts
	npm --prefix tools/mcp ci --ignore-scripts

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

build:
	npm --prefix web run build
	go build -trimpath -ldflags='-s -w' -o bin/serverman ./cmd/serverman

clean:
	rm -rf bin web/dist internal/webui/assets/dist
