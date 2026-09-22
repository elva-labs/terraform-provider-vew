# VEW Provider Domain Packages and Component Versions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize the VEW Terraform provider into domain packages without changing `vew_component`, then add a production-shaped `vew_component_version` resource that supports create, read, update, import, drift reconciliation, asynchronous validation, and retire-as-delete.

**Architecture:** Shared OAuth, HTTP, retry, error, and polling code lives in `internal/vew`; component wire models and endpoints live in `internal/vew/components`; Terraform resources live in `internal/provider/components`; and `internal/providerdata` carries configured domain interfaces from the root provider to resources. Component-version operations use status-based polling from `CREATING` through `CREATED` and `TESTING` to `VALIDATED`, preserve partial state on failure, and never model release as part of this slice.

**Tech Stack:** Go 1.27.0, Terraform 1.16.x, Terraform Plugin Framework v1.19.0, Terraform Plugin Testing v1.16.0, Terraform protocol 6, and the Go standard library.

**Spec:** `docs/superpowers/specs/2026-09-22-component-versions-and-domain-packages-design.md`

## Global Constraints

- Module path remains `github.com/elva-labs/terraform-provider-vew`.
- Invoke Go as `/Users/ruanheyns/.govm/go/bin/go` until `govm` is on the shell `PATH`.
- Work test-first: add one focused failing test, observe the expected failure, implement the smallest behavior, then rerun the focused test.
- Preserve all current `vew_component` schema, import, retry, archive, and acceptance behavior during the package move.
- Do not add component-version release management.
- Do not mutate the existing AWS CLI component or component version from an automated live test.
- Never put credentials, OAuth tokens, response bodies, or `definition_json` in logs or diagnostics.
- A single create call must use one UUID idempotency key and byte-identical request body across every transport retry.
- Treat component-version `404` and `RETIRED` as absent Terraform state.
- Treat `FAILED` as a terminal operation error while preserving the resource identifiers and latest remote state so the next apply can recover.
- Default operation timeouts are 60 minutes for create, 60 minutes for update, and 30 minutes for delete; the resource exposes matching `timeouts` overrides.
- Run `go test ./... -race -count=1` before every task commit that changes production code.

## Target Package Contracts

The following contracts are fixed for this implementation. If compilation proves a framework callback needs a mechanical signature adjustment, preserve these semantics and update all call sites in the same task.

```go
// internal/vew/transport.go
package vew

type Config struct {
	APIURL       string
	TokenURL     string
	ClientID     string
	ClientSecret string
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type Transport interface {
	Do(ctx context.Context, method string, pathSegments []string, body []byte, idempotencyKey string) (Response, error)
}

func NewTransport(config Config) (Transport, error)
func RetryAfter(header http.Header, now time.Time) time.Duration
```

```go
// internal/vew/waiter.go
package vew

type PollResult struct {
	Status     string
	RetryAfter time.Duration
}

type StatusReader func(context.Context) (PollResult, error)
type StatusEvaluator func(status string) (done bool, err error)

type Waiter interface {
	Until(ctx context.Context, timeout, initialDelay time.Duration, read StatusReader, evaluate StatusEvaluator) error
}

func NewWaiter() Waiter

type TerminalStatusError struct{ Status string }
type TimeoutError struct{ LastStatus string }
```

```go
// internal/vew/components/component.go
package components

type ComponentAPI interface {
	CreateComponent(context.Context, string, CreateComponentInput) (string, error)
	GetComponent(context.Context, string, string) (Component, error)
	UpdateComponent(context.Context, string, string, UpdateComponentInput) error
	ArchiveComponent(context.Context, string, string) error
}
```

