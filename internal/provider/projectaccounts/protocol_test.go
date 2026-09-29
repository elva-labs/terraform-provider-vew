package projectaccounts_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestProjectAccountProtocolCreateUpdateDeactivate(t *testing.T) {
	fixture := newAccountProtocolFixture(t)
	config := func(name string) string {
		return fmt.Sprintf(`
provider "vew" {
  api_url = %q
  projects_api_url = %q
  token_url = %q
  client_id = "test-client"
  client_secret = "test-secret"
}
resource "vew_project_account" "test" {
  project_id = "project-1"
  aws_account_id = "000000000000"
  account_type = "USER"
  name = %q
  description = "protocol test"
  technology_id = "technology-1"
  stage = "dev"
  region = "eu-west-1"
}
`, fixture.server.URL+"/clients/packaging/v1", fixture.server.URL+"/clients/projects/v1", fixture.server.URL+"/oauth/token", name)
	}
	testresource.Test(t, testresource.TestCase{
		IsUnitTest: true,
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")()),
		},
		Steps: []testresource.TestStep{
			{Config: config("Build"), Check: testresource.ComposeAggregateTestCheckFunc(
				testresource.TestCheckResourceAttr("vew_project_account.test", "id", "account-internal"),
				testresource.TestCheckResourceAttr("vew_project_account.test", "status", "Active"),
			)},
			{Config: config("Updated build"), Check: testresource.ComposeAggregateTestCheckFunc(
				testresource.TestCheckResourceAttr("vew_project_account.test", "id", "account-internal"),
				testresource.TestCheckResourceAttr("vew_project_account.test", "name", "Updated build"),
			)},
			{ResourceName: "vew_project_account.test", ImportState: true, ImportStateId: "project-1/account-internal", ImportStateVerify: true},
			{Config: config("Updated build")},
			{Config: config("Updated build"), PreConfig: fixture.markInactive},
		},
		CheckDestroy: func(_ *terraform.State) error {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.creates != 2 || fixture.createRequests != 3 || fixture.updates != 1 || fixture.deactivations != 1 || fixture.totalPolls < 4 {
				return fmt.Errorf("create/update/deactivate calls = %d/%d/%d", fixture.creates, fixture.updates, fixture.deactivations)
			}
			if len(fixture.createKeys) < 2 || fixture.createKeys[0] == "" || fixture.createKeys[0] != fixture.createKeys[1] || !regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`).MatchString(fixture.createKeys[0]) || !bytes.Equal(fixture.createBodies[0], fixture.createBodies[1]) {
				return fmt.Errorf("transport retry changed create key or body: keys=%q", fixture.createKeys)
			}
			if fixture.offboardings != 0 {
				return fmt.Errorf("unexpected offboarding calls = %d", fixture.offboardings)
			}
			return nil
		},
	})
}

type accountProtocolFixture struct {
	mu                                            sync.Mutex
	server                                        *httptest.Server
	account                                       map[string]any
	creates, updates, deactivations, offboardings int
	createRequests, polls, totalPolls             int
	createKeys                                    []string
	createBodies                                  [][]byte
}

func newAccountProtocolFixture(t *testing.T) *accountProtocolFixture {
	t.Helper()
	f := &accountProtocolFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/clients/projects/v1/projects/project-1/accounts/account-internal/offboard" {
			f.offboardings++
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/clients/projects/v1/projects/project-1/accounts" && r.URL.Path != "/clients/projects/v1/projects/project-1/accounts/account-internal" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodPost:
			f.createRequests++
			bodyBytes, _ := io.ReadAll(r.Body)
			f.createKeys = append(f.createKeys, r.Header.Get("Idempotency-Key"))
			f.createBodies = append(f.createBodies, append([]byte(nil), bodyBytes...))
			if f.createRequests == 1 {
				w.Header().Set("Retry-After", "0")
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":503,"code":"TEMPORARY","retryable":true}`))
				return
			}
			f.creates++
			var body map[string]any
			if json.Unmarshal(bodyBytes, &body) != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if _, hasName := body["name"]; !hasName {
				http.Error(w, "create body must use name", http.StatusBadRequest)
				return
			}
			if _, hasDescription := body["description"]; !hasDescription {
				http.Error(w, "create body must use description", http.StatusBadRequest)
				return
			}
			f.polls = 0
			f.account = protocolAccount(body, "OnBoarding", "")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accountId":"account-internal"}`))
		case http.MethodGet:
			if f.account == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			f.polls++
			f.totalPolls++
			if f.account["status"] == "OnBoarding" && f.polls > 1 {
				f.account["status"], f.account["lastOnboardingResult"] = "Active", "Succeeded"
			}
			if f.account["status"] == "ReOnBoarding" && f.polls > 1 {
				f.account["status"], f.account["lastOnboardingResult"] = "Active", "Succeeded"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.account)
		case http.MethodPut:
			f.updates++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			if _, hasName := body["name"]; !hasName {
				http.Error(w, "update body must use name", http.StatusBadRequest)
				return
			}
			if _, hasDescription := body["description"]; !hasDescription {
				http.Error(w, "update body must use description", http.StatusBadRequest)
				return
			}
			body["awsAccountId"] = "000000000000"
			f.polls = 0
			f.account = protocolAccount(body, "ReOnBoarding", "")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "0")
			_, _ = w.Write([]byte(`{"accountId":"account-internal"}`))
		case http.MethodDelete:
			f.deactivations++
			f.account["status"] = "Inactive"
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *accountProtocolFixture) markInactive() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.account != nil {
		f.account["status"] = "Inactive"
	}
}

func protocolAccount(body map[string]any, status, result string) map[string]any {
	return map[string]any{
		"accountId": "account-internal", "projectId": "project-1", "awsAccountId": body["awsAccountId"],
		"name": body["name"], "description": body["description"],
		"accountType": body["accountType"], "technologyId": body["technologyId"], "stage": body["stage"], "region": body["region"],
		"status": status, "lastOnboardingResult": result, "lastOnboardingError": "",
		"createDate": "2026-09-01T00:00:00Z", "lastUpdateDate": "2026-09-02T00:00:00Z",
	}
}
