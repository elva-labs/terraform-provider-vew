package testhelpers

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/elva-labs/terraform-provider-vew/internal/vew/components"
)

const (
	testClientID     = "terraform-test-client"
	testClientSecret = "terraform-test-secret"
	testAccessToken  = "terraform-test-access-token"
)

type VEWServer struct {
	t *testing.T

	mu                 sync.Mutex
	server             *httptest.Server
	component          components.Component
	archived           bool
	notFound           bool
	createCalls        int
	getCalls           int
	updateCalls        int
	archiveCalls       int
	idempotencyKey     string
	create             components.CreateComponentInput
	update             components.UpdateComponentInput
	failGets           int
	failGetDetail      string
	versionReads       map[string][]VersionResponse
	versionRequests    []VersionRequest
	versionRetryAfter  string
	versionActionReads map[string][]VersionResponse
}

// VersionResponse scripts one version GET. The last response is repeated.
type VersionResponse struct {
	Version    components.ComponentVersion
	StatusCode int
	Detail     string
	RetryAfter string
}

type VersionRequest struct {
	Method, Path, IdempotencyKey string
	Body                         json.RawMessage
}

func (f *VEWServer) QueueVersionReads(project, component, version string, responses ...VersionResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.versionReads == nil {
		f.versionReads = make(map[string][]VersionResponse)
	}
	f.versionReads["/projects/"+project+"/components/"+component+"/versions/"+version] = responses
}

func (f *VEWServer) VersionRequests() []VersionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]VersionRequest(nil), f.versionRequests...)
}

func (f *VEWServer) SetVersionRetryAfter(value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versionRetryAfter = value
}

// QueueVersionActionReads installs a fresh GET sequence when an action is accepted.
func (f *VEWServer) QueueVersionActionReads(method, project, component, version string, responses ...VersionResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.versionActionReads == nil {
		f.versionActionReads = make(map[string][]VersionResponse)
	}
	f.versionActionReads[method+" /projects/"+project+"/components/"+component+"/versions/"+version] = responses
}

