package projectsettings

import (
	"context"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectsettings"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// VEW's bounds: an idle workbench is checked every 5 minutes, so below 10
// minutes a user who just stepped away would lose the session.
const (
	minIdleMinutes = 10
	maxIdleMinutes = 1440
)

var (
	_ resource.Resource                   = (*lifecycleResource)(nil)
	_ resource.ResourceWithConfigure      = (*lifecycleResource)(nil)
	_ resource.ResourceWithImportState    = (*lifecycleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*lifecycleResource)(nil)
)

type lifecycleResource struct{ client api.API }

type lifecycleModel struct {
	ID                          types.String `tfsdk:"id"`
	ProjectID                   types.String `tfsdk:"project_id"`
	AlwaysOn                    types.Bool   `tfsdk:"always_on"`
	IdleStopMinutes             types.Int64  `tfsdk:"idle_stop_minutes"`
	NightlyStop                 types.Bool   `tfsdk:"nightly_stop"`
	WeekendStop                 types.Bool   `tfsdk:"weekend_stop"`
	AllowUserDisableNightlyStop types.Bool   `tfsdk:"allow_user_disable_nightly_stop"`
	AllowUserIdleTimeout        types.Bool   `tfsdk:"allow_user_idle_timeout"`
	UserIdleTimeoutMinMinutes   types.Int64  `tfsdk:"user_idle_timeout_min_minutes"`
	UserIdleTimeoutMaxMinutes   types.Int64  `tfsdk:"user_idle_timeout_max_minutes"`
}

// NewWorkbenchLifecycleResource constructs the vew_project_workbench_lifecycle resource.
func NewWorkbenchLifecycleResource() resource.Resource { return &lifecycleResource{} }

func (r *lifecycleResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_project_workbench_lifecycle"
}

func (r *lifecycleResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	flag := func(description string) schema.BoolAttribute {
		return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: description}
	}
	minutes := func(value int64, description string) schema.Int64Attribute {
		return schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(value), Description: description}
	}
	res.Schema = schema.Schema{
		Description: "A VEW project's workbench stop policy and what its users may change on their own workbenches.",
		Attributes: map[string]schema.Attribute{
			"id":                              idAttribute(),
			"project_id":                      projectIDAttribute(),
			"always_on":                       flag("Workbenches bypass idle, nightly and weekend stops."),
			"idle_stop_minutes":               schema.Int64Attribute{Optional: true, Description: "Stop after this many idle minutes (10-1440). Unset uses the deployment's default."},
			"nightly_stop":                    schema.BoolAttribute{Optional: true, Description: "Stop running workbenches nightly. Unset uses the deployment's default."},
			"weekend_stop":                    schema.BoolAttribute{Optional: true, Description: "Stop running workbenches at the weekend. Unset uses the deployment's default."},
			"allow_user_disable_nightly_stop": flag("Owners may turn off the nightly stop on their own workbenches."),
			"allow_user_idle_timeout":         flag("Owners may set their own idle timeout within the bounds below."),
			"user_idle_timeout_min_minutes":   minutes(30, "Lowest idle timeout an owner may set (10-1440)."),
			"user_idle_timeout_max_minutes":   minutes(480, "Highest idle timeout an owner may set (10-1440)."),
		},
	}
}

func (r *lifecycleResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if client := configure(req, res); client != nil {
		r.client = client
	}
}

func inBounds(v types.Int64) bool {
	return v.IsNull() || v.IsUnknown() || (v.ValueInt64() >= minIdleMinutes && v.ValueInt64() <= maxIdleMinutes)
}