```go
// internal/vew/components/component_version.go
package components

type ActionResult struct {
	ID         string
	RetryAfter time.Duration
}

type Dependency struct {
	ComponentID   string  `json:"componentId"`
	ComponentName string  `json:"componentName"`
	VersionID     string  `json:"componentVersionId"`
	VersionName   string  `json:"componentVersionName"`
	Type          string  `json:"componentVersionType"`
	Order         int64   `json:"order"`
	Position      *string `json:"position,omitempty"`
}

type CreateComponentVersionInput struct {
	Description      string          `json:"componentVersionDescription"`
	Dependencies     []Dependency    `json:"componentVersionDependencies"`
	ReleaseType      string          `json:"componentVersionReleaseType"`
	Definition       json.RawMessage `json:"componentVersionDefinition"`
	SoftwareVendor   string          `json:"softwareVendor"`
	SoftwareVersion  string          `json:"softwareVersion"`
	LicenseDashboard *string         `json:"licenseDashboard,omitempty"`
	Notes            *string         `json:"notes,omitempty"`
}

type UpdateComponentVersionInput struct {
	Description      string          `json:"componentVersionDescription"`
	Dependencies     []Dependency    `json:"componentVersionDependencies"`
	Definition       json.RawMessage `json:"componentVersionDefinition"`
	SoftwareVendor   string          `json:"softwareVendor"`
	SoftwareVersion  string          `json:"softwareVersion"`
	LicenseDashboard *string         `json:"licenseDashboard,omitempty"`
	Notes            *string         `json:"notes,omitempty"`
}

type ComponentVersion struct {
	ComponentID      string
	ID               string
	Description      string
	Name             string
	Dependencies     []Dependency
	Definition       json.RawMessage
	SoftwareVendor   string
	SoftwareVersion  string
	LicenseDashboard *string
	Notes            *string
	Status           string
	CreatedAt        string
	CreatedBy        string
	UpdatedAt        string
	UpdatedBy        string
}

type ComponentVersionAPI interface {
	CreateComponentVersion(context.Context, string, string, CreateComponentVersionInput) (ActionResult, error)
	GetComponentVersion(context.Context, string, string, string) (ComponentVersion, error)
	UpdateComponentVersion(context.Context, string, string, string, UpdateComponentVersionInput) (ActionResult, error)
	RetireComponentVersion(context.Context, string, string, string) (ActionResult, error)
}

type Client struct {
	transport vew.Transport
}
func NewClient(transport vew.Transport) *Client
```

```go
// internal/providerdata/data.go
package providerdata

type Data struct {
	Components        components.ComponentAPI
	ComponentVersions components.ComponentVersionAPI
	Waiter            vew.Waiter
}
```

The one configured `components.Client` implements both API interfaces. Resource constructors remain dependency-free; their `Configure` methods type-assert `providerdata.Data` and select only the interfaces they need.

---

### Task 1: Reorganize Existing Code by Domain Without Behavior Changes

**Files:**

- Create: `internal/vew/transport.go`
- Create: `internal/vew/transport_test.go`
- Create: `internal/vew/oauth.go`
- Create: `internal/vew/oauth_test.go`
- Create: `internal/vew/errors.go`
- Create: `internal/vew/components/client.go`
- Create: `internal/vew/components/client_test.go`
- Create: `internal/vew/components/component.go`
- Create: `internal/providerdata/data.go`
- Create: `internal/provider/components/component_resource.go`
- Create: `internal/provider/components/component_resource_test.go`
- Create: `internal/provider/components/component_resource_protocol_test.go`
- Create: `internal/provider/components/component_resource_acceptance_test.go`
- Create: `internal/provider/testhelpers/vew_server.go`
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/provider_test.go`
- Delete after replacements compile: `internal/client/client.go`
- Delete after replacements compile: `internal/client/client_test.go`
- Delete after replacements compile: `internal/client/oauth.go`
- Delete after replacements compile: `internal/client/oauth_test.go`
- Delete after replacements compile: `internal/client/types.go`
- Delete after replacements compile: `internal/provider/component_resource.go`
- Delete after replacements compile: `internal/provider/component_resource_test.go`
- Delete after replacements compile: `internal/provider/component_resource_acceptance_test.go`
- Delete after replacements compile: `internal/provider/provider_test_helpers_test.go`

**Interfaces:** Produces the shared transport, component domain client, `providerdata.Data`, and `provider/components.NewComponentResource`; consumes no new external dependency.

- [ ] **Step 1: Record the green baseline**

Run:

```bash
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
```

Expected: all existing OAuth, client, provider, component unit, protocol, and gated acceptance tests pass or skip.

- [ ] **Step 2: Move OAuth and problem errors behind `internal/vew`**

Move the existing token source and tests from `internal/client` to `internal/vew` with package declarations changed to `vew`. Move `Problem`, `APIError`, and `IsNotFound` into `errors.go`. Preserve all public behavior, exact OAuth scopes, token caching, safe error text, and current test names.

Run:

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew -run 'Test(OAuth|APIError)' -count=1
```

Expected: the moved tests pass.

- [ ] **Step 3: Extract the authenticated transport**

Move URL validation, path escaping, authorization, `401` refresh, response-size limiting, bounded transient retry, RFC 9457 decoding, idempotency headers, and injected sleep from the old client into `vew.Transport`. Keep retry policy and limits byte-for-byte equivalent. `Do` must build paths exclusively from `pathSegments`, must never log bodies, and must return response headers so domain clients can read `Retry-After`.

