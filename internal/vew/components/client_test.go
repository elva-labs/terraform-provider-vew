package components

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

// These request fixtures catch a regression that changes the S2S version
// payload from structured JSON or renames any of its wire fields.
func TestCreateComponentVersionMapsRequestAndActionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/projects/proj-1/components/cmp-1/versions" || request.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("request = %s %s key=%q", request.Method, request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		var got map[string]any
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"componentVersionDescription": "description",
			"componentVersionDependencies": []any{map[string]any{
				"componentId": "dependency-component", "componentName": "dependency", "componentVersionId": "dependency-version", "componentVersionName": "1.0", "componentVersionType": "RUNTIME", "order": float64(3), "position": "before",
			}},
			"componentVersionReleaseType": "INTERNAL",
			"componentVersionDefinition":  map[string]any{"steps": []any{}},
			"softwareVendor":              "vendor",
			"softwareVersion":             "2.0",
			"licenseDashboard":            "license",
			"notes":                       "notes",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body = %#v, want %#v", got, want)
		}
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	transport := newComponentTestTransport(t, server.URL)
	position := "before"
	got, err := NewClient(transport).CreateComponentVersion(context.Background(), "proj-1", "cmp-1", CreateComponentVersionInput{
		Description: "description", Dependencies: []Dependency{{ComponentID: "dependency-component", ComponentName: "dependency", VersionID: "dependency-version", VersionName: "1.0", Type: "RUNTIME", Order: 3, Position: &position}}, ReleaseType: "INTERNAL", Definition: json.RawMessage(`{"steps":[]}`), SoftwareVendor: "vendor", SoftwareVersion: "2.0", LicenseDashboard: stringPointer("license"), Notes: stringPointer("notes"),
	})
	if err != nil || got.ID != "version-123" || got.RetryAfter != 3*time.Second {
		t.Fatalf("result/err = %#v/%v", got, err)
	}
}

// This catches a create retry that generates a second key or remarshal of the
// request after a response is lost after the server received it.
func TestCreateComponentVersionReusesKeyAndBodyAfterLostResponse(t *testing.T) {
	var bodies [][]byte
	var keys []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies, keys = append(bodies, body), append(keys, request.Header.Get("Idempotency-Key"))
		calls++
		if calls == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	transport := newComponentTestTransport(t, server.URL)
	client := NewClient(transport)
	client.idempotencyKey = func() string { return "stable-key" }
	if _, err := client.CreateComponentVersion(context.Background(), "proj", "cmp", CreateComponentVersionInput{Definition: json.RawMessage(`{"steps":[]}`)}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || keys[0] == "" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("calls/keys/bodies = %d/%q/%q", len(bodies), keys, bodies)
	}
}

// This catches a GET decoder that reads the internal API's string definition
// or the wrong S2S envelope key.
func TestGetComponentVersionDecodesEnvelopeAndDefinition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/components/cmp/versions/version" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(w, `{"component_version":{"componentId":"cmp","componentVersionId":"version","componentVersionDescription":"description","componentVersionName":"name","componentVersionDependencies":[{"componentId":"dep-cmp","componentName":"dep","componentVersionId":"dep-version","componentVersionName":"1.0","componentVersionType":"RUNTIME","order":1,"position":"after"}],"softwareVendor":"vendor","softwareVersion":"2.0","licenseDashboard":null,"notes":null,"status":"VALIDATED","createDate":"2026-09-21T00:00:00Z","createdBy":"creator","lastUpdateDate":"2026-09-22T00:00:00Z","lastUpdatedBy":"updater"},"componentVersionDefinition":{"steps":[{"name":"install"}]}}`)
	}))
	defer server.Close()
	got, err := NewClient(newComponentTestTransport(t, server.URL)).GetComponentVersion(context.Background(), "proj", "cmp", "version")
	if err != nil {
		t.Fatal(err)
	}
	if got.ComponentID != "cmp" || got.ID != "version" || got.Name != "name" || got.Status != "VALIDATED" || got.UpdatedBy != "updater" || len(got.Dependencies) != 1 || got.Dependencies[0].Position == nil || *got.Dependencies[0].Position != "after" || string(got.Definition) != `{"steps":[{"name":"install"}]}` {
		t.Fatalf("component version = %#v", got)
	}
}

