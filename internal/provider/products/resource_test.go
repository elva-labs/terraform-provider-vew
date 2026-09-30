package products

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewproducts "github.com/elva-labs/terraform-provider-vew/internal/vew/products"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type productAPIStub struct {
	createCalls, updateCalls, archiveCalls int
	keys                                   []string
	createInput                            vewproducts.CreateProductInput
	updateInput                            vewproducts.UpdateProductInput
	remote                                 vewproducts.Product
	createErrs                             []error
	readErr, updateErr                     error
	archivingPolls                         int
}

func (f *productAPIStub) CreateProduct(_ context.Context, _ string, input vewproducts.CreateProductInput, key string) (string, error) {
	f.createCalls++
	f.createInput = input
	f.keys = append(f.keys, key)
	if len(f.createErrs) > 0 {
		err := f.createErrs[0]
		f.createErrs = f.createErrs[1:]
		if err != nil {
			return "", err
		}
	}
	return "prod-1", nil
}

func (f *productAPIStub) GetProduct(context.Context, string, string) (vewproducts.Product, error) {
	return f.remote, f.readErr
}

func (f *productAPIStub) UpdateProduct(_ context.Context, _, _ string, input vewproducts.UpdateProductInput) error {
	f.updateCalls++
	f.updateInput = input
	if f.updateErr != nil {
		return f.updateErr
	}
	f.remote.Name, f.remote.Description = input.Name, input.Description
	return nil
}

func (f *productAPIStub) ArchiveProduct(context.Context, string, string) (bool, time.Duration, error) {
	f.archiveCalls++
	return f.archiveCalls > f.archivingPolls, time.Second, nil
}

type immediateWaiter struct{}

func (immediateWaiter) Until(ctx context.Context, _, _ time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	for {
		result, err := read(ctx)
		if err != nil {
			return err
		}
		if done, err := evaluate(result.Status); done || err != nil {
			return err
		}
	}
}

func remoteProduct() vewproducts.Product {
	return vewproducts.Product{
		ProjectID: "project-1", ID: "prod-1", Name: "Workbench", Type: "WORKBENCH", Description: "desc",
		TechnologyID: "tech-1", TechnologyName: "Ubuntu", Status: "CREATED", AvailableStages: []string{"DEV"},
		CreatedAt: "created", UpdatedAt: "updated",
	}
}

func plannedModel() productModel {
	return productModel{
		ID: types.StringUnknown(), ProjectID: types.StringValue("project-1"), Name: types.StringValue("Workbench"),
		Description: types.StringValue("desc"), Type: types.StringValue("WORKBENCH"), TechnologyID: types.StringValue("tech-1"),
		TechnologyName: types.StringUnknown(), Status: types.StringUnknown(), RecommendedVersionID: types.StringUnknown(),
		AvailableStages: types.ListUnknown(types.StringType), CreatedAt: types.StringUnknown(), UpdatedAt: types.StringUnknown(),
		Timeouts: types.ObjectNull(timeoutAttributeTypes),
	}
}

func productTestState(t *testing.T, model productModel) tfsdk.State {
	t.Helper()
	var schemaResponse resource.SchemaResponse
	NewProductResource().Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatalf("encode product state: %v", d)
	}
	return state
}

func createProduct(t *testing.T, r *productResource) resource.CreateResponse {
	t.Helper()
	plan := productTestState(t, plannedModel())
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: plan.Schema, Raw: plan.Raw}}, &response)
	return response
}

func TestProductSchema(t *testing.T) {
	var response resource.SchemaResponse
	NewProductResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	attributes := response.Schema.Attributes
	for _, name := range []string{"project_id", "name", "type", "technology_id"} {
		if !attributes[name].IsRequired() {
			t.Errorf("%s must be required", name)
		}
	}
	for _, name := range []string{"id", "technology_name", "status", "recommended_version_id", "available_stages", "created_at", "updated_at"} {
		if !attributes[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
	if !attributes["description"].IsOptional() {
		t.Error("description must be optional")
	}
}

func TestProductCreateReadUpdateDelete(t *testing.T) {
	ctx := context.Background()
	client := &productAPIStub{remote: remoteProduct(), archivingPolls: 2}
	r := &productResource{client: client, waiter: immediateWaiter{}}

	create := createProduct(t, r)
	var created productModel
	if d := create.State.Get(ctx, &created); create.Diagnostics.HasError() || d.HasError() {
		t.Fatalf("create diagnostics = %v / %v", create.Diagnostics, d)
	}
	if client.createInput != (vewproducts.CreateProductInput{Name: "Workbench", Type: "WORKBENCH", Description: "desc", TechnologyID: "tech-1"}) {
		t.Fatalf("create input = %#v", client.createInput)
	}
	if created.ID.ValueString() != "prod-1" || created.TechnologyName.ValueString() != "Ubuntu" || created.Status.ValueString() != "CREATED" || !created.RecommendedVersionID.IsNull() {
		t.Fatalf("created state = %#v", created)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(client.keys[0]) {
		t.Fatalf("idempotency key = %q", client.keys[0])
	}

	plan := created
	plan.Name, plan.Description = types.StringValue("Renamed"), types.StringValue("")
	planState := productTestState(t, plan)
	update := resource.UpdateResponse{State: create.State}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: planState.Schema, Raw: planState.Raw}, State: create.State}, &update)
	var updated productModel
	if d := update.State.Get(ctx, &updated); update.Diagnostics.HasError() || d.HasError() || updated.Name.ValueString() != "Renamed" || client.updateInput.Description != "" {
		t.Fatalf("update = %#v / %v", updated, update.Diagnostics)
	}

	deletion := resource.DeleteResponse{State: update.State}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &deletion)
	if deletion.Diagnostics.HasError() || client.archiveCalls != 3 {
		t.Fatalf("delete diagnostics = %v, archive calls = %d", deletion.Diagnostics, client.archiveCalls)
	}
}

