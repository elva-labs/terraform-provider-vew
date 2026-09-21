# Task 8 report: document and verify the PoC

## Delivered

- Added `examples/provider/provider.tf` with the `elva-labs/vew` provider source
  and empty provider configuration.
- Added `examples/resources/vew_component/resource.tf` for project `prog-73488`
  with Linux, `arm64`/`x86_64`, and Ubuntu 24.
- Added the requested `Makefile` targets: `fmt`, `test`, `build`, and `testacc`.
- Added `README.md` covering prerequisites, environment fallbacks, local
  Terraform dev overrides, resource attributes, import, replacement/update
  semantics, archive deletion, the idempotency crash window, and live-test
  side effects.

## Verification

All requested non-acceptance checks passed:

- `go mod tidy`
- `go fmt ./...`
- `go vet ./...`
- `go test ./... -race -count=1`
- `go build ./cmd/terraform-provider-vew`
- `terraform version` (`v1.16.3`)
- `git diff --check`

`TF_ACC` was not run and no external API calls were made. The existing module
dependency pins were already tidy, so `go.mod` and `go.sum` required no diff.
