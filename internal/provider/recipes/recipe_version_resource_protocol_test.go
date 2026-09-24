package recipes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestRecipeVersionProtocolPlanActions(t *testing.T) {
	const address = "vew_recipe_version.test"
	tests := []struct {
		name, edit, status string
		want               plancheck.ResourceActionType
		wantUpdates        int
	}{
		{"project ID replaces", `project_id = "other-project"`, "VALIDATED", plancheck.ResourceActionReplace, 0},
		{"recipe ID replaces", `recipe_id = "other-recipe"`, "VALIDATED", plancheck.ResourceActionReplace, 0},
		{"release type replaces", `release_type = "MINOR"`, "VALIDATED", plancheck.ResourceActionReplace, 0},
		{"draft description updates", `description = "changed"`, "VALIDATED", plancheck.ResourceActionUpdate, 1},
		{"released description replaces", `description = "changed"`, "RELEASED", plancheck.ResourceActionReplace, 0},
		{"released volume replaces", `volume_size = 30`, "RELEASED", plancheck.ResourceActionReplace, 0},
		{"released components replace", `configured_components = []`, "RELEASED", plancheck.ResourceActionReplace, 0},
		{"released integrations replace", `integrations = ["a"]`, "RELEASED", plancheck.ResourceActionReplace, 0},
		{"timeout updates state", `timeouts { update = "3m" }`, "VALIDATED", plancheck.ResourceActionUpdate, 0},
		{"integration order is unchanged", `integrations = ["z", "a"]`, "VALIDATED", plancheck.ResourceActionNoop, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRecipeVersionProtocolFixture(t)
			base := fixture.config("")
			changed := fixture.config(tc.edit)
			if base == changed {
				t.Fatal("plan edit did not change configuration")
			}
			testresource.Test(t, testresource.TestCase{
				IsUnitTest: true,
				ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
					"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
				},
				CheckDestroy: func(*terraform.State) error { return fixture.checkCalls(tc.want, tc.wantUpdates) },
				Steps: []testresource.TestStep{
					{Config: base, Check: testresource.TestCheckResourceAttr(address, "status", "VALIDATED")},
					{Config: changed,
						PreConfig: func() { fixture.setStatus(tc.status) },
						ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(address, tc.want),
						}}},
				},
			})
		})
	}
}

type recipeVersionProtocolFixture struct {
	mu           sync.Mutex
	server       *httptest.Server
	status       string
	created      bool
	createCalls  int
	updateCalls  int
	deleteCalls  int
	description  string
	volumeSize   string
	integrations []string
	components   []map[string]any
}

func newRecipeVersionProtocolFixture(t *testing.T) *recipeVersionProtocolFixture {
	t.Helper()
	f := &recipeVersionProtocolFixture{status: "VALIDATED"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		versionPath := strings.Contains(r.URL.Path, "/recipes/") && strings.Contains(r.URL.Path, "/versions")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/components/component"):
			writeJSON(w, http.StatusOK, map[string]any{"component": map[string]any{"componentId": "component", "componentName": "component name"}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/components/component/versions/component-version"):
			writeJSON(w, http.StatusOK, map[string]any{"component_version": map[string]any{"componentId": "component", "componentVersionId": "component-version", "componentVersionName": "1.0.0"}})
		case r.Method == http.MethodPost && versionPath && strings.HasSuffix(r.URL.Path, "/versions"):
			f.createCalls++
			f.created = true
			f.status = "VALIDATED"
			f.readInput(t, r)
			writeJSON(w, http.StatusCreated, map[string]string{"recipeVersionId": "version"})
		case r.Method == http.MethodGet && versionPath && strings.HasSuffix(r.URL.Path, "/versions/version"):
			if !f.created || f.status == "RETIRED" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			parts := strings.Split(r.URL.Path, "/")
			writeJSON(w, http.StatusOK, map[string]any{"recipe_version": map[string]any{
				"recipeId": parts[4], "recipeVersionId": "version", "recipeVersionName": "1.0.0",
				"recipeVersionDescription": f.description, "recipeVersionVolumeSize": f.volumeSize,
				"recipeVersionIntegrations":    f.integrations,
				"configuredComponentsVersions": f.components,
				"effectiveComponentsVersions":  []any{}, "status": f.status,
				"createDate": "2026-09-23T00:00:00Z", "createdBy": "fixture",
				"lastUpdateDate": "2026-09-23T00:00:00Z", "lastUpdatedBy": "fixture",
			}})
		case r.Method == http.MethodDelete && versionPath && strings.HasSuffix(r.URL.Path, "/versions/version"):
			f.deleteCalls++
			f.status = "RETIRED"
			writeJSON(w, http.StatusOK, map[string]string{"recipeVersionId": "version"})
		case r.Method == http.MethodPut && versionPath:
			f.updateCalls++
			f.readInput(t, r)
			writeJSON(w, http.StatusOK, map[string]string{"recipeVersionId": "version"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *recipeVersionProtocolFixture) config(edit string) string {
	resource := `resource "vew_recipe_version" "test" {
  project_id = "project"
  recipe_id = "recipe"
  description = "description"
  release_type = "PATCH"
  volume_size = 20
  integrations = ["a", "z"]
  configured_components = [{
    component_id = "component"
    version_id = "component-version"
    type = "APP"
  }]
}
`
	if edit != "" {
		key := strings.SplitN(edit, " ", 2)[0]
		if key == "timeouts" {
			resource = strings.Replace(resource, "\n}\n", "\n  "+edit+"\n}\n", 1)
		} else {
			start := strings.Index(resource, "  "+key+" = ")
			end := strings.Index(resource[start:], "\n")
			if key == "configured_components" {
				end = strings.Index(resource[start:], "  }]\n") + len("  }]")
			}
			resource = resource[:start] + "  " + edit + resource[start+end:]
		}
	}
	return fmt.Sprintf(`provider "vew" {
  api_url = %q
  token_url = %q
  client_id = "id"
  client_secret = "secret"
}
`, f.server.URL, f.server.URL+"/oauth/token") + resource
}

func (f *recipeVersionProtocolFixture) setStatus(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *recipeVersionProtocolFixture) readInput(t *testing.T, r *http.Request) {
	t.Helper()
	var input struct {
		Description  string           `json:"recipeVersionDescription"`
		VolumeSize   string           `json:"recipeVersionVolumeSize"`
		Integrations []string         `json:"recipeVersionIntegrations"`
		Components   []map[string]any `json:"configuredComponentsVersions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		t.Errorf("decode recipe version input: %v", err)
	}
	f.description, f.volumeSize = input.Description, input.VolumeSize
	f.integrations, f.components = input.Integrations, input.Components
}

func (f *recipeVersionProtocolFixture) checkCalls(action plancheck.ResourceActionType, wantUpdates int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	wantCreates := 1
	if action == plancheck.ResourceActionReplace {
		wantCreates = 2
	}
	if f.createCalls != wantCreates || f.updateCalls != wantUpdates || f.deleteCalls != wantCreates {
		return fmt.Errorf("recipe version calls: create=%d update=%d delete=%d", f.createCalls, f.updateCalls, f.deleteCalls)
	}
	return nil
}
