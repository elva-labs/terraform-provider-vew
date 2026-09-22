# VEW Terraform Provider: Domain Packages and Component Versions

## Summary

Reorganize the VEW Terraform provider around VEW domains and add a
production-shaped `vew_component_version` resource. Shared OAuth, HTTP,
retry, polling, and problem handling remain reusable infrastructure; component
and component-version API and Terraform code move into focused `components`
packages. Future pipelines, recipes, and images will follow the same package
pattern without expanding a monolithic client or provider package.

The component-version resource manages the draft lifecycle: create, read,
update, import, drift reconciliation, and retire-as-delete. Release remains out
of scope. VEW automatically tests a published component version, so create and
update wait through the asynchronous build and testing states until the version
becomes `VALIDATED` or `FAILED`.

## Goals

- Organize the provider so each VEW domain has separate API and Terraform
  packages.
- Preserve all existing `vew_component` behavior during the package move.
- Add complete Terraform lifecycle and import support for component versions.
- Represent the flexible component definition without mirroring its entire
  evolving schema in the provider.
- Wait for VEW asynchronous operations using status-based polling rather than
  fixed sleeps.
- Preserve enough Terraform state after partial failures to prevent duplicate
  component-version creation.
- Keep credentials, tokens, response bodies, and component definitions out of
  diagnostics and logs.

## Non-goals

- Manage component-version release.
- Manage component-version testing as a separate Terraform resource or action.
- Add pipeline, recipe, or image resources in this slice.
- Generate a client for the complete VEW OpenAPI document.
- Publish the provider to the Terraform Registry.
- Hide the component definition from Terraform state; configured resource data
  necessarily remains in state.

## Chosen architecture

Use layered domain packages. The shared `vew` package owns transport concerns,
while its `components` child package owns component wire models and endpoints.
The Terraform provider has a matching `components` child package that owns both
component resources. Provider configuration remains at the provider root.

```text
terraform-provider-vew/
├── cmd/terraform-provider-vew/main.go
├── internal/
│   ├── vew/
│   │   ├── transport.go
│   │   ├── transport_test.go
│   │   ├── oauth.go
│   │   ├── oauth_test.go
│   │   ├── errors.go
│   │   ├── waiter.go
│   │   ├── waiter_test.go
│   │   └── components/
│   │       ├── client.go
│   │       ├── client_test.go
│   │       ├── component.go
│   │       └── component_version.go
│   ├── providerdata/
│   │   └── data.go
│   └── provider/
│       ├── provider.go
│       ├── provider_test.go
│       ├── components/
│       │   ├── component_resource.go
│       │   ├── component_resource_test.go
│       │   ├── component_resource_acceptance_test.go
│       │   ├── component_version_resource.go
│       │   ├── component_version_resource_test.go
│       │   └── component_version_resource_acceptance_test.go
│       └── testhelpers/
│           └── vew_server.go
└── examples/
    └── resources/
        ├── vew_component/resource.tf
        └── vew_component_version/resource.tf
```

Future domains use the same pair of packages:

```text
internal/vew/pipelines/       internal/provider/pipelines/
internal/vew/recipes/         internal/provider/recipes/
internal/vew/images/          internal/provider/images/
```

This is preferred over a single vertical package per domain because Terraform
Plugin Framework concerns remain separate from HTTP and wire-format concerns.
It is preferred over reorganizing only the provider resources because that
would leave the API client as the next monolith.

## Package responsibilities and wiring

`internal/vew` configures OAuth and exposes a bounded authenticated transport.
The transport owns request construction, response-size limits, `401` refresh,
bounded retry, `Retry-After`, idempotency headers, and RFC 9457 problem errors.
It does not contain component wire models.

`internal/vew/components` consumes the shared transport. It exposes focused
component and component-version interfaces and owns endpoint paths, input
models, response envelopes, and status values. Component create retries retain
their existing stable idempotency key and byte-identical body behavior.
Component-version create receives the same guarantee.

`internal/providerdata.Data` is the value placed in
`provider.ConfigureResponse.ResourceData`. It contains the configured domain
clients required by resources. This avoids passing one growing client type to
every resource and gives future domain packages an explicit dependency.