Move the existing transport-level tests and make them call a small test-only wrapper around `Transport.Do`. Add one regression test proving each path segment is escaped independently.

Run:

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew -run 'Test(Transport|ClientRefreshes|ClientStops|ClientDecodes)' -count=1
```

Expected: all transport, refresh, retry, and problem tests pass.

- [ ] **Step 4: Move component endpoints into `internal/vew/components`**

Move `Component`, `CreateComponentInput`, `UpdateComponentInput`, `ComponentAPI`, and the four component methods into the components package. Construct request bodies once before `Transport.Do`; pass the stable per-create UUID as the idempotency key. Preserve all existing wire names and endpoints.

Run:

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew/components -run TestClient -count=1
```

Expected: the moved component client tests pass with identical path, payload, retry, and idempotency assertions.

- [ ] **Step 5: Move the Terraform component resource and split test responsibilities**

Move the resource to package `internal/provider/components`. Keep direct schema/configuration tests in package `components`; move protocol lifecycle tests to `component_resource_protocol_test.go` in package `components_test`; move the live test to package `components_test`. Extract the fake VEW HTTP server into `internal/provider/testhelpers` without importing the root provider package, preventing an import cycle.

`providerdata.Data` initially contains the component client and a nil waiter. Change resource `Configure` to reject the wrong data type with a diagnostic and store `data.Components` on success.

- [ ] **Step 6: Rewire root provider construction and registration**

In root provider `Configure`, create one `vew.Transport`, pass it to `components.NewClient`, and set both `ResourceData` and `DataSourceData` to:

```go
providerdata.Data{
	Components: api,
}
```

Register `providercomponents.NewComponentResource` from `Resources`. Keep provider schema, environment fallback, and diagnostics unchanged.

- [ ] **Step 7: Prove behavior preservation, then remove old files**

Run:

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/vew ./internal/provider ./internal/providerdata
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
rg 'internal/client|NewComponentResource' --glob '*.go'
```

Expected: all tests pass; `rg` shows no imports of `internal/client` and only the new constructor/registration locations. Delete the superseded files listed above, rerun the test command, and confirm the result remains green.

- [ ] **Step 8: Commit the structural refactor**

```bash
git add internal
git commit -m "refactor: organize provider by vew domain"
```

---

### Task 2: Add the Shared Status Waiter

**Files:**

- Create: `internal/vew/waiter.go`
- Create: `internal/vew/waiter_test.go`

**Interfaces:** Produces the `Waiter`, `PollResult`, `StatusReader`, `StatusEvaluator`, `TerminalStatusError`, and `TimeoutError` contracts shown above.

- [ ] **Step 1: Write failing deterministic waiter tests**

Add table-driven tests using injected `now`, `sleep`, and `jitter` functions:

```go
func TestWaiterReturnsWhenEvaluatorIsDone(t *testing.T)
func TestWaiterPassesEveryObservedStatusToEvaluator(t *testing.T)
func TestWaiterUsesInitialAndRetryAfterDelays(t *testing.T)
func TestWaiterUsesBoundedBackoffWhenRetryAfterIsAbsent(t *testing.T)
func TestWaiterReturnsTerminalStatusError(t *testing.T)
func TestWaiterReturnsTimeoutWithLastStatus(t *testing.T)
func TestWaiterStopsOnContextCancellation(t *testing.T)
```

The delay test asserts an action's five-second initial delay occurs before the first read and a poll's seven-second `RetryAfter` overrides calculated backoff. The backoff test asserts 1s, 2s, 4s, 8s, and a 15s cap before deterministic jitter. No test performs a real sleep.

- [ ] **Step 2: Verify red**

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew -run TestWaiter -count=1
```

Expected: compile failure because `NewWaiter` and waiter types do not exist.

- [ ] **Step 3: Implement cancellation-aware polling**

`Until` derives a timeout context, sleeps through a timer selected against `ctx.Done`, calls `read`, stores the last successful status, and passes it to `evaluate`. It returns evaluator errors unchanged, converts deadline expiry to `TimeoutError{LastStatus: lastStatus}`, and returns parent cancellation unchanged. The production jitter function varies delays by at most ±20%; tests replace it deterministically.

Define safe errors:

```go
func (e *TerminalStatusError) Error() string {
	return "VEW operation reached terminal status " + e.Status
}

func (e *TimeoutError) Error() string {
	return "timed out waiting for VEW operation; last status: " + e.LastStatus
}
```

