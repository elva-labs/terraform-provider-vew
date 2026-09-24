resource "vew_recipe_version" "example" {
  project_id   = vew_recipe.example.project_id
  recipe_id    = vew_recipe.example.id
  description  = "Managed by Terraform"
  release_type = "PATCH"
  volume_size  = 20

  configured_components = [{
    component_id = vew_component_version.example.component_id
    version_id   = vew_component_version.example.id
    type         = "HELPER"
  }]
}