// This catches an update that silently omits one of the VEW mutable fields.
func TestUpdateComponentVersionMapsFullMutablePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodPut || request.URL.Path != "/projects/proj/components/cmp/versions/version" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var got map[string]any
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"componentVersionDescription": "description", "componentVersionDependencies": []any{map[string]any{"componentId": "dependency-component", "componentName": "dependency", "componentVersionId": "dependency-version", "componentVersionName": "1.0", "componentVersionType": "RUNTIME", "order": float64(3), "position": "before"}}, "componentVersionDefinition": map[string]any{"steps": []any{}}, "softwareVendor": "vendor", "softwareVersion": "2.0", "licenseDashboard": "license", "notes": "notes",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body = %#v, want %#v", got, want)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	position := "before"
	got, err := NewClient(newComponentTestTransport(t, server.URL)).UpdateComponentVersion(context.Background(), "proj", "cmp", "version", UpdateComponentVersionInput{Description: "description", Dependencies: []Dependency{{ComponentID: "dependency-component", ComponentName: "dependency", VersionID: "dependency-version", VersionName: "1.0", Type: "RUNTIME", Order: 3, Position: &position}}, Definition: json.RawMessage(`{"steps":[]}`), SoftwareVendor: "vendor", SoftwareVersion: "2.0", LicenseDashboard: stringPointer("license"), Notes: stringPointer("notes")})
	if err != nil || got.ID != "version-123" {
		t.Fatalf("result/err = %#v/%v", got, err)
	}
}

// This catches a retire action that targets the component rather than the
// individual version or fails to decode the asynchronous action ID.
func TestRetireComponentVersionMapsRequestAndActionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodDelete || request.URL.Path != "/projects/proj/components/cmp/versions/version" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	got, err := NewClient(newComponentTestTransport(t, server.URL)).RetireComponentVersion(context.Background(), "proj", "cmp", "version")
	if err != nil || got.ID != "version-123" {
		t.Fatalf("result/err = %#v/%v", got, err)
	}
}

func TestReleaseComponentVersionMapsBodylessRequestAndAcceptsAlreadyReleased(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost || request.URL.Path != "/projects/proj/components/cmp/versions/version/release" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if len(body) != 0 || request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("body/key = %q/%q", body, request.Header.Get("Idempotency-Key"))
		}
		_, _ = io.WriteString(w, `{"componentVersionId":"version"}`)
	}))
	defer server.Close()
	client := NewClient(newComponentTestTransport(t, server.URL))
	if err := client.ReleaseComponentVersion(context.Background(), "proj", "cmp", "version"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReleaseComponentVersion(context.Background(), "proj", "cmp", "version"); err != nil {
		t.Fatalf("already released: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestReleaseComponentVersionRejectsMismatchedResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		_, _ = io.WriteString(w, `{"componentVersionId":"other-version"}`)
	}))
	defer server.Close()
	err := NewClient(newComponentTestTransport(t, server.URL)).ReleaseComponentVersion(context.Background(), "proj", "cmp", "version")
	if err == nil || err.Error() != "VEW component version release response ID did not match requested version" {
		t.Fatalf("error = %v", err)
	}
}

func TestReleaseComponentVersionReturnsVEWRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"INVALID_COMPONENT_VERSION_STATUS","requestId":"request-1"}`)
	}))
	defer server.Close()
	err := NewClient(newComponentTestTransport(t, server.URL)).ReleaseComponentVersion(context.Background(), "proj", "cmp", "version")
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Problem.Code != "INVALID_COMPONENT_VERSION_STATUS" || apiErr.Problem.RequestID != "request-1" {
		t.Fatalf("error = %#v", err)
	}
}

func TestReleaseComponentVersionRetriesAfterLostResponseWithoutCreateKey(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 || request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("body/key = %q/%q", body, request.Header.Get("Idempotency-Key"))
		}
		if calls == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		_, _ = io.WriteString(w, `{"componentVersionId":"version"}`)
	}))
	defer server.Close()
	if err := NewClient(newComponentTestTransport(t, server.URL)).ReleaseComponentVersion(context.Background(), "proj", "cmp", "version"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestComponentVersionActionParsesRetryAfterDeltaSeconds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		w.Header().Set("Retry-After", "4")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	got, err := NewClient(newComponentTestTransport(t, server.URL)).RetireComponentVersion(context.Background(), "proj", "cmp", "version")
	if err != nil || got.RetryAfter != 4*time.Second {
		t.Fatalf("result/err = %#v/%v", got, err)
	}
}

func TestComponentVersionActionParsesRetryAfterHTTPDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		w.Header().Set("Retry-After", time.Now().UTC().Add(10*time.Second).Format(http.TimeFormat))
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"componentVersionId":"version-123"}`)
	}))
	defer server.Close()
	got, err := NewClient(newComponentTestTransport(t, server.URL)).RetireComponentVersion(context.Background(), "proj", "cmp", "version")
	if err != nil || got.RetryAfter < 8*time.Second || got.RetryAfter > 10*time.Second {
		t.Fatalf("result/err = %#v/%v", got, err)
	}
}

