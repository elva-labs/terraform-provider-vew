terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

provider "vew" {}

variable "project_id" {
  description = "VEW project containing the objects to read."
  type        = string
}

variable "component_id" {
  description = "Existing component ID."
  type        = string
}

variable "component_version_id" {
  description = "Existing component version ID."
  type        = string
}

variable "recipe_id" {
  description = "Existing recipe ID."
  type        = string
}

variable "recipe_version_id" {
  description = "Existing recipe version ID."
  type        = string
}

variable "pipeline_id" {
  description = "Existing pipeline ID."
  type        = string
}

variable "image_id" {
  description = "Existing image ID."
  type        = string
}

data "vew_component" "existing" {
  project_id   = var.project_id
  component_id = var.component_id
}

data "vew_component_version" "existing" {
  project_id   = var.project_id
  component_id = var.component_id
  version_id   = var.component_version_id
}

data "vew_recipe" "existing" {
  project_id = var.project_id
  recipe_id  = var.recipe_id
}

data "vew_recipe_version" "existing" {
  project_id = var.project_id
  recipe_id  = var.recipe_id
  version_id = var.recipe_version_id
}

data "vew_pipeline" "existing" {
  project_id  = var.project_id
  pipeline_id = var.pipeline_id
}

data "vew_pipelines" "project" {
  project_id = var.project_id
}

data "vew_image" "existing" {
  project_id = var.project_id
  image_id   = var.image_id
}

data "vew_images" "pipeline" {
  project_id  = var.project_id
  pipeline_id = var.pipeline_id
  status      = "CREATED"
}

output "component_platform" {
  value = data.vew_component.existing.platform
}

output "component_version_status" {
  value = data.vew_component_version.existing.status
}

output "component_definition_json" {
  description = "Canonical definition data is persisted in Terraform state."
  value       = data.vew_component_version.existing.definition_json
}

output "recipe_status" {
  value = data.vew_recipe.existing.status
}

output "recipe_effective_components" {
  value = data.vew_recipe_version.existing.effective_components
}

output "pipeline_status" {
  value = data.vew_pipeline.existing.status
}

output "pipeline_ids" {
  value = [for pipeline in data.vew_pipelines.project.pipelines : pipeline.pipeline_id]
}

output "image_upstream_id" {
  value = data.vew_image.existing.image_upstream_id
}

output "created_image_ids_for_pipeline" {
  value = [for image in data.vew_images.pipeline.images : image.image_id]
}
