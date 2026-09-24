terraform {
  required_providers {
    vew = {
      source  = "elva-labs/vew"
      version = "0.1.0"
    }
  }
}

provider "vew" {}

resource "vew_recipe" "example" {
  project_id   = "prog-73488"
  name         = "example-recipe-release"
  description  = "Recipe for the release action example"
  platform     = "Linux"
  architecture = "amd64"
  os_version   = "Ubuntu 24"
}

resource "vew_recipe_version" "example" {
  project_id   = vew_recipe.example.project_id
  recipe_id    = vew_recipe.example.id
  description  = "Recipe version release action example"
  release_type = "PATCH"
  volume_size  = 20

  configured_components = []
}

action "vew_recipe_version_release" "example" {
  config {
    project_id = vew_recipe_version.example.project_id
    recipe_id  = vew_recipe_version.example.recipe_id
    version_id = vew_recipe_version.example.id
  }
}
