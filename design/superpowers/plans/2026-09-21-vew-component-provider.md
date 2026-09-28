# VEW Component Terraform Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Build a standalone protocol-6 Terraform provider that manages the complete lifecycle of one VEW Packaging component.

**Architecture:** The provider configures a small internal OAuth/API client and injects it into vew_component. The client owns token caching, bounded retries, typed errors, and per-create idempotency; the resource owns Terraform schema, replacement rules, state mapping, import, and archive-as-delete behavior.

**Tech Stack:** Go 1.27.0, Terraform 1.16.3, Terraform Plugin Framework v1.19.0, Terraform Plugin Testing v1.16.0, and the Go standard library.

**Spec:** design/superpowers/specs/2026-09-21-vew-component-provider-design.md

## Global Constraints

- Module: github.com/elva-labs/terraform-provider-vew with go 1.27.0.
- Serve Terraform Plugin Framework protocol 6.
- Expose only vew_component.
- Never expose OAuth secrets or tokens in state, logs, examples, or diagnostics.
- Reuse one random UUID and an identical body for every retry within one Create call.
- Treat VEW 404 and ARCHIVED as absent state.
- Delete means archive.
- Live acceptance requires TF_ACC=1, all VEW variables, and explicit authorization.
- Invoke Go as /Users/ruanheyns/.govm/go/bin/go until govm is on the shell PATH.

## File Map

| File | Responsibility |
| --- | --- |
| cmd/terraform-provider-vew/main.go | Protocol-6 entrypoint. |
| internal/client/oauth.go | OAuth token acquisition, caching, and refresh. |
| internal/client/types.go | Component wire models and VEW problem errors. |
| internal/client/client.go | Authenticated component CRUD, retry, and idempotency. |
| internal/provider/provider.go | Provider schema, environment fallback, client configuration. |
| internal/provider/component_resource.go | Resource schema, lifecycle, import, and drift. |
| internal/provider/provider_test_helpers_test.go | Fake VEW server and protocol-6 test factory. |
| internal/provider/component_resource_acceptance_test.go | Gated live lifecycle test. |
| examples, README.md, Makefile | Usage and developer workflow. |

---

### Task 1: Bootstrap the Provider

**Files:**
- Create: go.mod
- Create: .gitignore
- Create: LICENSE
- Create: cmd/terraform-provider-vew/main.go
- Create: internal/provider/provider.go
- Create: internal/provider/provider_test.go

**Interfaces:**
- Produces: provider.New(version string) func() provider.Provider
- Produces: provider type vew with api_url, token_url, client_id, client_secret
- Consumes: none

- [ ] **Step 1: Create module and repository metadata**

Create go.mod:

~~~go
module github.com/elva-labs/terraform-provider-vew

go 1.27.0

require (
	github.com/hashicorp/terraform-plugin-framework v1.19.0
	github.com/hashicorp/terraform-plugin-testing v1.16.0
)
~~~

Ignore Terraform state, .terraform, the local provider binary, and terraform.rc. Copy Apache-2.0 LICENSE from ../virtual-engineering-workbench/LICENSE.

- [ ] **Step 2: Write failing metadata and schema tests**

Tests directly call Metadata and Schema. Assert TypeName vew, Version test, exactly the four named configuration attributes, and Sensitive true for client_secret.

~~~go
func TestProviderMetadata(t *testing.T)
func TestProviderSchema(t *testing.T)
~~~

- [ ] **Step 3: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run 'TestProvider(Metadata|Schema)$' -count=1
~~~

Expected: compile failure because New is undefined.

- [ ] **Step 4: Implement minimal provider and main**

provider.go defines vewProvider, providerModel, New, Metadata, Schema, an empty Configure, no resources, and no data sources. All config attributes are optional strings and client_secret is sensitive.

main.go serves:

~~~go
providerserver.Serve(
	context.Background(),
	provider.New(version),
	providerserver.ServeOpts{Address: "registry.terraform.io/elva-labs/vew"},
)
~~~

- [ ] **Step 5: Format, test, build, and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go mod tidy
/Users/ruanheyns/.govm/go/bin/go fmt ./...
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -count=1
/Users/ruanheyns/.govm/go/bin/go build ./cmd/terraform-provider-vew
git add .gitignore LICENSE go.mod go.sum cmd internal/provider
git commit -m "feat: scaffold vew terraform provider"
~~~

---

### Task 2: Add OAuth Client-Credentials Authentication

**Files:**
- Create: internal/client/oauth.go
- Create: internal/client/oauth_test.go

**Interfaces:**
- Produces: TokenSource.Token(context.Context, forceRefresh bool) (string, error)
- Produces: NewOAuthTokenSource(tokenURL, clientID, clientSecret string, httpClient *http.Client)
- Consumes: standard-library HTTP and synchronization

