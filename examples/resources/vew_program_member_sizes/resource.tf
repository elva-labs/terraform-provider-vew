resource "vew_program_member_sizes" "engineer" {
  project_id = "proj-example"
  user_id    = "01234567-89AB-CDEF-0123-456789ABCDEF"
  user_email = "engineer@example.com"

  # Beyond everyone's (the catalog's alwaysAllowed sizes, for example a 4 vCPU size and a 250 GB disk).
  sizes = ["standard-m", "disk-500"]
}
