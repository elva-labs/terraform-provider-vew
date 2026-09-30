# Terraform Provider for VEW

[Virtual Engineering Workbench (VEW)](https://github.com/awslabs/virtual-engineering-workbench)
helps teams deliver self-service engineering environments on AWS. Platform
engineers build machine images from reusable software components and recipes,
publish them as products, and let developers launch ready-to-use workbenches.
This provider puts the packaging workflow in Terraform so image definitions
can be reviewed, repeated, and managed alongside other infrastructure.

This proof-of-concept provider manages VEW components, component
versions, recipes, recipe versions, image pipelines, project technologies, and
AWS account assignments. It uses the VEW OAuth 2.0 client-credentials flow
and exposes the `vew_component`, `vew_component_version`, `vew_recipe`,
`vew_recipe_version`, `vew_pipeline`, `vew_technology`,
`vew_project_account`, `vew_product`, and `vew_product_version_promotion`
resources.

It also provides read-only data sources for components, versions, recipes,
pipelines, and images, plus an explicitly invoked image-build action.

## Prerequisites

- Go 1.27 or newer (the module declares `go 1.27.0`).
- Terraform 1.16.3 for the documented local-development workflow.
- A VEW deployment with the project-scoped S2S Packaging API and idempotency
  support. These changes are in the
  [Elva Labs fork](https://github.com/elva-labs/virtual-engineering-workbench),
  but not yet in upstream VEW. See the
  [Packaging API prerequisite](docs/guides/packaging-api.md).
- VEW API credentials and a project in which to manage resources.

The provider uses these environment variables when the corresponding provider
attribute is omitted or empty:

| Provider attribute | Environment variable | Description |
| --- | --- | --- |
| `api_url` | `VEW_API_URL` | Absolute VEW API HTTP(S) URL, including `/clients/packaging/v1` (the provider appends `/projects/...`) |
| `projects_api_url` | `VEW_PROJECTS_API_URL` | Absolute VEW Projects API HTTP(S) URL (used by technologies and account assignments) |
| `publishing_api_url` | `VEW_PUBLISHING_API_URL` | Absolute VEW Publishing API HTTP(S) URL (used by products) |
| `token_url` | `VEW_TOKEN_URL` | Absolute OAuth token HTTP(S) URL |
| `client_id` | `VEW_CLIENT_ID` | OAuth client ID |
| `client_secret` | `VEW_CLIENT_SECRET` | OAuth client secret (sensitive) |

Explicit provider attributes take precedence over environment variables. The
packaging API URL, token URL, client ID, and client secret are required after
fallback resolution. Configure the Projects API URL when using
`vew_technology` or `vew_project_account`. The provider configuration can
remain empty for resources that do not use the Projects API, as in
[`examples/provider/provider.tf`](examples/provider/provider.tf):

```hcl
provider "vew" {}
```

## Build and test

The repository includes a Makefile using `go` from `PATH` by default.
Override `GO` to use another installation:

```shell
make fmt
make test
make build
```

Projects and access can be managed with `vew_project`,
`vew_project_assignment`, `vew_project_group_assignment`, and
`vew_project_client_assignment`. See the [project access guide](docs/guides/project-access.md)
for scopes, import IDs, bootstrap, role resolution, and disposable validation.
Recovery clients must explicitly set `project_client_bootstrap = true` on a
separate provider alias; ordinary configurations leave it unset.

Registry pages in `docs/` are generated from the live provider schema and
`templates/`. After changing a schema or a template, run `make docs` and
commit the resulting pages. Run `make docs-check` to regenerate into a
temporary directory, compare every page, and validate the Registry layout.
The GitHub Actions workflow runs this check and the Go test suite on pushes to
`main` and `beta`, and pull requests targeting either branch, including forks.
It uses Terraform 1.16.3 and `tfplugindocs` v0.25.0. These jobs run on
GitHub-hosted ARM runners without access to VEW credentials. Live acceptance
tests remain separately gated. The historical design notes live in
`design/superpowers/` so `docs/` contains only Registry pages.

The equivalent commands are:

```shell
go mod tidy
go fmt ./...
go vet ./...
go test ./... -race -count=1
go build ./cmd/terraform-provider-vew
```

The unit and framework tests use local fakes and do not call VEW. The
acceptance test is intentionally separate:

```shell
make testacc
```

`testacc` requires `TF_ACC=1`, all four provider environment variables, and
`VEW_PROJECT_ID`. It creates a real component, updates its description,
imports it, and archives it during cleanup. Run it only against a disposable
project; it has live API side effects and may leave an archived component if
the run is interrupted.

The component-version acceptance test is separately and explicitly gated:

```shell
TF_ACC=1 VEW_ACC_COMPONENT_VERSION=1 \
  VEW_API_URL=https://vew.example/api \
  VEW_TOKEN_URL=https://oauth.example/token \
  VEW_CLIENT_ID=... VEW_CLIENT_SECRET=... \
  VEW_TEST_PROJECT_ID=project-example \
  make testacc-component-version
```

It creates a uniquely named disposable `vew_component` in the supplied project,
creates and updates its component version, verifies import, retires the version,
and archives the disposable component during cleanup. It never uses an existing
component ID. The test is skipped unless every gate above is set.
The process timeout is four hours to accommodate create, update, retirement,
and cleanup. To compile and check both acceptance gates without contacting VEW,
run `make testacc TF_ACC=` and
`make testacc-component-version TF_ACC= VEW_ACC_COMPONENT_VERSION=`.

The recipe acceptance test has its own explicit gate and target:

```shell
TF_ACC=1 VEW_ACC_RECIPE=1 \
  VEW_API_URL=https://vew.example/api \
  VEW_TOKEN_URL=https://oauth.example/token \
  VEW_CLIENT_ID=... VEW_CLIENT_SECRET=... \
  VEW_TEST_PROJECT_ID=project-example \
  make testacc-recipe
```

It creates uniquely named disposable recipe resources in the supplied project,
updates and imports a recipe version, then retires the version before archiving
the recipe. It does not mutate existing recipes. Run it only in a disposable
project; the live run has API side effects and may leave resources behind if it
is interrupted. It is skipped unless `TF_ACC=1`, `VEW_ACC_RECIPE=1`, the four
provider environment variables, and `VEW_TEST_PROJECT_ID` are all set. To check
that the gated test compiles without contacting VEW, run
`make testacc-recipe TF_ACC= VEW_ACC_RECIPE=`.

The pipeline acceptance test has a separate gate and target. It requires a
released recipe version in the supplied project:

```shell
TF_ACC=1 VEW_ACC_PIPELINE=1 \
  VEW_API_URL=https://vew.example/api \
  VEW_TOKEN_URL=https://oauth.example/token \
  VEW_CLIENT_ID=... VEW_CLIENT_SECRET=... \
  VEW_TEST_PROJECT_ID=project-example \
  VEW_TEST_RECIPE_ID=recipe-123 \
  VEW_TEST_RECIPE_VERSION_ID=version-456 \
  make testacc-pipeline
```

The test creates a uniquely named disposable pipeline using that already
released recipe version, verifies update and import, and retires the pipeline
during cleanup. It does not start an image build. It is skipped unless
`TF_ACC=1`, `VEW_ACC_PIPELINE=1`, all four provider environment variables,
`VEW_TEST_PROJECT_ID`, `VEW_TEST_RECIPE_ID`, and
`VEW_TEST_RECIPE_VERSION_ID` are set. Use a disposable project and a released
version that belongs to the supplied recipe. To compile the gated test without
contacting VEW, run
`make testacc-pipeline TF_ACC= VEW_ACC_PIPELINE=`.

The data-source acceptance test reads existing objects without changing them.
Set `TF_ACC=1`, `VEW_ACC_DATA_SOURCES=1`, the four provider environment
variables, `VEW_TEST_PROJECT_ID`, and `VEW_TEST_COMPONENT_ID`,
`VEW_TEST_COMPONENT_VERSION_ID`, `VEW_TEST_RECIPE_ID`,
`VEW_TEST_RECIPE_VERSION_ID`, `VEW_TEST_PIPELINE_ID`, and
`VEW_TEST_IMAGE_ID`, then run `make testacc-data-sources`. All IDs must belong
to the supplied project. The test skips when any gate is missing.

The image-build acceptance test invokes a real build, which may incur AWS
charges. Set `TF_ACC=1`, `VEW_ACC_IMAGE_BUILD=1`, the four provider environment
variables, `VEW_TEST_PROJECT_ID`, `VEW_TEST_PIPELINE_ID` for an existing
disposable pipeline, and `VEW_TEST_IMAGE_IDEMPOTENCY_KEY` to an RFC 4122 UUID.
Then run `make testacc-image-build`. The test invokes the action twice with the
same key and checks that VEW returns the same image ID. It requires Terraform
1.16.3 and skips when a gate is missing.

The technology live acceptance test creates, updates, imports, and deletes a
uniquely named disposable technology. It requires `TF_ACC=1`,
`VEW_ACC_TECHNOLOGY=1`, all four provider environment variables,
`VEW_PROJECTS_API_URL`, and `VEW_TEST_PROJECT_ID`, then runs with
`make testacc-technology`. It needs a project assignment and the technology
read and write scopes. It is skipped before API access when any gate is
missing. To check the gate without contacting VEW, run
`make testacc-technology TF_ACC= VEW_ACC_TECHNOLOGY=`.

The project-account live acceptance test is gated separately because it starts
real AWS onboarding. It requires `TF_ACC=1`, `VEW_ACC_PROJECT_ACCOUNT=1`, all
four provider environment variables, `VEW_PROJECTS_API_URL`,
`VEW_TEST_PROJECT_ID`, an existing `VEW_TEST_TECHNOLOGY_ID` in that project,
`VEW_TEST_AWS_ACCOUNT_ID` containing exactly 12 digits,
`VEW_TEST_ACCOUNT_TYPE` (`USER` or `TOOLCHAIN`),
`VEW_TEST_ACCOUNT_STAGE` (`dev`, `qa`, or `prod`), and
`VEW_TEST_ACCOUNT_REGION` (for example `eu-west-1`). It also requires the
explicit confirmation `VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS=I_CONFIRM_REAL_AWS_ONBOARDING`.
Run it with `make testacc-project-account`. The account must be disposable and
approved for VEW onboarding: the operation can create or change AWS resources
and incur charges. The test imports the assignment and then deletes the
Terraform resource, which deactivates the VEW assignment; it does not offboard
the AWS account. VEW retains the assignment's technology reference. The live
fixture is one-shot: use a new disposable AWS account for another run, because
deactivation retains the VEW assignment record. Missing or invalid gates cause
the test to skip before it requests a token or calls the API. To check its
gate without contacting VEW, run
`make testacc-project-account TF_ACC= VEW_ACC_PROJECT_ACCOUNT= VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS=`.

## Local Terraform development override

Build the provider binary, then add a development override to the Terraform
CLI configuration file (normally `~/.terraformrc`):

```hcl
provider_installation {
  dev_overrides {
    "elva-labs/vew" = "/absolute/path/to/terraform-provider-vew"
  }
  direct {}
}
```

The override bypasses normal provider installation and uses the binary built
by `make build`. Terraform may print a warning that the development override
is active; this is expected. Keep `direct {}` so unrelated providers retain
their normal installation behavior.

## Component resource

The complete example is in
[`examples/resources/vew_component/resource.tf`](examples/resources/vew_component/resource.tf):

```hcl
resource "vew_component" "example" {
  project_id              = "project-example"
  name                    = "example-component"
  description             = "Managed by Terraform"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
```

### Resource attributes

Required configuration attributes:

- `project_id` — VEW project identifier.
- `name` — component name.
- `description` — component description.
- `platform` — component platform, such as `Linux`.
- `supported_architectures` — set of architectures, such as `arm64` and
  `x86_64`.
- `supported_os_versions` — set of supported OS versions, such as `Ubuntu 24`.

The provider returns these computed attributes: `id`, `status`, `created_at`,
`created_by`, `updated_at`, and `updated_by`.

## Lifecycle semantics

`project_id`, `name`, `platform`, `supported_architectures`, and
`supported_os_versions` require replacement when changed. `description` is the
only mutable configuration field and is sent through the VEW update API.

Terraform delete maps to VEW archive, not physical deletion. A missing
component is treated as already archived. A subsequent refresh removes the
Terraform resource when VEW reports `404` or an `ARCHIVED` status.

Create sends one idempotency key for the operation and then reads the canonical
component. If the process or connection fails after VEW accepts the create but
before Terraform receives the response, Terraform cannot automatically reuse
that key on a later apply. This is the idempotency crash window: inspect VEW
before retrying so an accepted component is not accidentally duplicated.

## Import

Import IDs use `project_id/component_id`:

```shell
terraform import vew_component.example project-example/cmp-123
```

After import, Terraform refreshes the component and populates its computed
attributes. The imported configuration must match the component's replacement
fields, or Terraform will plan a replacement.

## Component version resource

The complete native Terraform example is in
[`examples/resources/vew_component_version/resource.tf`](examples/resources/vew_component_version/resource.tf).
It references its `vew_component` parent directly and uses `jsonencode` for the
definition.

### Resource attributes

`project_id`, `component_id`, `description`, `release_type`, `definition_json`,
`software_vendor`, and `software_version` are required. `dependencies` defaults
to an empty list; `license_dashboard` and `notes` are optional. Each dependency
contains `component_id`, `component_name`, `version_id`, `version_name`, and
`order`; `type` defaults to `HELPER`, while `position` is optional. The provider
computes `id`, `name`, `status`, and the created/updated timestamps and actors.
Configured `license_dashboard` and `notes` must be non-empty; omit them or use
`null` to leave them unset.

`definition_json` is retained in Terraform state. Do not put secrets or other
sensitive content in the definition unless storing that content in state is
acceptable for your Terraform backend and access controls.

### Lifecycle semantics

Changing `project_id` or `component_id` replaces the resource. VEW validates a
created or updated version asynchronously: Terraform waits through `CREATING`,
`CREATED`, `TESTING`, and `UPDATING`, and completes only when the status is
`VALIDATED`. The default timeouts are 60 minutes for create, 60 minutes for
update, and 30 minutes for delete; override them with a `timeouts` block using
Go duration strings, for example `create = "90m"`.

If VEW reports `FAILED`, Terraform preserves the IDs and latest lifecycle state
and returns an error. After a failed update, it also preserves the prior mutable
configuration through refresh so an unchanged apply retries validation. Correct
the issue and run apply again to recover; do not remove the resource from state
merely to retry. Terraform delete maps to VEW
retire, not physical deletion. A remote `RETIRED` version or `404` is therefore
treated as absent from state.

VEW does not return the configured release type. Immediately after import,
Terraform permits one-time adoption of the configured `release_type` without an
update; later changes to that value require replacement. Release itself is out
of scope for this resource and is not managed by Terraform.
Definition formatting, equivalent dependency ordering, and timeout-only edits
update Terraform state without a VEW mutation, including for released imports.
Genuine mutable changes to a released version require replacement.

Component-version creation uses one idempotency key for its transport retries.
If Terraform crashes after VEW accepts the request but before Terraform records
the response, a later apply cannot reuse that key: inspect VEW before retrying
to avoid accidentally creating a duplicate version. This is the idempotency
crash window.

## Component version import

Import IDs use `project_id/component_id/version_id`:

```shell
terraform import vew_component_version.example PROJECT_ID/COMPONENT_ID/VERSION_ID
```

## Recipe resource

The complete example is in
[`examples/resources/vew_recipe/resource.tf`](examples/resources/vew_recipe/resource.tf).
`vew_recipe` requires `project_id`, `name`, `description`, `platform`,
`architecture`, and `os_version`. It computes `id`, `status`, `created_at`,
`created_by`, `updated_at`, and `updated_by`. VEW currently accepts `Linux` with
`Ubuntu 24` and `amd64` or `arm64`, or `Windows` with `Microsoft Windows Server
2025` and `amd64`. The provider validates these combinations before sending a
request. All configured fields require replacement because VEW has no recipe
update endpoint.

The S2S OAuth client must have both `clients/packaging/recipe.read` and
`clients/packaging/recipe.write` scopes explicitly granted, as well as access to
the VEW project named by `project_id`. The recipe resources use that project for
all API operations; recipe versions must use the same project and the parent
recipe's ID.

Deleting a recipe archives it in VEW. VEW refuses to archive a recipe while it
has any version that is not `RETIRED`, so Terraform dependencies should ensure
the versions retire before the recipe is archived. A remote `ARCHIVED` recipe
or `404` is treated as absent and removed from state. When VEW identifies a
blocking version, the archive diagnostic names that version and its status;
Terraform keeps the recipe in state so deletion can be retried.

Recipe import IDs use `project_id/recipe_id`:

```shell
terraform import vew_recipe.example project-example/recipe-123
```

Recipe create uses an idempotency key across transport retries. If VEW accepts
the create but Terraform loses the response or crashes before recording the
recipe ID, a later apply cannot reuse that key. Inspect VEW before retrying to
avoid creating a duplicate.

## Recipe version resource

The resource example is in
[`examples/resources/vew_recipe_version/resource.tf`](examples/resources/vew_recipe_version/resource.tf).
The recipe ID and project are direct references to the parent recipe. The
`configured_components` can reference component and version IDs from
`vew_component` and `vew_component_version`. Each ordered entry requires
`component_id`, `version_id`, and `type` (`MAIN` or `HELPER`). The provider
derives order from list position and looks up omitted names through the
component read API before writes. Existing configurations can still supply
names and positive unique order values. The OAuth client therefore also needs
`clients/packaging/component.read` and `.write` granted because the component
transport requests both scopes. The provider does not release
component versions for you. VEW accepts selected component
versions in `VALIDATED` or `RELEASED` state; releasing a recipe and any
required component versions remains a separate VEW action.

Required attributes are `project_id`, `recipe_id`, `description`,
`volume_size`, and `configured_components`. Supply the configured list for both
create and import; use `[]` when no components are selected. Set `release_type`
to `MAJOR`, `MINOR`, or `PATCH` when creating a version; omit it when importing
because VEW does not expose the original create instruction. `volume_size` is
an integer from 8 through 500 GB. `integrations` is an optional set of nonempty strings and defaults to an
empty set. Computed attributes include `id`, `name`, `status`,
`effective_components`, and the created/updated timestamps and actors.
`configured_components` preserves the caller's intended selection;
`effective_components` reflects VEW's resolved list, including mandatory
components, and does not replace configured intent.

VEW tests a created or updated version asynchronously. Terraform waits through
`CREATING`, `CREATED`, `TESTING`, and `UPDATING` until the version is
`VALIDATED` or `FAILED`. Create and update default to 60 minutes, and delete
defaults to 30 minutes. Override these with a `timeouts` block using Go duration
strings, for example `create = "90m"`. Terraform delete retires the version and
waits for `RETIRED`; a `RETIRED` version or `404` is treated as absent.

Release is a separate VEW action and is not managed by Terraform. A released
version remains readable, but changes to its content require replacement.
VEW does not return `release_type`. The provider retains it for versions it
creates, but imported versions leave it unset. VEW does return the version name
(for example, `1.0.0`) as computed `name`. Changing a recorded `release_type`
requires replacement; removing it only forgets the local create instruction.
VEW may omit configured component selection on reads. The provider
keeps a known Terraform selection during refresh. On the first refresh after
import, it uses the effective list as the comparison baseline, even when VEW
omits configured selection. Import itself changes only Terraform state. The
first plan compares ordered component IDs, version IDs, and type; differences
plan an update for a mutable version or replacement for a released version.
VEW may include mandatory components in that baseline, so review the first
plan before applying it. Supply `configured_components` in the imported
resource's Terraform configuration, including `[]` for an empty selection.

Recipe-version import IDs use `project_id/recipe_id/version_id`:

```shell
terraform import vew_recipe_version.example project-example/recipe-123/version-456
```

## Release actions

VEW releases are one-way operations, so they are separate provider actions and
never happen as a side effect of creating or updating a version resource. The
[`vew_component_version_release`](docs/actions/component_version_release.md)
action releases a validated component version, and the
[`vew_recipe_version_release`](docs/actions/recipe_version_release.md)
action releases a validated recipe version. The caller must have the
corresponding VEW release permission and access to the specified project.

Apply the version resources first so they exist and are validated. Then invoke
the component release action, followed by the recipe release action. A recipe
can only be released after its selected component versions are released when
VEW requires that state:

```shell
terraform apply
terraform apply -invoke=action.vew_component_version_release.example
terraform apply -invoke=action.vew_recipe_version_release.example
terraform apply
```

The last normal apply refreshes the version resources and records their
`RELEASED` status before dependent configuration, such as a pipeline, is
applied. If an action fails, correct the cause and explicitly invoke that
action again; Terraform does not remember a failed invocation as a retryable
resource operation. Do not remove or taint the version to retry a release.

Runnable CLI examples are in
[`examples/actions/component-version-release`](examples/actions/component-version-release)
and [`examples/actions/recipe-version-release`](examples/actions/recipe-version-release).
Optional automatic-release examples are in
[`examples/actions/component-version-release-after-create`](examples/actions/component-version-release-after-create)
and [`examples/actions/recipe-version-release-after-create`](examples/actions/recipe-version-release-after-create).
They use `lifecycle.action_trigger` after creation with `on_failure = halt`,
which stops the apply without tainting or replacing the version. A later apply
does not retry the action; invoke it explicitly after correcting the failure.

Release actions do not create Terraform state or reverse a release. Removing
an action block only removes its local invocation configuration. Removing a
version resource from configuration still follows that resource's delete
behavior (VEW retirement), and VEW may reject retirement of a released
version. The provider does not automatically retire released versions or
release versions based on updates. VEW remains the authority for release
eligibility, prerequisites, and whether a release request is accepted. Do not
configure `after_update` triggers: they can invoke a one-way release
repeatedly. There is no provider-imposed limit of two release invocations; VEW
decides whether a given version can be released again.

Recipe-version create also uses an idempotency key across transport retries. If
VEW accepts creation but Terraform crashes before recording the returned ID,
the key cannot be reused by a later apply; inspect VEW before retrying. After
validation starts, Terraform retains the version ID and reconciles remote state
on a later apply if polling is interrupted.

## Pipeline resource

The example is in
[`examples/resources/vew_pipeline/resource.tf`](examples/resources/vew_pipeline/resource.tf).
It imports the existing recipe into Terraform using its project and recipe IDs,
then references that resource and takes a released recipe-version ID as an
input. Set the recipe attributes to their current VEW values so import does not
plan a recipe replacement. The version must belong to that recipe and already
be `RELEASED` in VEW before creating or updating the pipeline. Recipe-version
release uses the provider's separate `vew_recipe_version_release` action and is
never a side effect of pipeline management. If VEW rejects an unreleased
version, the provider reports `Recipe version is not released`, names the
requested recipe and version IDs, and directs you to invoke the release action,
refresh, and retry `terraform apply`.

The required configuration fields are `project_id`, `name`, `description`,
`recipe_id`, `recipe_version_id`, `build_instance_types`, and `schedule`.
`build_instance_types` is a nonempty ordered list of nonempty strings.
`schedule` is VEW's six-field expression without a `cron(...)` wrapper. The
optional `product_id` associates a product. Computed values include `id`,
`status`, recipe names, distribution and infrastructure configuration ARNs,
pipeline ARN, and audit fields. A nullable ARN or product association remains
null when VEW has no value.

Changes to `project_id`, `name`, `description`, or `recipe_id` replace the
pipeline. Changes to `recipe_version_id`, `build_instance_types`, `schedule`,
or `product_id` update it in place. Remove `product_id` from configuration (or
set it to `null`) to clear the remote association; the update sends that clear
explicitly. Build instance types preserve list order. Terraform does structural
validation, while VEW validates the deployment-specific instance types and
complete schedule semantics.

The OAuth client needs `clients/packaging/pipeline.read` and
`clients/packaging/pipeline.write` explicitly granted, plus project assignment
for the configured project. Pipeline management does not require
`clients/packaging/pipeline.execute`. Creating or updating a pipeline only
configures it; image-build execution is a separate operation and is never
started by this resource.

Create and update wait through asynchronous work until the pipeline is
`CREATED`; delete retires it and waits until it is `RETIRED`. The defaults are
60 minutes for create and update and 30 minutes for delete. Override them with
resource `timeouts` using Go duration strings, such as `create = "90m"`.
Terraform read removes a missing (`404`) or retired pipeline from state. Import
IDs use `project_id/pipeline_id` and only read the remote pipeline:

```shell
terraform import vew_pipeline.example project-example/pipeline-123
```

If polling is interrupted after VEW accepts create or update, Terraform keeps
the pipeline ID and latest safe status so a later apply can reconcile the
operation. There remains a process-crash window if VEW accepts create before
Terraform records the returned ID; the same idempotency key is reused for
transport retries within that attempt, but not across a later Terraform run.
Inspect VEW before retrying after such a crash to avoid creating a duplicate.

## Projects API resources

The `vew_technology` and `vew_project_account` resources use the Projects API
URL from `projects_api_url` or `VEW_PROJECTS_API_URL`. Both need an active
assignment to the project. Technology management needs
`clients/projects/technology.read` and
`clients/projects/technology.write`; project-account management needs
`clients/projects/account.read` and `clients/projects/account.write`.

`vew_technology` manages a project's technology catalog entry. Import IDs are
`project_id/technology_id`. VEW can reject technology deletion while any
retained account assignment refers to it, even after that assignment has been
deactivated. Resolve the account reference through VEW before deleting the
technology. Add `lifecycle { prevent_destroy = true }` when Terraform must
block a planned deletion or replacement.

`vew_project_account` associates a project's technology, stage, and AWS region
with a 12-digit AWS account ID. Its account type is `USER` or `TOOLCHAIN`; its
stage is `dev`, `qa`, or `prod`. Import IDs are `project_id/account_id`.
Creating and updating starts asynchronous onboarding or re-onboarding. Terraform
polls for completion for up to two hours by default, configurable with
`timeouts.create` and `timeouts.update`. After deactivation, creating the
assignment again for the same project and AWS account makes VEW reuse the
inactive assignment's internal ID and start onboarding again.

Removing a project-account resource deactivates its VEW assignment. This does
not remove the AWS account, undo onboarding, or offboard AWS resources. VEW
retains the account's technology reference after deactivation. Onboarding
changes the real AWS account and can incur charges, so use only an explicitly
approved disposable account. Add `lifecycle { prevent_destroy = true }` to
block a planned deletion or replacement while the assignment needs to remain
active. Examples are in
[`examples/resources/vew_technology/resource.tf`](examples/resources/vew_technology/resource.tf)
and
[`examples/resources/vew_project_account/resource.tf`](examples/resources/vew_project_account/resource.tf).

## Publishing API resources

The `vew_product` resource uses the Publishing API URL from
`publishing_api_url` or `VEW_PUBLISHING_API_URL`. It needs an active
assignment to the project and the `clients/publishing/product.read` and
`clients/publishing/product.write` scopes. Import IDs are
`project_id/product_id`. The product's `type` (`WORKBENCH`, `VIRTUAL_TARGET`,
or `CONTAINER`) and `technology_id` are fixed; changing them replaces the
product. Deleting it archives the product and unpublishes all of its versions.

`vew_product_version_promotion` promotes a product version to `DEV`, `QA`, or
`PROD` and waits until it is published in the stage's accounts. It needs
`clients/publishing/version.read` and `clients/publishing/version.promote`.
Import IDs are `project_id/product_id/version_id/stage`. Destroying it only
removes it from Terraform; VEW does not undo a release. The
`vew_product_versions` data source lists a product's versions and their stages
with `clients/publishing/version.read`.

## Data sources

The provider reads existing components, component versions, recipes, recipe
versions, pipelines, and images with the exact-ID data sources `vew_component`,
`vew_component_version`, `vew_recipe`, `vew_recipe_version`, `vew_pipeline`, and
`vew_image`. `vew_pipelines` and `vew_images` list project objects. These
sources do not mutate VEW; exact lookups require an existing ID, while lists
can be empty. The caller needs project assignment and the corresponding
component, recipe, or pipeline read scope.

See the [data-source guide](docs/guides/data-sources.md) for scope and behavior
details and the [example](examples/data-sources/README.md) for all eight
data sources. Image build progress is not available as a structured Terraform
value; read a known image ID or refresh `vew_images` after the build.

## Image build action

`vew_image_build` explicitly starts a build for an existing pipeline and waits
for it to finish. A normal plan or apply does not invoke it. The action needs
the pipeline execute and read scopes, and a caller-supplied UUID
`idempotency_key` to recover the same logical build after an interruption.
Builds may incur AWS charges.

See the [action guide](docs/actions/image_build.md) for invocation, timeout,
and retry behavior and the [runnable example](examples/actions/image-build/README.md).

## Stable and beta releases

The [release workflow](.github/workflows/release.yml) builds signed Terraform
Registry assets from the current `main` or `beta` commit. Start **Release
provider** in GitHub Actions, select the branch in **Use workflow from**, and
enter a new version tag:

- On `main`, use a stable SemVer tag such as `v0.2.0`.
- On `beta`, use a tag of the form `v0.2.0-beta.1`. Increment the beta number
  for each subsequent release, such as `v0.2.0-beta.2`.

The workflow rejects versions that do not match the branch and commits that
are no longer the branch tip. It reruns tests and the docs check on a
GitHub-hosted ARM runner, creates the tag on that commit, then uses GoReleaser
to publish a GitHub Release. Beta releases keep the `-beta.N` version suffix
and do not replace GitHub's latest stable release. The GitHub prerelease
checkbox is left off during publication so the Terraform Registry can index
the release. Terraform recognizes the beta from its version suffix and
requires an exact version constraint to install it.

After publication, confirm that the new version appears in the Registry and
installs with `terraform init -upgrade`. If it is missing, use **Resync** in
the provider's Registry settings with the GitHub prerelease checkbox still
off. During publication of `v0.2.0-beta.1`, the Registry returned
`Ignored draft release event` when the GitHub prerelease checkbox was enabled;
the version was indexed after clearing it and resyncing. Once indexed, the
GitHub prerelease checkbox can optionally be enabled. See the
[reported Registry prerelease issue](https://discuss.hashicorp.com/t/publishing-pre-release-versions-of-provider-ignored-draft-release-event/69536).

To graduate a beta, merge `beta` into `main` and publish the stable version,
such as `v0.2.0`. Do not move or replace a published version tag; release a
new version for corrections.

Create `beta` from a commit containing this workflow before releasing from
that branch. Both release types publish under the same `elva-labs/vew`
provider address. Testers must opt in with an exact prerelease version:

```hcl
terraform {
  required_providers {
    vew = {
      source  = "elva-labs/vew"
      version = "= 0.2.0-beta.1"
    }
  }
}
```

Run `terraform init -upgrade` after changing the version constraint to update
the dependency lock file. Terraform excludes prereleases from ordinary
version ranges. The Registry website can still show a higher-version beta
as latest, even though Terraform requires an exact constraint to select it.
See the [Terraform Registry prerelease FAQ](https://developer.hashicorp.com/terraform/registry/faq#can-i-prevent-prereleases-from-being-the-latest-version).

Before the first run, configure repository Actions secrets `GPG_PRIVATE_KEY`
and `PASSPHRASE` for an RSA or DSA release-signing key. Keep the private key
outside the repository. Add the matching public key to the Terraform Registry
account that will publish the provider. The manifest declares Terraform plugin
protocol 6.0. After the first signed GitHub Release exists, publish the
provider through the Terraform Registry UI; later releases are discovered by
its GitHub webhook.

## Domain package layout

Shared OAuth, HTTP transport, errors, and polling live in `internal/vew`.
Domain API models and endpoint clients live in `internal/vew/components`,
`internal/vew/recipes`, `internal/vew/pipelines`, and `internal/vew/images`.
Terraform resources, data sources, and actions live in the matching
`internal/provider` packages, while `internal/providerdata` carries their
configured interfaces from the root provider.
