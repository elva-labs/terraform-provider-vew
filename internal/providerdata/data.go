package providerdata

import (
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/baseimages"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/images"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/mandatorycomponents"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/pipelines"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/projectsettings"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/technologies"
)

// Data is shared by provider resources and data sources.
type Data struct {
	Components               components.API
	ComponentReads           components.ComponentReadAPI
	ComponentVersions        components.ComponentVersionAPI
	ComponentVersionReads    components.ComponentVersionReadAPI
	ComponentVersionReleases components.ComponentVersionReleaseAPI
	Images                   images.API
	ImageReads               images.ReadAPI
	ProjectAPIURL            string
	Technologies             technologies.API
	ProjectAccounts          projectaccounts.API
	ProjectAccountReads      projectaccounts.ListAPI
	ProjectAccess            projectaccess.API
	ProjectBootstrap         projectaccess.BootstrapAPI
	ClientID                 string
	ProjectClientBootstrap   bool
	ProjectSettings          projectsettings.API
	BaseImages               baseimages.API
	MandatoryComponents      mandatorycomponents.API
	PublishingAPIURL         string
	Products                 products.API
	Promotions               products.PromotionAPI
	ProductVersionReads      products.VersionReadAPI
	Pipelines                pipelines.API
	PipelineReads            pipelines.ReadAPI
	Recipes                  recipes.RecipeAPI
	RecipeReads              recipes.RecipeReadAPI
	RecipeVersions           recipes.RecipeVersionAPI
	RecipeVersionReads       recipes.RecipeVersionReadAPI
	RecipeVersionReleases    recipes.RecipeVersionReleaseAPI
	Waiter                   vew.Waiter
}
