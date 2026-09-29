GO ?= go

export TF_ACC VEW_ACC_COMPONENT_VERSION VEW_ACC_RECIPE VEW_ACC_PIPELINE VEW_ACC_RELEASE_ACTIONS VEW_ACC_IMAGE_BUILD VEW_ACC_TECHNOLOGY VEW_ACC_PROJECT_ACCOUNT VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS VEW_ACC_DATA_SOURCES VEW_API_URL VEW_PROJECTS_API_URL VEW_TOKEN_URL VEW_CLIENT_ID VEW_CLIENT_SECRET VEW_TEST_PROJECT_ID VEW_TEST_COMPONENT_ID VEW_TEST_COMPONENT_VERSION_ID VEW_TEST_RECIPE_ID VEW_TEST_RECIPE_VERSION_ID VEW_TEST_PIPELINE_ID VEW_TEST_IMAGE_ID VEW_TEST_IMAGE_IDEMPOTENCY_KEY VEW_TEST_AWS_ACCOUNT_ID VEW_TEST_TECHNOLOGY_ID VEW_TEST_ACCOUNT_TYPE VEW_TEST_ACCOUNT_STAGE VEW_TEST_ACCOUNT_REGION

.PHONY: fmt test build docs docs-check testacc testacc-component-version testacc-recipe testacc-pipeline testacc-release-actions testacc-image-build testacc-technology testacc-project-account testacc-data-sources

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./... -race -count=1

build:
	$(GO) build ./cmd/terraform-provider-vew

docs:
	python3 scripts/registry_docs.py generate

docs-check:
	python3 scripts/registry_docs.py check

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

testacc-image-build: export TF_ACC ?= 1
testacc-image-build:
	$(GO) test ./internal/provider/imageactions -run '^TestAccImageBuildLiveExistingPipeline$$' -v -count=1 -timeout 4h

testacc-technology: export TF_ACC ?= 1
testacc-technology:
	$(GO) test ./internal/provider/technologies -run '^TestAccTechnologyResourceLiveDisposable$$' -v -count=1 -timeout 30m

testacc-project-account: export TF_ACC ?= 1
testacc-project-account:
	$(GO) test ./internal/provider/projectaccounts -run '^TestAccProjectAccountResourceLiveDisposable$$' -v -count=1 -timeout 5h

# Set VEW_ACC_DATA_SOURCES=1 and provide VEW_API_URL, VEW_TOKEN_URL,
# VEW_CLIENT_ID, VEW_CLIENT_SECRET, VEW_TEST_PROJECT_ID, and all six
# VEW_TEST_*_ID fixtures (component, component version, recipe, recipe version,
# pipeline, image) before running this read-only acceptance target.
testacc-data-sources: export TF_ACC ?= 1
testacc-data-sources:
	$(GO) test ./internal/provider -run '^TestAccDataSourcesReadExistingObjectsOnly$$' -v -count=1 -timeout 30m
