package technologies

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewtechnologies "github.com/elva-labs/terraform-provider-vew/internal/vew/technologies"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const technologyCreateKey = "technology_create_idempotency_key"

var safeTechnologyCorrelation = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

var (
	_ resource.Resource                   = (*technologyResource)(nil)
	_ resource.ResourceWithConfigure      = (*technologyResource)(nil)
	_ resource.ResourceWithImportState    = (*technologyResource)(nil)
	_ resource.ResourceWithValidateConfig = (*technologyResource)(nil)
)

type technologyResource struct {
	client vewtechnologies.API
}

type technologyModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func NewTechnologyResource() resource.Resource { return &technologyResource{} }

func (r *technologyResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_technology"
}

func (r *technologyResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true},
		"project_id":  schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"name":        schema.StringAttribute{Required: true},
		"description": schema.StringAttribute{Optional: true, Computed: true},
		"created_at":  schema.StringAttribute{Computed: true},
		"updated_at":  schema.StringAttribute{Computed: true},
	}}
}

func (r *technologyResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}
	data, ok := request.ProviderData.(providerdata.Data)
	if !ok {
		response.Diagnostics.AddError("Missing Projects API configuration", "The vew_technology resource requires a configured Projects API URL. Set projects_api_url or VEW_PROJECTS_API_URL.")
		return
	}
	if strings.TrimSpace(data.ProjectAPIURL) == "" {
		response.Diagnostics.AddError("Missing Projects API URL", "The vew_technology resource requires a Projects API endpoint. Set projects_api_url or VEW_PROJECTS_API_URL.")
		return
	}
	if data.Technologies == nil {
		response.Diagnostics.AddError("Missing Technology API", "Provider data does not include the Projects Technology API.")
		return
	}
	r.client = data.Technologies
}

func (r *technologyResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config technologyModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}
	for name, value := range map[string]types.String{"project_id": config.ProjectID, "name": config.Name} {
		if !value.IsUnknown() && !value.IsNull() && strings.TrimSpace(value.ValueString()) == "" {
			response.Diagnostics.AddAttributeError(path.Root(name), "Empty technology value", name+" must be non-empty.")
		}
	}
}

func (r *technologyResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	projectID, technologyID, err := parseTechnologyImportID(request.ID)
	if err != nil {
		response.Diagnostics.AddError("Invalid technology import ID", "Expected project_id/technology_id.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("id"), technologyID)...)
}

func parseTechnologyImportID(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		return "", "", errors.New("expected project_id/technology_id")
	}
	return parts[0], parts[1], nil
}

func (r *technologyResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	var model technologyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	key, err := newTechnologyIdempotencyKey()
	if err != nil {
		response.Diagnostics.AddError("Unable to create VEW technology", "A stable create idempotency key could not be generated.")
		return
	}
	if response.Private != nil {
		privateKey, marshalErr := json.Marshal(string(key))
		if marshalErr != nil {
			response.Diagnostics.AddError("Unable to create VEW technology", "The create idempotency key could not be preserved safely.")
			return
		}
		response.Diagnostics.Append(response.Private.SetKey(ctx, technologyCreateKey, privateKey)...)
		if response.Diagnostics.HasError() {
			return
		}
	}
	input := technologyInput(model)
	id, err := r.client.CreateTechnology(ctx, model.ProjectID.ValueString(), input, string(key))
	if err != nil {
		// The idempotency reservation makes a second identical submission safe.
		// This recovers accepted creates when the response was lost or an
		// in-progress reservation outlived the transport's bounded retries.
		if ambiguousTechnologyCreate(err) {
			id, err = r.client.CreateTechnology(ctx, model.ProjectID.ValueString(), input, string(key))
		}
		if err != nil {
			addTechnologyError(&response.Diagnostics, "create", err, model.ProjectID.ValueString())
			return
		}
	}
	model.ID = types.StringValue(id)
	remote, err := r.client.GetTechnology(ctx, model.ProjectID.ValueString(), id)
	if err != nil {
		model.CreatedAt, model.UpdatedAt = types.StringNull(), types.StringNull()
		stateCtx := context.WithoutCancel(ctx)
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
		response.Diagnostics.AddError("Unable to read VEW technology after create", fmt.Sprintf("Technology %q was accepted and its ID is retained in state, but canonical state could not be read. Refresh it on the next Terraform operation.", id))
		return
	}
	if identityErr := validateTechnologyIdentity(remote, model.ProjectID.ValueString(), id); identityErr != nil {
		model.CreatedAt, model.UpdatedAt = types.StringNull(), types.StringNull()
		stateCtx := context.WithoutCancel(ctx)
		response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
		response.Diagnostics.AddError("Invalid VEW technology response", "The technology response did not match the requested project and technology identity. The accepted technology ID is retained in state.")
		return
	}
	setTechnologyState(&model, remote)
	stateCtx := context.WithoutCancel(ctx)
	response.Diagnostics.Append(response.State.Set(stateCtx, &model)...)
	if response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(stateCtx, technologyCreateKey, nil)...)
	}
}

func ambiguousTechnologyCreate(err error) bool {
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		return true
	}
	return apiError.Status == 429 || apiError.Status >= 500 || (apiError.Status == 409 && apiError.Problem.Code == "IDEMPOTENCY_REQUEST_IN_PROGRESS")
}

