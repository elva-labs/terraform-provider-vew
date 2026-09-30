terraform {
  required_providers {
    vew = {
      source  = "elva-labs/vew"
      version = "0.1.0"
    }
  }
}

provider "vew" {}

# Use an existing project and pipeline. Keep this UUID for recovery; change it
# only when you intentionally request another image build.
action "vew_image_build" "example" {
  config {
    project_id      = "your-project-id"
    pipeline_id     = "your-existing-pipeline-id"
    idempotency_key = "90827b61-8399-4d8a-9843-d75d1556fed0"
    timeout_minutes = 120
  }
}
