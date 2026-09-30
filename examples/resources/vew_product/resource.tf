terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

provider "vew" {
  api_url            = "https://api.example.invalid/clients/packaging/v1"
  publishing_api_url = "https://api.example.invalid/clients/publishing"
}

resource "vew_product" "example" {
  project_id    = "project-example"
  name          = "Example workbench"
  description   = "Managed by Terraform"
  type          = "WORKBENCH"
  technology_id = "tech-example"
}
