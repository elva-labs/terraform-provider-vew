package products

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func newTestRetirementClient(t *testing.T, serverURL string) *RetirementClient {
	t.Helper()
	return NewRetirementClient(
		productTransport(t, serverURL, "clients/publishing/version.retire"),
		productTransport(t, serverURL, "clients/publishing/version.read"),
	)
}

const retirementBody = `{"projectId":"project","productId":"prod-1","versionId":"vers-1","versionName":"1.0.0","status":"RETIRING","retireReason":"Superseded","distributions":[{"awsAccountId":"123456789012","region":"eu-north-1","status":"RETIRING"}]}`

func TestRetireVersionUsesRetireScopePathAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || r.URL.Path != "/projects/project/products/prod-1/versions/vers-1/retirement" || string(body) != `{"reason":"Superseded","force":true}` {
			t.Errorf("request = %s %s %q", r.Method, r.URL.Path, body)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/version.retire" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, retirementBody)
	}))
	defer server.Close()
	retirement, err := newTestRetirementClient(t, server.URL).RetireVersion(context.Background(), "project", "prod-1", "vers-1", RetireRequest{Reason: "Superseded", Force: true})
	if err != nil || retirement.Status != "RETIRING" || retirement.RetireReason != "Superseded" || len(retirement.Distributions) != 1 {
		t.Fatalf("RetireVersion = %#v, %v", retirement, err)
	}
}

func TestRetireVersionWithoutOptionsSendsAnEmptyObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		if body, _ := io.ReadAll(r.Body); string(body) != `{}` {
			t.Errorf("body = %q", body)
		}
		_, _ = io.WriteString(w, retirementBody)
	}))
	defer server.Close()
	if _, err := newTestRetirementClient(t, server.URL).RetireVersion(context.Background(), "project", "prod-1", "vers-1", RetireRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRetireVersionKeepsTheInUseProblemCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"type":"https://x/version-in-use","title":"Conflict","status":409,"detail":"2 workbench(es) are still on this version","code":"VERSION_IN_USE","retryable":false,"requestId":"req-1"}`)
	}))
	defer server.Close()
	_, err := newTestRetirementClient(t, server.URL).RetireVersion(context.Background(), "project", "prod-1", "vers-1", RetireRequest{})
	apiError, ok := err.(*vew.APIError)
	if !ok || apiError.Status != http.StatusConflict || apiError.Problem.Code != "VERSION_IN_USE" {
		t.Fatalf("err = %#v", err)
	}
}

func TestGetRetirementUsesReadScopeAndForgetIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/version.read" {
				t.Errorf("authorization = %q", got)
			}
			_, _ = io.WriteString(w, retirementBody)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"https://x/not-found","title":"Not Found","status":404,"detail":"missing","code":"NOT_FOUND","retryable":false}`)
		default:
			t.Errorf("method = %s", r.Method)
		}
	}))
	defer server.Close()
	client := newTestRetirementClient(t, server.URL)
	if retirement, err := client.GetRetirement(context.Background(), "project", "prod-1", "vers-1"); err != nil || retirement.VersionName != "1.0.0" {
		t.Fatalf("GetRetirement = %#v, %v", retirement, err)
	}
	if err := client.ForgetRetirement(context.Background(), "project", "prod-1", "vers-1"); err != nil {
		t.Fatalf("ForgetRetirement = %v", err)
	}
}

func TestRetirementRejectsEmptyIdentifiers(t *testing.T) {
	client := NewRetirementClient(nil, nil)
	if _, err := client.GetRetirement(context.Background(), "project", "prod-1", " "); err == nil {
		t.Fatal("expected an error for an empty version ID")
	}
}
