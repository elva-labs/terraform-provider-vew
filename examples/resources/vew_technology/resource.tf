terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

provider "vew" {
  api_url          = "https://api.example.invalid/clients/packaging/v1"
  projects_api_url = "https://api.example.invalid/clients/projects/v1"
}

resource "vew_technology" "example" {
  project_id   = "project-example"
  name         = "example-technology"
  description  = "Managed by Terraform"

  lifecycle {
    prevent_destroy = true
  }
}
