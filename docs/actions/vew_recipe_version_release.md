# vew_recipe_version_release action

Releases one validated VEW recipe version. This action performs a one-way VEW
operation and does not create, update, or delete Terraform state.

## Configuration

The action's `config` block requires:

| Attribute | Description |
| --- | --- |
| `project_id` | Project that owns the recipe version. |
| `recipe_id` | Recipe that owns the version. |
| `version_id` | Version to release. |

The IDs must identify the same version in VEW. VEW enforces whether the
version is eligible for release and whether the caller has permission. Ensure
any component versions selected by the recipe are released first when VEW
requires it.

## CLI invocation

Declare the action in the same root module as the recipe-version resource,
apply the version configuration, and then invoke the action explicitly:

```hcl
action "vew_recipe_version_release" "example" {
  config {
    project_id = vew_recipe_version.example.project_id
    recipe_id  = vew_recipe_version.example.recipe_id
    version_id = vew_recipe_version.example.id
  }
}
```

```shell
terraform apply
terraform apply -invoke=action.vew_recipe_version_release.example
terraform apply
```

An invocation plan contains only the action; it does not apply other pending
resource changes. Apply the recipe version first. Run a normal `terraform
apply` after release so Terraform refreshes and records the remote `RELEASED`
status before a pipeline or other dependent changes are applied.

If invocation fails, fix the cause and invoke the action again explicitly. The
action does not store a retry request or reverse a successful release. Removing
the action block only removes its Terraform configuration. Removing the
`vew_recipe_version` resource from configuration invokes that resource's VEW
retirement behavior; it is not a release undo, and VEW may reject retiring a
released version. The provider does not automatically retire released
versions.

## Optional create trigger

To release immediately after Terraform creates the version, replace the CLI
action's literal resource references with the caller expressions below and add
the trigger to the `vew_recipe_version` resource's `lifecycle` block:

```hcl
action "vew_recipe_version_release" "example" {
  config {
    project_id = caller.project_id
    recipe_id  = caller.recipe_id
    version_id = caller.id
  }
}

# A separate action can be invoked directly after a trigger failure.
action "vew_recipe_version_release" "retry" {
  config {
    project_id = vew_recipe_version.example.project_id
    recipe_id  = vew_recipe_version.example.recipe_id
    version_id = vew_recipe_version.example.id
  }
}

resource "vew_recipe_version" "example" {
  # The recipe-version configuration goes here.

  lifecycle {
    action_trigger {
      events     = [after_create]
      actions    = [action.vew_recipe_version_release.example]
      on_failure = halt
    }
  }
}
```

`on_failure = halt` reports the action error and leaves the created version
untainted. Terraform will not retry the action on a later no-op apply. Because
`caller` is available only through the trigger, direct retries use the separate
action with literal resource references:

```shell
terraform apply -invoke=action.vew_recipe_version_release.retry
```

Do not use `after_update` for this one-way action. The provider does not impose
a two-invocation limit; VEW determines whether another release request is
valid.