- [ ] **Step 4: Test and commit**

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/vew
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew -run TestWaiter -race -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
git add internal/vew/waiter.go internal/vew/waiter_test.go
git commit -m "feat: add asynchronous status waiter"
```

---

### Task 3: Add Component-Version API Operations

**Files:**

- Create: `internal/vew/components/component_version.go`
- Modify: `internal/vew/components/client.go`
- Modify: `internal/vew/components/client_test.go`
- Modify: `internal/vew/transport.go`
- Modify: `internal/vew/transport_test.go`

**Interfaces:** Produces `ComponentVersionAPI`, the inputs, `ComponentVersion`, `Dependency`, and `ActionResult` shown in Target Package Contracts.

- [ ] **Step 1: Write failing request/response tests**

Add:

```go
func TestCreateComponentVersionMapsRequestAndActionResponse(t *testing.T)
func TestCreateComponentVersionReusesKeyAndBodyAfterLostResponse(t *testing.T)
func TestGetComponentVersionDecodesEnvelopeAndDefinition(t *testing.T)
func TestUpdateComponentVersionMapsFullMutablePayload(t *testing.T)
func TestRetireComponentVersionMapsRequestAndActionResponse(t *testing.T)
func TestComponentVersionActionParsesRetryAfterDeltaSeconds(t *testing.T)
func TestComponentVersionActionParsesRetryAfterHTTPDate(t *testing.T)
```

Assert these exact endpoint shapes:

```text
POST   /projects/{projectId}/components/{componentId}/versions
GET    /projects/{projectId}/components/{componentId}/versions/{versionId}
PUT    /projects/{projectId}/components/{componentId}/versions/{versionId}
DELETE /projects/{projectId}/components/{componentId}/versions/{versionId}
```

Assert POST and PUT use the exact camelCase body fields from the approved spec, including dependency fields `componentId`, `componentName`, `componentVersionId`, `componentVersionName`, `componentVersionType`, `order`, and `position`. Assert POST retries retain the same non-empty `Idempotency-Key` and byte-identical body. Assert action responses decode `{ "componentVersionId": "version-123" }` and preserve `Retry-After` as a duration.

- [ ] **Step 2: Verify red**

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew/components -run 'Test(Create|Get|Update|Retire)ComponentVersion|TestComponentVersionAction' -count=1
```

Expected: compile failure because component-version types and methods do not exist.

- [ ] **Step 3: Implement `RetryAfter` parsing at the transport boundary**

Support both non-negative delta seconds and an RFC 7231 HTTP date. Return zero for absent, malformed, or past values. Keep parsing independent from transport retry sleeps so action responses can hand their delay to the waiter.

- [ ] **Step 4: Implement component-version wire models and envelopes**

Decode GET from:

```json
{
  "component_version": {
    "componentId": "...",
    "componentVersionId": "...",
    "componentVersionDescription": "...",
    "componentVersionName": "...",
    "componentVersionDependencies": [],
    "softwareVendor": "...",
    "softwareVersion": "...",
    "licenseDashboard": null,
    "notes": null,
    "status": "VALIDATED",
    "createDate": "...",
    "createdBy": "...",
    "lastUpdateDate": "...",
    "lastUpdatedBy": "..."
  },
  "componentVersionDefinition": {}
}
```

Keep the definition as `json.RawMessage`; never interpolate it into errors. Create marshals once, generates one UUID, and calls `Transport.Do`. Update and delete accept VEW `202`; GET accepts `200`; every other response is already converted by the transport to a safe `vew.APIError`.

