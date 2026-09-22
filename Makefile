GO ?= /Users/ruanheyns/.govm/go/bin/go

.PHONY: fmt test build testacc testacc-component-version

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./... -race -count=1

build:
	$(GO) build ./cmd/terraform-provider-vew

testacc:
	TF_ACC=1 $(GO) test ./internal/provider -run '^TestAccComponentResource$$' -v -count=1 -timeout 20m

testacc-component-version:
	TF_ACC="$(TF_ACC)" VEW_ACC_COMPONENT_VERSION="$(VEW_ACC_COMPONENT_VERSION)" VEW_API_URL="$(VEW_API_URL)" VEW_TOKEN_URL="$(VEW_TOKEN_URL)" VEW_CLIENT_ID="$(VEW_CLIENT_ID)" VEW_CLIENT_SECRET="$(VEW_CLIENT_SECRET)" VEW_TEST_PROJECT_ID="$(VEW_TEST_PROJECT_ID)" $(GO) test ./internal/provider/components -run '^TestAccComponentVersionResource$$' -v -count=1 -timeout 20m
