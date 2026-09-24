variable "project_id" {
  description = "VEW project that owns the recipe and pipeline."
  type        = string
}

variable "recipe_id" {
  description = "ID of the existing VEW recipe used by this pipeline."
  type        = string
}

variable "recipe_name" {
  description = "Current name of the existing recipe."
  type        = string
}

variable "recipe_description" {
  description = "Current description of the existing recipe."
  type        = string
}

variable "recipe_platform" {
  description = "Platform of the existing recipe, such as Linux."
  type        = string
}

variable "recipe_architecture" {
  description = "Architecture of the existing recipe, such as amd64."
  type        = string
}

variable "recipe_os_version" {
  description = "OS version of the existing recipe."
  type        = string
}

variable "released_recipe_version_id" {
  description = "ID of a version of this recipe that has already been released in VEW."
  type        = string
}

resource "vew_recipe" "example" {
  project_id   = var.project_id
  name         = var.recipe_name
  description  = var.recipe_description
  platform     = var.recipe_platform
  architecture = var.recipe_architecture
  os_version   = var.recipe_os_version
}

import {
  to = vew_recipe.example
  id = "${var.project_id}/${var.recipe_id}"
}

resource "vew_pipeline" "example" {
  project_id           = vew_recipe.example.project_id
  name                 = "example-pipeline"
  description          = "Managed by Terraform"
  recipe_id            = vew_recipe.example.id
  recipe_version_id    = var.released_recipe_version_id
  build_instance_types = ["m8i.2xlarge"]
  schedule             = "0 0 * * ? *"
}
