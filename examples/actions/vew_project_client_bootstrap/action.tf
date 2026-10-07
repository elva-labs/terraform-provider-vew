terraform {
  required_version = ">= 1.16.0"
  required_providers {
    vew = { source = "elva-labs/vew" }
  }
}

# Supply recovery credentials through VEW_* environment variables.
provider "vew" {
  alias                    = "recovery"
  project_client_bootstrap = true
}

variable "project_id" {
  type = string
}

variable "management_client_id" {
  type = string
}

action "vew_project_client_bootstrap" "manager" {
  provider = vew.recovery
  config {
    project_id = var.project_id
    client_id  = var.management_client_id
  }
}
