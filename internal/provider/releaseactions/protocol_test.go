package releaseactions_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/providerdata"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

type terraform1163Only struct{}

func (terraform1163Only) CheckTerraformVersion(_ context.Context, request tfversion.CheckTerraformVersionRequest, response *tfversion.CheckTerraformVersionResponse) {
	want := version.Must(version.NewVersion("1.16.3"))
	if !request.TerraformVersion.Equal(want) {
		response.Skip = fmt.Sprintf("release-action protocol coverage is pinned to Terraform 1.16.3, found %s", request.TerraformVersion)
	}
}

type fastReleaseProvider struct{ provider.Provider }

func (p *fastReleaseProvider) Actions(ctx context.Context) []func() action.Action {
	return p.Provider.(provider.ProviderWithActions).Actions(ctx)
}

func (p *fastReleaseProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	p.Provider.Configure(ctx, request, response)
	if response.Diagnostics.HasError() {
		return
	}
	data := response.ResourceData.(providerdata.Data)
	data.Waiter = releaseProtocolWaiter{}
	response.ResourceData = data
	response.DataSourceData = data
	// Preserve the separately configured release-only clients for actions.
}

type releaseProtocolWaiter struct{}

func (releaseProtocolWaiter) Until(ctx context.Context, _, _ time.Duration, read vew.StatusReader, evaluate vew.StatusEvaluator) error {
	for range 10 {
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

type releaseProtocolServer struct {
	server *httptest.Server
	mu     sync.Mutex
	status string
	events []string

	releaseCalls  int
	pipelineCalls int
}

func newReleaseProtocolServer(t *testing.T) *releaseProtocolServer {
	t.Helper()
	fake := &releaseProtocolServer{status: "VALIDATED"}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *releaseProtocolServer) providerConfig() string {
	return fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = "test-client"
  client_secret = "test-secret"
}
`, f.server.URL, f.server.URL+"/oauth/token")
}

func (f *releaseProtocolServer) snapshot() (string, []string, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, append([]string(nil), f.events...), f.releaseCalls, f.pipelineCalls
}

func (f *releaseProtocolServer) handle(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/oauth/token" {
		f.writeJSON(w, http.StatusOK, map[string]any{"access_token": "token", "expires_in": 3600})
		return
	}
	if request.Header.Get("Authorization") != "Bearer token" {
		f.writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED"})
		return
	}
	const versionPath = "/projects/project/components/component/versions/version"
	switch {
	case request.Method == http.MethodPost && request.URL.Path == versionPath+"/release":
		body, _ := io.ReadAll(request.Body)
		if len(body) != 0 || request.Header.Get("Idempotency-Key") != "" {
			f.writeJSON(w, http.StatusBadRequest, map[string]any{"code": "INVALID_RELEASE_REQUEST"})
			return
		}
		f.mu.Lock()
		f.releaseCalls++
		f.events = append(f.events, "release:"+f.status)
		f.status = "RELEASED"
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, map[string]string{"componentVersionId": "version"})
	case request.Method == http.MethodPost && request.URL.Path == "/projects/project/components/component/versions":
		f.mu.Lock()
		f.status = "VALIDATED"
		f.events = append(f.events, "create")
		f.mu.Unlock()
		f.writeJSON(w, http.StatusAccepted, map[string]string{"componentVersionId": "version"})
	case request.Method == http.MethodGet && request.URL.Path == versionPath:
		f.mu.Lock()
		status := f.status
		f.events = append(f.events, "read:"+status)
		f.mu.Unlock()
		f.writeJSON(w, http.StatusOK, map[string]any{
			"component_version": map[string]any{
				"componentId": "component", "componentVersionId": "version", "componentVersionName": "1.0.0",
				"componentVersionDescription": "protocol", "componentVersionDependencies": []any{}, "softwareVendor": "VEW",
				"softwareVersion": "1.0", "status": status,
			},
			"componentVersionDefinition": map[string]any{"phases": []any{}},
		})
	case request.Method == http.MethodDelete && request.URL.Path == versionPath:
		f.mu.Lock()
		f.status = "RETIRED"
		f.events = append(f.events, "retire")
		f.mu.Unlock()
		f.writeJSON(w, http.StatusAccepted, map[string]string{"componentVersionId": "version"})
	case request.Method == http.MethodPost && request.URL.Path == "/projects/project/pipelines":
		f.mu.Lock()
		f.pipelineCalls++
		f.mu.Unlock()
		f.writeJSON(w, http.StatusAccepted, map[string]string{"pipelineId": "pipeline"})
	default:
		f.writeJSON(w, http.StatusNotFound, map[string]any{"code": "NOT_FOUND"})
	}
}

func (f *releaseProtocolServer) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func releaseProtocolFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"vew": providerserver.NewProtocol6WithError(&fastReleaseProvider{Provider: rootprovider.New("test")()}),
	}
}

func TestActionDeclarationAloneDoesNotReleaseProtocol(t *testing.T) {
	fake := newReleaseProtocolServer(t)
	config := fake.providerConfig() + `
resource "terraform_data" "unrelated" {
  input = "ordinary apply"
}

action "vew_component_version_release" "declared" {
  config {
    project_id   = "project"
    component_id = "component"
    version_id   = "version"
  }
}
`
	testresource.UnitTest(t, testresource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{terraform1163Only{}},
		ProtoV6ProviderFactories: releaseProtocolFactories(),
		Steps: []testresource.TestStep{{
			Config: config,
			Check: func(*terraform.State) error {
				_, _, releases, _ := fake.snapshot()
				if releases != 0 {
					return fmt.Errorf("ordinary apply invoked declared release action %d times", releases)
				}
				return nil
			},
		}},
	})
}

func TestAfterCreateReleaseRunsAfterValidationAndRefreshObservesReleasedProtocol(t *testing.T) {
	fake := newReleaseProtocolServer(t)
	config := fake.providerConfig() + `
resource "vew_component_version" "test" {
  project_id       = "project"
  component_id     = "component"
  description      = "protocol"
  release_type     = "PATCH"
  definition_json  = jsonencode({ phases = [] })
  software_vendor  = "VEW"
  software_version = "1.0"

  lifecycle {
    action_trigger {
      events     = [after_create]
      actions    = [action.vew_component_version_release.after_create]
      on_failure = halt
    }
  }
}

action "vew_component_version_release" "after_create" {
  config {
    project_id   = caller.project_id
    component_id = caller.component_id
    version_id   = caller.id
  }
}
`
	testresource.UnitTest(t, testresource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{terraform1163Only{}},
		ProtoV6ProviderFactories: releaseProtocolFactories(),
		Steps: []testresource.TestStep{
			{
				Config: config,
				Check: func(state *terraform.State) error {
					status, events, releases, _ := fake.snapshot()
					if status != "RELEASED" || releases != 1 {
						return fmt.Errorf("after-create status/releases = %s/%d", status, releases)
					}
					joined := strings.Join(events, ",")
					if !strings.Contains(joined, "create,read:VALIDATED,release:VALIDATED") {
						return fmt.Errorf("release did not follow create validation: %v", events)
					}
					resource := state.RootModule().Resources["vew_component_version.test"]
					if resource == nil || resource.Primary.Attributes["status"] != "VALIDATED" {
						return fmt.Errorf("action unexpectedly wrote resource state: %#v", resource)
					}
					return nil
				},
			},
			{
				Config: config,
				Check: testresource.ComposeTestCheckFunc(
					testresource.TestCheckResourceAttr("vew_component_version.test", "status", "RELEASED"),
					func(*terraform.State) error {
						_, _, releases, _ := fake.snapshot()
						if releases != 1 {
							return fmt.Errorf("unchanged normal apply repeated after_create release %d times", releases)
						}
						return nil
					},
				),
			},
		},
	})
}