- [ ] **Step 1: Write failing tests**

Create:

~~~go
func TestOAuthTokenSourceRequestsClientCredentialsAndScopes(t *testing.T)
func TestOAuthTokenSourceCachesUnexpiredToken(t *testing.T)
func TestOAuthTokenSourceRefreshesExpiringToken(t *testing.T)
func TestOAuthTokenSourceForceRefreshesToken(t *testing.T)
func TestOAuthTokenSourceReturnsSafeError(t *testing.T)
~~~

The fake endpoint asserts POST, HTTP Basic Auth, grant_type=client_credentials, and this exact scope value:

~~~text
clients/packaging/component.read clients/packaging/component.write
~~~

The safe-error test places the secret in a response body and proves the returned error excludes it.

- [ ] **Step 2: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/client -run TestOAuthTokenSource -count=1
~~~

Expected: compile failure because TokenSource is undefined.

- [ ] **Step 3: Implement OAuth**

OAuthTokenSource stores token URL, credentials, HTTP client, clock function, mutex, cached token, and expiry. NewOAuthTokenSource validates non-empty credentials and an absolute http/https token URL. A nil HTTP client becomes a client with a 30-second timeout.

Token returns a cached token only when more than 30 seconds remain. Otherwise POST form fields grant_type and scope with Basic Auth. Require 2xx, non-empty access_token, and positive expires_in. Cache the token under the mutex. Errors contain status or decoding context, never response bodies or credentials.

- [ ] **Step 4: Format, test, and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/client
/Users/ruanheyns/.govm/go/bin/go test ./internal/client -run TestOAuthTokenSource -count=1
git add internal/client
git commit -m "feat: add oauth client credentials authentication"
~~~

---

### Task 3: Add the VEW Component Client

**Files:**
- Create: internal/client/types.go
- Create: internal/client/client.go
- Create: internal/client/client_test.go

**Interfaces:**
- Consumes: TokenSource
- Produces: Config, Client, ComponentAPI, APIError, IsNotFound
- ComponentAPI methods:
  - CreateComponent(context.Context, projectID, CreateComponentInput) (string, error)
  - GetComponent(context.Context, projectID, componentID) (Component, error)
  - UpdateComponent(context.Context, projectID, componentID, UpdateComponentInput) error
  - ArchiveComponent(context.Context, projectID, componentID) error

- [ ] **Step 1: Write failing client tests**

Create:

~~~go
func TestClientCreateComponentMapsRequestAndResponse(t *testing.T)
func TestClientCreateComponentReusesKeyAndBodyAfterLostResponse(t *testing.T)
func TestClientCreateComponentHonorsRetryAfterForInProgressOperation(t *testing.T)
func TestClientRefreshesTokenOnceAfterUnauthorized(t *testing.T)
func TestClientGetComponentDecodesEnvelope(t *testing.T)
func TestClientUpdateComponentSendsDescriptionOnly(t *testing.T)
func TestClientArchiveComponentAcceptsNotFound(t *testing.T)
func TestClientDecodesProblemResponse(t *testing.T)
func TestClientStopsAfterMaximumRetryAttempts(t *testing.T)
func TestAPIErrorNotFound(t *testing.T)
~~~

The lost-response RoundTripper records body and Idempotency-Key, returns io.ErrUnexpectedEOF once, then 201. Assert two calls, identical non-empty keys, and byte-identical bodies. Inject sleep for Retry-After tests so they do not wait.

- [ ] **Step 2: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/client -run 'Test(Client|APIError)' -count=1
~~~

Expected: compile failure because client types are missing.

- [ ] **Step 3: Implement exact wire models**

CreateComponentInput maps name, description, platform, supported architectures, and supported OS versions to the API camelCase names. UpdateComponentInput contains only description. Component maps ID, all configuration fields, status, created/updated timestamps, and actors.

Problem maps type, title, status, detail, code, requestId, and retryable. APIError includes HTTP status and Problem. Error text contains safe problem detail and request ID. IsNotFound uses errors.As and status 404.

- [ ] **Step 4: Implement authenticated CRUD and retry**

Client stores parsed base URL, HTTP client, TokenSource, max attempts, injected sleep, and injected idempotency generator. New validates API URL, constructs OAuthTokenSource, uses four attempts, context-aware sleep, and Go 1.27 uuid.New().String.

Build only:

~~~text
POST   /projects/{projectId}/components
GET    /projects/{projectId}/components/{componentId}
PUT    /projects/{projectId}/components/{componentId}
DELETE /projects/{projectId}/components/{componentId}
~~~

