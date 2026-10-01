resource "vew_project_client_assignment" "automation" {
  project_id = vew_project.example.id
  client_id  = "packaging-automation"
  status     = "ACTIVE"
}