`internal/provider/components` consumes only the component interfaces in
provider data. It owns Terraform schemas, plan modifiers, JSON normalization,
state mapping, import parsing, CRUD orchestration, and diagnostics.

The root provider registers resource constructors from domain packages. It
continues to expose the existing provider schema and environment fallbacks.

## `vew_component_version` resource

### Example

```hcl
resource "vew_component_version" "aws_cli" {
  project_id   = vew_component.aws_cli.project_id
  component_id = vew_component.aws_cli.id

  description      = "Install AWS CLI"
  release_type     = "MAJOR"
  software_vendor  = "Amazon"
  software_version = "2"

  definition_json = jsonencode({
    schemaVersion = "1.0"
    phases = [{
      name = "build"
      steps = [{
        name   = "InstallAwsCli"
        action = "ExecuteBash"
        inputs = {
          commands = ["install-aws-cli"]
        }
      }]
    }]
  })

  dependencies = []
  notes        = "Managed by Terraform"
}
```

### Schema

| Attribute | Kind | Lifecycle |
| --- | --- | --- |
| `id` | Computed string | VEW component-version identifier |
| `project_id` | Required string | Replacement on change |
| `component_id` | Required string | Replacement on change |
| `description` | Required string | Updated in place while the remote version is mutable |
| `release_type` | Required string | `MAJOR`, `MINOR`, or `PATCH`; create-only replacement policy |
| `definition_json` | Required string | JSON validated and updated in place |
| `dependencies` | Optional list of nested objects, default empty | Updated in place |
| `software_vendor` | Required string | Updated in place |
| `software_version` | Required string | Updated in place |
| `license_dashboard` | Optional string | Updated in place |
| `notes` | Optional string | Updated in place |
| `name` | Computed string | VEW semantic version name |
| `status` | Computed string | Current VEW lifecycle status |
| `created_at` | Computed string | Refreshed from VEW |
| `created_by` | Computed string | Refreshed from VEW |
| `updated_at` | Computed string | Refreshed from VEW |
| `updated_by` | Computed string | Refreshed from VEW |

Each dependency contains these Terraform attributes:

| Attribute | Kind | VEW field |
| --- | --- | --- |
| `component_id` | Required string | `componentId` |
| `component_name` | Required string | `componentName` |
| `version_id` | Required string | `componentVersionId` |
| `version_name` | Required string | `componentVersionName` |
| `type` | Optional string, default `HELPER` | `componentVersionType` |
| `order` | Required integer | `order` |
| `position` | Optional string | `position` |

Dependencies are a list rather than a set because VEW requires an explicit
order. The provider validates `type` as `MAIN` or `HELPER`, `position` as
`APPEND` or `PREPEND`, and `order` as a positive integer. The provider sends
dependencies ordered by `order` and rejects duplicate order values during
planning.

### Component-definition JSON

`definition_json` is deliberately a JSON string produced with `jsonencode`.
Component step inputs can contain arbitrary JSON structures, and duplicating
the evolving VEW definition schema as deeply nested Terraform attributes would
make the provider brittle.

The provider parses the value during planning. Invalid JSON or a non-object
top-level value produces an attribute diagnostic before an API call. Valid JSON
is canonicalized by recursively decoding and encoding it, so whitespace and
object-key order do not create drift. Array order is retained.

VEW returns default values for component steps. Before comparison and request
serialization, the provider adds the same defaults when they are absent:

- `timeoutSeconds`: `7200`
- `onFailure`: `Abort`
- `maxAttempts`: `1`

The provider never writes the definition into logs or diagnostics. Because the
configured JSON is stored in Terraform state, documentation warns users not to
embed secrets and to protect remote state appropriately.

### Create-only release type and import

VEW uses `componentVersionReleaseType` to select the next semantic version but
does not return the historical value. The provider therefore treats
`release_type` as a create and recreation policy, not as remotely observable
state.

For provider-created resources, changing a previously stored `release_type`
requires replacement. During the first plan after import, a custom plan modifier
allows the configured release type to be adopted into state without calling the
update endpoint or forcing replacement. After adoption, later changes require
replacement. The provider never claims that an imported release type came from
VEW.