Marshal request bodies once. Every attempt gets Bearer auth and JSON headers. Create adds one stable Idempotency-Key.

Retry network errors, 429, retryable problems, 5xx, and 409 only for IDEMPOTENCY_IN_PROGRESS. Honor seconds or HTTP-date Retry-After; otherwise use 250ms, 500ms, 1s plus up to 25 percent jitter. Force token refresh and replay exactly once after the first 401. Limit bodies to 2 MiB. Decode create ID and component envelope. Archive normalizes 404 to success.

- [ ] **Step 5: Run with race detection and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/client
/Users/ruanheyns/.govm/go/bin/go test ./internal/client -race -count=1
git add internal/client
git commit -m "feat: add idempotent vew component client"
~~~

---

### Task 4: Configure the Client from Terraform

**Files:**
- Modify: internal/provider/provider.go
- Modify: internal/provider/provider_test.go

**Interfaces:**
- Consumes: client.Config, client.New, client.ComponentAPI
- Produces: resolveProviderConfig(providerModel, getenv)
- Produces: ResourceData containing ComponentAPI

- [ ] **Step 1: Write failing configuration tests**

Cover explicit values, environment-only values, explicit-over-environment precedence, each missing value, invalid API/token URLs, and unknown Terraform values. Use a map-backed getenv. Assert no diagnostic contains the test secret.

- [ ] **Step 2: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run TestProviderConfig -count=1
~~~

Expected: failure because configuration resolution is absent.

- [ ] **Step 3: Implement configuration**

Use os.Getenv through an injectable lookupEnv. For each field, reject unknown, prefer a known non-empty configured value, then read VEW_API_URL, VEW_TOKEN_URL, VEW_CLIENT_ID, or VEW_CLIENT_SECRET. Missing diagnostics name both the attribute and environment variable. Require absolute http/https URLs.

Configure decodes config, resolves it, calls client.New, and assigns client.ComponentAPI(api) to resp.ResourceData. Use Unable to configure VEW client as the safe error summary.

- [ ] **Step 4: Test and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run TestProvider -count=1
git add internal/provider
git commit -m "feat: configure vew api client"
~~~

---

### Task 5: Define vew_component Schema and Import

**Files:**
- Create: internal/provider/component_resource.go
- Create: internal/provider/component_resource_test.go
- Modify: internal/provider/provider.go

**Interfaces:**
- Consumes: configured client.ComponentAPI
- Produces: NewComponentResource
- Produces: parseComponentImportID
- Produces: createInput, updateInput, setComponentState

- [ ] **Step 1: Write failing schema and import tests**

Assert:

~~~text
id                       computed string
project_id               required string, replace on change
name                     required string, replace on change
description              required string, update in place
platform                 required string, replace on change
supported_architectures  required set(string), replace on change
supported_os_versions    required set(string), replace on change
status                   computed string
created_at               computed string
created_by               computed string
updated_at               computed string
updated_by               computed string
~~~

Import prog-73488/cmp-123 succeeds. Empty, missing-slash, empty-half, and multiple-slash inputs fail.

- [ ] **Step 2: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run 'TestComponentResource(Schema|Import)' -count=1
~~~

Expected: compile failure because the resource is undefined.

- [ ] **Step 3: Implement resource shape**

componentModel uses types.String for scalar attributes and types.Set for the two collections. Apply stringplanmodifier.RequiresReplace and setplanmodifier.RequiresReplace to create-only fields. Configure accepts only client.ComponentAPI. ImportState parses exactly one slash and sets project_id and id.

Convert sets with ElementsAs, reject null/unknown values, and sort before building CreateComponentInput. Register NewComponentResource in provider Resources.

- [ ] **Step 4: Test and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run 'TestComponentResource(Schema|Import)' -count=1
git add internal/provider
git commit -m "feat: define vew component resource"
~~~

---

### Task 6: Implement CRUD and Drift Handling

**Files:**
- Modify: internal/provider/component_resource.go
- Modify: internal/provider/component_resource_test.go
- Create: internal/provider/provider_test_helpers_test.go

**Interfaces:**
- Consumes: ComponentAPI and componentModel
- Produces: Create, Read, Update, Delete
- Produces: fake VEW server and protocol-6 factory

- [ ] **Step 1: Build the fake server**

Create a mutex-protected fake that stores one Component, archived flag, call counts, and captured idempotency key. Token handler validates Basic Auth and scopes. API handlers validate Bearer auth and implement real response envelopes/statuses.

Provide testProviderConfig(fake) and testProtoV6ProviderFactories().

- [ ] **Step 2: Write failing lifecycle tests**

Use resource.Test with IsUnitTest true and:

