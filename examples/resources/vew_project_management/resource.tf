terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

provider "vew" {
  api_url          = "https://api.example.invalid/clients/packaging/v1"
  projects_api_url = "https://api.example.invalid/clients/projects"
}

resource "vew_project_management" "example" {
  project_id = "project-example"
  source     = "example-org/vew-config programs/example"
}
