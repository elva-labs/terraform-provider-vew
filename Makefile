GO ?= /Users/ruanheyns/.govm/go/bin/go

export TF_ACC VEW_ACC_COMPONENT_VERSION VEW_ACC_RECIPE VEW_ACC_PIPELINE VEW_ACC_RELEASE_ACTIONS VEW_ACC_DATA_SOURCES VEW_API_URL VEW_TOKEN_URL VEW_CLIENT_ID VEW_CLIENT_SECRET VEW_TEST_PROJECT_ID VEW_TEST_COMPONENT_ID VEW_TEST_COMPONENT_VERSION_ID VEW_TEST_RECIPE_ID VEW_TEST_RECIPE_VERSION_ID VEW_TEST_PIPELINE_ID VEW_TEST_IMAGE_ID

.PHONY: fmt test build testacc testacc-component-version testacc-recipe testacc-pipeline testacc-release-actions testacc-data-sources

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

testacc-recipe: export TF_ACC ?= 1
testacc-recipe:
	$(GO) test ./internal/provider/recipes -run '^TestAccRecipeResource$$' -v -count=1 -timeout 4h

testacc-pipeline:
	$(GO) test ./internal/provider/pipelines -run '^TestAccPipelineResource$$' -v -count=1 -timeout 4h

testacc-release-actions: export TF_ACC ?= 1
testacc-release-actions:
	$(GO) test ./internal/provider/releaseactions -run '^TestAccReleaseActionsLiveDisposableVersions$$' -v -count=1 -timeout 4h

# Set VEW_ACC_DATA_SOURCES=1 and provide VEW_API_URL, VEW_TOKEN_URL,
# VEW_CLIENT_ID, VEW_CLIENT_SECRET, VEW_TEST_PROJECT_ID, and all six
# VEW_TEST_*_ID fixtures (component, component version, recipe, recipe version,
# pipeline, image) before running this read-only acceptance target.
testacc-data-sources: export TF_ACC ?= 1
testacc-data-sources:
	$(GO) test ./internal/provider -run '^TestAccDataSourcesReadExistingObjectsOnly$$' -v -count=1 -timeout 30m
