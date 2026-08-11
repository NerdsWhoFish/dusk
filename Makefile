.PHONY: test vet check build clean

# What CI runs on every PR and push to main.
test:
	go test -race ./...

vet:
	go vet ./...

check: vet test

build:
	go build ./...

clean:
	rm -rf bin
