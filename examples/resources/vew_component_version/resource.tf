resource "vew_component" "example" {
  project_id              = "prog-73488"
  name                    = "example-component-version-parent"
  description             = "Parent component managed by Terraform"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}

resource "vew_component_version" "example" {
  project_id   = vew_component.example.project_id
  component_id = vew_component.example.id
  description  = "Managed by Terraform"
  release_type = "PATCH"
  definition_json = jsonencode({
    schemaVersion = "1.0"
    phases = [{
      name = "build"
      steps = [{
        name   = "HarmlessCheck"
        action = "ExecuteBash"
        inputs = { commands = ["true"] }
      }]
    }]
  })
  dependencies      = []
  software_vendor   = "Example vendor"
  software_version  = "1.0.0"
  license_dashboard = "https://licenses.example.invalid/component"
  notes             = "Example component version"

  # To add a dependency, replace the empty list above with an object containing
  # all dependency fields:
  # dependencies = [{
  #   component_id   = "cmp-456"
  #   component_name = "dependency-component"
  #   version_id     = "ver-789"
  #   version_name   = "1.2.3"
  #   type           = "HELPER"
  #   order          = 1
  #   position       = "PREPEND"
  # }]
}
