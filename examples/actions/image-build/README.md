# Image build action

Set the existing project and pipeline IDs in `main.tf`, then configure the VEW
provider credentials. The UUID in `idempotency_key` identifies one logical
build; keep it stable while recovering an interrupted invocation.

```shell
terraform init
terraform apply -invoke=action.vew_image_build.example
```

This direct invocation starts a potentially billable build. A normal apply or
removing the action block does not build or remove an image. Progress shows the
reserved VEW image ID and, on success, its upstream image ID. Neither is a
Terraform output or state attribute.

To request a second build, replace the UUID with a new one. VEW retains
completed idempotency replays for at least 24 hours. A retry with the same key
after that window may start another build, so verify the original build before
retrying late. The default wait is 120 minutes; adjust `timeout_minutes` to a
larger positive value for long pipelines. Do not use `after_update` to invoke
this action when a pipeline changes.
