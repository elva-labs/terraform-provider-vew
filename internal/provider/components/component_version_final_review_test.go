package components

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestComponentVersionUnavailableDefinitionKeepsLifecycleRecoverable(t *testing.T) {
	for _, definition := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		t.Run(string(definition), func(t *testing.T) {
			t.Run("create publishes later", func(t *testing.T) {
				r, fake, _ := versionHarness(t)
				pending := versionFixture("CREATING")
				pending.Definition = definition
				fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: pending}, testhelpers.VersionResponse{Version: versionFixture("VALIDATED")})
				response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
				if response.Diagnostics.HasError() {
					t.Fatal(response.Diagnostics)
				}
				if decodeVersionState(t, response.State).Status.ValueString() != "VALIDATED" {
					t.Fatal("create did not finish validation")
				}
				assertVersionMethods(t, fake, "POST", "GET", "GET")
			})
			for _, status := range []string{"CREATING", "FAILED"} {
				t.Run("read "+status, func(t *testing.T) {
					r, fake, _ := versionHarness(t)
					version := versionFixture(status)
					version.Definition = definition
					fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: version})
					model := validComponentVersionModel(t)
					response := readVersion(t, r, model)
					if response.Diagnostics.HasError() {
						t.Fatal(response.Diagnostics)
					}
					got := decodeVersionState(t, response.State)
					if got.Status.ValueString() != status || !got.DefinitionJSON.Equal(model.DefinitionJSON) {
						t.Fatal("read lost status or configured definition")
					}
				})
			}
			t.Run("early failure can retire", func(t *testing.T) {
				r, fake, _ := versionHarness(t)
				failed := versionFixture("FAILED")
				failed.Definition = definition
				fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: failed})
				created := createVersion(t, context.Background(), r, validComponentVersionModel(t))
				if !diagnosticsContain(created.Diagnostics, "FAILED") {
					t.Fatalf("missing lifecycle failure: %v", created.Diagnostics)
				}
				retired := failed
				retired.Status = "RETIRED"
				fake.QueueVersionActionReads("DELETE", "project", "component", "version", testhelpers.VersionResponse{Version: retired})
				deleted := deleteVersion(t, r, decodeVersionState(t, created.State))
				if deleted.Diagnostics.HasError() || !deleted.State.Raw.IsNull() {
					t.Fatalf("delete failed: %v", deleted.Diagnostics)
				}
				assertVersionMethods(t, fake, "POST", "GET", "GET", "DELETE", "GET")
			})
		})
	}
}

type deadlineVersionAPI struct {
	vewcomponents.ComponentVersionAPI
	createDeadline time.Time
	getDeadlines   []time.Time
}

func (a *deadlineVersionAPI) CreateComponentVersion(ctx context.Context, project, component string, input vewcomponents.CreateComponentVersionInput) (vewcomponents.ActionResult, error) {
	a.createDeadline, _ = ctx.Deadline()
	return a.ComponentVersionAPI.CreateComponentVersion(ctx, project, component, input)
}

func (a *deadlineVersionAPI) GetComponentVersion(ctx context.Context, project, component, version string) (vewcomponents.ComponentVersion, error) {
	deadline, _ := ctx.Deadline()
	a.getDeadlines = append(a.getDeadlines, deadline)
	return a.ComponentVersionAPI.GetComponentVersion(ctx, project, component, version)
}

func TestComponentVersionCreateDeadlineIncludesPostAndPolling(t *testing.T) {
	r, _, waiter := versionHarness(t, "CREATING", "VALIDATED")
	api := &deadlineVersionAPI{ComponentVersionAPI: r.client}
	r.client = api
	model := validComponentVersionModel(t)
	model.Timeouts = timeoutsValue(t, "2m", "", "")
	start := time.Now()
	response := createVersion(t, context.Background(), r, model)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if api.createDeadline.Before(start.Add(2*time.Minute)) || api.createDeadline.After(time.Now().Add(2*time.Minute)) {
		t.Fatalf("POST deadline %v does not enforce the create budget", api.createDeadline)
	}
	if len(waiter.deadlines) != 1 || !waiter.deadlines[0].Equal(api.createDeadline) {
		t.Fatal("polling received a different deadline")
	}
	for _, deadline := range api.getDeadlines {
		if !deadline.Equal(api.createDeadline) {
			t.Fatal("GET extended the operation deadline")
		}
	}
}

