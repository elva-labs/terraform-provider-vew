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

# Withdraw an old version of a product once its users have moved on.
resource "vew_product_version_retirement" "example" {
  project_id = "project-example"
  product_id = "prod-example"
  version_id = "vers-example"
  reason     = "Superseded by 1.1.0"
}
