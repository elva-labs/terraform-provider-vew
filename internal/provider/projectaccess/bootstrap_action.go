package projectaccess

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type bootstrapAction struct {
	client   api.BootstrapAPI
	callerID string
}
type bootstrapModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	ClientID  types.String `tfsdk:"client_id"`
}

var _ action.ActionWithConfigure = (*bootstrapAction)(nil)

func NewBootstrapAction() action.Action { return &bootstrapAction{} }
func (a *bootstrapAction) Metadata(_ context.Context, req action.MetadataRequest, res *action.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_project_client_bootstrap"
}
func (a *bootstrapAction) Schema(_ context.Context, _ action.SchemaRequest, res *action.SchemaResponse) {
	res.Schema = schema.Schema{
		Description: "Grants a different management client access to an existing project with no active service-client assignments. Invoke explicitly with platform recovery credentials and project_client_bootstrap = true. This one-time grant has no refresh or destroy lifecycle.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Required: true, Description: "Existing orphaned project."},
			"client_id":  schema.StringAttribute{Required: true, Description: "Management client to assign. Must differ from the configured recovery client."},
		},
	}
}
func (a *bootstrapAction) Configure(_ context.Context, req action.ConfigureRequest, res *action.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok {
		res.Diagnostics.AddError("Unable to configure VEW bootstrap action", "Unexpected provider data type.")
		return
	}
	if !data.ProjectClientBootstrap || data.ProjectBootstrap == nil || data.ProjectAPIURL == "" || data.ClientID == "" {
		res.Diagnostics.AddError("Missing bootstrap configuration", "Set projects_api_url and project_client_bootstrap = true on a separate recovery provider alias. Grant that client client_assignment.write and client_assignment.bootstrap.")
		return
	}
	a.client, a.callerID = data.ProjectBootstrap, data.ClientID
}
func (a *bootstrapAction) Invoke(ctx context.Context, req action.InvokeRequest, res *action.InvokeResponse) {
	var model bootstrapModel
	res.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if res.Diagnostics.HasError() {
		return
	}
	if !known(model.ProjectID) || strings.TrimSpace(model.ProjectID.ValueString()) == "" ||
		!known(model.ClientID) || strings.TrimSpace(model.ClientID.ValueString()) == "" {
		res.Diagnostics.AddError("Invalid bootstrap target", "project_id and client_id must be known and nonempty.")
		return
	}
	if a.client == nil || a.callerID == "" {
		res.Diagnostics.AddError("Missing bootstrap configuration", "Configure the action with a separate recovery provider alias and project_client_bootstrap = true.")
		return
	}
	if model.ClientID.ValueString() == a.callerID {
		res.Diagnostics.AddError("Self-assignment is forbidden", "The recovery client cannot assign itself. Set client_id to a different management client.")
		return
	}
	assignment, err := a.client.AssignClient(ctx, model.ProjectID.ValueString(), model.ClientID.ValueString())
	if err != nil {
		detail := "The bootstrap request could not be confirmed. It may have succeeded; verify the target assignment using management credentials before retrying."
		var apiErr *vew.APIError
		if errors.As(err, &apiErr) && apiErr.Status < 500 {
			detail = fmt.Sprintf("The Projects API returned HTTP %d. Bootstrap requires assignment-write and bootstrap scopes, a different target client, and an existing project with no active client assignments. If a previous attempt may have succeeded, verify access using management credentials.", apiErr.Status)
		}
		res.Diagnostics.AddError("Unable to bootstrap VEW project access", detail)
		return
	}
	if assignment.ProjectID != model.ProjectID.ValueString() || assignment.ClientID != model.ClientID.ValueString() || assignment.Status != "ACTIVE" {
		res.Diagnostics.AddError("Invalid VEW bootstrap response", "The response did not confirm an ACTIVE assignment for the requested project and management client. The grant may have succeeded; verify it using management credentials before retrying.")
		return
	}
	if res.SendProgress != nil {
		res.SendProgress(action.InvokeProgressEvent{Message: "Management client assignment confirmed ACTIVE. Use management credentials for subsequent project operations."})
	}
}
