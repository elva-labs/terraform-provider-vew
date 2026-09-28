# VEW Terraform Provider: Recipes and Recipe Versions

## Purpose and scope

Add `vew_recipe` and `vew_recipe_version` so Terraform can manage the recipe
base and its versioned, tested content. A user can create a recipe, select
component versions, wait for VEW validation, update a draft version, import
existing resources, and retire or archive them. Release remains a separate
VEW action outside this change, as it does for component versions.

The contract comes from the adjacent `virtual-engineering-workbench` repository:
`docs/packaging-s2s-api.md`, the Bruno `VEW S2S API/Packaging` examples, and
`backend/app/packaging/entrypoints/s2s_api/{model/api_model.py,routers/recipes.py}`.
The provider already has shared OAuth, HTTP retry, problem handling, and polling
under `internal/vew`; this change reuses them.

## Resource and package boundaries

- `internal/vew/recipes` owns recipe wire models, endpoint paths, response
  envelopes, and narrow `RecipeAPI` and `RecipeVersionAPI` interfaces.
- `internal/provider/recipes` owns the Terraform schemas, planning, lifecycle,
  import, and state conversion for both resources.
- `internal/providerdata.Data` carries the recipe interfaces and existing
  `vew.Waiter` to resources. `internal/provider/provider.go` constructs and
  registers the new domain client and resources.
- Existing component resources and their schemas retain their behavior.

The provider uses these S2S paths relative to its configured packaging API URL:

| Operation | Method and path |
| --- | --- |
| Create recipe | `POST /projects/{projectId}/recipes` |
| Read/archive recipe | `GET`/`DELETE /projects/{projectId}/recipes/{recipeId}` |
| Create version | `POST /projects/{projectId}/recipes/{recipeId}/versions` |
| Read/update/retire version | `GET`/`PUT`/`DELETE /projects/{projectId}/recipes/{recipeId}/versions/{versionId}` |

Both creates send one generated `Idempotency-Key` that remains unchanged across
transport retries, together with byte-identical request content. No other
operation sends that header. The existing transport handles authentication,
escaping path segments, retries, and safe problem diagnostics. A later Terraform
run cannot recover a key lost when the process exits after VEW accepts a create;
the documentation must state this crash window.

## `vew_recipe`

The resource accepts required `project_id`, `name`, `description`, `platform`,
`architecture`, and `os_version`. These map to `recipeName`,
`recipeDescription`, `recipePlatform`, `recipeArchitecture`, and
`recipeOsVersion`. `id`, `status`, `created_at`, `created_by`, `updated_at`, and
`updated_by` are computed from VEW.

All configured fields require replacement when changed. The S2S API offers no
recipe update endpoint. Validate nonempty strings and the current VEW system
configuration combinations: `Linux` with `Ubuntu 24` and either `amd64` or
`arm64`, or `Windows` with `Microsoft Windows Server 2025` and `amd64`. Keep
the validation isolated so supported values can follow a future API change.

Create posts the complete payload, saves the returned `recipeId`, and reads the
canonical resource. Read refreshes all fields and removes Terraform state on
`404` or `ARCHIVED`. Delete calls VEW archive; `404` or an already archived
recipe is success. VEW rejects archive while any recipe version is not
`RETIRED`, so normal Terraform references should order version retirement
before recipe archive; otherwise surface the VEW problem without losing state.
Import IDs use `project_id/recipe_id`.

## `vew_recipe_version`

Required configured fields are `project_id`, `recipe_id`, `description`,
`release_type`, `volume_size`, and `configured_components`. `integrations`
defaults to an empty set. `project_id` and `recipe_id` require replacement.
`release_type` accepts `MAJOR`, `MINOR`, or `PATCH` and requires replacement
after its initial value is stored. VEW does not return it, so a first plan after
import may adopt the configured value into state without a VEW mutation, as
`vew_component_version` does.