- [ ] **Step 5: Test and commit**

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/vew
/Users/ruanheyns/.govm/go/bin/go test ./internal/vew/components -race -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
git add internal/vew
git commit -m "feat: add component version api client"
```

---

### Task 4: Normalize Definitions and Dependencies

**Files:**

- Create: `internal/provider/components/component_version_definition.go`
- Create: `internal/provider/components/component_version_definition_test.go`
- Create: `internal/provider/components/component_version_dependencies.go`
- Create: `internal/provider/components/component_version_dependencies_test.go`

**Interfaces:** Produces `normalizeDefinition(string) (json.RawMessage, error)` and `expandDependencies(context.Context, types.List) ([]vewcomponents.Dependency, diag.Diagnostics)` for use by create, update, diff comparison, and state refresh.

- [ ] **Step 1: Write failing definition tests**

Add:

```go
func TestNormalizeDefinitionRejectsInvalidJSON(t *testing.T)
func TestNormalizeDefinitionRejectsNonObjectTopLevel(t *testing.T)
func TestNormalizeDefinitionRejectsTrailingJSON(t *testing.T)
func TestNormalizeDefinitionCanonicalizesObjectsAndPreservesArrayOrder(t *testing.T)
func TestNormalizeDefinitionInjectsMissingStepDefaults(t *testing.T)
func TestNormalizeDefinitionPreservesExplicitStepValues(t *testing.T)
func TestNormalizeDefinitionDoesNotExposeInputInErrors(t *testing.T)
```

The default assertion is exact for every object in every `phases[*].steps[*]` array:

```json
{"timeoutSeconds":7200,"onFailure":"Abort","maxAttempts":1}
```

Inject only missing keys. Preserve explicit zero, empty, or non-default values. Decode with `json.Decoder.UseNumber`, require exactly one top-level object, and re-encode with `json.Marshal` for stable key ordering.

- [ ] **Step 2: Verify definition tests red, then implement**

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -run TestNormalizeDefinition -count=1
```

Expected: compile failure because `normalizeDefinition` does not exist. Implement it and rerun until green.

- [ ] **Step 3: Write failing dependency tests**

Add:

```go
func TestExpandDependenciesDefaultsTypeAndSortsByOrder(t *testing.T)
func TestExpandDependenciesAllowsMainAndHelper(t *testing.T)
func TestExpandDependenciesAllowsAppendAndPrependPosition(t *testing.T)
func TestExpandDependenciesRejectsNonPositiveOrder(t *testing.T)
func TestExpandDependenciesRejectsDuplicateOrder(t *testing.T)
func TestExpandDependenciesRejectsUnknownType(t *testing.T)
func TestExpandDependenciesRejectsUnknownPosition(t *testing.T)
func TestExpandDependenciesTreatsNullListAsEmpty(t *testing.T)
```

Use this Terraform element model:

```go
type dependencyModel struct {
	ComponentID   types.String `tfsdk:"component_id"`
	ComponentName types.String `tfsdk:"component_name"`
	VersionID     types.String `tfsdk:"version_id"`
	VersionName   types.String `tfsdk:"version_name"`
	Type          types.String `tfsdk:"type"`
	Order         types.Int64  `tfsdk:"order"`
	Position      types.String `tfsdk:"position"`
}
```

- [ ] **Step 4: Verify dependency tests red, then implement**

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -run TestExpandDependencies -count=1
```

Expected: compile failure because the model and converter do not exist. Implement list decoding, field validation, the default `HELPER`, optional nil position, duplicate-order detection, and ascending order sort. Diagnostics name the dependency index and field but do not include the definition.

- [ ] **Step 5: Test and commit**

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider/components
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -race -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
git add internal/provider/components
git commit -m "feat: normalize component version configuration"
```

---

### Task 5: Define the Terraform Resource Contract and Import Semantics

**Files:**

- Create: `internal/provider/components/component_version_resource.go`
- Create: `internal/provider/components/component_version_resource_test.go`
- Modify: `internal/provider/provider.go`
- Modify: `internal/provider/provider_test.go`
- Modify: `internal/providerdata/data.go`

**Interfaces:** Produces `NewComponentVersionResource() resource.Resource`; implements `ResourceWithConfigure`, `ResourceWithImportState`, `ResourceWithValidateConfig`, and `ResourceWithModifyPlan`.

- [ ] **Step 1: Write failing metadata and schema tests**

Add direct tests asserting resource type `vew_component_version` and every field in the approved schema. The Go model is:

```go
type componentVersionModel struct {
	ID               types.String `tfsdk:"id"`
	ProjectID        types.String `tfsdk:"project_id"`
	ComponentID      types.String `tfsdk:"component_id"`
	Description      types.String `tfsdk:"description"`
	ReleaseType      types.String `tfsdk:"release_type"`
	DefinitionJSON   types.String `tfsdk:"definition_json"`
	Dependencies     types.List   `tfsdk:"dependencies"`
	SoftwareVendor   types.String `tfsdk:"software_vendor"`
	SoftwareVersion  types.String `tfsdk:"software_version"`
	LicenseDashboard types.String `tfsdk:"license_dashboard"`
	Notes            types.String `tfsdk:"notes"`
	Name             types.String `tfsdk:"name"`
	Status           types.String `tfsdk:"status"`
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
	Timeouts         types.Object `tfsdk:"timeouts"`
}

type timeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}
```

