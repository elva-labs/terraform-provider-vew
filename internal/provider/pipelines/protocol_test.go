package pipelines_test

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

func TestPipelineProtocolPlan(t *testing.T) {
	for _, tc := range []struct {
		name, edit       string
		want             plancheck.ResourceActionType
		creates, updates int
	}{
		{"project replaces", `project_id = "other-project"`, plancheck.ResourceActionReplace, 2, 0},
		{"name replaces", `name = "other-name"`, plancheck.ResourceActionReplace, 2, 0},
		{"description replaces", `description = "other-description"`, plancheck.ResourceActionReplace, 2, 0},
		{"recipe replaces", `recipe_id = "other-recipe"`, plancheck.ResourceActionReplace, 2, 0},
		{"version updates", `recipe_version_id = "version-2"`, plancheck.ResourceActionUpdate, 1, 1},
		{"instance order updates", `build_instance_types = ["m5.xlarge", "m5.large"]`, plancheck.ResourceActionUpdate, 1, 1},
		{"schedule updates", `schedule = "0 6 * * ? *"`, plancheck.ResourceActionUpdate, 1, 1},
		{"product clears", `product_id = null`, plancheck.ResourceActionUpdate, 1, 1},
		{"unchanged", `product_id = "product"`, plancheck.ResourceActionNoop, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProtocolFixture(t)
			base := f.config("")
			next := f.config(tc.edit)
			testresource.Test(t, testresource.TestCase{
				IsUnitTest:               true,
				ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
				CheckDestroy: func(*terraform.State) error {
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.creates != tc.creates || f.updates != tc.updates {
						return fmt.Errorf("create/update calls = %d/%d, want %d/%d", f.creates, f.updates, tc.creates, tc.updates)
					}
					return nil
				},
				Steps: []testresource.TestStep{{Config: base}, {Config: next, ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("vew_pipeline.test", tc.want)}}}},
			})
		})
	}
}

func TestPipelineProtocolImportProductClearing(t *testing.T) {
	f := newProtocolFixture(t)
	base := f.config("")
	withoutProduct := f.config("product_id = null")
	testresource.Test(t, testresource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creates != 1 || f.updates != 1 || f.productID != nil {
				return fmt.Errorf("import product clearing: creates=%d updates=%d product=%v", f.creates, f.updates, f.productID)
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: base},
			{ResourceName: "vew_pipeline.test", ImportState: true, ImportStateId: "project/pipeline", ImportStateVerify: true},
			{Config: withoutProduct, ConfigPlanChecks: testresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("vew_pipeline.test", plancheck.ResourceActionUpdate)}}},
		},
	})
}

type protocolFixture struct {
	mu                                               sync.Mutex
	server                                           *httptest.Server
	created                                          bool
	creates, updates                                 int
	name, description, recipeID, versionID, schedule string
	instances                                        []string
	productID                                        *string
}

func newProtocolFixture(t *testing.T) *protocolFixture {
	t.Helper()
	f := &protocolFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			writeProtocolJSON(w, http.StatusOK, map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.Path, "/pipelines") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodPost:
			f.creates++
			f.created = true
			f.readInput(t, r, true)
			writeProtocolJSON(w, http.StatusAccepted, map[string]string{"pipelineId": "pipeline"})
		case http.MethodPut:
			f.updates++
			f.readInput(t, r, false)
			writeProtocolJSON(w, http.StatusAccepted, map[string]string{"pipelineId": "pipeline"})
		case http.MethodGet:
			if !f.created {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeProtocolJSON(w, http.StatusOK, map[string]any{"pipeline": map[string]any{"pipelineId": "pipeline", "projectId": strings.Split(r.URL.Path, "/")[2], "pipelineName": f.name, "pipelineDescription": f.description, "recipeId": f.recipeID, "recipeName": "recipe name", "recipeVersionId": f.versionID, "recipeVersionName": "1.0.0", "buildInstanceTypes": f.instances, "pipelineSchedule": f.schedule, "productId": f.productID, "status": "CREATED", "createDate": "2026-09-24", "createdBy": "fixture", "lastUpdateDate": "2026-09-24", "lastUpdatedBy": "fixture"}})
		case http.MethodDelete:
			f.created = false
			writeProtocolJSON(w, http.StatusAccepted, map[string]string{"pipelineId": "pipeline"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *protocolFixture) readInput(t *testing.T, r *http.Request, create bool) {
	t.Helper()
	var input struct {
		Name        string   `json:"pipelineName"`
		Description string   `json:"pipelineDescription"`
		RecipeID    string   `json:"recipeId"`
		VersionID   string   `json:"recipeVersionId"`
		Instances   []string `json:"buildInstanceTypes"`
		Schedule    string   `json:"pipelineSchedule"`
		ProductID   *string  `json:"productId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		t.Errorf("decode pipeline input: %v", err)
	}
	if create {
		f.name, f.description, f.recipeID = input.Name, input.Description, input.RecipeID
	}
	f.versionID, f.instances, f.schedule, f.productID = input.VersionID, input.Instances, input.Schedule, input.ProductID
}

func (f *protocolFixture) config(edit string) string {
	resource := `resource "vew_pipeline" "test" {
  project_id = "project"
  name = "name"
  description = "description"
  recipe_id = "recipe"
  recipe_version_id = "version"
  build_instance_types = ["m5.large", "m5.xlarge"]
  schedule = "0 0 * * ? *"
  product_id = "product"
}
`
	if edit != "" {
		key := strings.SplitN(edit, " ", 2)[0]
		start := strings.Index(resource, "  "+key+" = ")
		end := strings.Index(resource[start:], "\n")
		resource = resource[:start] + "  " + edit + resource[start+end:]
	}
	return fmt.Sprintf("provider \"vew\" {\n  api_url = %q\n  token_url = %q\n  client_id = \"id\"\n  client_secret = \"secret\"\n}\n", f.server.URL, f.server.URL+"/oauth/token") + resource
}

func writeProtocolJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