func TestComponentVersionPollCarriesServerRetryAfter(t *testing.T) {
	r, fake, waiter := versionHarness(t)
	fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: versionFixture("CREATING"), RetryAfter: "7"}, testhelpers.VersionResponse{Version: versionFixture("VALIDATED")})
	response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if len(waiter.pollDelays) != 2 || waiter.pollDelays[0] != 7*time.Second || waiter.pollDelays[1] != 0 {
		t.Fatalf("poll delays = %v", waiter.pollDelays)
	}
}

func TestComponentVersionRejectsEmptyOptionalStringsBeforeMutation(t *testing.T) {
	for _, name := range []string{"notes", "license_dashboard"} {
		for _, value := range []types.String{types.StringValue(""), types.StringNull(), types.StringUnknown(), types.StringValue("value")} {
			r := NewComponentVersionResource().(*componentVersionResource)
			model := validComponentVersionModel(t)
			if name == "notes" {
				model.Notes = value
			} else {
				model.LicenseDashboard = value
			}
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: componentVersionConfig(t, r, model)}, &response)
			wantError := !value.IsNull() && !value.IsUnknown() && value.ValueString() == ""
			if response.Diagnostics.HasError() != wantError {
				t.Fatalf("%s value %v diagnostics = %v", name, value, response.Diagnostics)
			}
			if wantError {
				attribute, ok := response.Diagnostics[0].(diag.DiagnosticWithPath)
				if !ok || !attribute.Path().Equal(path.Root(name)) {
					t.Fatal("missing attribute diagnostic")
				}
			}
		}
	}
}

func TestComponentVersionErrorsRetainSafeCorrelationAndClassifyTimeout(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"api", &vew.APIError{Status: 403, Problem: vew.Problem{Code: "ACCESS_DENIED", RequestID: "request-123", Detail: "secret definition body", Title: "bearer token"}}, "ACCESS_DENIED"},
		{"deadline", context.DeadlineExceeded, "Timed out"},
		{"cancel", context.Canceled, "cancelled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var diagnostics diag.Diagnostics
			addVersionError(&diagnostics, "update", test.err, "")
			if !diagnosticsContain(diagnostics, test.want) {
				t.Fatalf("diagnostics missing %q: %v", test.want, diagnostics)
			}
			if test.name == "api" && !diagnosticsContain(diagnostics, "request-123") {
				t.Fatal("missing request correlation")
			}
			for _, unsafe := range []string{"secret", "definition body", "bearer token"} {
				if diagnosticsContain(diagnostics, unsafe) {
					t.Fatal("unsafe diagnostic")
				}
			}
		})
	}
}

func TestComponentVersionSemanticNoOpIsStateOnlyForEveryStatus(t *testing.T) {
	for _, status := range []string{"CREATING", "CREATED", "TESTING", "UPDATING", "VALIDATED", "FAILED", "RELEASED"} {
		for _, release := range []types.String{types.StringNull(), types.StringValue("MAJOR")} {
			t.Run(status+release.String(), func(t *testing.T) {
				r, fake, _ := versionHarness(t, status)
				state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
				state.ReleaseType, state.Status = release, types.StringValue(status)
				plan.DefinitionJSON = types.StringValue(`{ "phases" : [] }`)
				plan.Timeouts = timeoutsValue(t, "", "3m", "")
				response := updateVersion(t, r, state, plan)
				if response.Diagnostics.HasError() {
					t.Fatal(response.Diagnostics)
				}
				assertVersionMethods(t, fake, "GET")
				got := decodeVersionState(t, response.State)
				if !got.DefinitionJSON.Equal(plan.DefinitionJSON) || !got.Timeouts.Equal(plan.Timeouts) || !got.ReleaseType.Equal(plan.ReleaseType) {
					t.Fatal("state-only change lost the configured representation")
				}
			})
		}
	}
}

