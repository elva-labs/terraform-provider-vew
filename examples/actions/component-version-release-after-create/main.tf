terraform {
  required_providers {
    vew = {
      source  = "elva-labs/vew"
      version = "0.1.0"
    }
  }
}

provider "vew" {}

resource "vew_component" "example" {
  project_id              = "prog-73488"
  name                    = "example-component-release-trigger"
  description             = "Component for the release trigger example"
  platform                = "Linux"
  supported_architectures = ["arm64", "x86_64"]
  supported_os_versions   = ["Ubuntu 24"]
}

resource "vew_component_version" "example" {
  project_id   = vew_component.example.project_id
  component_id = vew_component.example.id
  description  = "Component version release trigger example"
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
  dependencies     = []
  software_vendor  = "Example vendor"
  software_version = "1.0.0"

  lifecycle {
    action_trigger {
      events     = [after_create]
      actions    = [action.vew_component_version_release.example]
      on_failure = halt
    }
  }
}

action "vew_component_version_release" "example" {
  config {
    project_id   = caller.project_id
    component_id = caller.component_id
    version_id   = caller.id
  }
}

action "vew_component_version_release" "retry" {
  config {
    project_id   = vew_component_version.example.project_id
    component_id = vew_component_version.example.component_id
    version_id   = vew_component_version.example.id
  }
}
