# VEW Terraform Provider: Component Vertical Slice

## Summary

Build a standalone Terraform provider for Virtual Engineering Workbench (VEW) in
`github.com/elva-labs/terraform-provider-vew`. The proof of concept will expose one
production-shaped resource, `vew_component`, backed by the VEW Packaging S2S API.

The slice must demonstrate provider configuration, OAuth client-credentials
authentication, Terraform CRUD and import semantics, VEW idempotent creation,
drift handling, unit tests, and an opt-in live acceptance test. Recipes, component
versions, pipelines, and images are deliberately out of scope.

## Goals

- Prove that VEW component lifecycle operations map cleanly to Terraform.
- Exercise the recently added S2S OAuth scopes and idempotent create contract.
- Give future resources a small, maintainable client and provider foundation.
- Detect configuration drift and converge it where the VEW API allows updates.
- Keep credentials out of Terraform configuration and state when environment
  variables are used.

## Non-goals

- Cover the complete Packaging API.
- Generate and maintain a client for the complete OpenAPI document.
- Publish the provider to the Terraform Registry in this iteration.
- Guarantee recovery if Terraform itself terminates after VEW commits a create but
  before Terraform persists resource state.

## Chosen approach

Use Go and Terraform Plugin Framework with protocol version 6. Implement a small,
handwritten VEW client containing only the OAuth and component endpoints needed by
this slice.

This is preferred over generating the whole OpenAPI client because the generated
surface and maintenance burden would be disproportionate to one resource. A
generic JSON passthrough resource is also rejected because it would sacrifice
schema validation, meaningful plans, and reliable drift detection.

## Repository structure

```text
terraform-provider-vew/
├── cmd/terraform-provider-vew/main.go
├── internal/client/
│   ├── client.go
│   ├── client_test.go
│   ├── oauth.go
│   ├── oauth_test.go
│   └── types.go
├── internal/provider/
│   ├── component_resource.go
│   ├── component_resource_test.go
│   ├── provider.go
│   ├── provider_test.go
│   └── provider_test_helpers_test.go
├── examples/
│   ├── provider/provider.tf
│   └── resources/vew_component/resource.tf
├── docs/superpowers/specs/
├── .gitignore
├── LICENSE
├── Makefile
├── README.md
└── go.mod
```

The packages remain internal until a second resource demonstrates a need for a
public library boundary.

## Provider configuration

The provider exposes these optional configuration attributes, each with an
environment-variable fallback:

| Attribute | Environment variable | Behavior |
| --- | --- | --- |
| `api_url` | `VEW_API_URL` | Base URL of the VEW S2S API |
| `token_url` | `VEW_TOKEN_URL` | OAuth token endpoint |
| `client_id` | `VEW_CLIENT_ID` | OAuth client identifier |
| `client_secret` | `VEW_CLIENT_SECRET` | Sensitive OAuth client secret |

Configuration fails with actionable diagnostics when required values are absent,
URLs are invalid, or conflicting values are supplied. Environment values are read
during provider configuration and are not copied into resource state.

The OAuth client requests the component read and write scopes. Access tokens are
cached in memory until shortly before expiry. A `401` causes one forced refresh and
one replay of the original request.

## `vew_component` schema

| Attribute | Kind | Lifecycle |
| --- | --- | --- |
| `id` | Computed string | VEW component identifier |
| `project_id` | Required string | Replacement on change |
| `name` | Required string | Replacement on change |
| `description` | Required string | Updated in place |
| `platform` | Required string | Replacement on change |
| `supported_architectures` | Required set of strings | Replacement on change |
| `supported_os_versions` | Required set of strings | Replacement on change |
| `status` | Computed string | Refreshed from VEW |
| `created_at` | Computed string | Refreshed from VEW |
| `created_by` | Computed string | Refreshed from VEW |
| `updated_at` | Computed string | Refreshed from VEW |
| `updated_by` | Computed string | Refreshed from VEW |

Sets are used for architectures and OS versions because their order has no domain
meaning. Conversion code will sort them before requests so tests and request
hashing remain deterministic.

## Lifecycle behavior

### Create

