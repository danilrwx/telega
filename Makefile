GO ?= go
GOLANGCI_LINT ?= golangci-lint
OUTPUT ?= telega
VERSION ?= dev
GO_ENV = env CGO_ENABLED=0 GOMAXPROCS=1 GOGC=25
GO_FLAGS = -mod=readonly -p=1 -gcflags='github.com/gotd/td/tg=-l -c=1'

all: build

build:
	$(GO_ENV) $(GO) build $(GO_FLAGS) -trimpath -ldflags='-s -w -X main.version=$(VERSION)' -o "$(OUTPUT).new" ./cmd/telega
	mv -f "$(OUTPUT).new" "$(OUTPUT)"

dm250:
	env GOOS=openbsd GOARCH=arm GOARM=7 $(MAKE) build OUTPUT=telega-dm250

check:
	$(GO_ENV) $(GO) test $(GO_FLAGS) ./...

lint:
	env CGO_ENABLED=0 GOMAXPROCS=2 GOGC=25 GOFLAGS='-p=1' $(GOLANGCI_LINT) run --timeout=10m

clean:
	rm -f telega telega.new telega-dm250 telega-dm250.new

.PHONY: all build dm250 check lint clean
