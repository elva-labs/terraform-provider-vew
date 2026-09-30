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

# Release a tested DEV version of a product to its users.
resource "vew_product_version_promotion" "example" {
  project_id = "project-example"
  product_id = "prod-example"
  version_id = "vers-example"
  stage      = "PROD"
}