func (f *VEWServer) ComponentVersionAPI(t *testing.T) components.ComponentVersionAPI {
	t.Helper()
	transport, err := vew.NewTransport(vew.Config{APIURL: f.server.URL, TokenURL: f.server.URL + "/oauth/token", ClientID: testClientID, ClientSecret: testClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	return components.NewClient(transport)
}

func (f *VEWServer) handleVersion(w http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		f.writeProblem(w, 400, "invalid request")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versionRequests = append(f.versionRequests, VersionRequest{Method: request.Method, Path: request.URL.Path, Body: body, IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if request.Method == http.MethodGet {
		queue := f.versionReads[request.URL.Path]
		if len(queue) == 0 {
			f.writeProblem(w, 404, "version not found")
			return
		}
		response := queue[0]
		if len(queue) > 1 {
			f.versionReads[request.URL.Path] = queue[1:]
		}
		if response.StatusCode >= 400 {
			f.writeProblem(w, response.StatusCode, response.Detail)
			return
		}
		w.Header().Set("Retry-After", response.RetryAfter)
		f.writeJSON(w, 200, map[string]any{"component_version": response.Version, "componentVersionDefinition": response.Version.Definition})
		return
	}
	path := request.URL.Path
	if request.Method == http.MethodPost {
		path += "/version"
	}
	if responses, ok := f.versionActionReads[request.Method+" "+path]; ok {
		if f.versionReads == nil {
			f.versionReads = make(map[string][]VersionResponse)
		}
		f.versionReads[path] = append([]VersionResponse(nil), responses...)
	}
	w.Header().Set("Retry-After", f.versionRetryAfter)
	f.writeJSON(w, 202, map[string]string{"componentVersionId": "version"})
}

// Snapshot exposes observed fake-server behavior to external protocol tests.
type Snapshot struct {
	Archived                                         bool
	CreateCalls, GetCalls, UpdateCalls, ArchiveCalls int
	IdempotencyKey                                   string
	Create                                           components.CreateComponentInput
	Update                                           components.UpdateComponentInput
}

func (f *VEWServer) Snapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Snapshot{Archived: f.archived, CreateCalls: f.createCalls, GetCalls: f.getCalls, UpdateCalls: f.updateCalls, ArchiveCalls: f.archiveCalls, IdempotencyKey: f.idempotencyKey, Create: f.create, Update: f.update}
}

// FailNextGets makes the next count component reads return a 503 problem.
func (f *VEWServer) FailNextGets(count int, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failGets, f.failGetDetail = count, detail
}

// ComponentAPI returns a real component client configured against this server.
func (f *VEWServer) ComponentAPI(t *testing.T) components.API {
	t.Helper()
	transport, err := vew.NewTransport(vew.Config{APIURL: f.server.URL, TokenURL: f.server.URL + "/oauth/token", ClientID: testClientID, ClientSecret: testClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	return components.NewClient(transport)
}

func (f *VEWServer) SetComponent(component components.Component, notFound bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.component, f.notFound = component, notFound
}

func NewVEWServer(t *testing.T) *VEWServer {
	t.Helper()
	fake := &VEWServer{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *VEWServer) handle(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/oauth/token" {
		f.handleToken(w, request)
		return
	}
	if request.Header.Get("Authorization") != "Bearer "+testAccessToken {
		f.writeProblem(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if strings.Contains(request.URL.Path, "/versions") {
		f.handleVersion(w, request)
		return
	}

	switch request.URL.Path {
	case "/projects/prog-73488/components":
		if request.Method == http.MethodPost {
			f.handleCreate(w, request)
			return
		}
	case "/projects/prog-73488/components/cmp-123":
		switch request.Method {
		case http.MethodGet:
			f.handleGet(w)
			return
		case http.MethodPut:
			f.handleUpdate(w, request)
			return
		case http.MethodDelete:
			f.handleArchive(w)
			return
		}
	}
	f.writeProblem(w, http.StatusNotFound, "not found")
}

func (f *VEWServer) handleToken(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		f.writeProblem(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	clientID, secret, ok := request.BasicAuth()
	if !ok || clientID != testClientID || secret != testClientSecret {
		f.writeProblem(w, http.StatusUnauthorized, "invalid client credentials")
		return
	}
	if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "client_credentials" || request.Form.Get("scope") != "clients/packaging/component.read clients/packaging/component.write" {
		f.writeProblem(w, http.StatusBadRequest, "invalid token request")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"access_token": testAccessToken, "expires_in": 3600})
}

func (f *VEWServer) handleCreate(w http.ResponseWriter, request *http.Request) {
	payload, ok := exactJSONBody(request, "componentName", "componentDescription", "componentPlatform", "componentSupportedArchitectures", "componentSupportedOsVersions")
	if !ok {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	var name, description, platform string
	var architectures, osVersions []string
	if json.Unmarshal(fields["componentName"], &name) != nil ||
		json.Unmarshal(fields["componentDescription"], &description) != nil ||
		json.Unmarshal(fields["componentPlatform"], &platform) != nil ||
		json.Unmarshal(fields["componentSupportedArchitectures"], &architectures) != nil ||
		json.Unmarshal(fields["componentSupportedOsVersions"], &osVersions) != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component create request")
		return
	}
	input := components.CreateComponentInput{Name: name, Description: description, Platform: platform, SupportedArchitectures: architectures, SupportedOSVersions: osVersions}
	f.mu.Lock()
	f.createCalls++
	f.idempotencyKey = request.Header.Get("Idempotency-Key")
	f.create = input
	f.archived, f.notFound = false, false
	f.component = components.Component{
		ID: "cmp-123", Name: input.Name, Description: input.Description, Platform: input.Platform,
		SupportedArchitectures: input.SupportedArchitectures, SupportedOSVersions: input.SupportedOSVersions,
		Status: "CREATED", CreatedAt: "2026-09-21T12:00:00Z", CreatedBy: "terraform-test-user",
		UpdatedAt: "2026-09-21T12:00:00Z", UpdatedBy: "terraform-test-user",
	}
	f.mu.Unlock()
	f.writeJSON(w, http.StatusCreated, map[string]any{"componentId": "cmp-123"})
}

func (f *VEWServer) handleGet(w http.ResponseWriter) {
	f.mu.Lock()
	f.getCalls++
	component, missing := f.component, f.notFound
	failing := f.failGets > 0
	if failing {
		f.failGets--
	}
	failureDetail := f.failGetDetail
	f.mu.Unlock()
	if failing {
		f.writeProblem(w, http.StatusServiceUnavailable, failureDetail)
		return
	}
	if missing {
		f.writeProblem(w, http.StatusNotFound, "component not found")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"component": fakeComponentJSON(component)})
}

func (f *VEWServer) handleUpdate(w http.ResponseWriter, request *http.Request) {
	payload, ok := exactJSONBody(request, "componentDescription")
	if !ok {
		f.writeProblem(w, http.StatusBadRequest, "invalid component update request")
		return
	}
	var input components.UpdateComponentInput
	if err := json.Unmarshal(payload, &input); err != nil {
		f.writeProblem(w, http.StatusBadRequest, "invalid component update request")
		return
	}
	f.mu.Lock()
	f.updateCalls++
	f.update = input
	f.component.Description = input.Description
	f.component.UpdatedAt = "2026-09-21T12:01:00Z"
	f.component.UpdatedBy = "terraform-test-updater"
	f.mu.Unlock()
	f.writeJSON(w, http.StatusOK, map[string]string{"result": "updated"})
}

func exactJSONBody(request *http.Request, expectedKeys ...string) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, false
	}
	payload, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || len(fields) != len(expectedKeys) {
		return nil, false
	}
	for _, key := range expectedKeys {
		if _, ok := fields[key]; !ok {
			return nil, false
		}
	}
	return payload, true
}

func (f *VEWServer) handleArchive(w http.ResponseWriter) {
	f.mu.Lock()
	f.archiveCalls++
	f.archived = true
	f.component.Status = "ARCHIVED"
	f.mu.Unlock()
	f.writeJSON(w, http.StatusNoContent, nil)
}

func (f *VEWServer) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.t.Errorf("encode fake VEW response: %v", err)
		}
	}
}

func (f *VEWServer) writeProblem(w http.ResponseWriter, status int, detail string) {
	f.writeJSON(w, status, map[string]any{"status": status, "title": http.StatusText(status), "detail": detail, "code": fmt.Sprintf("HTTP_%d", status), "retryable": false})
}

func fakeComponentJSON(component components.Component) map[string]any {
	return map[string]any{
		"componentId":                     component.ID,
		"componentName":                   component.Name,
		"componentDescription":            component.Description,
		"componentPlatform":               component.Platform,
		"componentSupportedArchitectures": component.SupportedArchitectures,
		"componentSupportedOsVersions":    component.SupportedOSVersions,
		"status":                          component.Status,
		"createDate":                      component.CreatedAt,
		"createdBy":                       component.CreatedBy,
		"lastUpdateDate":                  component.UpdatedAt,
		"lastUpdatedBy":                   component.UpdatedBy,
	}
}

// ProviderConfig configures a provider to use this fake VEW server.
func (f *VEWServer) ProviderConfig() string {
	return fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}
`, f.server.URL, f.server.URL+"/oauth/token", testClientID, testClientSecret)
}
