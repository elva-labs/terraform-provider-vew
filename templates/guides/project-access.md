---
page_title: "Project access - VEW"
subcategory: ""
description: |-
  Configure Projects S2S scopes, bootstrap, role grants, imports, and a gated disposable validation flow.
---

# Project access

Set `projects_api_url` or `VEW_PROJECTS_API_URL` to the Projects S2S endpoint,
usually ending in `/clients/projects/v1`. Packaging-only configurations can
omit it. Set `api_url`, `token_url`, `client_id`, and `client_secret` (or their
`VEW_*` environment variables) as usual. The provider requests narrow read
and write scopes for each Projects operation:

| Resource | Read scope | Write scope |
| --- | --- | --- |
| `vew_project` | `clients/projects/program.read` | `clients/projects/program.write` |
| `vew_project_assignment` | `clients/projects/assignment.read` | `clients/projects/assignment.write` |
| `vew_project_group_assignment` | `clients/projects/group_assignment.read` | `clients/projects/group_assignment.write` |
| `vew_project_client_assignment` | `clients/projects/client_assignment.read` | `clients/projects/client_assignment.write` |

The client needs the operation scope **and** an active assignment to the path
project. Creating a project is the exception because VEW creates the project
and its creator assignment together. Scope grants are configured in VEW and
are not automatically added by this provider. If the client lacks a scope or
project access, the provider reports a sanitized diagnostic without relaying
tokens, claims, or arbitrary backend error bodies.

## Separate management and Packaging credentials

CDK creates the Cognito clients and their credentials. Terraform consumes those
credentials; it does not create OAuth clients. Use an assigned management
provider alias for client assignments and separate Packaging credentials for
components, recipes, and pipelines. Never grant the same client both
`clients/projects/client_assignment.write` and `clients/packaging/*` scopes.
A client may be assigned to multiple projects; credentials need not be created
per project.

The management client receives `program.read/write` and
`client_assignment.read/write`. Its own project assignment is a prerequisite,
not a self-seeding Terraform resource. Assignment PUT requests cannot target the
configured caller, even when that caller already has access.

## First assignment for an existing project

An existing orphaned project with **no active client assignments** needs a
platform recovery client with `clients/projects/client_assignment.write` and
`clients/projects/client_assignment.bootstrap`. Use a separate action-only
Terraform configuration and set `project_client_bootstrap = true` on its
recovery provider alias:

```hcl
provider "vew" {
  alias                    = "recovery"
  api_url                  = var.packaging_api_url
  projects_api_url         = var.projects_api_url
  token_url                = var.token_url
  client_id                = var.recovery_client_id
  client_secret            = var.recovery_client_secret
  project_client_bootstrap = true
}

action "vew_project_client_bootstrap" "manager" {
  provider = vew.recovery
  config {
    project_id = var.project_id
    client_id  = var.management_client_id
  }
}
```

Preview and explicitly invoke the action:

```shell
terraform plan -invoke=action.vew_project_client_bootstrap.manager
terraform apply -invoke=action.vew_project_client_bootstrap.manager
```

The action validates the PUT response's project ID, client ID, and `ACTIVE`
status. It performs no recovery-client GET and has no refresh or destroy
lifecycle. Declaring or removing the action does not grant or revoke access.
Normal assignment resources reject recovery mode; the opt-in enables only this
action's bootstrap scope.

The recovery client must target a **different management client**. After the
grant, switch to that manager's credentials for reads and ordinary assignments:

```hcl
provider "vew" {
  alias            = "management"
  api_url          = var.packaging_api_url
  projects_api_url = var.projects_api_url
  token_url        = var.token_url
  client_id        = var.management_client_id
  client_secret    = var.management_client_secret
}

resource "vew_project_client_assignment" "packaging" {
  provider   = vew.management
  project_id = var.project_id
  client_id  = var.packaging_client_id
  status     = "ACTIVE"
}
```

Bootstrap does not authorize the recovery caller to read or revoke assignments.
It cannot bypass any active assignment. A successful grant makes the project
ineligible for another orphan bootstrap, so the provider does not automatically
retry uncertain requests. After an interrupted request or invalid response,
verify the target assignment using management credentials before retrying.
A later `403` is not proof that the initial grant failed.

