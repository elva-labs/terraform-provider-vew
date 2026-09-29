resource "vew_recipe" "example" {
  project_id   = "project-example"
  name         = "example-recipe"
  description  = "Managed by Terraform"
  platform     = "Linux"
  architecture = "amd64"
  os_version   = "Ubuntu 24"
}
