---
page_title: "VEW Packaging API Prerequisite"
subcategory: ""
description: |-
  Deploy the project-scoped S2S Packaging API with idempotency support before using the VEW provider.
---

# Packaging API prerequisite

This provider requires VEW's **project-scoped service-to-service (S2S)
Packaging API with idempotency support**. The provider calls that API to
manage components, recipes, and image pipelines. It sends idempotency keys on
operations that create packaging objects or start image builds so a retry can
recover the same logical operation instead of creating a duplicate.

As of this provider release, the required API changes are **not available in
the [upstream VEW repository](https://github.com/awslabs/virtual-engineering-workbench)**.
They are available in the
[Elva Labs fork](https://github.com/elva-labs/virtual-engineering-workbench),
where the [Packaging S2S and idempotency changes](https://github.com/elva-labs/virtual-engineering-workbench/pull/3)
have been merged. Deploy a revision of that fork containing those changes
before configuring this provider. A deployment from upstream alone cannot
serve the API that the provider expects.

Set `api_url` (or `VEW_API_URL`) to the deployed Packaging API base URL ending
in `/clients/packaging/v1`. The provider appends project-specific paths such
as `/projects/{project_id}/components`. Supply OAuth client credentials with
the Packaging scopes needed by the resources you use, and assign that client
to the target VEW project. Resources for project technologies and AWS account
assignments also need `projects_api_url` and the corresponding Projects API
scopes; see the [provider configuration](../index.md).
