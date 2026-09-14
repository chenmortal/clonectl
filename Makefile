# rclone-sync (Go) build & test
GO ?= go

.PHONY: build test vet fmt web-dist all

build:
	$(GO) build -o rclone-sync ./cmd/rclone-sync

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w internal/ cmd/

web-dist:
	npm --prefix web run build

# Release flow: frontend build first, then the Go binary embeds web/dist.
all: web-dist build
