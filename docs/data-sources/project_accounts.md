---
page_title: "vew_project_accounts Data Source - VEW"
subcategory: ""
description: |-
  Lists the AWS account assignments of a VEW project with their onboarding state.
---

# vew_project_accounts data source

Lists the AWS account assignments of a project, including inactive ones, with
their status and onboarding state. Use `onboarded_at` to gate resources that
need an onboarded account, for example resources in the account that assume a
role VEW's onboarding creates: VEW sets it when onboarding first succeeds and
never clears it, also not when the assignment is deactivated.

A failed lookup is an error, never an empty list, so configurations gated on
this data source cannot lose resources because the API was unreachable.

Permission: project assignment and `clients/projects/account.read`. The
provider must be configured with `projects_api_url` or `VEW_PROJECTS_API_URL`.

## Selectors

| Attribute | Type | Description |
| --- | --- | --- |
| `project_id` | string | Required VEW project identifier. |

## Computed attributes

`accounts` is a list of objects with `id`, `aws_account_id`, `account_type`,
`name`, `technology_id`, `stage`, `region`, `status`,
`last_onboarding_result`, `onboarding_revision` and `onboarded_at`.

## Example

```terraform
data "vew_project_accounts" "this" {
  project_id = var.project_id
}

locals {
  onboarded_account_ids = toset([
    for account in data.vew_project_accounts.this.accounts : account.aws_account_id
    if account.onboarded_at != null
  ])
}
```
