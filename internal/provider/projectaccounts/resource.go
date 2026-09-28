package projectaccounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_               resource.Resource                   = (*projectAccountResource)(nil)
	_               resource.ResourceWithConfigure      = (*projectAccountResource)(nil)
	_               resource.ResourceWithValidateConfig = (*projectAccountResource)(nil)
	_               resource.ResourceWithImportState    = (*projectAccountResource)(nil)
	awsRegion                                           = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]$`)
	safeCorrelation                                     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

const createKeyName = "project_account_create_idempotency_key"

type projectAccountResource struct {
	client         vewaccounts.API
	waiter         vew.Waiter
	projectsAPIURL string
}

func NewProjectAccountResource() resource.Resource { return &projectAccountResource{} }

func (r *projectAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_account"
}

func (r *projectAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = resourceSchema()
}

func (r *projectAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(providerdata.Data)
	if !ok {
		resp.Diagnostics.AddError("Missing Project Account API", "Expected provider data to include a ProjectAccounts API and Waiter.")
		return
	}
	if strings.TrimSpace(data.ProjectAPIURL) == "" {
		resp.Diagnostics.AddError("Missing Projects API URL", "Set projects_api_url or VEW_PROJECTS_API_URL before managing vew_project_account resources.")
		return
	}
	if data.ProjectAccounts == nil || data.Waiter == nil {
		resp.Diagnostics.AddError("Missing Project Account API", "Expected provider data to include a ProjectAccounts API and Waiter.")
		return
	}
	r.client, r.waiter, r.projectsAPIURL = data.ProjectAccounts, data.Waiter, data.ProjectAPIURL
}

func (r *projectAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config model
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateModel(ctx, config)...)
}

func (r *projectAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		resp.Diagnostics.AddError("Invalid project account import ID", "Expected project_id/account_id.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func (r *projectAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateBeforeRequest(ctx, m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ready(&resp.Diagnostics); err != nil {
		return
	}
	timeout := operationTimeout(ctx, m.Timeouts, "create")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var privateSetter interface {
		SetKey(context.Context, string, []byte) diag.Diagnostics
	}
	if resp.Private != nil {
		privateSetter = resp.Private
	}
	key, err := persistedIdempotencyKey(ctx, privateSetter, &resp.Diagnostics)
	if err != nil {
		return
	}
	action, err := r.client.CreateAccount(ctx, m.ProjectID.ValueString(), vewaccounts.AccountInput{
		AWSAccountID: m.AWSAccountID.ValueString(), AccountType: m.AccountType.ValueString(), Name: m.Name.ValueString(),
		Description: m.Description.ValueString(), TechnologyID: m.TechnologyID.ValueString(), Stage: m.Stage.ValueString(), Region: m.Region.ValueString(),
	}, key)
	if err != nil {
		addAccountError(&resp.Diagnostics, "create", err, "")
		return
	}
	m.ID = types.StringValue(action.ID)
	m.Status = types.StringValue("OnBoarding")
	m.LastOnboardingResult, m.LastOnboardingError, m.CreatedAt, m.UpdatedAt = types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull()
	stateCtx := context.WithoutCancel(ctx)
	resp.Diagnostics.Append(resp.State.Set(stateCtx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err = r.waitFor(ctx, &m, timeout, action.RetryAfter)
	resp.Diagnostics.Append(resp.State.Set(stateCtx, &m)...)
	if err != nil {
		addAccountError(&resp.Diagnostics, "create", err, m.ID.ValueString())
	}
}

func (r *projectAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m model
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ready(&resp.Diagnostics); err != nil {
		return
	}
	remote, err := r.client.GetAccount(ctx, m.ProjectID.ValueString(), m.ID.ValueString())
	if vew.IsNotFound(err) || (err == nil && removedStatus(remote.Status)) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAccountError(&resp.Diagnostics, "read", err, m.ID.ValueString())
		return
	}
	prior := m
	if err := setState(ctx, &m, remote); err != nil {
		addAccountError(&resp.Diagnostics, "read", err, m.ID.ValueString())
		return
	}
	if mutableKnown(prior) && (pendingStatus(remote.Status) || activeFailed(m)) {
		preserveMutable(&m, prior)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *projectAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, plan model
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateBeforeRequest(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ready(&resp.Diagnostics); err != nil {
		return
	}
	prior := m
	m.Timeouts = plan.Timeouts
	timeout := operationTimeout(ctx, plan.Timeouts, "update")
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stateCtx := context.WithoutCancel(ctx)
	completed := false
	removed := false
	defer func() {
		if removed {
			resp.State.RemoveResource(stateCtx)
			return
		}
		if !completed {
			preserveMutable(&m, prior)
		}
		resp.Diagnostics.Append(resp.State.Set(stateCtx, &m)...)
	}()
	remote, err := r.client.GetAccount(ctx, m.ProjectID.ValueString(), m.ID.ValueString())
	if vew.IsNotFound(err) || (err == nil && removedStatus(remote.Status)) {
		removed = true
		return
	}
	if err != nil {
		addAccountError(&resp.Diagnostics, "update", err, m.ID.ValueString())
		return
	}
	if err := setState(stateCtx, &m, remote); err != nil {
		addAccountError(&resp.Diagnostics, "update", err, m.ID.ValueString())
		return
	}
	if pendingStatus(remote.Status) {
		if err := r.waitFor(ctx, &m, timeout, remote.RetryAfter); err != nil {
			addAccountError(&resp.Diagnostics, "update", err, m.ID.ValueString())
			return
		}
	}
	if activeFailed(m) && sameDesired(m, plan) {
		// A later apply can retry the same desired configuration after a failed operation.
	} else if successful(m) && sameDesired(m, plan) {
		copyDesired(&m, plan)
		completed = true
		return
	}
	if !successful(m) && !activeFailed(m) && normalized(m.Status.ValueString()) != "failed" {
		addAccountError(&resp.Diagnostics, "update", errors.New("account is not in an updateable status"), m.ID.ValueString())
		return
	}
	action, err := r.client.UpdateAccount(ctx, m.ProjectID.ValueString(), m.ID.ValueString(), vewaccounts.UpdateAccountInput{
		AccountType: plan.AccountType.ValueString(), Name: plan.Name.ValueString(), Description: plan.Description.ValueString(),
		TechnologyID: plan.TechnologyID.ValueString(), Stage: plan.Stage.ValueString(), Region: plan.Region.ValueString(),
	})
	if err != nil {
		addAccountError(&resp.Diagnostics, "update", err, m.ID.ValueString())
		return
	}
	copyDesired(&m, plan)
	m.Status = types.StringValue("ReOnBoarding")
	err = r.waitFor(ctx, &m, timeout, action.RetryAfter)
	if err != nil {
		// Retain immutable identity and the latest server view so a later apply can recover the operation.
		addAccountError(&resp.Diagnostics, "update", err, m.ID.ValueString())
		return
	}
	completed = true
}

func (r *projectAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m model
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.ready(&resp.Diagnostics); err != nil {
		return
	}
	remote, err := r.client.GetAccount(ctx, m.ProjectID.ValueString(), m.ID.ValueString())
	if vew.IsNotFound(err) {
		return
	}
	if err != nil {
		addAccountError(&resp.Diagnostics, "deactivate", err, m.ID.ValueString())
		return
	}
	if removedStatus(remote.Status) {
		if normalized(remote.Status) == "inactive" || normalized(remote.Status) == "archived" {
			addRetainedTechnologyWarning(&resp.Diagnostics)
		}
		return
	}
	if err := r.client.DeactivateAccount(ctx, m.ProjectID.ValueString(), m.ID.ValueString()); err != nil {
		addAccountError(&resp.Diagnostics, "deactivate", err, m.ID.ValueString())
		return
	}
	addRetainedTechnologyWarning(&resp.Diagnostics)
}

func addRetainedTechnologyWarning(d *diag.Diagnostics) {
	d.AddWarning("Project account record is retained in VEW", "The account assignment is inactive, but VEW retains its record and technology reference. That reference may prevent technology deletion; resolve it in VEW before deleting the technology. Deactivation does not offboard AWS infrastructure.")
}

func (r *projectAccountResource) ready(d *diag.Diagnostics) error {
	if strings.TrimSpace(r.projectsAPIURL) == "" {
		d.AddError("Missing Projects API URL", "Set projects_api_url or VEW_PROJECTS_API_URL before managing vew_project_account resources.")
		return errors.New("missing projects api url")
	}
	if r.client == nil || r.waiter == nil {
		d.AddError("Missing Project Account API", "Configure the provider with a Projects account API before using vew_project_account.")
		return errors.New("missing project account API")
	}
	return nil
}

func persistedIdempotencyKey(ctx context.Context, next interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}, d *diag.Diagnostics) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		d.AddError("Unable to create project account", "Could not generate a safe idempotency key.")
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	key := []byte(fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]), hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16])))
	if next != nil {
		encodedKey, err := json.Marshal(string(key))
		if err != nil {
			d.AddError("Unable to create project account", "Could not persist a safe idempotency key.")
			return "", err
		}
		d.Append(next.SetKey(ctx, createKeyName, encodedKey)...)
		if d.HasError() {
			return "", errors.New("could not persist project account idempotency key")
		}
	}
	return string(key), nil
}

func (r *projectAccountResource) waitFor(ctx context.Context, m *model, timeout, initial time.Duration) error {
	return r.waiter.Until(ctx, timeout, initial, func(pollCtx context.Context) (vew.PollResult, error) {
		remote, err := r.client.GetAccount(pollCtx, m.ProjectID.ValueString(), m.ID.ValueString())
		if err != nil {
			return vew.PollResult{}, err
		}
		if err := setState(context.WithoutCancel(pollCtx), m, remote); err != nil {
			return vew.PollResult{}, err
		}
		return vew.PollResult{Status: remote.Status + "/" + remote.LastOnboardingResult, RetryAfter: remote.RetryAfter}, nil
	}, settledAccountStatus)
}

func setState(ctx context.Context, m *model, a vewaccounts.Account) error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("project account response missing account ID")
	}
	if !m.ProjectID.IsNull() && !m.ProjectID.IsUnknown() && a.ProjectID != "" && a.ProjectID != m.ProjectID.ValueString() {
		return errors.New("project account response project ID differs from requested project")
	}
	if !m.ID.IsNull() && !m.ID.IsUnknown() && a.ID != m.ID.ValueString() {
		return errors.New("project account response ID differs from requested account")
	}
	if !m.AWSAccountID.IsNull() && !m.AWSAccountID.IsUnknown() && a.AWSAccountID != "" && a.AWSAccountID != m.AWSAccountID.ValueString() {
		return errors.New("project account response AWS account ID differs from requested account")
	}
	m.ID, m.ProjectID, m.AWSAccountID = types.StringValue(a.ID), types.StringValue(first(a.ProjectID, m.ProjectID.ValueString())), types.StringValue(first(a.AWSAccountID, m.AWSAccountID.ValueString()))
	m.Name, m.Description, m.AccountType = types.StringValue(a.Name), types.StringValue(a.Description), types.StringValue(a.AccountType)
	m.TechnologyID, m.Stage, m.Region = types.StringValue(a.TechnologyID), types.StringValue(a.Stage), types.StringValue(a.Region)
	m.Status = types.StringValue(a.Status)
	m.LastOnboardingResult = types.StringValue(a.LastOnboardingResult)
	m.LastOnboardingError = sanitizedOnboardingError(a.LastOnboardingResult, a.LastOnboardingErrorMessage)
	m.CreatedAt, m.UpdatedAt = dateValue(a.CreatedAt), dateValue(a.UpdatedAt)
	_ = ctx
	return nil
}

func dateValue(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339Nano))
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func normalized(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "_", ""), " ", ""))
}
func pendingStatus(s string) bool {
	n := normalized(s)
	return n == "onboarding" || n == "reonboarding"
}
func removedStatus(s string) bool { n := normalized(s); return n == "inactive" || n == "archived" }
func successful(m model) bool {
	return normalized(m.Status.ValueString()) == "active" && normalized(m.LastOnboardingResult.ValueString()) == "succeeded"
}
func activeFailed(m model) bool {
	return normalized(m.Status.ValueString()) == "active" && normalized(m.LastOnboardingResult.ValueString()) == "failed"
}
func settledAccountStatus(s string) (bool, error) {
	parts := strings.SplitN(s, "/", 2)
	status, result := normalized(parts[0]), ""
	if len(parts) > 1 {
		result = normalized(parts[1])
	}
	if status == "onboarding" || status == "reonboarding" {
		return false, nil
	}
	if status == "active" && result == "succeeded" {
		return true, nil
	}
	if status == "failed" || (status == "active" && result == "failed") {
		return false, &vew.TerminalStatusError{Status: safeStatus(s)}
	}
	if status == "inactive" || status == "archived" {
		return false, &vew.TerminalStatusError{Status: safeStatus(s)}
	}
	return false, errors.New("unrecognized project account status")
}
func safeStatus(s string) string {
	if safeCorrelation.MatchString(s) {
		return s
	}
	return "terminal"
}
func sameDesired(a, b model) bool {
	return a.AccountType.Equal(b.AccountType) && a.Name.Equal(b.Name) && a.Description.Equal(b.Description) && a.TechnologyID.Equal(b.TechnologyID) && a.Stage.Equal(b.Stage) && a.Region.Equal(b.Region)
}
func copyDesired(a *model, b model) {
	a.AccountType, a.Name, a.Description, a.TechnologyID, a.Stage, a.Region = b.AccountType, b.Name, b.Description, b.TechnologyID, b.Stage, b.Region
}

func preserveMutable(target *model, previous model) {
	target.AccountType, target.Name, target.Description = previous.AccountType, previous.Name, previous.Description
	target.TechnologyID, target.Stage, target.Region = previous.TechnologyID, previous.Stage, previous.Region
}

func mutableKnown(m model) bool {
	for _, value := range []types.String{m.AccountType, m.Name, m.Description, m.TechnologyID, m.Stage, m.Region} {
		if value.IsNull() || value.IsUnknown() {
			return false
		}
	}
	return true
}

// VEW error messages may contain workflow details or account parameters. Expose
// a fixed safe summary only when a failure result is present.
func sanitizedOnboardingError(result, _ string) types.String {
	if normalized(result) == "failed" {
		return types.StringValue("Onboarding failed; inspect the VEW project account workflow for details.")
	}
	return types.StringNull()
}
