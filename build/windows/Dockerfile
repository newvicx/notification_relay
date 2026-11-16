# Windows cross-compile toolchain for notification_relay.
#
# A Linux image with Go plus the mingw-w64 cross compiler, so the cgo-based
# SQLite driver (github.com/mattn/go-sqlite3) can be built into a Windows
# .exe without a C compiler on the host.
#
# Build the image (the context is this directory only, so the repo isn't sent):
#   docker build -t nr-wincross build/windows
#
# Build the Windows binaries from the repo root:
#   docker run --rm -v "$PWD":/src nr-wincross
#
# Run something else, e.g. the Linux test suite (native gcc is also present):
#   docker run --rm -v "$PWD":/src -e GOOS=linux -e CC=gcc nr-wincross go test ./...

ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc-mingw-w64-x86-64 \
 && rm -rf /var/lib/apt/lists/*

# Cross-compile settings for windows/amd64 with cgo.
ENV CGO_ENABLED=1 \
    GOOS=windows \
    GOARCH=amd64 \
    CC=x86_64-w64-mingw32-gcc \
    GOFLAGS=-buildvcs=false \
    GOCACHE=/tmp/go-cache \
    GOMODCACHE=/tmp/go-mod

WORKDIR /src

# Static linking so the .exe doesn't need libgcc / libwinpthread DLLs at runtime.
CMD ["sh", "-c", "go build -trimpath -ldflags='-s -w -extldflags=-static' -o dist/notification_relay.exe ./cmd/notification_relay && go build -trimpath -ldflags='-s -w -extldflags=-static' -o dist/nrcli.exe ./cmd/nrcli && ls -l dist"]