func TestComponentVersionFailedUpdateRetainsPriorMutableConfiguration(t *testing.T) {
	r, fake, _ := versionHarness(t, "VALIDATED")
	failed := versionFixture("FAILED")
	failed.Description, failed.SoftwareVendor, failed.SoftwareVersion = "new description", "new vendor", "2"
	failed.Definition = json.RawMessage(`{"phases":[],"attempted":true}`)
	failed.Notes, failed.LicenseDashboard = new("new notes"), new("https://example.invalid/new")
	fake.QueueVersionActionReads("PUT", "project", "component", "version", testhelpers.VersionResponse{Version: failed})
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	plan.Description, plan.SoftwareVendor, plan.SoftwareVersion = types.StringValue(failed.Description), types.StringValue(failed.SoftwareVendor), types.StringValue(failed.SoftwareVersion)
	plan.DefinitionJSON, plan.Notes, plan.LicenseDashboard = types.StringValue(string(failed.Definition)), types.StringPointerValue(failed.Notes), types.StringPointerValue(failed.LicenseDashboard)
	response := updateVersion(t, r, state, plan)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected failed update")
	}
	got := decodeVersionState(t, response.State)
	if got.Status.ValueString() != "FAILED" || !equivalentVersionConfiguration(context.Background(), got, state) {
		t.Fatal("failed update overwrote prior mutable state and lost retry intent")
	}
}

func TestComponentVersionFailureDiagnosticsIncludeKnownID(t *testing.T) {
	r, _, _ := versionHarness(t, "FAILED")
	response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
	if !diagnosticsContain(response.Diagnostics, "Resource ID: version") {
		t.Fatalf("missing known resource ID: %v", response.Diagnostics)
	}
}

func TestComponentVersionUnavailableDefinitionRetainsLastObservation(t *testing.T) {
	r, fake, _ := versionHarness(t)
	observed, failed := versionFixture("TESTING"), versionFixture("FAILED")
	observed.Definition, failed.Definition = json.RawMessage(`{"last":"published"}`), json.RawMessage(`null`)
	fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: observed}, testhelpers.VersionResponse{Version: failed})
	response := createVersion(t, context.Background(), r, validComponentVersionModel(t))
	got := decodeVersionState(t, response.State)
	if !diagnosticsContain(response.Diagnostics, "FAILED") || got.DefinitionJSON.ValueString() != `{"last":"published"}` {
		t.Fatal("unavailable definition lost the last successful observation")
	}
}

func TestComponentVersionUpdateSettlesEarlyFailureThenRetries(t *testing.T) {
	r, fake, _ := versionHarness(t)
	pending, failed := versionFixture("UPDATING"), versionFixture("FAILED")
	pending.Definition, failed.Definition = nil, nil
	fake.QueueVersionReads("project", "component", "version", testhelpers.VersionResponse{Version: pending}, testhelpers.VersionResponse{Version: failed}, testhelpers.VersionResponse{Version: versionFixture("VALIDATED")})
	state, plan := validComponentVersionModel(t), validComponentVersionModel(t)
	plan.Description = types.StringValue("retry")
	response := updateVersion(t, r, state, plan)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	assertVersionMethods(t, fake, "GET", "GET", "PUT", "GET")
}

func TestComponentVersionDiagnosticsOmitUnsafeCorrelationFields(t *testing.T) {
	var diagnostics diag.Diagnostics
	addVersionError(&diagnostics, "read", &vew.APIError{Status: 400, Problem: vew.Problem{Code: "unsafe-code\nsecret", RequestID: "unsafe-request bearer", Detail: "body secret", Title: "unsafe-title"}}, "unsafe-resource\nsecret")
	if !diagnosticsContain(diagnostics, "400") {
		t.Fatal("HTTP status lost")
	}
	for _, unsafe := range []string{"unsafe", "secret", "body", "bearer"} {
		if diagnosticsContain(diagnostics, unsafe) {
			t.Fatalf("unsafe correlation leaked: %v", diagnostics)
		}
	}
}
