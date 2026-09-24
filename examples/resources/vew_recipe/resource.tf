resource "vew_recipe" "example" {
  project_id   = "prog-73488"
  name         = "example-recipe"
  description  = "Managed by Terraform"
  platform     = "Linux"
  architecture = "amd64"
  os_version   = "Ubuntu 24"
}
