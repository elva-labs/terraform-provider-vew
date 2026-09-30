terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

variable "project_id" {
  description = "VEW project that owns this account assignment."
  type        = string
}

variable "aws_account_id" {
  description = "12-digit AWS account ID explicitly approved for VEW onboarding."
  type        = string
}

variable "technology_id" {
  description = "ID of an existing technology in the VEW project."
  type        = string
}

provider "vew" {
  api_url          = "https://api.example.invalid/clients/packaging/v1"
  projects_api_url = "https://api.example.invalid/clients/projects/v1"
}

resource "vew_project_account" "example" {
  project_id     = var.project_id
  aws_account_id = var.aws_account_id
  account_type   = "USER"
  name           = "example-development-account"
  description    = "Managed by Terraform"
  technology_id  = var.technology_id
  stage          = "dev"
  region         = "eu-west-1"

  timeouts = {
    create = "2h"
    update = "2h"
  }

  lifecycle {
    prevent_destroy = true
  }
}
