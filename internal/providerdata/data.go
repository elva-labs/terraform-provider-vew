package providerdata

import (
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
)

// Data is shared by provider resources and data sources.
type Data struct {
	Components               components.API
	ComponentVersions        components.ComponentVersionAPI
	ComponentVersionReleases components.ComponentVersionReleaseAPI
	Pipelines                pipelines.API
	Recipes                  recipes.RecipeAPI
	RecipeVersions           recipes.RecipeVersionAPI
	RecipeVersionReleases    recipes.RecipeVersionReleaseAPI
	Waiter                   vew.Waiter
}
