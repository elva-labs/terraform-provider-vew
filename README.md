# Terraform Provider for VEW

This proof-of-concept Terraform provider manages VEW components. It uses the
VEW OAuth 2.0 client-credentials flow and exposes the `vew_component` resource.

## Prerequisites

- Go 1.27 or newer (the module declares `go 1.27.0`).
- Terraform 1.16.3 for the documented local-development workflow.
- VEW API credentials and a project in which to manage components.

The provider uses these environment variables when the corresponding provider
attribute is omitted or empty:

| Provider attribute | Environment variable | Description |
| --- | --- | --- |
| `api_url` | `VEW_API_URL` | Absolute VEW API HTTP(S) URL |
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
