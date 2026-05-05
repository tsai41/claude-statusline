BINARY := claude-statusline
GOBIN  := $(shell go env GOPATH)/bin
PREFIX ?= /usr/local

.PHONY: all build install symlink uninstall clean test tidy fmt vet release-snapshot

all: build

build:
	go build -o $(BINARY) .

install:
	go install .
	@echo "Installed to $(GOBIN)/$(BINARY)"

symlink: install
	sudo ln -sf $(GOBIN)/$(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "Symlinked $(PREFIX)/bin/$(BINARY) -> $(GOBIN)/$(BINARY)"

uninstall:
	rm -f $(GOBIN)/$(BINARY)
	sudo rm -f $(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)

test:
	go test ./...

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

release-snapshot:
	goreleaser release --snapshot --clean