~~~hcl
resource "vew_component" "test" {
  project_id              = "prog-73488"
  name                    = "terraform-test-component"
  description             = "created by provider test"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
~~~

Steps: create/check state, empty plan, update description, import prog-73488/cmp-123, and destroy/archive. Add direct Read tests for 404 and ARCHIVED removal.

- [ ] **Step 3: Verify red**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run 'TestComponentResource(Lifecycle|ReadRemoves)' -count=1
~~~

Expected: lifecycle failures because CRUD is absent.

- [ ] **Step 4: Implement Create and state mapping**

Create decodes plan, builds input, calls CreateComponent, then calls GetComponent with the returned ID. setComponentState uses types.SetValueFrom and maps every remote field. Append all conversion diagnostics. Use operation-specific error summaries.

- [ ] **Step 5: Implement Read, Update, Delete**

Read uses project_id and id. Remove state on IsNotFound or ARCHIVED. Update sends only planned description, then reads canonical state. Delete calls ArchiveComponent and relies on the client treating 404 as success.

- [ ] **Step 6: Test and commit**

~~~bash
/Users/ruanheyns/.govm/go/bin/go fmt ./internal/provider
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -count=1
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
git add internal/provider
git commit -m "feat: manage vew component lifecycle"
~~~

---

### Task 7: Add Gated Live Acceptance Coverage

**Files:**
- Create: internal/provider/component_resource_acceptance_test.go

**Interfaces:**
- Consumes: provider.New, Terraform Plugin Testing, deployed VEW credentials
- Produces: TestAccComponentResource

- [ ] **Step 1: Write acceptance helpers and test**

testAccPreCheck requires VEW_API_URL, VEW_TOKEN_URL, VEW_CLIENT_ID, VEW_CLIENT_SECRET, and VEW_PROJECT_ID, reporting all missing names together.

TestAccComponentResource creates a unique tf-acc timestamp name and runs: create/state check, empty plan, description update, import verification, and destroy. CheckDestroy uses the client and succeeds only on ARCHIVED or 404.

- [ ] **Step 2: Compile without external writes**

~~~bash
/Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run '^$' -count=1
~~~

Expected: package compiles and no test runs.

- [ ] **Step 3: Run only after authorization**

~~~bash
TF_ACC=1 /Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run '^TestAccComponentResource$' -v -count=1 -timeout 20m
~~~

Expected: create, empty plan, update, import, and archive pass. If live authorization is unavailable, record this verification as pending without weakening the source.

- [ ] **Step 4: Commit**

~~~bash
git add internal/provider/component_resource_acceptance_test.go
git commit -m "test: add vew component acceptance coverage"
~~~

---

### Task 8: Document and Verify the PoC

**Files:**
- Create: examples/provider/provider.tf
- Create: examples/resources/vew_component/resource.tf
- Create: README.md
- Create: Makefile
- Modify: go.mod
- Modify: go.sum

**Interfaces:**
- Consumes: Tasks 1-7
- Produces: reproducible build, test, usage, and import instructions

- [ ] **Step 1: Add examples**

Provider source is elva-labs/vew and provider configuration is empty so environment fallbacks are demonstrated. The resource example uses project prog-73488, Linux, arm64/x86_64, and Ubuntu 24.

- [ ] **Step 2: Add Make targets**

~~~make
GO ?= /Users/ruanheyns/.govm/go/bin/go

.PHONY: fmt test build testacc

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./... -race -count=1

build:
	$(GO) build ./cmd/terraform-provider-vew

testacc:
	TF_ACC=1 $(GO) test ./internal/provider -run '^TestAccComponentResource$$' -v -count=1 -timeout 20m
~~~

- [ ] **Step 3: Write README**

Include prerequisites, build/test commands, Terraform dev override, all provider attributes and environment fallbacks, component HCL, import command, replacement fields, archive semantics, idempotency crash window, and live-test side effects.

- [ ] **Step 4: Run final verification**

~~~bash
/Users/ruanheyns/.govm/go/bin/go mod tidy
/Users/ruanheyns/.govm/go/bin/go fmt ./...
/Users/ruanheyns/.govm/go/bin/go vet ./...
/Users/ruanheyns/.govm/go/bin/go test ./... -race -count=1
/Users/ruanheyns/.govm/go/bin/go build ./cmd/terraform-provider-vew
terraform version
git diff --check
~~~

Expected: all commands exit zero and Terraform reports v1.16.3.

- [ ] **Step 5: Commit and review**

~~~bash
git add README.md Makefile examples go.mod go.sum
git commit -m "docs: document vew component provider poc"
git log --oneline --decorate --reverse
git diff cdd5042..HEAD --stat
git status --short
~~~

Expected: one focused commit per task and a clean worktree.
