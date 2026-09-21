GO ?= /Users/ruanheyns/.govm/go/bin/go

.PHONY: fmt test build testacc

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./... -race -count=1

build:
	$(GO) build ./cmd/terraform-provider-vew

testacc:
	TF_ACC=1 $(GO) test ./internal/provider -run '^TestAccComponentResource$$' -v -count=1 -timeout 20m
