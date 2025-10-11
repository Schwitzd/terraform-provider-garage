TEST?=$$(go list ./... | grep -v 'vendor')
NAMESPACE=schwitzd
NAME=garage
BINARY=terraform-provider-${NAME}
VERSION ?= $(shell \
	if [[ -n "$$VERSION" ]]; then echo "$$VERSION"; \
	else git describe --tags --abbrev=0 | sed 's/^v//'; fi \
)
OS_ARCH=linux_amd64
GOCACHE_DIR ?= $(CURDIR)/.gocache
COVERAGE_FILE ?= coverage.out
UNIT_TEST_FLAGS ?= -count=1 -race -covermode=atomic -parallel=4 -timeout=5m
ACCEPTANCE_TEST_FLAGS ?= -count=1 -timeout=120m

.PHONY: all build release install test testacc clean docs

all: docs build

build: clean
	@echo "Building release version $(VERSION)"
	CGO_ENABLED=0 go build -trimpath -o bin/${BINARY}

release:
	@mkdir -p bin
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_darwin_amd64
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_darwin_arm64
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_linux_amd64
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_linux_arm64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_windows_amd64.exe
	GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o ./bin/$(BINARY)_$(VERSION)_windows_arm64.exe

local-install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	cp bin/${BINARY} ~/.terraform.d/plugins/registry.terraform.io/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}

test:
	@echo "==> Running unit tests"
	@rm -rf $(GOCACHE_DIR) $(COVERAGE_FILE)
	@GOCACHE=$(GOCACHE_DIR) go test $(UNIT_TEST_FLAGS) -coverprofile=$(COVERAGE_FILE) $(TESTARGS) $(TEST)
	@go tool cover -func=$(COVERAGE_FILE) | grep total

testacc:
	@echo "==> Running acceptance tests (TF_ACC=1)"
	@rm -rf $(GOCACHE_DIR)
	@TF_ACC=1 GOCACHE=$(GOCACHE_DIR) go test $(ACCEPTANCE_TEST_FLAGS) $(TESTARGS) $(TEST)

docs:
	@echo "Generating Terraform provider docs with tfplugindocs"
	@~/go/bin/tfplugindocs

clean:
	rm -rf bin $(BINARY)