`project_id` and `component_id` use `stringplanmodifier.RequiresReplace`. `release_type` is validated to `MAJOR`, `MINOR`, or `PATCH`, but replacement is decided by `ModifyPlan` so imports can adopt it once. `dependencies` is an optional `schema.ListNestedAttribute` defaulting to an empty list. `timeouts` is an optional `schema.SingleNestedBlock` with optional duration strings.

- [ ] **Step 2: Verify schema tests red, then implement schema and configure**

```bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -run 'TestComponentVersionResource(Metadata|Schema|Configure)' -count=1
```

Expected: compile failure because the resource constructor does not exist. Implement metadata, schema, interface assertions, and `Configure`. `Configure` stores `ComponentVersions` and `Waiter`, and diagnoses missing or wrongly typed provider data.

- [ ] **Step 3: Write failing validation and timeout tests**

Add protocol-level tests for invalid release type, invalid JSON, duplicate dependency order, negative dependency order, invalid dependency type/position, malformed timeouts, and non-positive timeouts. Add unit tests for `operationTimeout` returning 60m/60m/30m defaults and configured overrides.

Implement `ValidateConfig` without making API calls. Unknown values are deferred; known values use the normalization helpers and `time.ParseDuration`.

- [ ] **Step 4: Write failing import tests**

Add:

```go
func TestComponentVersionImportParsesThreePartID(t *testing.T)
func TestComponentVersionImportRejectsWrongPartCount(t *testing.T)
func TestComponentVersionImportRejectsEmptyPart(t *testing.T)
func TestImportedComponentVersionAdoptsReleaseTypeWithoutUpdate(t *testing.T)
func TestKnownReleaseTypeChangeRequiresReplacement(t *testing.T)
```

Import accepts exactly `project_id/component_id/version_id`. It sets those three state fields and leaves `release_type` null because VEW does not return it.

`ModifyPlan` applies these exact rules:

1. If prior `release_type` is null or unknown, configuring a valid value adopts it without replacement.
2. If prior `release_type` is known and differs from plan, append `path.Root("release_type")` to `RequiresReplace`.
3. If prior status is `RELEASED`, any change to description, definition, dependencies, software vendor/version, license dashboard, or notes appends that changed attribute path to `RequiresReplace`.
4. Unknown values do not trigger speculative replacement.

The adoption protocol test imports, refreshes, plans with `release_type = "PATCH"`, applies, and asserts the server received GET only—no PUT and no replacement. `Update` will implement the no-PUT adoption path in Task 6.

- [ ] **Step 5: Register and wire the resource**

Initialize root provider data with both domain interfaces and the waiter:

```go
providerdata.Data{
	Components:        api,
	ComponentVersions: api,
	Waiter:            vew.NewWaiter(),
}
```

Register both constructors in stable order:

```go
[]func() resource.Resource{
	providercomponents.NewComponentResource,
	providercomponents.NewComponentVersionResource,
}
```

Update root provider tests to assert both resource type names.

- [ ] **Step 6: Test and commit the contract**

