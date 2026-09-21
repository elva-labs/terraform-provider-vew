# VEW component provider final fix report

Date: 2026-09-21

## Scope and authoritative contract

The implementation was checked against the read-only VEW packaging S2S sources:

- `backend/app/packaging/entrypoints/s2s_api/model/api_model.py`
- `backend/app/packaging/entrypoints/s2s_api/schema/proserve-workbench-s2s-packaging-api-schema.yaml`
- `backend/app/packaging/entrypoints/s2s_api/routers/components.py`
- `backend/app/packaging/domain/exceptions/s2s_exception.py`

The contract is: create fields `componentName`, `componentDescription`,
`componentPlatform`, `componentSupportedArchitectures`, and
`componentSupportedOsVersions`; create response `componentId`; read envelope
`{ "component": ... }`; component fields `componentId`, `createDate`,
`lastUpdateDate`, and `lastUpdatedBy`; initial status `CREATED`; and retryable
in-progress code `IDEMPOTENCY_REQUEST_IN_PROGRESS`.

## Changes

- Corrected client wire tags and removed non-contract create/read response
  fallbacks.
- Converted client and provider protocol fixtures to literal JSON/maps so
  production JSON tags cannot self-validate.
- Retried only `IDEMPOTENCY_REQUEST_IN_PROGRESS` conflicts, honoring
  `Retry-After`, while explicitly proving `IDEMPOTENCY_KEY_REUSED` is not
  retried.
- Updated fake and acceptance expectations from `ACTIVE` to `CREATED`.
- Made OAuth token parsing bounded and strict: oversized payloads, a second
  JSON value, and trailing junk are rejected.
- Added one sanitized provider API diagnostic formatter that retains operation,
  HTTP status, problem code, and request ID without including response detail,
  body, bearer, or client secret. Create/read/update/archive are covered.
- Documented that `VEW_API_URL` includes `/clients/packaging/v1` because the
  client appends `/projects/...`.

## TDD evidence

### RED

Command:

```shell
/Users/ruanheyns/.govm/go/bin/go test ./internal/client ./internal/provider \
  -run 'TestClient(CreateComponentMapsRequestAndResponse|CreateComponentHonorsRetryAfterForInProgressOperation|GetComponentDecodesEnvelope)|TestOAuthTokenSourceRejectsTrailingOrOversizedJSON|TestComponentResourceLifecycle' \
  -count=1
```

Expected failures were observed before production changes:

- create response missing component ID for literal `componentId`;
- 409 `IDEMPOTENCY_REQUEST_IN_PROGRESS` was not retried;
- read response missing component for literal component envelope/fields;
- OAuth accepted trailing JSON, trailing junk, and oversized response bodies;
- fake lifecycle returned `ACTIVE` instead of `CREATED`.

The expanded RED run also showed all create/read/update/delete diagnostics
omitted the safe status/code/request-ID context.

### GREEN

Focused command:

```shell
/Users/ruanheyns/.govm/go/bin/go test ./internal/client ./internal/provider \
  -run 'TestClient(CreateComponentMapsRequestAndResponse|CreateComponentHonorsRetryAfterForInProgressOperation|CreateComponentDoesNotRetryReusedIdempotencyKey|GetComponentDecodesEnvelope)|TestOAuthTokenSourceRejectsTrailingOrOversizedJSON|TestComponentResource(OperationDiagnosticsAreSanitizedAndContextual|Lifecycle)' \
  -count=1
```

Output:

```text
ok github.com/elva-labs/terraform-provider-vew/internal/client
ok github.com/elva-labs/terraform-provider-vew/internal/provider
```

## Final verification

All commands used `/Users/ruanheyns/.govm/go/bin/go` and completed with exit
code 0:

```text
go test ./... -race -count=1
? github.com/elva-labs/terraform-provider-vew/cmd/terraform-provider-vew [no test files]
ok github.com/elva-labs/terraform-provider-vew/internal/client
ok github.com/elva-labs/terraform-provider-vew/internal/provider

go vet ./...
go build ./cmd/terraform-provider-vew
go fmt ./...
terraform fmt -check -recursive
git diff --check
```

`TF_ACC` was not set and no acceptance or external VEW calls were run.

## Review notes

The diff was reviewed for stale `ACTIVE`, stale idempotency code, old component
JSON tags, unnecessary response-envelope fallbacks, secrets in diagnostics,
and whitespace errors. No remaining issue was found.
