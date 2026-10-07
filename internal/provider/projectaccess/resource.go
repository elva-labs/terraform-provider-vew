package projectaccess

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
	api "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccess"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const createKey = "project_create_idempotency_key"

var groupUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var userID = regexp.MustCompile(`^[A-Z0-9_-]+$`)
var _ resource.Resource = (*accessResource)(nil)
var _ resource.ResourceWithConfigure = (*accessResource)(nil)
var _ resource.ResourceWithImportState = (*accessResource)(nil)
var _ resource.ResourceWithValidateConfig = (*accessResource)(nil)

type accessResource struct {
	kind   string
	client api.API
}
type model struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	UserID        types.String `tfsdk:"user_id"`
	GroupID       types.String `tfsdk:"group_id"`
	ClientID      types.String `tfsdk:"client_id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	IsActive      types.Bool   `tfsdk:"is_active"`
	RemoteSupport types.Bool   `tfsdk:"remote_support_enabled"`
	Experience    types.String `tfsdk:"experience"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
	Roles         types.Set    `tfsdk:"roles"`
	Email         types.String `tfsdk:"user_email"`
	DisplayName   types.String `tfsdk:"user_display_name"`
	Status        types.String `tfsdk:"status"`
}

type projectModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	IsActive      types.Bool   `tfsdk:"is_active"`
	RemoteSupport types.Bool   `tfsdk:"remote_support_enabled"`
	Experience    types.String `tfsdk:"experience"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}
type userModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	UserID      types.String `tfsdk:"user_id"`
	Roles       types.Set    `tfsdk:"roles"`
	Email       types.String `tfsdk:"user_email"`
	DisplayName types.String `tfsdk:"user_display_name"`
}
type groupModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	GroupID   types.String `tfsdk:"group_id"`
	Roles     types.Set    `tfsdk:"roles"`
}
type clientModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	ClientID  types.String `tfsdk:"client_id"`
	Status    types.String `tfsdk:"status"`
}
type modelReader interface {
	Get(context.Context, any) diag.Diagnostics
}

func (r *accessResource) get(ctx context.Context, source modelReader) (model, diag.Diagnostics) {
	var m model
	var d diag.Diagnostics
	switch r.kind {
	case "project":
		var v projectModel
		d = source.Get(ctx, &v)
		m.ID = v.ID
		m.Name = v.Name
		m.Description = v.Description
		m.IsActive = v.IsActive
		m.RemoteSupport = v.RemoteSupport
		m.Experience = v.Experience
		m.CreatedAt = v.CreatedAt
		m.UpdatedAt = v.UpdatedAt
	case "assignment":
		var v userModel
		d = source.Get(ctx, &v)
		m.ID = v.ID
		m.ProjectID = v.ProjectID
		m.UserID = v.UserID
		m.Roles = v.Roles
		m.Email = v.Email
		m.DisplayName = v.DisplayName
	case "group_assignment":
		var v groupModel
		d = source.Get(ctx, &v)
		m.ID = v.ID
		m.ProjectID = v.ProjectID
		m.GroupID = v.GroupID
		m.Roles = v.Roles
	case "client_assignment":
		var v clientModel
		d = source.Get(ctx, &v)
		m.ID = v.ID
		m.ProjectID = v.ProjectID
		m.ClientID = v.ClientID
		m.Status = v.Status
	}
	return m, d
}
func (r *accessResource) set(ctx context.Context, state *tfsdk.State, m *model) diag.Diagnostics {
	switch r.kind {
	case "project":
		return state.Set(ctx, &projectModel{m.ID, m.Name, m.Description, m.IsActive, m.RemoteSupport, m.Experience, m.CreatedAt, m.UpdatedAt})
	case "assignment":
		return state.Set(ctx, &userModel{m.ID, m.ProjectID, m.UserID, m.Roles, m.Email, m.DisplayName})
	case "group_assignment":
		return state.Set(ctx, &groupModel{m.ID, m.ProjectID, m.GroupID, m.Roles})
	case "client_assignment":
		return state.Set(ctx, &clientModel{m.ID, m.ProjectID, m.ClientID, m.Status})
	}
	return nil
}
func NewProjectResource() resource.Resource { return &accessResource{kind: "project"} }
func NewUserResource() resource.Resource    { return &accessResource{kind: "assignment"} }
func NewGroupResource() resource.Resource   { return &accessResource{kind: "group_assignment"} }
func NewClientResource() resource.Resource  { return &accessResource{kind: "client_assignment"} }
func (r *accessResource) Metadata(_ context.Context, req resource.MetadataRequest, res *resource.MetadataResponse) {
	res.TypeName = req.ProviderTypeName + "_project"
	if r.kind != "project" {
		res.TypeName += "_" + r.kind
	}
}
func (r *accessResource) Schema(_ context.Context, _ resource.SchemaRequest, res *resource.SchemaResponse) {
	// The id never changes once known: without UseStateForUnknown an in-place update of the project
	// plans it as unknown, and every resource keyed on project_id (RequiresReplace) plans a replacement.
	stable := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	attrs := map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true, PlanModifiers: stable}}
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	if r.kind == "project" {
		attrs["name"] = schema.StringAttribute{Required: true}
		attrs["description"] = schema.StringAttribute{Optional: true, Computed: true}
		attrs["is_active"] = schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)}
		// Unset keeps the project's value (a new project allows remote support).
		attrs["remote_support_enabled"] = schema.BoolAttribute{
			Optional:    true,
			Computed:    true,
			Description: "Whether the project's support staff may ask to join its users' desktops (each request is accepted by the user). Unset keeps the current value; new projects allow it.",
		}
		// Unset keeps the project's value (a new project is "full").
		attrs["experience"] = schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Description: "What the project's members get in the portal: \"full\", or \"workbench-only\" (members other than admins only see and launch their workbenches, from released versions). Unset keeps the current value; new projects are full.",
		}
		attrs["created_at"] = schema.StringAttribute{Computed: true, PlanModifiers: stable}
		attrs["updated_at"] = schema.StringAttribute{Computed: true}
	} else {
		attrs["project_id"] = schema.StringAttribute{Required: true, PlanModifiers: immutable}
		switch r.kind {
		case "assignment":
			attrs["user_id"] = schema.StringAttribute{Required: true, PlanModifiers: immutable}
			attrs["roles"] = schema.SetAttribute{Required: true, ElementType: types.StringType}
			attrs["user_email"] = schema.StringAttribute{Optional: true, Computed: true}
			attrs["user_display_name"] = schema.StringAttribute{Optional: true, Computed: true}
		case "group_assignment":
			attrs["group_id"] = schema.StringAttribute{Required: true, PlanModifiers: immutable}
			attrs["roles"] = schema.SetAttribute{Required: true, ElementType: types.StringType}
		case "client_assignment":
			attrs["client_id"] = schema.StringAttribute{Required: true, PlanModifiers: immutable}
			attrs["status"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("ACTIVE")}
		}
	}
	res.Schema = schema.Schema{Attributes: attrs}
}
func (r *accessResource) Configure(_ context.Context, req resource.ConfigureRequest, res *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok || strings.TrimSpace(data.ProjectAPIURL) == "" || data.ProjectAccess == nil {
		res.Diagnostics.AddError("Missing Projects API configuration", "Set projects_api_url or VEW_PROJECTS_API_URL to manage VEW project access.")
		return
	}
	r.client = data.ProjectAccess
}
func (r *accessResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, res *resource.ValidateConfigResponse) {
	m, ds := r.get(ctx, req.Config)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	if r.kind == "project" {
		if known(m.Name) && strings.TrimSpace(m.Name.ValueString()) == "" {
			res.Diagnostics.AddAttributeError(path.Root("name"), "Invalid project name", "name must be nonempty.")
		}
		if known(m.Experience) && !validExperience(m.Experience.ValueString()) {
			res.Diagnostics.AddAttributeError(path.Root("experience"), "Invalid project experience", "experience must be \"full\" or \"workbench-only\".")
		}
		return
	}
	if known(m.ProjectID) && strings.TrimSpace(m.ProjectID.ValueString()) == "" {
		res.Diagnostics.AddAttributeError(path.Root("project_id"), "Invalid project ID", "project_id must be nonempty.")
	}
	switch r.kind {
	case "assignment":
		if known(m.UserID) && !userID.MatchString(m.UserID.ValueString()) {
			res.Diagnostics.AddAttributeError(path.Root("user_id"), "Invalid user ID", "user_id must be an uppercase Entra user identifier.")
		}
	case "group_assignment":
		if known(m.GroupID) && !groupUUID.MatchString(m.GroupID.ValueString()) {
			res.Diagnostics.AddAttributeError(path.Root("group_id"), "Invalid group ID", "group_id must be an Entra UUID.")
		}
	case "client_assignment":
		if known(m.Status) && m.Status.ValueString() != "ACTIVE" {
			res.Diagnostics.AddAttributeError(path.Root("status"), "Invalid desired status", "status must be ACTIVE.")
		}
	}
	if r.kind == "assignment" || r.kind == "group_assignment" {
		if !m.Roles.IsNull() && !m.Roles.IsUnknown() && len(m.Roles.Elements()) == 0 {
			res.Diagnostics.AddAttributeError(path.Root("roles"), "Empty roles", "At least one project role is required.")
		}
	}
}
func known(v types.String) bool     { return !v.IsNull() && !v.IsUnknown() }
func validExperience(v string) bool { return v == "full" || v == "workbench-only" }
func parseImport(id string) (string, string, error) {
	p := strings.Split(id, "/")
	if len(p) != 2 || strings.TrimSpace(p[0]) != p[0] || strings.TrimSpace(p[1]) != p[1] || p[0] == "" || p[1] == "" {
		return "", "", errors.New("expected projectId/targetId")
	}
	return p[0], p[1], nil
}
func (r *accessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, res *resource.ImportStateResponse) {
	if r.kind == "project" {
		if strings.TrimSpace(req.ID) == "" || strings.Contains(req.ID, "/") {
			res.Diagnostics.AddError("Invalid project import ID", "Expected a project ID.")
			return
		}
		res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
		return
	}
	pid, target, e := parseImport(req.ID)
	if e != nil {
		res.Diagnostics.AddError("Invalid project assignment import ID", "Expected projectId/targetId.")
		return
	}
	field := "user_id"
	if r.kind == "group_assignment" {
		field = "group_id"
		target = strings.ToLower(target)
		if !groupUUID.MatchString(target) {
			res.Diagnostics.AddError("Invalid group import ID", "group ID must be a UUID.")
			return
		}
	}
	if r.kind == "client_assignment" {
		field = "client_id"
	}
	if r.kind == "assignment" && !userID.MatchString(target) {
		res.Diagnostics.AddError("Invalid user import ID", "user ID must be uppercase.")
		return
	}
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("project_id"), pid)...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root(field), target)...)
	res.Diagnostics.Append(res.State.SetAttribute(ctx, path.Root("id"), pid+"/"+target)...)
}
func uuid() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&0xf | 0x40
	b[8] = b[8]&0x3f | 0x80
	raw := hex.EncodeToString(b[:])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:], nil
}
func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}
func strPtr(v types.String) *string {
	if !known(v) {
		return nil
	}
	s := v.ValueString()
	return &s
}
func (r *accessResource) projectInput(m model) api.ProjectInput {
	return api.ProjectInput{
		Name:                 m.Name.ValueString(),
		Description:          strPtr(m.Description),
		IsActive:             m.IsActive.ValueBool(),
		RemoteSupportEnabled: boolPtr(m.RemoteSupport),
		Experience:           strPtr(m.Experience),
	}
}
func roles(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
	var out []string
	d := v.ElementsAs(ctx, &out, false)
	return out, d
}
func (r *accessResource) Create(ctx context.Context, req resource.CreateRequest, res *resource.CreateResponse) {
	m, ds := r.get(ctx, req.Plan)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	if r.kind == "project" {
		key, e := uuid()
		if e != nil {
			res.Diagnostics.AddError("Unable to create VEW project", "Could not generate a stable idempotency key.")
			return
		}
		if res.Private != nil {
			encoded, _ := json.Marshal(key)
			res.Diagnostics.Append(res.Private.SetKey(ctx, createKey, encoded)...)
			if res.Diagnostics.HasError() {
				return
			}
		}
		input := r.projectInput(m)
		id, e := r.client.CreateProject(ctx, input, key)
		if e != nil {
			var apiErr *vew.APIError
			if !errors.As(e, &apiErr) || apiErr.Status == 429 || apiErr.Status >= 500 || (apiErr.Status == 409 && apiErr.Problem.Code == "IDEMPOTENCY_REQUEST_IN_PROGRESS") {
				id, e = r.client.CreateProject(ctx, input, key)
			}
		}
		if e != nil {
			addError(&res.Diagnostics, r.kind, "create", e, "")
			return
		}
		m.ID = types.StringValue(id)
		m.CreatedAt, m.UpdatedAt = types.StringNull(), types.StringNull()
		res.Diagnostics.Append(r.set(context.WithoutCancel(ctx), &res.State, &m)...)
		if res.Diagnostics.HasError() {
			return
		}
		accepted := m
		if !r.refresh(ctx, &m, &res.State, &res.Diagnostics) {
			res.Diagnostics.Append(r.set(context.WithoutCancel(ctx), &res.State, &accepted)...)
			if !res.Diagnostics.HasError() {
				res.Diagnostics.AddError("Unable to read VEW project after create", "The project was accepted and its ID was retained in state. Refresh the resource to read canonical fields.")
			}
			return
		}
		if res.Private != nil {
			res.Diagnostics.Append(res.Private.SetKey(ctx, createKey, nil)...)
		}
		return
	}
	pid := m.ProjectID.ValueString()
	target := r.target(m)
	switch r.kind {
	case "assignment":
		rr, d := roles(ctx, m.Roles)
		res.Diagnostics.Append(d...)
		if res.Diagnostics.HasError() {
			return
		}
		e := r.client.CreateUser(ctx, pid, target, api.UserInput{Roles: rr, Email: strPtr(m.Email), DisplayName: strPtr(m.DisplayName)})
		if e != nil {
			addError(&res.Diagnostics, r.kind, "create", e, pid)
			return
		}
	case "group_assignment":
		rr, d := roles(ctx, m.Roles)
		res.Diagnostics.Append(d...)
		if res.Diagnostics.HasError() {
			return
		}
		e := r.client.PutGroup(ctx, pid, target, api.GroupInput{Roles: rr})
		if e != nil {
			addError(&res.Diagnostics, r.kind, "create", e, pid)
			return
		}
	case "client_assignment":
		if e := r.client.ActivateClient(ctx, pid, target); e != nil {
			addError(&res.Diagnostics, r.kind, "activate", e, pid)
			return
		}
	}
	m.ID = types.StringValue(pid + "/" + target)
	if r.kind == "assignment" {
		if m.Email.IsUnknown() {
			m.Email = types.StringNull()
		}
		if m.DisplayName.IsUnknown() {
			m.DisplayName = types.StringNull()
		}
	}
	res.Diagnostics.Append(r.set(context.WithoutCancel(ctx), &res.State, &m)...)
	if res.Diagnostics.HasError() {
		return
	}
	accepted := m
	if !r.refresh(ctx, &m, &res.State, &res.Diagnostics) {
		res.Diagnostics.Append(r.set(context.WithoutCancel(ctx), &res.State, &accepted)...)
		if !res.Diagnostics.HasError() {
			res.Diagnostics.AddError("Unable to read VEW assignment after create", "The assignment was accepted and its identity was retained in state. Refresh the resource to read canonical fields.")
		}
	}
}
func (r *accessResource) target(m model) string {
	switch r.kind {
	case "assignment":
		return m.UserID.ValueString()
	case "group_assignment":
		return strings.ToLower(m.GroupID.ValueString())
	default:
		return m.ClientID.ValueString()
	}
}
func (r *accessResource) Read(ctx context.Context, req resource.ReadRequest, res *resource.ReadResponse) {
	m, ds := r.get(ctx, req.State)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	r.refresh(ctx, &m, &res.State, &res.Diagnostics)
}
func (r *accessResource) refresh(ctx context.Context, m *model, state *tfsdk.State, d *diag.Diagnostics) bool {
	pid := m.ProjectID.ValueString()
	target := r.target(*m)
	switch r.kind {
	case "project":
		v, e := r.client.GetProject(ctx, m.ID.ValueString())
		if vew.IsNotFound(e) {
			state.RemoveResource(ctx)
			return false
		}
		if e != nil {
			addError(d, r.kind, "read", e, "")
			return false
		}
		if v.ID != m.ID.ValueString() {
			d.AddError("Invalid VEW project response", "The returned project identity differs from state.")
			return false
		}
		m.Name = types.StringValue(v.Name)
		if v.Description == nil {
			m.Description = types.StringNull()
		} else {
			m.Description = types.StringValue(*v.Description)
		}
		m.IsActive = types.BoolValue(v.IsActive)
		if v.RemoteSupportEnabled == nil {
			m.RemoteSupport = types.BoolNull()
		} else {
			m.RemoteSupport = types.BoolValue(*v.RemoteSupportEnabled)
		}
		if v.Experience == nil {
			m.Experience = types.StringNull()
		} else {
			m.Experience = types.StringValue(*v.Experience)
		}
		m.CreatedAt = types.StringValue(v.CreatedAt)
		m.UpdatedAt = types.StringValue(v.UpdatedAt)
	case "assignment":
		v, e := r.client.GetUser(ctx, pid, target)
		if vew.IsNotFound(e) {
			state.RemoveResource(ctx)
			return false
		}
		if e != nil {
			addError(d, r.kind, "read", e, pid)
			return false
		}
		if v.ProjectID != pid || v.UserID != target {
			d.AddError("Invalid VEW assignment response", "The returned assignment identity differs from state.")
			return false
		}
		roleSet, dummy := setRoles(ctx, v.Roles)
		m.Roles = roleSet
		d.Append(dummy...)
		if v.Email == nil {
			m.Email = types.StringNull()
		} else {
			m.Email = types.StringValue(*v.Email)
		}
		if v.DisplayName == nil {
			m.DisplayName = types.StringNull()
		} else {
			m.DisplayName = types.StringValue(*v.DisplayName)
		}
	case "group_assignment":
		v, e := r.client.GetGroup(ctx, pid, target)
		if vew.IsNotFound(e) {
			state.RemoveResource(ctx)
			return false
		}
		if e != nil {
			addError(d, r.kind, "read", e, pid)
			return false
		}
		if v.ProjectID != pid || strings.ToLower(v.GroupID) != target {
			d.AddError("Invalid VEW group response", "The returned group assignment identity differs from state.")
			return false
		}
		m.GroupID = types.StringValue(target)
		roleSet, dummy := setRoles(ctx, v.Roles)
		m.Roles = roleSet
		d.Append(dummy...)
	case "client_assignment":
		v, e := r.client.GetClient(ctx, pid, target)
		if vew.IsNotFound(e) {
			state.RemoveResource(ctx)
			return false
		}
		if e != nil {
			addError(d, r.kind, "read", e, pid)
			return false
		}
		if v.ProjectID != pid || v.ClientID != target {
			d.AddError("Invalid VEW client response", "The returned client assignment identity differs from state.")
			return false
		}
		m.Status = types.StringValue(v.Status)
	}
	if d.HasError() {
		return false
	}
	d.Append(r.set(ctx, state, m)...)
	return !d.HasError()
}
func setRoles(ctx context.Context, rr []string) (types.Set, diag.Diagnostics) {
	return types.SetValueFrom(ctx, types.StringType, rr)
}
func (r *accessResource) Update(ctx context.Context, req resource.UpdateRequest, res *resource.UpdateResponse) {
	m, ds := r.get(ctx, req.Plan)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	previous, ds := r.get(ctx, req.State)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	m.ID = previous.ID
	pid := m.ProjectID.ValueString()
	target := r.target(m)
	var e error
	switch r.kind {
	case "project":
		e = r.client.UpdateProject(ctx, m.ID.ValueString(), r.projectInput(m))
	case "assignment":
		var rr []string
		var d diag.Diagnostics
		rr, d = roles(ctx, m.Roles)
		res.Diagnostics.Append(d...)
		if res.Diagnostics.HasError() {
			return
		}
		e = r.client.UpdateUser(ctx, pid, target, api.UserInput{Roles: rr, Email: strPtr(m.Email), DisplayName: strPtr(m.DisplayName)})
	case "group_assignment":
		var rr []string
		var d diag.Diagnostics
		rr, d = roles(ctx, m.Roles)
		res.Diagnostics.Append(d...)
		if res.Diagnostics.HasError() {
			return
		}
		e = r.client.PutGroup(ctx, pid, target, api.GroupInput{Roles: rr})
	case "client_assignment":
		e = r.client.ActivateClient(ctx, pid, target)
	}
	if e != nil {
		addError(&res.Diagnostics, r.kind, "update", e, pid)
		return
	}
	r.refresh(ctx, &m, &res.State, &res.Diagnostics)
}
func (r *accessResource) Delete(ctx context.Context, req resource.DeleteRequest, res *resource.DeleteResponse) {
	m, ds := r.get(ctx, req.State)
	res.Diagnostics.Append(ds...)
	if res.Diagnostics.HasError() {
		return
	}
	pid := m.ProjectID.ValueString()
	target := r.target(m)
	var e error
	switch r.kind {
	case "project":
		e = r.client.DeactivateProject(ctx, m.ID.ValueString())
	case "assignment":
		e = r.client.DeleteUser(ctx, pid, target)
	case "group_assignment":
		e = r.client.DeleteGroup(ctx, pid, target)
	case "client_assignment":
		e = r.client.RevokeClient(ctx, pid, target)
	}
	if e != nil && !vew.IsNotFound(e) {
		addError(&res.Diagnostics, r.kind, "delete", e, pid)
	}
}
func addError(d *diag.Diagnostics, kind, op string, err error, pid string) {
	_ = pid
	title := fmt.Sprintf("Unable to %s VEW project %s", op, kind)
	var apiErr *vew.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == 401 || apiErr.Status == 403 {
			scope := map[string]string{"project": "program", "assignment": "assignment", "group_assignment": "group_assignment", "client_assignment": "client_assignment"}[kind]
			access := "write"
			if op == "read" {
				access = "read"
			}
			suffix := " and an active client assignment to the project"
			if kind == "project" && op == "create" {
				suffix = ""
			}
			if kind == "client_assignment" && access == "write" {
				suffix = ". For an orphaned project, use a separately configured platform recovery client with clients/projects/client_assignment.bootstrap"
			}
			d.AddError(title, "The service client needs Projects scope clients/projects/"+scope+"."+access+suffix+". Check the OAuth grant and project access.")
			return
		}
		d.AddError(title, fmt.Sprintf("The Projects API returned HTTP status %d. Check the request and retry.", apiErr.Status))
		return
	}
	d.AddError(title, "The Projects API request could not be completed. Check connectivity and retry.")
}