`volume_size` is an integer number of GB in Terraform, constrained to 8–500;
the API receives and returns its decimal string representation in
`recipeVersionVolumeSize`. Integrations are nonempty strings sent as
`recipeVersionIntegrations`. `configured_components` is an ordered list of
objects with `component_id`, `component_name`, `version_id`, `version_name`,
`type`, and `order`. Each object maps to the six fields in
`configuredComponentsVersions`, including `componentVersionType`. IDs, names,
type, and order are required; order must be positive and unique. The user
supplies all names because the S2S request requires them. The provider does not
look them up or release referenced component versions. VEW accepts component
versions in `VALIDATED` or `RELEASED` state.

Computed fields are `id`, `name`, `status`, `effective_components`,
`created_at`, `created_by`, `updated_at`, and `updated_by`.
`effective_components` mirrors `effectiveComponentsVersions`, including VEW's
mandatory components and resolved ordering. It never replaces the configured
selection in state or in update requests. This separation prevents mandatory
components from causing recurring plans. `integrations` has set semantics;
the configured component list preserves order. Empty integrations serialize as
`[]`, not `null`.

VEW omits `configuredComponentsVersions` for historical versions without saved
configured selection. Read must distinguish an omitted field from an empty
list; it must not copy `effectiveComponentsVersions` into configured state.
An imported historical version can be read, but a user who supplies a
configured list in Terraform explicitly plans a change (or replacement if the
version is released). Document this import behavior.

### Version lifecycle

Create sends `recipeVersionDescription`, `recipeVersionReleaseType`,
`recipeVersionVolumeSize`, `recipeVersionIntegrations`, and
`configuredComponentsVersions`. It stores the accepted `recipeVersionId`
before polling, so a later apply can resume if validation fails. Update sends
the complete mutable payload without release type. Both calls return `202` and
`Retry-After`; polling reads the version until `VALIDATED` or terminal `FAILED`.
Intermediate `CREATING`, `CREATED`, `TESTING`, and `UPDATING` states continue
waiting. The existing waiter handles cancellation, timeout, bounded backoff,
and `Retry-After`. Default operation timeouts match component versions: 60
minutes for create, 60 minutes for update, and 30 minutes for delete, with a
Terraform `timeouts` block for overrides.

Read refreshes the remote state. `404` and `RETIRED` remove the resource from
Terraform state. A `RELEASED` version remains readable and importable, but VEW
does not allow content updates. Planning a genuine mutable change against a
released version requires replacement. A change only to timeout settings or
equivalent set ordering updates state without a VEW call.

Delete calls retirement and waits for `RETIRED` or `404`; `FAILED` is an
operation error. VEW accepts retirement from `VALIDATED`, `FAILED`, and
`RELEASED`. Import IDs use `project_id/recipe_id/version_id`. The version's
release action is never invoked by these resources.

If a create or update is accepted and polling fails, retain identifiers and
the latest observed status. A later apply first reconciles the pending remote
operation and only sends another mutation when it is safe. After a failed
update, preserve the prior mutable configuration so an unchanged apply can
retry. Errors must not include credentials, tokens, response bodies, or
component selection data.

## User workflow and documentation

Provide examples where `vew_recipe_version.recipe_id` references
`vew_recipe.id`, and selected component IDs and version IDs reference
`vew_component` and `vew_component_version`. Document the two import forms,
archive and retirement behavior, automatic testing duration, release being
external, the create crash window, and the requirement for explicitly granted
`clients/packaging/recipe.read` and `.write` scopes on the VEW S2S client.
Released component versions are needed for a later recipe release, but release
is outside this provider slice.

## Verification

- Client tests assert exact endpoints, payload field names, response envelopes,
  omission versus empty configured selections, `Retry-After`, and stable
  idempotency keys and request bodies across retries.
- Resource tests cover create, read, update, import, drift, replacement,
  archived/retired absence, released imports, polling, timeout, cancellation,
  partial failure recovery, and safe diagnostics. Framework protocol tests
  verify plan behavior and state consistency.
- The race enabled Go suite remains the local gate. A separately gated live
  acceptance test creates a uniquely named disposable recipe and version in a
  selected project, validates update and import, retires the version, then
  archives the recipe. It never mutates an existing recipe. It requires
  `TF_ACC=1`, an explicit recipe acceptance gate, credentials, and a project ID.

## Out of scope

Recipe version release, pipeline and image resources, new provider
authentication behavior, external IDs, and a generated client for the entire
VEW API are outside this change.
