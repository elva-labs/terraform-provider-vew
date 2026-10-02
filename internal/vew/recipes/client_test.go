package recipes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func testTransport(t *testing.T, url string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransport(vew.Config{APIURL: url, TokenURL: url + "/oauth/token", ClientID: "id", ClientSecret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func tokenOrServe(w http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/oauth/token" {
		return false
	}
	_, _ = io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
	return true
}

func TestRecipeClientLifecycle(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		calls++
		switch calls {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/projects/proj/recipes" || request.Header.Get("Idempotency-Key") == "" {
				t.Fatalf("create = %s %s", request.Method, request.URL.Path)
			}
			var got map[string]any
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"recipeName": "recipe", "recipeDescription": "description", "recipePlatform": "Linux", "recipeArchitecture": "arm64", "recipeOsVersion": "Ubuntu 24"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("body = %#v", got)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"recipeId":"reci-1"}`)
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/projects/proj/recipes/reci-1" {
				t.Fatalf("get = %s %s", request.Method, request.URL.Path)
			}
			_, _ = io.WriteString(w, `{"recipe":{"recipeId":"reci-1","recipeName":"recipe","status":"CREATED","createdBy":"actor"}}`)
		case 3:
			if request.Method != http.MethodDelete || request.URL.Path != "/projects/proj/recipes/reci-1" {
				t.Fatalf("delete = %s %s", request.Method, request.URL.Path)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"NOT_FOUND"}`)
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer server.Close()
	client := NewClient(testTransport(t, server.URL))
	id, err := client.CreateRecipe(context.Background(), "proj", CreateRecipeInput{Name: "recipe", Description: "description", Platform: "Linux", Architecture: "arm64", OSVersion: "Ubuntu 24"})
	if err != nil || id != "reci-1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	recipe, err := client.GetRecipe(context.Background(), "proj", id)
	if err != nil || recipe.ID != id || recipe.Status != "CREATED" || recipe.CreatedBy != "actor" {
		t.Fatalf("read = %#v, %v", recipe, err)
	}
	if err := client.ArchiveRecipe(context.Background(), "proj", id); err != nil {
		t.Fatal(err)
	}
}

