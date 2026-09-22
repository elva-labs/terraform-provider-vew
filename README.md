# Terraform Provider for VEW

This proof-of-concept Terraform provider manages VEW components and component
versions. It uses the VEW OAuth 2.0 client-credentials flow and exposes the
`vew_component` and `vew_component_version` resources.

## Prerequisites

- Go 1.27 or newer (the module declares `go 1.27.0`).
- Terraform 1.16.3 for the documented local-development workflow.
- VEW API credentials and a project in which to manage components.

The provider uses these environment variables when the corresponding provider
attribute is omitted or empty:

| Provider attribute | Environment variable | Description |
| --- | --- | --- |
| `api_url` | `VEW_API_URL` | Absolute VEW API HTTP(S) URL, including `/clients/packaging/v1` (the provider appends `/projects/...`) |
| `token_url` | `VEW_TOKEN_URL` | Absolute OAuth token HTTP(S) URL |
| `client_id` | `VEW_CLIENT_ID` | OAuth client ID |
| `client_secret` | `VEW_CLIENT_SECRET` | OAuth client secret (sensitive) |

Explicit provider attributes take precedence over environment variables. All
four values are required after fallback resolution. The provider configuration
can therefore remain empty, as in
[`examples/provider/provider.tf`](examples/provider/provider.tf):

```hcl
provider "vew" {}
```

## Build and test

The repository includes a Makefile using the Go toolchain at
`/Users/ruanheyns/.govm/go/bin/go` by default. Override `GO` when using another
installation:

```shell
make fmt
make test
make build
```

The equivalent commands are:

```shell
/Users/ruanheyns/.govm/go/bin/go mod tidy
/Users/ruanheyns/.govm/go/bin/go fmt ./...
/Users/ruanheyns/.govm/go/bin/go vet ./...
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
/Users/ruanheyns/.govm/go/bin/go build ./cmd/terraform-provider-vew
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
  VEW_TEST_PROJECT_ID=prog-73488 \
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
  project_id              = "prog-73488"
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
terraform import vew_component.example prog-73488/cmp-123
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

## Domain package layout

Shared OAuth, HTTP transport, errors, and polling live in `internal/vew`.
Component and component-version API models and endpoint clients live in
`internal/vew/components`. Terraform resource implementations live in
`internal/provider/components`, while `internal/providerdata` carries the
configured domain interfaces from the root provider to those resources.
