package components_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/provider/testhelpers"
	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewcomponents "github.com/elva-labs/terraform-provider-vew/internal/vew/components"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type fastVersionProvider struct{ provider.Provider }

func (p *fastVersionProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	p.Provider.Configure(ctx, request, response)
	if response.Diagnostics.HasError() {
		return
	}
	data := response.ResourceData.(providerdata.Data)
	data.Waiter = protocolVersionWaiter{}
	response.ResourceData, response.DataSourceData = data, data
}

type protocolVersionWaiter struct{}

func (protocolVersionWaiter) Until(ctx context.Context, _, _ time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	for n := 0; n < 20; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := read(ctx)
		if err != nil {
			return err
		}
		if done, err := evaluate(result.Status); done || err != nil {
			return err
		}
	}
	return &vew.TimeoutError{}
}

func TestComponentVersionLifecycleProtocolPreservesEquivalentConfiguration(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	remote := vewcomponents.ComponentVersion{
		ID: "version", ComponentID: "cmp-123", Description: "created by provider test", Name: "1.0.0", SoftwareVendor: "VEW", SoftwareVersion: "1.0",
		Definition: json.RawMessage(`{"phases":[{"steps":[{"maxAttempts":1,"name":"install","onFailure":"Abort","timeoutSeconds":7200}]}]}`),
		Dependencies: []vewcomponents.Dependency{
			{ComponentID: "a", ComponentName: "A", VersionID: "va", VersionName: "1", Type: "HELPER", Order: 1},
			{ComponentID: "b", ComponentName: "B", VersionID: "vb", VersionName: "2", Type: "HELPER", Order: 2},
		},
		CreatedAt: "2026-09-22T00:00:00Z", CreatedBy: "creator", UpdatedAt: "2026-09-22T00:00:00Z", UpdatedBy: "updater",
	}
	queue := func(method, description string, statuses ...string) {
		responses := make([]testhelpers.VersionResponse, len(statuses))
		for i, status := range statuses {
			version := remote
			version.Status, version.Description = status, description
			responses[i] = testhelpers.VersionResponse{Version: version}
		}
		fake.QueueVersionActionReads(method, "prog-73488", "cmp-123", "version", responses...)
	}
	queue("POST", "created by provider test", "CREATING", "CREATED", "TESTING", "VALIDATED")
	queue("PUT", "updated by provider test", "UPDATING", "CREATED", "TESTING", "VALIDATED")
	queue("DELETE", "updated by provider test", "UPDATING", "RETIRED")
	config := func(description string) string {
		return fake.ProviderConfig() + fmt.Sprintf(`
resource "vew_component_version" "test" {
  project_id = "prog-73488"
  component_id = "cmp-123"
  description = %q
  release_type = "PATCH"
  definition_json = <<-JSON
{ "phases": [ { "steps": [ { "name": "install" } ] } ] }
JSON
  software_vendor = "VEW"
  software_version = "1.0"
  dependencies = [
    { component_id = "b", component_name = "B", version_id = "vb", version_name = "2", order = 2 },
    { component_id = "a", component_name = "A", version_id = "va", version_name = "1", order = 1 }
  ]
}
`, description)
	}
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(&fastVersionProvider{Provider: rootprovider.New("test")()})},
		CheckDestroy: func(*terraform.State) error {
			counts := map[string]int{}
			for _, request := range fake.VersionRequests() {
				counts[request.Method]++
			}
			if counts["POST"] != 1 || counts["PUT"] != 1 || counts["DELETE"] != 1 {
				return fmt.Errorf("action counts = %v", counts)
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: config("created by provider test"), Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttr("vew_component_version.test", "status", "VALIDATED"), testresource.TestCheckResourceAttr("vew_component_version.test", "dependencies.0.component_id", "b"))},
			{Config: config("created by provider test"), PlanOnly: true, ExpectNonEmptyPlan: false},
			{Config: config("updated by provider test"), Check: testresource.TestCheckResourceAttr("vew_component_version.test", "description", "updated by provider test")},
			{Config: config("updated by provider test"), PlanOnly: true, ExpectNonEmptyPlan: false},
			{ResourceName: "vew_component_version.test", ImportState: true, ImportStateId: "prog-73488/cmp-123/version",
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].ID != "version" || states[0].Attributes["release_type"] != "" {
						return fmt.Errorf("import did not retain identity with unknown release type")
					}
					return nil
				}},
		},
	})
}

func TestComponentVersionImportAdoptionProtocolDoesNotPut(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	remote := vewcomponents.ComponentVersion{ID: "version", ComponentID: "cmp-123", Description: "created by provider test", Name: "1.0.0", SoftwareVendor: "VEW", SoftwareVersion: "1.0", Definition: json.RawMessage(`{"phases":[]}`), Dependencies: []vewcomponents.Dependency{}, Status: "VALIDATED"}
	fake.QueueVersionReads("prog-73488", "cmp-123", "version", testhelpers.VersionResponse{Version: remote})
	remote.Status = "RETIRED"
	fake.QueueVersionActionReads("DELETE", "prog-73488", "cmp-123", "version", testhelpers.VersionResponse{Version: remote})
	config := fake.ProviderConfig() + strings.Replace(componentVersionResourceConfig(), `jsonencode({ phases = [] })`, fmt.Sprintf("%q", `{ "phases": [] }`), 1)
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(&fastVersionProvider{Provider: rootprovider.New("test")()})},
		CheckDestroy: func(*terraform.State) error {
			for _, request := range fake.VersionRequests() {
				if request.Method == "POST" || request.Method == "PUT" {
					return fmt.Errorf("adoption issued %s", request.Method)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: config, ResourceName: "vew_component_version.test", ImportState: true, ImportStateId: "prog-73488/cmp-123/version", ImportStatePersist: true},
			{Config: config, Check: testresource.TestCheckResourceAttr("vew_component_version.test", "release_type", "PATCH")},
			{Config: config, PlanOnly: true, ExpectNonEmptyPlan: false},
		},
	})
}

func TestComponentVersionResourceCreationPlan(t *testing.T) {
	fake := testhelpers.NewVEWServer(t)
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true, ProtoV6ProviderFactories: testProtoV6ProviderFactories(),
		Steps: []testresource.TestStep{
			{Config: fake.ProviderConfig() + componentVersionResourceConfig(), PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

func componentVersionResourceConfig() string {
	return `
resource "vew_component_version" "test" {
  project_id       = "prog-73488"
  component_id     = "cmp-123"
  description      = "created by provider test"
  release_type     = "PATCH"
  definition_json  = jsonencode({ phases = [] })
  software_vendor  = "VEW"
  software_version = "1.0"
}
`
}
