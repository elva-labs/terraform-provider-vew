resource "vew_project_group_assignment" "engineers" {
  project_id = vew_project.example.id
  group_id   = "01234567-89ab-cdef-0123-456789abcdef"
  roles      = ["PLATFORM_USER"]
  group_name = "vew-engineers" # optional label shown in the portal
}
