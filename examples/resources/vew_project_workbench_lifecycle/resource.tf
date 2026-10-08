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

resource "vew_project_workbench_lifecycle" "example" {
  project_id = "project-example"

  # Unset: the deployment's defaults for the idle and nightly stops.
  weekend_stop = true

  allow_user_disable_nightly_stop = true
  allow_user_idle_timeout         = true
  user_idle_timeout_min_minutes   = 10
  user_idle_timeout_max_minutes   = 480
}
