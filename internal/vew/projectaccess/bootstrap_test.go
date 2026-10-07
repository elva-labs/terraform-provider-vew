package projectaccess

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func TestBootstrapDoesNotRetryOrRead(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"success", `{"assignment":{"projectId":"project","clientId":"manager","status":"ACTIVE"}}`, 200, false},
		{"missing", `{}`, 200, true},
		{"invalid", `not-json`, 200, true},
		{"server failure", `{"detail":"sensitive"}`, 500, true},
		{"rate limited", `{"detail":"sensitive"}`, 429, true},
		{"retryable conflict", `{"code":"IDEMPOTENCY_REQUEST_IN_PROGRESS"}`, 409, true},
		{"forbidden", `{"detail":"sensitive"}`, 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			puts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					if got := r.FormValue("scope"); got != "clients/projects/client_assignment.write clients/projects/client_assignment.bootstrap" {
						t.Errorf("scope=%q", got)
					}
					fmt.Fprint(w, `{"access_token":"test-token","expires_in":3600}`)
					return
				}
				if r.Method != "PUT" || r.URL.Path != "/projects/project/clients/manager" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				puts++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			transport, err := vew.NewTransportWithScopes(vew.Config{APIURL: server.URL, TokenURL: server.URL + "/token", ClientID: "recovery", ClientSecret: "test-secret"}, "clients/projects/client_assignment.write", "clients/projects/client_assignment.bootstrap")
			if err != nil {
				t.Fatal(err)
			}
			_, err = NewBootstrapClient(transport).AssignClient(context.Background(), "project", "manager")
			if (err != nil) != tc.wantError || puts != 1 {
				t.Fatalf("err=%v puts=%d", err, puts)
			}
		})
	}
}