func (r *lifecycleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, res *resource.ValidateConfigResponse) {
	var m lifecycleModel
	res.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	for name, v := range map[string]types.Int64{
		"idle_stop_minutes": m.IdleStopMinutes, "user_idle_timeout_min_minutes": m.UserIdleTimeoutMinMinutes,
		"user_idle_timeout_max_minutes": m.UserIdleTimeoutMaxMinutes,
	} {
		if !inBounds(v) {
			res.Diagnostics.AddAttributeError(path.Root(name), "Out of range", name+" must be between 10 and 1440 minutes.")
		}
	}
	lo, hi := m.UserIdleTimeoutMinMinutes, m.UserIdleTimeoutMaxMinutes
	if !lo.IsNull() && !lo.IsUnknown() && !hi.IsNull() && !hi.IsUnknown() && lo.ValueInt64() > hi.ValueInt64() {
		res.Diagnostics.AddAttributeError(path.Root("user_idle_timeout_min_minutes"), "Invalid bounds", "user_idle_timeout_min_minutes must not exceed user_idle_timeout_max_minutes.")
	}
}

func int64Ptr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueInt64()
	return &x
}

func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := v.ValueBool()
	return &x
}

func (m lifecycleModel) input() api.WorkbenchLifecycle {
	return api.WorkbenchLifecycle{
		AlwaysOn:                    m.AlwaysOn.ValueBool(),
		IdleStopMinutes:             int64Ptr(m.IdleStopMinutes),
		NightlyStop:                 boolPtr(m.NightlyStop),
		WeekendStop:                 boolPtr(m.WeekendStop),
		AllowUserDisableNightlyStop: m.AllowUserDisableNightlyStop.ValueBool(),
		AllowUserIdleTimeout:        m.AllowUserIdleTimeout.ValueBool(),
		UserIdleTimeoutMinMinutes:   m.UserIdleTimeoutMinMinutes.ValueInt64(),
		UserIdleTimeoutMaxMinutes:   m.UserIdleTimeoutMaxMinutes.ValueInt64(),
	}
}

func (m *lifecycleModel) apply(l api.WorkbenchLifecycle) {
	m.ID = m.ProjectID
	m.AlwaysOn = types.BoolValue(l.AlwaysOn)
	m.IdleStopMinutes = types.Int64PointerValue(l.IdleStopMinutes)
	m.NightlyStop = types.BoolPointerValue(l.NightlyStop)
	m.WeekendStop = types.BoolPointerValue(l.WeekendStop)
	m.AllowUserDisableNightlyStop = types.BoolValue(l.AllowUserDisableNightlyStop)
	m.AllowUserIdleTimeout = types.BoolValue(l.AllowUserIdleTimeout)
	m.UserIdleTimeoutMinMinutes = types.Int64Value(l.UserIdleTimeoutMinMinutes)
	m.UserIdleTimeoutMaxMinutes = types.Int64Value(l.UserIdleTimeoutMaxMinutes)
}

func (r *lifecycleResource) put(ctx context.Context, m *lifecycleModel, d *diag.Diagnostics) {
	if err := r.client.PutWorkbenchLifecycle(ctx, m.ProjectID.ValueString(), m.input()); err != nil {
		d.AddError("Setting the VEW project workbench lifecycle failed", err.Error())
		return
	}
	m.ID = m.ProjectID
}

func (r *lifecycleResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	var m lifecycleModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *lifecycleResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	var m lifecycleModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetWorkbenchLifecycle(ctx, m.ProjectID.ValueString())
	if vew.IsNotFound(err) {
		res.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		res.Diagnostics.AddError("Reading the VEW project workbench lifecycle failed", err.Error())
		return
	}
	m.apply(remote)
	res.Diagnostics.Append(res.State.Set(ctx, &m)...)
}

func (r *lifecycleResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	var m lifecycleModel
	res.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	r.put(ctx, &m, &res.Diagnostics)
	if !res.Diagnostics.HasError() {
		res.Diagnostics.Append(res.State.Set(ctx, &m)...)
	}
}

func (r *lifecycleResource) Delete(ctx context.Context, req resource.DeleteRequest, res *resource.DeleteResponse) {
	var m lifecycleModel
	res.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteWorkbenchLifecycle(ctx, m.ProjectID.ValueString()); err != nil {
		res.Diagnostics.AddError("Removing the VEW project workbench lifecycle failed", err.Error())
	}
}

func (r *lifecycleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	importProjectID(ctx, req, res)
}
