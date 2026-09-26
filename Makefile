# SPDX-License-Identifier: AGPL-3.0-or-later

GO ?= go
VERSION ?= dev

.PHONY: screens build test vet fmt check image clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/zlatan ./cmd/zlatan

# What CI gates on: unit and contract tests, with the race detector.
test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

check: vet test
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "not gofmt clean:"; echo "$$unformatted"; exit 1; fi

image:
	docker build --build-arg VERSION=$(VERSION) -t zlatan:$(VERSION) .

clean:
	rm -rf bin

# Renders every screen, in every language, from the template the service runs.
screens:
	ZLATAN_SCREENS_DIR=$(CURDIR)/docs/design/screens go test ./internal/web/ -run TestEveryScreen -count=1