### Released imports

A `RELEASED` component version may be imported and read. Since VEW does not
allow released versions to be updated, changing a mutable configuration
attribute on a state whose status is `RELEASED` requires replacement rather
than an in-place update. Release itself remains unmanaged.

## Lifecycle behavior

### VEW asynchronous state flow

Create and update do not stop at `CREATED`. Deploying the Image Builder
component publishes `ComponentVersionPublished`; the VEW EventBridge rule starts
the component-version testing state machine. The observed success path is:

```text
CREATING -> CREATED -> TESTING -> VALIDATED
UPDATING -> CREATED -> TESTING -> VALIDATED
```

Any failed deployment or test ends in `FAILED`. Retirement moves through
`UPDATING` and finishes at `RETIRED`.

The provider waiter treats `CREATING`, `CREATED`, `TESTING`, and `UPDATING` as
pending where appropriate. Create and update succeed at `VALIDATED`, terminate
with an error at `FAILED`, and tolerate `RELEASED` only for reads of externally
released or imported resources. Delete succeeds at `RETIRED` or `404`.

### Create

1. Decode the Terraform plan and canonicalize `definition_json`.
2. Generate one UUID idempotency key and marshal one immutable request body.
3. Send `POST /projects/{projectId}/components/{componentId}/versions`.
4. Reuse the exact key and body for all retries in that Create call.
5. Save provisional Terraform state containing the returned version ID, planned
   configuration, and unknown computed audit values.
6. Poll the canonical GET endpoint until `VALIDATED` or `FAILED`.
7. On success, map the canonical response into state.

Setting provisional state before waiting prevents a timeout or downstream
failure from losing the remote identifier. A create timeout, cancellation, or
`FAILED` result returns an error while preserving the partial state so Terraform
does not blindly issue a second create. Framework tests must prove this behavior.

The existing narrow crash window remains: if Terraform terminates after VEW
commits the POST but before the provider receives and stores the ID, a later
apply cannot recover the random idempotency key. The provider documents this
rather than deriving a reusable key from configuration.

### Read

Fetch the component version using project, component, and version IDs. A `404`
or `RETIRED` status removes it from Terraform state. Other statuses and canonical
attributes refresh state. Read preserves the configured create-only release type
because VEW cannot return it.

If an interrupted earlier operation left the resource in a pending state, the
next mutating operation waits for that operation to settle before issuing another
mutation.

### Update

Compare planned mutable attributes with prior state. If the only difference is
release-type adoption immediately after import, store it without making a VEW
request. Otherwise send the complete VEW update payload, because the endpoint is
replace-shaped rather than patch-shaped, then wait through deployment and tests
until `VALIDATED` or `FAILED`.

VEW allows retries from `FAILED`; the provider therefore preserves the prior
configuration in state when update fails so a subsequent apply plans the update
again. A released remote version uses replacement semantics instead of Update.

### Delete

If the resource is pending, first wait for `VALIDATED` or `FAILED`. Send
`DELETE /projects/{projectId}/components/{componentId}/versions/{versionId}` and
poll until `RETIRED` or `404`. VEW accepts retirement from `VALIDATED`, `FAILED`,
or `RELEASED`. Terraform then removes the resource from state.

### Import

Import IDs use:

```text
project_id/component_id/version_id
```

The parser requires exactly three non-empty path segments. Import writes the
three identity fields, and Read populates remotely observable attributes. The
first plan adopts the configured `release_type` as described above.

## Waiting, timeouts, and cancellation

The shared waiter is condition-based and accepts a read function, pending and
terminal status predicates, an injected clock/sleep function for tests, and a
deadline. It always honors `context.Context` cancellation.

The resource exposes standard configurable operation timeouts with these
defaults:

- Create: 60 minutes
- Update: 60 minutes
- Delete: 30 minutes

The long create and update defaults account for launching EC2 test environments
for every supported architecture and operating-system combination. A valid
`Retry-After` value from VEW determines the next polling interval. Without one,
the waiter uses bounded exponential backoff with jitter. Polling never extends
past the operation deadline.

Transient GET failures use the shared bounded request retry policy. A terminal
authorization, validation, or not-found error is not hidden by the waiter.

