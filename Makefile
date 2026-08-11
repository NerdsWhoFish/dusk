.PHONY: test lint nocgo check build clean

# What CI runs on every PR and push to main.
test:
	go test -race ./...

lint:
	golangci-lint run

# Enforces ADR-0017: no cgo without an ADR. A cgo dependency usually arrives
# transitively and unnoticed; this build is what makes it fail loudly instead.
nocgo:
	CGO_ENABLED=0 go build ./...

check: lint nocgo test

build:
	go build ./...

clean:
	rm -rf bin
