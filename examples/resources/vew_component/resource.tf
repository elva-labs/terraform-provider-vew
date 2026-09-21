resource "vew_component" "example" {
  project_id              = "prog-73488"
  name                    = "example-component"
  description             = "Managed by Terraform"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}
