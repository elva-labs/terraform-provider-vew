package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestComponentDataSourceSchema(t *testing.T) {
	ds := NewComponentDataSource()
	var response datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &response)
	for _, name := range []string{"project_id", "component_id"} {
		a, ok := response.Schema.Attributes[name].(schema.StringAttribute)
		if !ok || !a.IsRequired() || a.IsComputed() {
			t.Fatalf("%s must be required selector: %#v", name, response.Schema.Attributes[name])
		}
	}
	for _, name := range []string{"name", "description", "platform", "supported_architectures", "supported_os_versions", "status", "created_at", "created_by", "updated_at", "updated_by"} {
		if !response.Schema.Attributes[name].IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
}

func TestComponentIdentifierValidationAndResponseMatching(t *testing.T) {
	for _, value := range []string{"abc", "A_1.x:y-z", strings.Repeat("a", 128)} {
		if !safeComponentIdentifier.MatchString(value) {
			t.Fatalf("valid identifier %q rejected", value)
		}
	}
	for _, value := range []string{"", " abc", "abc ", "a/b", strings.Repeat("a", 129), "é"} {
		if safeComponentIdentifier.MatchString(value) {
			t.Fatalf("invalid identifier %q accepted", value)
		}
	}
	if componentResponseMatchesSelector(vewcomponents.Component{ID: "other"}, "wanted") {
		t.Fatal("mismatched remote component accepted")
	}
	if !componentResponseMatchesSelector(vewcomponents.Component{ID: "wanted"}, "wanted") {
		t.Fatal("matching remote component rejected")
	}
}

func TestComponentSelectorUnknownIsDeferredUntilRead(t *testing.T) {
	var configDiagnostics diag.Diagnostics
	validateSelector(&configDiagnostics, "component_id", types.StringUnknown(), true)
	if configDiagnostics.HasError() {
		t.Fatalf("unknown config selector should be deferred: %v", configDiagnostics)
	}

	var readDiagnostics diag.Diagnostics
	validateSelector(&readDiagnostics, "component_id", types.StringUnknown(), false)
	if !readDiagnostics.HasError() {
		t.Fatal("unknown read selector should be rejected")
	}

	for _, invalid := range []types.String{types.StringNull(), types.StringValue(" bad/id ")} {
		for _, allowUnknown := range []bool{true, false} {
			var diagnostics diag.Diagnostics
			validateSelector(&diagnostics, "component_id", invalid, allowUnknown)
			if !diagnostics.HasError() {
				t.Fatalf("invalid selector %s accepted with allowUnknown=%t", invalid.String(), allowUnknown)
			}
		}
	}
}

func TestComponentReadDiagnosticFiltersUnsafeProblemMetadata(t *testing.T) {
	err := &vew.APIError{Status: 403, Problem: vew.Problem{Code: "bad/code", RequestID: "secret token"}}
	detail := safeComponentReadDiagnostic(err)
	if !strings.Contains(detail, "HTTP status 403") || strings.Contains(detail, "bad/code") || strings.Contains(detail, "secret token") {
		t.Fatalf("unsafe diagnostic %q", detail)
	}
	err.Problem.Code = "FORBIDDEN"
	err.Problem.RequestID = "req-123"
	detail = safeComponentReadDiagnostic(err)
	if !strings.Contains(detail, "FORBIDDEN") || !strings.Contains(detail, "req-123") {
		t.Fatalf("safe metadata omitted: %q", detail)
	}
}

func TestComponentDataSourceMapsArchivedObject(t *testing.T) {
	client := componentReadStub{component: vewcomponents.Component{ID: "c1", Status: "ARCHIVED"}}
	ds := &componentDataSource{client: client}
	c, err := ds.client.GetComponent(context.Background(), "p1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "ARCHIVED" {
		t.Fatalf("status = %q", c.Status)
	}
}

type componentReadStub struct{ component vewcomponents.Component }

func (s componentReadStub) GetComponent(context.Context, string, string) (vewcomponents.Component, error) {
	return s.component, nil
}