Run all unit tests except the adoption apply test if CRUD methods still return explicit not-implemented diagnostics; that test becomes green in Task 6.

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider ./internal/providerdata
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -run 'TestComponentVersionResource|TestComponentVersionImport|TestKnownReleaseType|TestOperationTimeout' -race -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
git add internal/provider internal/providerdata
git commit -m "feat: define component version resource contract"
```

Expected: all implemented contract tests pass. Do not commit a deliberately failing adoption test; keep it ready in the next task's first red step if necessary.

---

### Task 6: Implement Component-Version CRUD and Asynchronous Reconciliation

**Files:**

- Modify: `internal/provider/components/component_version_resource.go`
- Modify: `internal/provider/components/component_version_resource_test.go`
- Create: `internal/provider/components/component_version_resource_protocol_test.go`
- Modify: `internal/provider/testhelpers/vew_server.go`

**Interfaces:** Consumes `ComponentVersionAPI` and `vew.Waiter`; completes create, read, update, delete, and import adoption.

- [ ] **Step 1: Extend the fake VEW server for deterministic lifecycle sequences**

Add independent queues of GET responses keyed by `project/component/version`, action request recording, action `Retry-After` control, and safe problem responses. Expose assertions for method, path, body, and call count. Keep the helper free of root provider imports.

- [ ] **Step 2: Write the failing create protocol tests**

Add:

```go
func TestComponentVersionCreateWaitsThroughCreatedAndTestingToValidated(t *testing.T)
func TestComponentVersionCreateUsesNormalizedDefinitionAndSortedDependencies(t *testing.T)
func TestComponentVersionCreatePreservesProvisionalStateOnFailedStatus(t *testing.T)
func TestComponentVersionCreatePreservesProvisionalStateOnTimeout(t *testing.T)
```

The success server sequence is `CREATING`, `CREATED`, `TESTING`, `VALIDATED`. Assert one POST, four GETs, the exact final computed state, injected definition defaults, sorted dependencies, and configured release type. For failure and timeout, assert diagnostics do not contain `definition_json`, and state retains `project_id`, `component_id`, returned `id`, `release_type`, and the latest status.

- [ ] **Step 3: Implement create and the shared refresh/wait helpers**

Create performs this exact order:

1. Decode the plan and validate/normalize definition and dependencies.
2. POST once through the retrying domain client.
3. Immediately write provisional state containing all planned fields, returned ID, and status `CREATING`.
4. Wait with create timeout and the action's `RetryAfter`.
5. During each read, write the latest remote state before evaluating status.
6. Continue for `CREATING`, `CREATED`, `TESTING`, and `UPDATING`; succeed only at `VALIDATED`; return `TerminalStatusError` for `FAILED`, `RELEASED`, or `RETIRED`; return a safe error for an unknown status.

Because framework state cannot be updated from inside the generic waiter callback, keep the latest `ComponentVersion` in a closure, call `response.State.Set` after `Until` returns, and add the waiter error after state is saved.

- [ ] **Step 4: Write failing read tests, then implement read**

Add:

```go
func TestComponentVersionReadRefreshesRemoteFieldsAndCanonicalDefinition(t *testing.T)
func TestComponentVersionReadPreservesConfiguredReleaseType(t *testing.T)
func TestComponentVersionReadRemovesStateOnNotFound(t *testing.T)
func TestComponentVersionReadRemovesStateOnRetired(t *testing.T)
```

Read fetches once. `404` or `RETIRED` calls `response.State.RemoveResource`. Otherwise map all remote values, normalize the returned definition, sort/map dependencies, and preserve `release_type` and timeout values from prior state.

- [ ] **Step 5: Write failing update tests, then implement update**

Add:

```go
func TestComponentVersionUpdateSendsFullMutablePayloadAndWaitsForValidated(t *testing.T)
func TestComponentVersionUpdateAdoptsImportedReleaseTypeWithoutPut(t *testing.T)
func TestComponentVersionUpdateWaitsForPendingRemoteOperationBeforePut(t *testing.T)
func TestComponentVersionUpdatePreservesStateOnFailedStatus(t *testing.T)
```

Update performs this exact order:

1. Read prior state and plan.
2. If current remote status is pending, wait for `VALIDATED` before deciding whether to PUT.
3. If the only Terraform difference is null/unknown state `release_type` becoming the configured value, perform no PUT; map the GET response and store the configured release type.
4. Otherwise PUT the full mutable payload, wait using update timeout and action delay, then save the latest remote state while preserving planned release type/timeouts.
5. A `FAILED` terminal response saves the latest state and returns a retryable diagnostic.

- [ ] **Step 6: Write failing delete tests, then implement delete**

Add:

```go
func TestComponentVersionDeleteWaitsForPendingOperationThenRetires(t *testing.T)
func TestComponentVersionDeleteWaitsUntilRetired(t *testing.T)
func TestComponentVersionDeleteTreatsNotFoundAsSuccess(t *testing.T)
func TestComponentVersionDeletePreservesStateOnFailedStatus(t *testing.T)
```

Delete first GETs the version. A `404` or `RETIRED` succeeds immediately. Pending status is polled to `VALIDATED`; then DELETE is sent. Poll until `RETIRED` or `404`, using delete timeout and action delay. Remove state only on success. On `FAILED`, retain state and return the terminal error.

- [ ] **Step 7: Make all lifecycle and import tests green**

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider/components -run 'TestComponentVersion(Create|Read|Update|Delete|Import|Known)' -race -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
```

Expected: every component and component-version test passes; the old `vew_component` lifecycle is unchanged.

- [ ] **Step 8: Commit lifecycle support**

```bash
git add internal/provider
git commit -m "feat: manage component version lifecycle"
```

---

### Task 7: Add Examples, Documentation, and an Explicitly Gated Live Test

**Files:**

- Create: `examples/resources/vew_component_version/resource.tf`
- Create: `internal/provider/components/component_version_resource_acceptance_test.go`
- Modify: `README.md`
- Modify: `Makefile`