func TestProductCreateRetriesAmbiguousFailureWithSameKey(t *testing.T) {
	client := &productAPIStub{remote: remoteProduct(), createErrs: []error{&vew.APIError{Status: 503}, nil}}
	create := createProduct(t, &productResource{client: client, waiter: immediateWaiter{}})
	if create.Diagnostics.HasError() || client.createCalls != 2 || client.keys[0] != client.keys[1] {
		t.Fatalf("diagnostics = %v, calls = %d, keys = %v", create.Diagnostics, client.createCalls, client.keys)
	}
}

func TestProductCreateReportsMissingTechnology(t *testing.T) {
	client := &productAPIStub{createErrs: []error{&vew.APIError{Status: 422, Problem: vew.Problem{Code: "TECHNOLOGY_NOT_FOUND", Detail: "secret"}}}}
	create := createProduct(t, &productResource{client: client, waiter: immediateWaiter{}})
	if !create.Diagnostics.HasError() || client.createCalls != 1 || !strings.Contains(create.Diagnostics[0].Detail(), "technology_id") || strings.Contains(create.Diagnostics[0].Detail(), "secret") {
		t.Fatalf("diagnostics = %v, calls = %d", create.Diagnostics, client.createCalls)
	}
}

func TestProductReadRemovesArchivedOrMissingProducts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		readErr error
	}{
		{"archiving", "ARCHIVING", nil},
		{"archived", "ARCHIVED", nil},
		{"missing", "CREATED", &vew.APIError{Status: 404}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &productAPIStub{remote: remoteProduct()}
			r := &productResource{client: client, waiter: immediateWaiter{}}
			create := createProduct(t, r)
			client.remote.Status, client.readErr = tc.status, tc.readErr
			read := resource.ReadResponse{State: create.State}
			r.Read(context.Background(), resource.ReadRequest{State: create.State}, &read)
			if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
				t.Fatalf("expected removal, diagnostics = %v", read.Diagnostics)
			}
		})
	}
}

func TestProductValidateConfig(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, want string
	}{
		{"valid", "name", "Workbench arm64", ""},
		{"name with punctuation", "name", "Workbench (arm64)", "Invalid product name"},
		{"name too long", "name", strings.Repeat("a", 51), "Invalid product name"},
		{"description with dot", "description", "v1.0", "Invalid product description"},
		{"unknown type", "type", "DESKTOP", "Invalid product type"},
		{"blank technology", "technology_id", " ", "technology_id must be non-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schemaResponse resource.SchemaResponse
			r := &productResource{}
			r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
			values := map[string]tftypes.Value{}
			for name, attribute := range schemaResponse.Schema.Attributes {
				values[name] = tftypes.NewValue(attribute.GetType().TerraformType(context.Background()), nil)
			}
			values["timeouts"] = tftypes.NewValue(schemaResponse.Schema.Blocks["timeouts"].Type().TerraformType(context.Background()), nil)
			config := map[string]string{"project_id": "project-1", "name": "Workbench", "type": "WORKBENCH", "technology_id": "tech-1"}
			config[tc.field] = tc.value
			for name, value := range config {
				values[name] = tftypes.NewValue(tftypes.String, value)
			}
			request := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema,
				Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), values)}}
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), request, &response)
			if tc.want == "" {
				if response.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
				}
				return
			}
			found := false
			for _, diagnostic := range response.Diagnostics {
				found = found || strings.Contains(diagnostic.Summary(), tc.want) || strings.Contains(diagnostic.Detail(), tc.want)
			}
			if !found {
				t.Fatalf("expected %q in diagnostics: %v", tc.want, response.Diagnostics)
			}
		})
	}
}
