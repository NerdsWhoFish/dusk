.PHONY: test lint nocgo check build clean web web-check web-dev

# What CI runs on every PR and push to main.
test:
	go test -race ./...

lint:
	golangci-lint run

# Enforces ADR-0017: no cgo without an ADR. A cgo dependency usually arrives
# transitively and unnoticed; this build is what makes it fail loudly instead.
nocgo:
	CGO_ENABLED=0 go build ./...

check: lint nocgo test web-check

# The UI is embedded, so `build` builds it first: a Go build alone succeeds
# with a stale or absent bundle and the mismatch only shows in a browser.
build: web
	go build ./...

web:
	rm -rf web/dist/assets web/dist/index.html
	cd web && npm ci --no-audit --fund=false && npm run build

# Typecheck without a full build, for the check target. Skipped rather than
# failed when dependencies are absent, so a Go-only contributor is not blocked.
web-check:
	@if [ -d web/node_modules ]; then cd web && npm run check; \
	else echo "web: skipping typecheck, run 'make web' first"; fi

# Vite with hot reload, proxying the API to a Dusk running on :8080.
web-dev:
	cd web && npm run dev

clean:
	rm -rf bin web/dist/assets web/dist/index.html
