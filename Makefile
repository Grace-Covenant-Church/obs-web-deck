BIN := obs-web-deck

# CGO_ENABLED=0 is not optional. With cgo on, Go links its libc resolver and
# the result is a dynamically linked binary that will not run on a machine
# with a different glibc. Off, it is genuinely static and portable.
GOFLAGS := CGO_ENABLED=0

.PHONY: all build clean test check run cross

all: build

build:
	$(GOFLAGS) go build -trimpath -ldflags="-s -w" -o $(BIN) .

test:
	go test ./...

check:
	gofmt -l .
	go vet ./...
	go test ./...

run: build
	./$(BIN)

clean:
	rm -f $(BIN) $(BIN)-linux-* $(BIN)-darwin-* $(BIN).exe

# The target machine needs no Go toolchain and no runtime.
cross:
	$(GOFLAGS) GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN)-linux-amd64 .
	$(GOFLAGS) GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN)-linux-arm64 .
	$(GOFLAGS) GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(BIN)-darwin-arm64 .
	$(GOFLAGS) GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BIN).exe .

# Regenerates web/js/icons.js from the lucide-static npm package.
icons:
	npm pack lucide-static
	tar xzf lucide-static-*.tgz
	node scripts/gen-icons.mjs ./package
	rm -rf package lucide-static-*.tgz
