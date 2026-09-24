# Recipe version release after create

This alternative to the explicit CLI example releases the version immediately
after creation. Terraform stops the apply on action failure and does not taint
the version. After fixing the cause, explicitly invoke the separately configured
retry action:

```shell
terraform apply -invoke=action.vew_recipe_version_release.retry
```

The retry action uses literal references to the version. The triggered action
uses `caller` and is available only during the trigger; a no-op apply does not
retry it.

When a recipe selects component versions, release them first if required by
VEW. The action is one-way; removing the resource invokes VEW retirement and
is not an undo, and VEW may reject retiring a released version. This example
triggers only on `after_create`; do not add `after_update` for a release
action.