func newComponentTestTransport(t *testing.T, serverURL string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransport(vew.Config{APIURL: serverURL, TokenURL: serverURL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func stringPointer(value string) *string { return &value }

func TestClientCreateComponentMapsRequestAndResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.URL.Path != "/projects/proj-1/components" || request.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("request = %s %q", request.URL.Path, request.Header.Get("Idempotency-Key"))
		}
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewClient(transport).CreateComponent(context.Background(), "proj-1", CreateComponentInput{Name: "name", Description: "description", Platform: "Linux", SupportedArchitectures: []string{"arm64"}, SupportedOSVersions: []string{"Ubuntu 24"}})
	if err != nil || id != "cmp-123" {
		t.Fatalf("id/err = %q/%v", id, err)
	}
}

func TestClientCreateComponentReusesStableKeyAndBodyAfterInProgressResponse(t *testing.T) {
	var bodies [][]byte
	var keys []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies, keys = append(bodies, body), append(keys, request.Header.Get("Idempotency-Key"))
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"IDEMPOTENCY_REQUEST_IN_PROGRESS"}`)
			return
		}
		_, _ = io.WriteString(w, `{"componentId":"cmp-123"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(transport)
	client.idempotencyKey = func() string { return "stable-key" }
	if _, err := client.CreateComponent(context.Background(), "proj", CreateComponentInput{Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || keys[0] != "stable-key" || keys[0] != keys[1] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("calls/keys/bodies = %d/%q/%q", len(bodies), keys, bodies)
	}
}

func TestClientGetComponentDecodesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/components/cmp" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(w, `{"component":{"componentId":"cmp","componentName":"name","componentDescription":"description","componentPlatform":"Linux","componentSupportedArchitectures":["arm64"],"componentSupportedOsVersions":["Ubuntu 24"],"status":"CREATED","createDate":"2026-09-21T00:00:00Z","createdBy":"creator","lastUpdateDate":"2026-09-22T00:00:00Z","lastUpdatedBy":"updater"}}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewClient(transport).GetComponent(context.Background(), "proj", "cmp")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "cmp" || got.Status != "CREATED" || got.Name != "name" || got.UpdatedBy != "updater" || len(got.SupportedArchitectures) != 1 || got.SupportedArchitectures[0] != "arm64" {
		t.Fatalf("component = %#v", got)
	}
}

func TestClientUpdateComponentSendsDescriptionOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if request.Method != http.MethodPut {
			t.Fatalf("method = %s", request.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["componentDescription"] != "new description" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient(transport).UpdateComponent(context.Background(), "proj", "cmp", UpdateComponentInput{Description: "new description"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientArchiveComponentAcceptsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"detail":"gone"}`)
	}))
	defer server.Close()
	transport, err := vew.NewTransport(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewClient(transport).ArchiveComponent(context.Background(), "proj", "cmp"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsEmptyResourceIdentifiers(t *testing.T) {
	transport, err := vew.NewTransport(vew.Config{APIURL: "https://api.example", TokenURL: "https://token.example", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(transport)
	if _, err := client.GetComponent(context.Background(), "", "cmp"); err == nil {
		t.Fatal("accepted empty project ID")
	}
	if _, err := client.GetComponent(context.Background(), "project", ""); err == nil {
		t.Fatal("accepted empty component ID")
	}
}
