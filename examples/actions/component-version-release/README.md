# Component version release action

This example creates and validates a disposable component version, then
releases it through the provider action. Set `project_id` to a project where
the configured OAuth client can create and release components.

```shell
terraform init
terraform apply
terraform apply -invoke=action.vew_component_version_release.example
terraform apply
```

The final normal apply refreshes the version resource after release. Release is
one-way; removing the resource invokes VEW retirement and is not an undo. VEW
may reject retiring a released version. To retry a failed release, fix its
cause and run the `-invoke` command again.

For automatic release on creation, use the alternative `lifecycle.action_trigger`
configuration in the action reference docs. That alternative uses
`caller.project_id`, `caller.component_id`, and `caller.id`, and sets
`on_failure = halt`.
