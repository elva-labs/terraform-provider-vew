# Task 4 report: Configure the Client from Terraform

## Result

Implemented provider configuration resolution in `internal/provider/provider.go` and comprehensive configuration tests in `internal/provider/provider_test.go`.

- Explicit known, non-empty Terraform values take precedence over `VEW_API_URL`, `VEW_TOKEN_URL`, `VEW_CLIENT_ID`, and `VEW_CLIENT_SECRET`.
- Environment-only configuration is supported through injectable lookup behavior in `resolveProviderConfig` and `os.LookupEnv` in `Configure`.
- Missing values identify both the Terraform attribute and its environment variable.
- Unknown Terraform values are rejected rather than falling back to environment values.
- API and token URLs require absolute `http` or `https` URLs.
- Configure diagnostics use the safe summary `Unable to configure VEW client`; tests assert the client secret never appears in diagnostics.
- `Configure` stores the initialized client as `client.ComponentAPI` in `ResourceData`.

## TDD evidence

### RED

Ran:

```text
GOCACHE=/private/tmp/vew-gocache /Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run TestProviderConfig -count=1
```

Before implementation, the test build failed because `resolveProviderConfig` was undefined. After correcting a test helper to match the framework diagnostics API, the failure was attributable to the missing production resolver.

### GREEN

Ran:

```text
GOCACHE=/private/tmp/vew-gocache /Users/ruanheyns/.govm/go/bin/go test ./internal/provider -run TestProvider -count=1
```

Result: provider tests passed.

## Full verification

Ran:

```text
GOCACHE=/private/tmp/vew-gocache /Users/ruanheyns/.govm/go/bin/go test ./...
```

Result:

```text
?    github.com/elva-labs/terraform-provider-vew/cmd/terraform-provider-vew [no test files]
ok   github.com/elva-labs/terraform-provider-vew/internal/client
ok   github.com/elva-labs/terraform-provider-vew/internal/provider
```

The first sandboxed full-suite attempt could not bind the existing `httptest` listener in client tests; the same suite passed with local listener permission enabled. `git diff --check` passed.
