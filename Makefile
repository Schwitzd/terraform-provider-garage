TEST?=$$(go list ./... | grep -v 'vendor')
NAMESPACE=schwitzd
NAME=garage
BINARY=terraform-provider-${NAME}
VERSION ?= $(shell \
	if [[ -n "$$VERSION" ]]; then echo "$$VERSION"; \
	else git describe --tags --abbrev=0 | sed 's/^v//'; fi \
)
OS_ARCH=linux_amd64

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
	go test -i $(TEST) || exit 1
	echo $(TEST) | xargs -t -n4 go test $(TESTARGS) -timeout=30s -parallel=4

testacc:
	TF_ACC=1 go test $(TEST) -v $(TESTARGS) -timeout 120m

docs:
	@echo "Generating Terraform provider docs with tfplugindocs"
	@~/go/bin/tfplugindocs

clean:
	rm -rf bin $(BINARY)