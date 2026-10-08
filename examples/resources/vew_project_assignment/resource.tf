resource "vew_project_assignment" "exception" {
  project_id        = vew_project.example.id
  user_id           = "01234567-89AB-CDEF-0123-456789ABCDEF"
  roles             = ["PLATFORM_USER"]
  user_email        = "engineer@example.com"
  user_display_name = "Example Engineer"
}