1. Convert the planned resource to `CreateComponentRequest`.
2. Generate a random UUID idempotency key once per Terraform Create invocation.
3. Send `POST /projects/{projectId}/components` with that key.
4. Reuse the exact key and body for every retry within the invocation.
5. Save the returned component ID, then read the canonical representation.

If VEW reports that the idempotency operation is still in progress, the client
honors `Retry-After` and repeats the same request. A successful replay is handled as
an ordinary create response.

Terraform Plugin Framework cannot recover provider-private data before any state
has been committed. Therefore, if the Terraform process terminates after VEW has
committed the component but before Create returns state, a subsequent apply cannot
recover the generated key. The provider documents this narrow crash window instead
of deriving a key from configuration, which could incorrectly replay a historical
create when an identical component is legitimately recreated.

### Read

Fetch `GET /projects/{projectId}/components/{componentId}` and map the canonical
response into state. A `404` removes the resource from state. An `ARCHIVED`
component is also treated as absent because archive is the API's delete semantic.

### Update

Only `description` is mutable. Send `PUT` with `componentDescription`, then perform
a Read to refresh canonical and audit values. All create-only fields use replacement
plan modifiers.

### Delete

Send `DELETE /projects/{projectId}/components/{componentId}`. A `404` is successful
because the desired absent state already exists. The API archives rather than
physically deletes the component; Terraform then drops the resource from state.

### Import

Accept `<project_id>/<component_id>`. Reject empty or malformed identifiers with an
actionable diagnostic. Import populates `project_id` and `id`; the next Read fills
the remaining state.

## Client behavior and errors

The client uses `context.Context` throughout so Terraform cancellation interrupts
OAuth and API calls. HTTP clients have bounded timeouts.

VEW `application/problem+json` responses are decoded into a typed error containing
status, code, detail, request ID, and retryability. Terraform diagnostics include
the operation and safe problem details, but never OAuth credentials or tokens.

The retry policy is bounded and uses exponential backoff with jitter:

- Retry `429`, explicitly retryable problems, and transient `5xx` responses.
- Honor a valid `Retry-After` header.
- Retry a create only with the original idempotency key and identical body.
- Do not retry ordinary validation, authorization, or conflict errors unless the
  response specifically identifies the same idempotency operation as in progress.

## Testing strategy

Implementation follows test-driven development.

Unit tests use `httptest.Server` and cover:

- OAuth request shape, token caching, expiry, and one-time refresh after `401`.
- Component request and response mapping.
- Typed problem decoding and Terraform-safe diagnostics.
- Retry bounds and `Retry-After` handling.
- Reuse of one idempotency key and identical body after a simulated lost response.
- Read removal for `404` and `ARCHIVED`.
- Replacement versus in-place update schema behavior.
- Import parsing and malformed import diagnostics.

The opt-in acceptance test uses `terraform-plugin-testing` and
`ProtoV6ProviderFactories`. With `TF_ACC=1` and the required `VEW_*` variables, it
will:

1. Create a uniquely named component.
2. Check local state and the remote component.
3. Verify a subsequent empty plan.
4. Update the description in place.
5. Import the component and verify imported state.
6. Destroy it and confirm the component is archived.

Live acceptance testing will run only after the OAuth deployment is confirmed and
with explicit authorization to create and archive a component in the selected VEW
project.

## Documentation and developer workflow

The README will contain installation-from-source instructions, provider
configuration, environment variables, a minimal resource example, import syntax,
the archive behavior, and the idempotency crash-window caveat.

The Makefile will expose focused `fmt`, `test`, and `testacc` targets. CI and
Terraform Registry release automation are deferred until the vertical slice is
validated.

Go and Terraform are not currently available on the local `PATH`. Toolchain setup
is therefore an explicit implementation prerequisite and must be verified before
the first test run.

## Acceptance criteria

- The provider starts under Terraform protocol version 6.
- Provider configuration authenticates using OAuth client credentials.
- `vew_component` plans and performs create, read, description update, archive, and
  import operations against the documented VEW S2S contract.
- Create retries reuse one idempotency key and body.
- External deletion/archive is reflected by removing the resource from state.
- Unit tests pass locally.
- The acceptance test is present, gated, and passes once credentials and a deployed
  target environment are provided.
- No secret or access token appears in logs, diagnostics, examples, or state.
