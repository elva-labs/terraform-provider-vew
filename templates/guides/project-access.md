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

## First assignment for an existing project

An existing orphaned project with **no active client assignments** needs a
platform recovery client with
`clients/projects/client_assignment.bootstrap`. Configure that client's
credentials in a separate provider alias and set
`project_client_bootstrap = true`. This adds the bootstrap scope only to that
alias's client-assignment write requests. First grant the recovery client
itself access to the orphaned project. The resource performs an exact read
after the grant, which requires that active assignment. Then use the assigned
recovery client to grant an ordinary automation client:

```hcl
provider "vew" {
  alias            = "recovery"
  api_url          = var.packaging_api_url
  projects_api_url = var.projects_api_url
  token_url        = var.token_url
  client_id        = var.recovery_client_id
  client_secret    = var.recovery_client_secret
  project_client_bootstrap = true
}

resource "vew_project_client_assignment" "seed_recovery" {
  provider   = vew.recovery
  project_id = "proj-existing"
  client_id  = var.recovery_client_id
  status     = "ACTIVE"
}

resource "vew_project_client_assignment" "restore_automation" {
  provider   = vew.recovery
  project_id = "proj-existing"
  client_id  = var.automation_client_id
  status     = "ACTIVE"
  depends_on = [vew_project_client_assignment.seed_recovery]
}
```

Grant bootstrap only to the recovery client in VEW. The normal provider leaves
`project_client_bootstrap` unset and does not request that scope. Bootstrap
cannot bypass an active assignment already present on a project; use an
assigned client or resolve its access first. For a new `vew_project`, use the
creating client's automatic assignment.
Keep the recovery client's seed assignment until the ordinary client has
access and any managed resources have migrated to a normal provider
configuration. Revoking the recovery client's own assignment early prevents
its exact reads and subsequent project operations. The dependency above also
orders the two assignments during destroy; review that plan before applying.

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