func TestCreateRecipeReusesKeyAndBodyAfterLostResponse(t *testing.T) {
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		keys, bodies = append(keys, request.Header.Get("Idempotency-Key")), append(bodies, body)
		if len(keys) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"recipeId":"reci-1"}`)
	}))
	defer server.Close()
	client := NewClient(testTransport(t, server.URL))
	client.idempotencyKey = func() string { return "stable-key" }
	if _, err := client.CreateRecipe(context.Background(), "proj", CreateRecipeInput{Name: "recipe"}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "stable-key" || keys[1] != keys[0] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("keys/bodies = %q/%q", keys, bodies)
	}
}

func TestRecipeVersionClientLifecycle(t *testing.T) {
	calls := 0
	component := ComponentVersion{ComponentID: "cmp", ComponentName: "component", VersionID: "vers", VersionName: "1.0.0", Type: "MAIN", Order: 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		calls++
		path := "/projects/proj/recipes/reci/versions"
		if calls == 1 {
			if request.Method != http.MethodPost || request.URL.Path != path || request.Header.Get("Idempotency-Key") == "" {
				t.Fatalf("create = %s %s", request.Method, request.URL.Path)
			}
			var got map[string]any
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"configuredComponentsVersions": []any{map[string]any{"componentId": "cmp", "componentName": "component", "componentVersionId": "vers", "componentVersionName": "1.0.0", "componentVersionType": "MAIN", "order": float64(1)}}, "recipeVersionDescription": "description", "recipeVersionReleaseType": "MAJOR", "recipeVersionVolumeSize": "30", "recipeVersionIntegrations": []any{}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("create body = %#v", got)
			}
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
			return
		}
		if request.URL.Path != path+"/version" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("Idempotency-Key") != "" {
			t.Fatal("non-create sent idempotency key")
		}
		switch calls {
		case 2:
			if request.Method != http.MethodGet {
				t.Fatalf("method = %s", request.Method)
			}
			_, _ = io.WriteString(w, `{"recipe_version":{"recipeId":"reci","recipeVersionId":"version","configuredComponentsVersions":[],"effectiveComponentsVersions":[{"componentId":"cmp","componentName":"component","componentVersionId":"vers","componentVersionName":"1.0.0","componentVersionType":"MAIN","order":1}],"status":"VALIDATED"}}`)
		case 3:
			if request.Method != http.MethodPut {
				t.Fatalf("method = %s", request.Method)
			}
			var got map[string]any
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if _, ok := got["recipeVersionReleaseType"]; ok {
				t.Fatal("update sent release type")
			}
			if len(got) != 4 {
				t.Fatalf("update fields = %#v", got)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
		case 4:
			if request.Method != http.MethodDelete {
				t.Fatalf("method = %s", request.Method)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
		default:
			t.Fatalf("unexpected call %d", calls)
		}
	}))
	defer server.Close()
	client := NewClient(testTransport(t, server.URL))
	created, err := client.CreateRecipeVersion(context.Background(), "proj", "reci", CreateRecipeVersionInput{Components: []ComponentVersion{component}, Description: "description", ReleaseType: "MAJOR", VolumeSize: "30", Integrations: []string{}})
	if err != nil || created.ID != "version" || created.RetryAfter != 4*time.Second {
		t.Fatalf("create = %#v, %v", created, err)
	}
	version, err := client.GetRecipeVersion(context.Background(), "proj", "reci", "version")
	if err != nil || version.ID != "version" || version.Components == nil || len(*version.Components) != 0 || len(version.EffectiveComponents) != 1 {
		t.Fatalf("read = %#v, %v", version, err)
	}
	if _, err := client.UpdateRecipeVersion(context.Background(), "proj", "reci", "version", UpdateRecipeVersionInput{Components: []ComponentVersion{}, Description: "changed", VolumeSize: "30", Integrations: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RetireRecipeVersion(context.Background(), "proj", "reci", "version"); err != nil {
		t.Fatal(err)
	}
}

func TestGetRecipeVersionPreservesMissingConfiguredComponents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		_, _ = io.WriteString(w, `{"recipe_version":{"recipeVersionId":"version","effectiveComponentsVersions":[],"status":"VALIDATED"}}`)
	}))
	defer server.Close()
	version, err := NewClient(testTransport(t, server.URL)).GetRecipeVersion(context.Background(), "proj", "reci", "version")
	if err != nil || version.Components != nil {
		t.Fatalf("read = %#v, %v", version, err)
	}
}

func TestCreateRecipeVersionReusesKeyAndBodyAfterLostResponse(t *testing.T) {
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		keys, bodies = append(keys, request.Header.Get("Idempotency-Key")), append(bodies, body)
		if len(keys) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
	}))
	defer server.Close()
	client := NewClient(testTransport(t, server.URL))
	client.idempotencyKey = func() string { return "stable-key" }
	if _, err := client.CreateRecipeVersion(context.Background(), "proj", "reci", CreateRecipeVersionInput{Components: []ComponentVersion{}, Integrations: []string{}}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "stable-key" || keys[1] != keys[0] || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("keys/bodies = %q/%q", keys, bodies)
	}
}

func TestReleaseRecipeVersionMapsBodylessRequestAndAcceptsAlreadyReleased(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost || request.URL.Path != "/projects/proj/recipes/reci/versions/version/release" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if len(body) != 0 || request.Header.Get("Idempotency-Key") != "" {
			t.Fatalf("body/key = %q/%q", body, request.Header.Get("Idempotency-Key"))
		}
		_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
	}))
	defer server.Close()
	client := NewClient(testTransport(t, server.URL))
	if err := client.ReleaseRecipeVersion(context.Background(), "proj", "reci", "version"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReleaseRecipeVersion(context.Background(), "proj", "reci", "version"); err != nil {
		t.Fatalf("already released: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestReleaseRecipeVersionRejectsMismatchedResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		_, _ = io.WriteString(w, `{"recipeVersionId":"other-version"}`)
	}))
	defer server.Close()
	err := NewClient(testTransport(t, server.URL)).ReleaseRecipeVersion(context.Background(), "proj", "reci", "version")
	if err == nil || err.Error() != "VEW recipe version release response ID did not match requested version" {
		t.Fatalf("error = %v", err)
	}
}

func TestReleaseRecipeVersionReturnsVEWRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"INVALID_RECIPE_VERSION_STATUS","requestId":"request-1"}`)
	}))
	defer server.Close()
	err := NewClient(testTransport(t, server.URL)).ReleaseRecipeVersion(context.Background(), "proj", "reci", "version")
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Problem.Code != "INVALID_RECIPE_VERSION_STATUS" || apiErr.Problem.RequestID != "request-1" {
		t.Fatalf("error = %#v", err)
	}
}

func TestReleaseRecipeVersionRetriesAfterLostResponseWithoutCreateKey(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if tokenOrServe(w, request) {
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
		_, _ = io.WriteString(w, `{"recipeVersionId":"version"}`)
	}))
	defer server.Close()
	if err := NewClient(testTransport(t, server.URL)).ReleaseRecipeVersion(context.Background(), "proj", "reci", "version"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRecipeVersionBaseImageChannelJSON(t *testing.T) {
	body, err := json.Marshal(UpdateRecipeVersionInput{BaseImageChannel: "test"})
	if err != nil || !strings.Contains(string(body), "\"baseImageChannel\":\"test\"") {
		t.Fatalf("update body = %s (%v)", body, err)
	}
	var version RecipeVersion
	if err := json.Unmarshal([]byte("{\"baseImageChannel\":\"prod\"}"), &version); err != nil || version.BaseImageChannel == nil || *version.BaseImageChannel != "prod" {
		t.Fatalf("decoded channel = %v (%v)", version.BaseImageChannel, err)
	}
}
