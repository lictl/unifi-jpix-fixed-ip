BINARY := inotify-go

# UniFi Dream Machine hardware is ARM64 (aarch64). Override GOARCH for other
# targets, e.g. `make GOARCH=amd64`.
GOARCH ?= arm64

# The code is Linux-only (inotify syscalls), so tests run in a Linux container.
# Pin a toolchain image >= the go directive in go.mod, with GOTOOLCHAIN=local so
# `go test` never tries to fetch a different toolchain over the network.
GO_IMAGE ?= golang:1.26

.PHONY: build test clean

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
		go build -trimpath -ldflags='-s -w' -o $(BINARY) .

# Run the test suite inside a Linux container (host caches reused via volumes).
test:
	docker run --rm \
		-v $(CURDIR):/src -w /src \
		-v inotify-go-gocache:/root/.cache/go-build \
		-v inotify-go-gomod:/go/pkg/mod \
		-e GOTOOLCHAIN=local \
		$(GO_IMAGE) go test -v ./...

clean:
	rm -f $(BINARY)
