---
page_title: "VEW Data Sources"
subcategory: ""
description: |-
  Read existing VEW project objects without managing them.
---

# VEW data sources

All VEW data sources are read-only. The caller needs assignment to the selected
VEW project and the domain read scope shown below. Configuring the provider
does not itself request a token or call VEW.

| Data sources | OAuth scope |
| --- | --- |
| `vew_component`, `vew_component_version` | `clients/packaging/component.read` |
| `vew_recipe`, `vew_recipe_version` | `clients/packaging/recipe.read` |
| `vew_pipeline`, `vew_pipelines`, `vew_image`, `vew_images` | `clients/packaging/pipeline.read` |

Exact data sources require explicit project and object identifiers. A missing
object is an error. Collection data sources return an empty list when no
objects match. Readable terminal objects remain visible, and optional values
remain null when VEW does not return them.

The component-version `definition_json` is stored in Terraform state. Secure
state and plan files accordingly. `vew_pipelines` and `vew_images` order
results by ascending ID; this does not imply recency. Image build actions
report progress but produce no structured HCL result during the invocation.
