terraform {
  required_providers {
    vew = {
      source = "elva-labs/vew"
    }
  }
}

provider "vew" {
  api_url = "https://api.example.invalid/clients/packaging/v1"
}

# The platform project releases a build to test first; prod only takes an
# image that is or was in test.
resource "vew_base_image_release" "test" {
  project_id   = "project-platform"
  architecture = "amd64"
  channel      = "test"
  image_id     = "image-example"
}

resource "vew_base_image_release" "prod" {
  project_id   = vew_base_image_release.test.project_id
  architecture = "amd64"
  channel      = "prod"
  image_id     = vew_base_image_release.test.image_id
}