**Interfaces:** Documents the public resource and adds opt-in live verification without touching the user's existing AWS CLI version.

- [ ] **Step 1: Add a native Terraform example**

The example uses `jsonencode` directly, references a `vew_component` for `project_id` and `component_id`, shows an empty dependency list, and includes a commented dependency object illustrating all fields. It does not use input variables merely to relay literal resource values.

Run:

```bash
terraform fmt -check examples/resources/vew_component_version/resource.tf
```

Expected: exit status 0.

- [ ] **Step 2: Document lifecycle and import**

Add the resource schema summary, status waiting behavior, timeout block, `FAILED` recovery behavior, and this exact import form:

```bash
terraform import vew_component_version.example PROJECT_ID/COMPONENT_ID/VERSION_ID
```

State clearly that release is not managed and `definition_json` is stored in Terraform state.

- [ ] **Step 3: Add a disposable live acceptance test**

The test skips unless all of the following are set:

```text
TF_ACC=1
VEW_ACC_COMPONENT_VERSION=1
VEW_API_URL
VEW_TOKEN_URL
VEW_CLIENT_ID
VEW_CLIENT_SECRET
VEW_TEST_PROJECT_ID
VEW_TEST_COMPONENT_ID
```

It creates a new disposable component version under the explicitly supplied test component, updates its notes/description, imports it, and retires it. It must refuse to run when `VEW_TEST_COMPONENT_ID` is empty and must not contain or default to the AWS CLI component ID.

- [ ] **Step 4: Add Make targets and run local verification**

Add a component-version acceptance target that forwards the gates above. Then run:

```bash
/Users/ruanheyns/.govm/go/bin/go fmt ./...
/Users/ruanheyns/.govm/go/bin/go vet ./...
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
terraform fmt -check -recursive examples
```

Expected: vet and tests pass; live tests skip; all examples are formatted.

- [ ] **Step 5: Commit documentation and live test**

```bash
git add README.md Makefile examples internal/provider/components/component_version_resource_acceptance_test.go
git commit -m "docs: add component version usage and acceptance coverage"
```

---

### Task 8: Final Regression, Safety, and Design Conformance Review

**Files:** Review all files changed since the design commit; modify only files required by findings.

- [ ] **Step 1: Run the complete clean verification gate**

```bash
/Users/ruanheyns/.govm/go/bin/go clean -testcache
/Users/ruanheyns/.govm/go/bin/go vet ./...
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
/Users/ruanheyns/.govm/go/bin/go build ./cmd/terraform-provider-vew
terraform fmt -check -recursive examples
git status --short
```

Expected: every command exits 0, tests run uncached, live acceptance tests skip, and `git status --short` is empty after any corrective commit.

- [ ] **Step 2: Audit package boundaries**

```bash
rg 'internal/client' . --glob '!docs/**'
rg 'provider/' internal/vew
rg 'terraform-plugin-framework' internal/vew
```

Expected: no old client imports; `internal/vew` imports neither provider packages nor Terraform Plugin Framework. Confirm future domain folders can be added beside `components` without editing shared wire models.

- [ ] **Step 3: Audit secret and definition handling**

```bash
rg 'ClientSecret|client_secret|access_token|definition_json|Definition' internal --glob '*.go'
```

Review every result. Credentials and tokens may appear only in configuration/authentication fields and test fixtures. Definition values may be marshalled, normalized, sent, or stored in state, but may not be passed to `fmt.Errorf`, diagnostics, or logging.

- [ ] **Step 4: Audit lifecycle coverage against the design**

Check off each transition in tests:

```text
create: CREATING -> CREATED -> TESTING -> VALIDATED
update: pending -> VALIDATED -> UPDATING -> VALIDATED
failure: any pending state -> FAILED with identifiers retained
delete: pending -> VALIDATED -> RETIRED or 404
read: 404 or RETIRED removes state
import: project/component/version + release_type adoption without PUT
released: mutable configuration change requires replacement
```

Also verify request payloads, idempotency, definition defaults, ordered dependencies, custom timeouts, cancellation, and old component behavior have direct assertions.

- [ ] **Step 5: Review diff and commit only actual corrections**

```bash
git diff 9e111a4 --stat
git diff 9e111a4 --check
git log --oneline --decorate -8
```

Expected: no whitespace errors and a task-oriented commit sequence. If the review required corrections:

```bash
git add internal README.md Makefile examples
git commit -m "fix: address component version review findings"
```

Repeat Step 1 after that commit. Do not squash or merge until the user selects the integration path.
