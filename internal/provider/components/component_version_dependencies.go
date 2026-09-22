package components

import (
	"context"
	"fmt"
	"sort"

	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type dependencyModel struct {
	ComponentID   types.String `tfsdk:"component_id"`
	ComponentName types.String `tfsdk:"component_name"`
	VersionID     types.String `tfsdk:"version_id"`
	VersionName   types.String `tfsdk:"version_name"`
	Type          types.String `tfsdk:"type"`
	Order         types.Int64  `tfsdk:"order"`
	Position      types.String `tfsdk:"position"`
}

func expandDependencies(ctx context.Context, dependencies types.List) ([]vewcomponents.Dependency, diag.Diagnostics) {
	if dependencies.IsNull() {
		return []vewcomponents.Dependency{}, nil
	}
	if dependencies.IsUnknown() {
		return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
			"Invalid component version dependencies",
			"dependencies must be known or null.",
		)}
	}

	var models []dependencyModel
	diagnostics := dependencies.ElementsAs(ctx, &models, false)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	expanded := make([]vewcomponents.Dependency, 0, len(models))
	seenOrders := make(map[int64]int, len(models))
	for index, model := range models {
		if model.Order.IsNull() || model.Order.IsUnknown() || model.Order.ValueInt64() <= 0 {
			diagnostics.AddError(
				"Invalid component version dependency",
				fmt.Sprintf("dependency[%d].order must be a positive integer.", index),
			)
			continue
		}
		if previousIndex, ok := seenOrders[model.Order.ValueInt64()]; ok {
			diagnostics.AddError(
				"Invalid component version dependency",
				fmt.Sprintf("dependency[%d].order duplicates dependency[%d].order.", index, previousIndex),
			)
			continue
		}
		seenOrders[model.Order.ValueInt64()] = index

		dependencyType := "HELPER"
		if !model.Type.IsNull() {
			if model.Type.IsUnknown() {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].type must be MAIN or HELPER.", index),
				)
				continue
			}
			dependencyType = model.Type.ValueString()
			if dependencyType != "MAIN" && dependencyType != "HELPER" {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].type must be MAIN or HELPER.", index),
				)
				continue
			}
		}

		var position *string
		if !model.Position.IsNull() {
			if model.Position.IsUnknown() {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].position must be APPEND or PREPEND.", index),
				)
				continue
			}
			positionValue := model.Position.ValueString()
			if positionValue != "APPEND" && positionValue != "PREPEND" {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].position must be APPEND or PREPEND.", index),
				)
				continue
			}
			position = &positionValue
		}

		expanded = append(expanded, vewcomponents.Dependency{
			ComponentID:   model.ComponentID.ValueString(),
			ComponentName: model.ComponentName.ValueString(),
			VersionID:     model.VersionID.ValueString(),
			VersionName:   model.VersionName.ValueString(),
			Type:          dependencyType,
			Order:         model.Order.ValueInt64(),
			Position:      position,
		})
	}
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	sort.SliceStable(expanded, func(left, right int) bool {
		return expanded[left].Order < expanded[right].Order
	})
	return expanded, diagnostics
}
