package recipes

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	vewrecipes "github.com/elva-labs/terraform-provider-vew/internal/vew/recipes"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func setRecipeVersionState(ctx context.Context, model *recipeVersionModel, remote vewrecipes.RecipeVersion) error {
	model.ID = types.StringValue(remote.ID)
	model.RecipeID = types.StringValue(remote.RecipeID)
	model.Status = types.StringValue(remote.Status)
	model.Name = types.StringValue(remote.Name)
	model.Description = types.StringValue(remote.Description)
	model.CreatedAt = types.StringValue(remote.CreatedAt)
	model.CreatedBy = types.StringValue(remote.CreatedBy)
	model.UpdatedAt = types.StringValue(remote.UpdatedAt)
	model.UpdatedBy = types.StringValue(remote.UpdatedBy)
	volume, err := strconv.ParseFloat(strings.TrimSpace(remote.VolumeSize), 64)
	if err != nil || math.IsNaN(volume) || math.IsInf(volume, 0) || math.Trunc(volume) != volume || volume < 8 || volume > 500 {
		return errors.New("VEW recipe version returned an invalid volume size")
	}
	model.VolumeSize = types.Int64Value(int64(volume))
	integrations := remote.Integrations
	if integrations == nil {
		integrations = []string{}
	}
	integrationSet, diagnostics := types.SetValueFrom(ctx, types.StringType, integrations)
	if diagnostics.HasError() {
		return errors.New("VEW recipe version returned invalid integrations")
	}
	model.Integrations = integrationSet
	model.BaseImageChannel = types.StringPointerValue(remote.BaseImageChannel)
	if model.ConfiguredComponents.IsNull() && remote.Components != nil {
		configured, diagnostics := recipeComponentList(ctx, *remote.Components)
		if diagnostics.HasError() {
			return errors.New("VEW recipe version returned invalid configured components")
		}
		model.ConfiguredComponents = configured
	}
	effective, diagnostics := recipeComponentList(ctx, remote.EffectiveComponents)
	if diagnostics.HasError() {
		return errors.New("VEW recipe version returned invalid effective components")
	}
	model.EffectiveComponents = effective
	return nil
}
