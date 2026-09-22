GO ?= /Users/ruanheyns/.govm/go/bin/go

export TF_ACC VEW_ACC_COMPONENT_VERSION VEW_API_URL VEW_TOKEN_URL VEW_CLIENT_ID VEW_CLIENT_SECRET VEW_TEST_PROJECT_ID

.PHONY: fmt test build testacc testacc-component-version

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./... -race -count=1

build:
	$(GO) build ./cmd/terraform-provider-vew

testacc: export TF_ACC ?= 1
testacc:
	$(GO) test ./internal/provider/components -run '^TestAccComponentResource$$' -v -count=1 -timeout 20m

testacc-component-version:
	$(GO) test ./internal/provider/components -run '^TestAccComponentVersionResource$$' -v -count=1 -timeout 4h
