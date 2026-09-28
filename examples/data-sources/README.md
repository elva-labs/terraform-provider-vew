# VEW data sources

This example reads existing project objects. Set the required selector values
to IDs that already exist in your VEW project, then initialize and plan:

```shell
terraform init
terraform plan \
  -var='project_id=your-project-id' \
  -var='component_id=your-component-id' \
  -var='component_version_id=your-component-version-id' \
  -var='recipe_id=your-recipe-id' \
  -var='recipe_version_id=your-recipe-version-id' \
  -var='pipeline_id=your-pipeline-id' \
  -var='image_id=your-image-id'
```

The configuration demonstrates all eight data sources. The caller needs
project assignment and `clients/packaging/component.read` for component reads,
`clients/packaging/recipe.read` for recipe reads, and
`clients/packaging/pipeline.read` for pipeline and image reads. Reads do not
mutate VEW. Exact lookups need existing IDs; list sources may return empty
collections. Terminal objects remain readable and nullable historical fields
stay null. The component-version definition is stored in Terraform state.

Pipelines and images are sorted by ascending ID. The image filter is optional;
change `status` to one of `CREATING`, `CREATED`, `FAILED`, `RETIRED`, or
`DELETED`, or set it to `null` to list all statuses. No data source selects a
“latest” image. `vew_image_build` progress cannot be referenced as a structured
HCL value in the same invocation; read a known image ID or query the collection
on a later refresh.
