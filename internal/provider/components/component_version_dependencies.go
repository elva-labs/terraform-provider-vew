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

	diagnostics.Append(validateDependencyModels(models, false)...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	expanded := make([]vewcomponents.Dependency, 0, len(models))
	for _, model := range models {
		dependencyType := "HELPER"
		if !model.Type.IsNull() {
			dependencyType = model.Type.ValueString()
		}

		var position *string
		if !model.Position.IsNull() {
			positionValue := model.Position.ValueString()
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

	sort.SliceStable(expanded, func(left, right int) bool {
		return expanded[left].Order < expanded[right].Order
	})
	return expanded, diagnostics
}

func validateDependencies(ctx context.Context, dependencies types.List) diag.Diagnostics {
	var models []dependencyModel
	diagnostics := dependencies.ElementsAs(ctx, &models, false)
	if diagnostics.HasError() {
		return diagnostics
	}
	return validateDependencyModels(models, true)
}

func dependenciesHaveUnknownValues(ctx context.Context, dependencies types.List) (bool, diag.Diagnostics) {
	if dependencies.IsUnknown() {
		return true, nil
	}
	if dependencies.IsNull() {
		return false, nil
	}

	var models []dependencyModel
	diagnostics := dependencies.ElementsAs(ctx, &models, false)
	if diagnostics.HasError() {
		return false, diagnostics
	}
	for _, model := range models {
		if dependencyHasUnknownValue(model) {
			return true, diagnostics
		}
	}
	return false, diagnostics
}

func dependenciesHaveKnownDifference(ctx context.Context, state, plan types.List) (bool, diag.Diagnostics) {
	if state.IsUnknown() || plan.IsUnknown() {
		return false, nil
	}

	var stateModels, planModels []dependencyModel
	var diagnostics diag.Diagnostics
	if !state.IsNull() {
		diagnostics.Append(state.ElementsAs(ctx, &stateModels, false)...)
	}
	if !plan.IsNull() {
		diagnostics.Append(plan.ElementsAs(ctx, &planModels, false)...)
	}
	if diagnostics.HasError() {
		return false, diagnostics
	}
	if len(stateModels) != len(planModels) {
		return true, diagnostics
	}

	stateMatches := make([]int, len(stateModels))
	for index := range stateMatches {
		stateMatches[index] = -1
	}
	var matchPlanDependency func(int, []bool) bool
	matchPlanDependency = func(planIndex int, visited []bool) bool {
		for stateIndex := range stateModels {
			if visited[stateIndex] || dependencyModelsHaveKnownDifference(stateModels[stateIndex], planModels[planIndex]) {
				continue
			}
			visited[stateIndex] = true
			if stateMatches[stateIndex] == -1 || matchPlanDependency(stateMatches[stateIndex], visited) {
				stateMatches[stateIndex] = planIndex
				return true
			}
		}
		return false
	}
	for planIndex := range planModels {
		if !matchPlanDependency(planIndex, make([]bool, len(stateModels))) {
			return true, diagnostics
		}
	}
	return false, diagnostics
}

func dependencyModelsHaveKnownDifference(state, plan dependencyModel) bool {
	return knownInt64Difference(state.Order, plan.Order) ||
		knownStringDifference(state.ComponentID, plan.ComponentID, "") ||
		knownStringDifference(state.ComponentName, plan.ComponentName, "") ||
		knownStringDifference(state.VersionID, plan.VersionID, "") ||
		knownStringDifference(state.VersionName, plan.VersionName, "") ||
		knownStringDifference(state.Type, plan.Type, "HELPER") ||
		knownStringDifference(state.Position, plan.Position, "")
}

func knownStringDifference(state, plan types.String, nullValue string) bool {
	if state.IsUnknown() || plan.IsUnknown() {
		return false
	}
	stateValue, planValue := nullValue, nullValue
	if !state.IsNull() {
		stateValue = state.ValueString()
	}
	if !plan.IsNull() {
		planValue = plan.ValueString()
	}
	return stateValue != planValue
}

func knownInt64Difference(state, plan types.Int64) bool {
	return !state.IsNull() && !state.IsUnknown() && !plan.IsNull() && !plan.IsUnknown() && state.ValueInt64() != plan.ValueInt64()
}

func validateDependencyModels(models []dependencyModel, deferUnknown bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	seenOrders := make(map[int64]int, len(models))
	for index, model := range models {
		if deferUnknown && dependencyHasUnknownValue(model) {
			continue
		}
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

		if !model.Position.IsNull() {
			if model.Position.IsUnknown() {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].position must be APPEND or PREPEND.", index),
				)
				continue
			}
			if positionValue := model.Position.ValueString(); positionValue != "APPEND" && positionValue != "PREPEND" {
				diagnostics.AddError(
					"Invalid component version dependency",
					fmt.Sprintf("dependency[%d].position must be APPEND or PREPEND.", index),
				)
				continue
			}
		}
	}
	return diagnostics
}

func dependencyHasUnknownValue(model dependencyModel) bool {
	return model.ComponentID.IsUnknown() ||
		model.ComponentName.IsUnknown() ||
		model.VersionID.IsUnknown() ||
		model.VersionName.IsUnknown() ||
		model.Type.IsUnknown() ||
		model.Order.IsUnknown() ||
		model.Position.IsUnknown()
}
