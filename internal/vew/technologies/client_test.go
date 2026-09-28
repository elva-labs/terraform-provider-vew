package technologies

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func technologyTransport(t *testing.T, serverURL, scope string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransportWithScopes(vew.Config{
		APIURL: serverURL, TokenURL: serverURL + "/oauth/token", ClientID: "client", ClientSecret: "secret",
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func technologyToken(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/oauth/token" {
		return false
	}
	_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
	return true
}

func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	return NewClient(
		technologyTransport(t, serverURL, "clients/projects/technology.write"),
		technologyTransport(t, serverURL, "clients/projects/technology.read"),
	)
}

func TestCreateTechnologyReusesUUIDAndBodyAcrossRetries(t *testing.T) {
	const key = "9f135a8d-47f2-4a8a-bc2a-8cba77df05b6"
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if technologyToken(w, r) {
			return
		}
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/projects/project%2Fone/technologies" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/projects/technology.write" {
			t.Errorf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		bodies = append(bodies, body)
		if len(keys) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"code":"IDEMPOTENCY_REQUEST_IN_PROGRESS"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"technologyId":"tech-1"}`)
	}))
	defer server.Close()

	id, err := newTestClient(t, server.URL).CreateTechnology(context.Background(), "project/one", TechnologyInput{Name: "Compiler", Description: "safe desc"}, key)
	if err != nil || id != "tech-1" {
		t.Fatalf("CreateTechnology = %q, %v", id, err)
	}
	wantBody := []byte(`{"name":"Compiler","description":"safe desc"}`)
	if !reflect.DeepEqual(keys, []string{key, key}) || len(bodies) != 2 || !bytes.Equal(bodies[0], wantBody) || !bytes.Equal(bodies[1], wantBody) {
		t.Fatalf("retry requests used keys %q and bodies %q", keys, bodies)
	}
}

func TestTechnologyReadUsesReadTransportAndExactPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if technologyToken(w, r) {
			if got := r.FormValue("scope"); got != "clients/projects/technology.read" {
				t.Errorf("token scope = %q", got)
			}
			return
		}
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/projects/project%2Fone/technologies/tech%2Fone" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/projects/technology.read" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("read sent idempotency key %q", got)
		}
		_, _ = io.WriteString(w, `{"technologyId":"tech/one","projectId":"project/one","name":"Compiler","description":"description","createDate":"created","lastUpdateDate":"updated"}`)
	}))
	defer server.Close()

	got, err := newTestClient(t, server.URL).GetTechnology(context.Background(), "project/one", "tech/one")
	if err != nil {
		t.Fatal(err)
	}
	want := Technology{ID: "tech/one", ProjectID: "project/one", Name: "Compiler", Description: "description", CreatedAt: "created", UpdatedAt: "updated"}
	if got != want {
		t.Fatalf("technology = %#v, want %#v", got, want)
	}
}

func TestTechnologyUpdateAndDeleteUseWriteTransport(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if technologyToken(w, r) {
			if got := r.FormValue("scope"); got != "clients/projects/technology.write" {
				t.Errorf("token scope = %q", got)
			}
			return
		}
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		if got := r.Header.Get("Authorization"); got != "Bearer clients/projects/technology.write" {
			t.Errorf("authorization = %q", got)
		}
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(body, []byte(`{"name":"New","description":""}`)) {
				t.Errorf("update body = %q, %v", body, err)
			}
			// Empty success bodies are valid for the synchronous PUT contract.
		case http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"detail":"sensitive backend detail"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if err := client.UpdateTechnology(context.Background(), "project", "tech-1", TechnologyInput{Name: "New"}); err != nil {
		t.Fatalf("UpdateTechnology: %v", err)
	}
	if err := client.DeleteTechnology(context.Background(), "project", "tech-1"); err != nil {
		t.Fatalf("DeleteTechnology of absent technology: %v", err)
	}
	want := []string{"PUT /projects/project/technologies/tech-1", "DELETE /projects/project/technologies/tech-1"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %q, want %q", requests, want)
	}
}

func TestTechnologyAPIErrorsAreSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if technologyToken(w, r) {
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"TECHNOLOGY_IN_USE","requestId":"request-123","title":"private title","detail":"secret account parameters"}`)
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).GetTechnology(context.Background(), "project", "tech")
	var apiErr *vew.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Problem.Code != "TECHNOLOGY_IN_USE" || apiErr.Problem.RequestID != "request-123" {
		t.Fatalf("sanitized API error = %#v, %v", apiErr, err)
	}
	if apiErr.Problem.Title != "" || apiErr.Problem.Detail != "" || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private title") {
		t.Fatalf("error exposed backend detail: %v", err)
	}
}

func TestTechnologyMethodsRejectMissingInputsBeforeNetwork(t *testing.T) {
	client := NewClient(nil, nil)
	if _, err := client.CreateTechnology(context.Background(), "", TechnologyInput{Name: "name"}, "9f135a8d-47f2-4a8a-bc2a-8cba77df05b6"); err == nil {
		t.Fatal("CreateTechnology accepted empty project ID")
	}
	if _, err := client.CreateTechnology(context.Background(), "project", TechnologyInput{Name: "name"}, "not-a-uuid"); err == nil {
		t.Fatal("CreateTechnology accepted a non-UUID idempotency key")
	}
	if _, err := client.GetTechnology(context.Background(), "project", ""); err == nil {
		t.Fatal("GetTechnology accepted empty technology ID")
	}
	if err := client.UpdateTechnology(context.Background(), "project", "tech", TechnologyInput{}); err == nil {
		t.Fatal("UpdateTechnology accepted empty name")
	}
	if err := client.DeleteTechnology(context.Background(), "", "tech"); err == nil {
		t.Fatal("DeleteTechnology accepted empty project ID")
	}
}
