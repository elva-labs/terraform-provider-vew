package products

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestVersionClient(t *testing.T, serverURL string) *VersionClient {
	t.Helper()
	return NewVersionClient(
		productTransport(t, serverURL, "clients/publishing/version.promote"),
		productTransport(t, serverURL, "clients/publishing/version.read"),
	)
}

const promotionBody = `{"projectId":"project","productId":"prod-1","versionId":"vers-1","versionName":"1.0.0","stage":"PROD","status":"CREATING","distributions":[{"awsAccountId":"123456789012","region":"eu-north-1","status":"CREATING"}]}`

func TestPromoteVersionUsesPromoteScopeAndPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || r.URL.Path != "/projects/project/products/prod-1/versions/vers-1/stages/PROD" || len(body) != 0 {
			t.Errorf("request = %s %s %q", r.Method, r.URL.Path, body)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/version.promote" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, promotionBody)
	}))
	defer server.Close()
	promotion, err := newTestVersionClient(t, server.URL).PromoteVersion(context.Background(), "project", "prod-1", "vers-1", "PROD")
	if err != nil || promotion.Status != "CREATING" || len(promotion.Distributions) != 1 || promotion.Distributions[0].AWSAccountID != "123456789012" {
		t.Fatalf("PromoteVersion = %#v, %v", promotion, err)
	}
}

func TestGetPromotionAndListVersionsUseReadScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if productToken(w, r) {
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer clients/publishing/version.read" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/projects/project/products/prod-1/versions":
			_, _ = io.WriteString(w, `{"versions":[{"versionId":"vers-1","versionName":"1.0.0-rc.1","versionType":"RELEASE_CANDIDATE","stages":[{"stage":"DEV","status":"CREATED"}]}]}`)
		case "/projects/project/products/prod-1/versions/vers-1/stages/PROD":
			_, _ = io.WriteString(w, promotionBody)
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestVersionClient(t, server.URL)
	versions, err := client.ListProductVersions(context.Background(), "project", "prod-1")
	if err != nil || len(versions) != 1 || versions[0].Stages[0].Stage != "DEV" {
		t.Fatalf("ListProductVersions = %#v, %v", versions, err)
	}
	promotion, err := client.GetPromotion(context.Background(), "project", "prod-1", "vers-1", "PROD")
	if err != nil || promotion.VersionName != "1.0.0" {
		t.Fatalf("GetPromotion = %#v, %v", promotion, err)
	}
}

func TestForgetPromotionToleratesMissing(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if productToken(w, r) {
				return
			}
			if r.Method != http.MethodDelete {
				t.Errorf("method = %s", r.Method)
			}
			w.WriteHeader(status)
		}))
		if err := newTestVersionClient(t, server.URL).ForgetPromotion(context.Background(), "project", "prod-1", "vers-1", "DEV"); err != nil {
			t.Fatalf("ForgetPromotion(%d) = %v", status, err)
		}
		server.Close()
	}
}

func TestPromotionRejectsInvalidStage(t *testing.T) {
	client := NewVersionClient(nil, nil)
	if _, err := client.PromoteVersion(context.Background(), "project", "prod-1", "vers-1", "prod"); err == nil {
		t.Fatal("expected invalid stage error")
	}
	if _, err := client.GetPromotion(context.Background(), "project", "prod-1", " ", "PROD"); err == nil {
		t.Fatal("expected empty version error")
	}
}
