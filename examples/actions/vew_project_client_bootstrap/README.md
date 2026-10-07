# Project client bootstrap

Use only for an existing project with no active service-client assignments.
Provide Projects and Packaging API URLs and recovery OAuth credentials through
`VEW_*`, and set `TF_VAR_project_id` and `TF_VAR_management_client_id` to an
existing project and a different management client.

Preview the grant with `terraform plan -invoke=action.vew_project_client_bootstrap.manager`,
then explicitly invoke it with `terraform apply -invoke=action.vew_project_client_bootstrap.manager`.
Normal apply and destroy do not invoke this action.

Switch to management credentials after success. The recovery client cannot read
the assignment it just granted. If a response is lost, check the assignment with
management credentials before retrying; successful bootstrap makes the project
ineligible for another bootstrap. Existing non-orphan projects must use an
already assigned management client.