func newTechnologyIdempotencyKey() ([]byte, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	key := make([]byte, 36)
	hex.Encode(key[0:8], raw[0:4])
	key[8] = '-'
	hex.Encode(key[9:13], raw[4:6])
	key[13] = '-'
	hex.Encode(key[14:18], raw[6:8])
	key[18] = '-'
	hex.Encode(key[19:23], raw[8:10])
	key[23] = '-'
	hex.Encode(key[24:36], raw[10:16])
	return key, nil
}

func technologyInput(model technologyModel) vewtechnologies.TechnologyInput {
	description := ""
	if !model.Description.IsNull() && !model.Description.IsUnknown() {
		description = model.Description.ValueString()
	}
	return vewtechnologies.TechnologyInput{Name: model.Name.ValueString(), Description: description}
}

func (r *technologyResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	var state technologyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	r.read(ctx, &state, &response.State, &response.Diagnostics)
	if !response.Diagnostics.HasError() && response.Private != nil {
		response.Diagnostics.Append(response.Private.SetKey(ctx, technologyCreateKey, nil)...)
	}
}

func (r *technologyResource) read(ctx context.Context, state *technologyModel, terraformState *tfsdk.State, diagnostics *diag.Diagnostics) {
	remote, err := r.client.GetTechnology(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if vew.IsNotFound(err) {
		terraformState.RemoveResource(ctx)
		return
	}
	if err != nil {
		addTechnologyError(diagnostics, "read", err, state.ProjectID.ValueString())
		return
	}
	if validateTechnologyIdentity(remote, state.ProjectID.ValueString(), state.ID.ValueString()) != nil {
		diagnostics.AddError("Invalid VEW technology response", "The technology response did not match the project and technology identity in state.")
		return
	}
	setTechnologyState(state, remote)
	diagnostics.Append(terraformState.Set(ctx, state)...)
}

func (r *technologyResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	var plan technologyModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	var state technologyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateTechnology(ctx, state.ProjectID.ValueString(), state.ID.ValueString(), technologyInput(plan)); err != nil {
		if vew.IsNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		addTechnologyError(&response.Diagnostics, "update", err, state.ProjectID.ValueString())
		return
	}
	remote, err := r.client.GetTechnology(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil {
		if vew.IsNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		addTechnologyError(&response.Diagnostics, "read after update", err, state.ProjectID.ValueString())
		return
	}
	if validateTechnologyIdentity(remote, state.ProjectID.ValueString(), state.ID.ValueString()) != nil {
		response.Diagnostics.AddError("Invalid VEW technology response", "The technology response did not match the project and technology identity in state.")
		return
	}
	setTechnologyState(&plan, remote)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

func validateTechnologyIdentity(remote vewtechnologies.Technology, projectID, technologyID string) error {
	if strings.TrimSpace(remote.ID) == "" || remote.ID != technologyID || remote.ProjectID != projectID {
		return errors.New("technology identity mismatch")
	}
	return nil
}

func (r *technologyResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	var state technologyModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteTechnology(ctx, state.ProjectID.ValueString(), state.ID.ValueString()); err != nil && !vew.IsNotFound(err) {
		addTechnologyError(&response.Diagnostics, "delete", err, state.ProjectID.ValueString())
	}
}

func setTechnologyState(state *technologyModel, remote vewtechnologies.Technology) {
	state.ID = types.StringValue(remote.ID)
	state.ProjectID = types.StringValue(remote.ProjectID)
	state.Name = types.StringValue(remote.Name)
	state.Description = types.StringValue(remote.Description)
	state.CreatedAt = types.StringValue(remote.CreatedAt)
	state.UpdatedAt = types.StringValue(remote.UpdatedAt)
}

func addTechnologyError(diagnostics *diag.Diagnostics, operation string, err error, projectID string) {
	message := "VEW Projects technology " + operation + " failed"
	var apiError *vew.APIError
	if !errors.As(err, &apiError) {
		diagnostics.AddError(message, "The Projects API request could not be completed safely.")
		return
	}
	if apiError.Status == 403 {
		scope := "technology.read"
		if operation == "create" || operation == "update" || operation == "delete" {
			scope = "technology.write"
		}
		diagnostics.AddError(message, fmt.Sprintf("The service client needs Projects scope %q and an active assignment to project %q.", scope, projectID))
		return
	}
	if apiError.Status == 409 && (apiError.Problem.Code == "TECHNOLOGY_IN_USE" || apiError.Problem.Code == "TECHNOLOGY_REFERENCED") {
		diagnostics.AddError("Unable to delete VEW technology", "Technology is still referenced by a retained project account. Resolve the account association before deleting this technology.")
		return
	}
	parts := []string{fmt.Sprintf("HTTP status %d", apiError.Status)}
	if safeTechnologyCorrelation.MatchString(apiError.Problem.Code) {
		parts = append(parts, "problem code "+apiError.Problem.Code)
	}
	if safeTechnologyCorrelation.MatchString(apiError.Problem.RequestID) {
		parts = append(parts, "request ID "+apiError.Problem.RequestID)
	}
	diagnostics.AddError(message, strings.Join(parts, ", "))
}
