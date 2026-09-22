package components

import (
	"context"
	"strings"
	"testing"

	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var dependencyAttributeTypes = map[string]attr.Type{
	"component_id":   types.StringType,
	"component_name": types.StringType,
	"version_id":     types.StringType,
	"version_name":   types.StringType,
	"type":           types.StringType,
	"order":          types.Int64Type,
	"position":       types.StringType,
}

func dependencyList(models ...dependencyModel) types.List {
	elements := make([]attr.Value, 0, len(models))
	for _, model := range models {
		elements = append(elements, types.ObjectValueMust(dependencyAttributeTypes, map[string]attr.Value{
			"component_id":   model.ComponentID,
			"component_name": model.ComponentName,
			"version_id":     model.VersionID,
			"version_name":   model.VersionName,
			"type":           model.Type,
			"order":          model.Order,
			"position":       model.Position,
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: dependencyAttributeTypes}, elements)
}

func dependencyModelWithOrder(order int64) dependencyModel {
	return dependencyModel{
		ComponentID:   types.StringValue("component-id"),
		ComponentName: types.StringValue("component-name"),
		VersionID:     types.StringValue("version-id"),
		VersionName:   types.StringValue("version-name"),
		Order:         types.Int64Value(order),
		Position:      types.StringNull(),
	}
}

func TestExpandDependenciesDefaultsTypeAndSortsByOrder(t *testing.T) {
	got, diagnostics := expandDependencies(context.Background(), dependencyList(
		func() dependencyModel {
			model := dependencyModelWithOrder(3)
			model.Type = types.StringNull()
			return model
		}(),
		func() dependencyModel {
			model := dependencyModelWithOrder(1)
			model.Type = types.StringValue("MAIN")
			return model
		}(),
	))
	if diagnostics.HasError() {
		t.Fatalf("expandDependencies returned diagnostics: %v", diagnostics)
	}

	want := []vewcomponents.Dependency{
		{ComponentID: "component-id", ComponentName: "component-name", VersionID: "version-id", VersionName: "version-name", Type: "MAIN", Order: 1},
		{ComponentID: "component-id", ComponentName: "component-name", VersionID: "version-id", VersionName: "version-name", Type: "HELPER", Order: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("expanded dependencies length = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].ComponentID != want[index].ComponentID || got[index].ComponentName != want[index].ComponentName || got[index].VersionID != want[index].VersionID || got[index].VersionName != want[index].VersionName || got[index].Type != want[index].Type || got[index].Order != want[index].Order || got[index].Position != want[index].Position {
			t.Fatalf("expanded dependency %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestExpandDependenciesAllowsMainAndHelper(t *testing.T) {
	for _, dependencyType := range []string{"MAIN", "HELPER"} {
		model := dependencyModelWithOrder(1)
		model.Type = types.StringValue(dependencyType)
		_, diagnostics := expandDependencies(context.Background(), dependencyList(model))
		if diagnostics.HasError() {
			t.Fatalf("type %q returned diagnostics: %v", dependencyType, diagnostics)
		}
	}
}

func TestExpandDependenciesAllowsAppendAndPrependPosition(t *testing.T) {
	for _, position := range []string{"APPEND", "PREPEND"} {
		model := dependencyModelWithOrder(1)
		model.Position = types.StringValue(position)
		got, diagnostics := expandDependencies(context.Background(), dependencyList(model))
		if diagnostics.HasError() {
			t.Fatalf("position %q returned diagnostics: %v", position, diagnostics)
		}
		if got[0].Position == nil || *got[0].Position != position {
			t.Fatalf("expanded position = %#v, want %q", got[0].Position, position)
		}
	}
}

func TestExpandDependenciesRejectsNonPositiveOrder(t *testing.T) {
	for _, order := range []int64{0, -1} {
		_, diagnostics := expandDependencies(context.Background(), dependencyList(dependencyModelWithOrder(order)))
		if !diagnostics.HasError() {
			t.Fatalf("order %d was accepted", order)
		}
		if !strings.Contains(diagnostics[0].Detail(), "dependency[0].order") {
			t.Fatalf("diagnostic = %q, want dependency index and field", diagnostics[0].Detail())
		}
	}
}

func TestExpandDependenciesRejectsDuplicateOrder(t *testing.T) {
	_, diagnostics := expandDependencies(context.Background(), dependencyList(dependencyModelWithOrder(1), dependencyModelWithOrder(1)))
	if !diagnostics.HasError() {
		t.Fatal("duplicate order was accepted")
	}
	if !strings.Contains(diagnostics[0].Detail(), "dependency[1].order") {
		t.Fatalf("diagnostic = %q, want duplicate dependency index and field", diagnostics[0].Detail())
	}
}

func TestExpandDependenciesRejectsUnknownType(t *testing.T) {
	model := dependencyModelWithOrder(1)
	model.Type = types.StringValue("RUNTIME")
	_, diagnostics := expandDependencies(context.Background(), dependencyList(model))
	if !diagnostics.HasError() {
		t.Fatal("unknown type was accepted")
	}
	if !strings.Contains(diagnostics[0].Detail(), "dependency[0].type") {
		t.Fatalf("diagnostic = %q, want dependency index and field", diagnostics[0].Detail())
	}
}

func TestExpandDependenciesRejectsUnknownPosition(t *testing.T) {
	model := dependencyModelWithOrder(1)
	model.Position = types.StringValue("BEFORE")
	_, diagnostics := expandDependencies(context.Background(), dependencyList(model))
	if !diagnostics.HasError() {
		t.Fatal("unknown position was accepted")
	}
	if !strings.Contains(diagnostics[0].Detail(), "dependency[0].position") {
		t.Fatalf("diagnostic = %q, want dependency index and field", diagnostics[0].Detail())
	}
}

func TestExpandDependenciesTreatsNullListAsEmpty(t *testing.T) {
	got, diagnostics := expandDependencies(context.Background(), types.ListNull(types.ObjectType{AttrTypes: dependencyAttributeTypes}))
	if diagnostics.HasError() {
		t.Fatalf("null list returned diagnostics: %v", diagnostics)
	}
	if got == nil {
		t.Fatal("null list returned nil dependencies, want empty list")
	}
	if len(got) != 0 {
		t.Fatalf("null list returned %d dependencies, want zero", len(got))
	}
}
