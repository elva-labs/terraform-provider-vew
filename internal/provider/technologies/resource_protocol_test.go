package technologies_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestTechnologyResourceProtocolLifecycle(t *testing.T) {
	var mu sync.Mutex
	created, deleted := false, false
	createCalls, getCalls, updateCalls, deleteCalls := 0, 0, 0, 0
	name, description := "example", ""
	var createKey string
	var createBodies [][]byte
	var scopes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			scopes = append(scopes, r.Form.Get("scope"))
			writeTechnologyJSON(w, http.StatusOK, map[string]any{"access_token": "test-token", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/projects/project/technologies":
			createCalls++
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				t.Error("technology create missing idempotency key")
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			createBodies = append(createBodies, body)
			var input map[string]any
			if err := json.Unmarshal(body, &input); err != nil || input["name"] != name || input["description"] != description || len(input) != 2 {
				t.Errorf("create input = %v, err = %v", input, err)
			}
			if createKey != "" && createKey != key {
				t.Errorf("replayed create key changed from %q to %q", createKey, key)
			}
			createKey = key
			if createCalls < 5 {
				writeTechnologyJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "TEMPORARY"})
				return
			}
			created = true
			writeTechnologyJSON(w, http.StatusCreated, map[string]string{"technologyId": "technology"})
		case r.Method == http.MethodGet && r.URL.Path == "/projects/project/technologies/technology":
			getCalls++
			if !created || deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeTechnologyJSON(w, http.StatusOK, map[string]any{"technology": map[string]any{
				"technologyId": "technology", "projectId": "project", "name": name, "description": description,
				"createDate": "2026-09-23T00:00:00Z", "lastUpdateDate": "2026-09-24T00:00:00Z",
			}})
		case r.Method == http.MethodPut && r.URL.Path == "/projects/project/technologies/technology":
			updateCalls++
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			name, _ = input["name"].(string)
			description, _ = input["description"].(string)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/projects/project/technologies/technology":
			deleteCalls++
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	providerConfig := fmt.Sprintf(`provider "vew" {
  api_url = %q
  projects_api_url = %q
  token_url = %q
  client_id = "id"
  client_secret = "secret"
}
`, server.URL, server.URL, server.URL+"/oauth/token")
	config := func(projectID, techName, techDescription string) string {
		descriptionLine := ""
		if techDescription != "" {
			descriptionLine = fmt.Sprintf("  description = %q\n", techDescription)
		}
		return providerConfig + fmt.Sprintf(`resource "vew_technology" "test" {
  project_id = %q
  name = %q
%s}
`, projectID, techName, descriptionLine)
	}
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		CheckDestroy: func(*terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if !deleted || deleteCalls != 1 || createCalls != 5 || updateCalls != 1 || getCalls < 2 || createKey == "" {
				return fmt.Errorf("lifecycle calls: create=%d get=%d update=%d delete=%d deleted=%t", createCalls, getCalls, updateCalls, deleteCalls, deleted)
			}
			for _, body := range createBodies[1:] {
				if string(body) != string(createBodies[0]) {
					return fmt.Errorf("create replay body changed: %q vs %q", body, createBodies[0])
				}
			}
			for _, scope := range scopes {
				if scope != "clients/projects/technology.read" && scope != "clients/projects/technology.write" {
					return fmt.Errorf("unexpected token scope %q", scope)
				}
			}
			if !strings.Contains(createKey, "-") {
				return fmt.Errorf("create key is not UUID-like: %q", createKey)
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: config("project", "example", ""), Check: testresource.ComposeTestCheckFunc(
				testresource.TestCheckResourceAttr("vew_technology.test", "id", "technology"),
				testresource.TestCheckResourceAttr("vew_technology.test", "project_id", "project"),
				testresource.TestCheckResourceAttr("vew_technology.test", "description", ""),
				testresource.TestCheckResourceAttr("vew_technology.test", "created_at", "2026-09-23T00:00:00Z"),
			)},
			{Config: config("project", "renamed", "updated"), Check: testresource.ComposeTestCheckFunc(
				testresource.TestCheckResourceAttr("vew_technology.test", "id", "technology"),
				testresource.TestCheckResourceAttr("vew_technology.test", "name", "renamed"),
				testresource.TestCheckResourceAttr("vew_technology.test", "description", "updated"),
			)},
			{Config: config("another-project", "renamed", "updated"), PlanOnly: true, ExpectNonEmptyPlan: true},
			{ResourceName: "vew_technology.test", ImportState: true, ImportStateId: "project/technology", ImportStateVerify: true},
		},
	})
}

func writeTechnologyJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