For an existing project with active clients, an already assigned client with
assignment-write access must grant the manager access. Provision and assign the
manager before deploying scope removal from the old client. Verify its access,
switch normal assignment resources to the management alias, and import existing
Packaging assignments before managing them. Keep the manager's initial access
outside the Packaging configuration so its destroy cannot remove the caller
needed to finish resource cleanup.

For a new `vew_project`, creating it with management credentials automatically
assigns the manager; it can then grant Packaging access without bootstrap.

## Roles and identity

`vew_project_assignment` manages direct roles for an uppercase Entra user ID.
`vew_project_group_assignment` manages roles for a lowercase canonical Entra
group UUID. VEW computes effective roles as the union of direct roles and the
roles of every matching group in the validated login claim. It does not copy
group members into user assignments. The portal can display a group-only
member's project access without a direct assignment. The human authorizer has
TTL zero and obtains trusted Cognito UserInfo on each request. Entra membership
changes appear after a refreshed login updates the Cognito group attribute.
An older token can carry its prior identity until that token expires. Terraform
does not need to change the group mapping.

Configure `OIDCGroupIdClaim` on the login path (default: `groups`) to carry
actual Entra group object UUIDs. The selected app-scoped group list must fit
the Cognito custom attribute limit. Display names, malformed values, and Entra
overage pointers such as `_claim_names` or `hasgroups` grant no group roles.
VEW does not resolve overage pointers through Microsoft Graph; configure the
claim and group scope so the UUIDs reach the trusted profile directly.

Group mapping grants and revocations reach the Authorization read model through
asynchronous events. Monotonic versions and deletion tombstones prevent a
delayed replay from restoring an older grant. If an event is missed, the
scheduled reconciliation currently runs Saturdays at 08:00 UTC, so recovery
can take up to seven days plus the job duration, assuming the job succeeds.
Operators must monitor event delivery and reconciliation to rely on that
recovery window. Group membership removal uses the refreshed trusted profile
and does not wait for mapping projection or reconciliation.

Import existing objects before taking ownership:

```shell
terraform import vew_project.existing proj-existing
terraform import vew_project_assignment.user proj-existing/01234567-89AB-CDEF-0123-456789ABCDEF
terraform import vew_project_group_assignment.group proj-existing/01234567-89ab-cdef-0123-456789abcdef
terraform import vew_project_client_assignment.client proj-existing/automation-client
```

Destroying a `vew_project` deactivates it while retaining its ID and history.
Destroying a client assignment revokes access while retaining the client and
assignment record. Destroying user or group assignments removes only that
direct or group grant.

## Opt-in disposable validation

The following is a live operation. Use only a disposable VEW project and
explicitly authorized test identities and credentials. It is not run by docs
generation or provider unit tests.

1. Set `VEW_API_URL`, `VEW_PROJECTS_API_URL`, `VEW_TOKEN_URL`, `VEW_CLIENT_ID`,
   and `VEW_CLIENT_SECRET` for a client granted the scopes above. Use a fresh
   isolated Terraform state and review the target endpoint and client ID.
2. Declare a disposable `vew_project`. Apply and record its `id`. Verify the
   creating client can read it and that an active creator client assignment
   exists.
3. Declare one test `vew_project_group_assignment` and one test
   `vew_project_client_assignment` for that project. Apply, then verify both
   through exact API reads. Use a test Entra user belonging only to the test
   group; after a fresh login claim, verify the project appears in the portal
   with the granted effective role.
4. Remove the group assignment and apply. Refresh the test user's login claim,
   then confirm group-only project access disappears. The human authorizer has
   TTL zero; allow any older token to expire before judging a membership
   removal.
   Inspect the direct assignment separately to confirm it was not changed.
5. Destroy the disposable project and its remaining assignments. Verify an
   exact project read reports `isActive=false`, the ID and history remain, and
   the test client assignment reports `REVOKED`. Keep the recorded ID so an
   operator can inspect or reactivate it if needed.

Do not run this flow against an existing production project or identity.