## Errors and recovery

Diagnostics contain the Terraform operation, HTTP status, safe VEW problem code,
request ID, resource ID when known, and terminal lifecycle status. They exclude
OAuth credentials, bearer tokens, response bodies, and component-definition
content.

The provider distinguishes:

- API request failure before VEW accepts an operation.
- Asynchronous lifecycle failure reported as `FAILED`.
- Poll timeout.
- Terraform or user cancellation.
- Resource disappearance reported as `404`.

Create errors after an ID is known retain provisional state. Update errors retain
the last confirmed state so configuration remains planned for retry. Delete
errors retain the resource. `404` during Read or Delete is successful absence.

## Migration strategy

The reorganization is a behavior-preserving first task. Existing tests move with
their production packages, provider registration is updated, and no resource
schema or wire behavior changes. The complete race-enabled suite must pass before
component-version production code is added.

After the mechanical move:

1. Add shared transport and waiter tests, then extract the reusable behavior.
2. Add component-version wire models and client operations test-first.
3. Add JSON and dependency normalization test-first.
4. Add the Terraform schema, import, and lifecycle test-first.
5. Add documentation, examples, and a gated live acceptance test.

Each step must leave both the existing component resource and all completed
component-version behavior green.

## Testing strategy

Implementation follows strict red-green-refactor test-driven development.

Shared transport and waiter tests cover:

- Existing OAuth caching and one-time `401` refresh.
- Bounded retry and `Retry-After` behavior.
- Context cancellation and operation deadlines.
- Pending-to-success status sequences.
- `FAILED`, timeout, not-found, and cancellation terminal behavior.

Component client tests cover:

- Exact create, get, update, and retire paths.
- Exact camelCase request and response mapping.
- One non-empty create idempotency key and a byte-identical request body across
  retries.
- `202` action responses and canonical GET envelopes.
- Typed problem decoding without leaking bodies or secrets.

Terraform resource tests cover:

- Attribute kinds, validation, replacement rules, and operation timeouts.
- Invalid JSON, canonical JSON equality, retained array order, and injected step
  defaults.
- Dependency validation, ordering, and duplicate-order diagnostics.
- Import parsing and release-type adoption.
- Create, empty plan, mutable update, released replacement planning, Read drift,
  retire-as-delete, `404`, and external retirement.
- Provisional state after create timeout, cancellation, and terminal failure.
- Retryable update state after failure.
- Sanitized diagnostics.

The gated live acceptance test requires `TF_ACC=1`, the four `VEW_*` provider
variables, and `VEW_PROJECT_ID`. It creates a disposable component and component
version, waits for automatic validation, verifies an empty plan, updates the
version and waits for revalidation, imports it, and retires it during cleanup.
The test must never target the existing AWS CLI component or version.

The standard local completion gate is:

```shell
go test ./... -race -count=1
```

## Documentation

The README and resource example document:

- The domain-oriented repository layout.
- `definition_json = jsonencode(...)` usage.
- Automatic build and testing behavior and expected duration.
- Operation timeouts.
- Import syntax and release-type adoption semantics.
- Release being out of scope.
- Retire-as-delete behavior.
- The idempotency crash window.
- The prohibition on secrets in component definitions and the need to secure
  Terraform state.

## Acceptance criteria

- Existing `vew_component` plans and lifecycle behavior remain unchanged.
- Shared OAuth, transport, errors, and waiting code contain no domain wire models.
- Component and component-version code live under matching domain packages.
- `vew_component_version` supports create, read, update, import, drift, and
  retire-as-delete.
- Create and update wait through automatic VEW testing and finish only at
  `VALIDATED` or fail at `FAILED`.
- Delete finishes only at `RETIRED` or `404`.
- JSON formatting, key ordering, and VEW step defaults do not cause perpetual
  plans.
- Create retries reuse one idempotency key and byte-identical request body.
- Partial failures preserve recoverable state and do not trigger blind duplicate
  creation.
- Released imports are readable and immutable changes plan replacement.
- No credential, token, response body, or component definition appears in logs or
  diagnostics.
- The complete race-enabled Go suite passes.
- The gated live acceptance test is present and documented.
