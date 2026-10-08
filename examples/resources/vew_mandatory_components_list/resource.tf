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

# Every recipe version created on "Ubuntu 24" (amd64) afterwards gets the
# marker first and the audit component last, around the recipe's own
# components. The versions must be released.
resource "vew_mandatory_components_list" "ubuntu_amd64" {
  project_id   = "project-platform"
  platform     = "Linux"
  os_version   = "Ubuntu 24"
  architecture = "amd64"

  prepended_components = [
    { component_id = "component-marker", component_version_id = "version-marker-1" },
  ]
  appended_components = [
    { component_id = "component-audit", component_version_id = "version-audit-1" },
  ]
}
