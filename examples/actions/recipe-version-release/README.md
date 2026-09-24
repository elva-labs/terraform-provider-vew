# Recipe version release action

This example creates and validates a recipe version with no selected
components, then releases it through the provider action. Set `project_id` to a
project where the configured OAuth client can create and release recipes.
When a recipe selects component versions, release those component versions
first if required by VEW.

```shell
terraform init
terraform apply
terraform apply -invoke=action.vew_recipe_version_release.example
terraform apply
```

The final normal apply refreshes the version resource after release, before a
pipeline uses it. Release is one-way; removing the resource invokes VEW
retirement and is not an undo. VEW may reject retiring a released version. To
retry a failed release, fix its cause and run the `-invoke` command again.

For automatic release on creation, use the alternative `lifecycle.action_trigger`
configuration in the action reference docs. That alternative uses
`caller.project_id`, `caller.recipe_id`, and `caller.id`, and sets
`on_failure = halt`.
