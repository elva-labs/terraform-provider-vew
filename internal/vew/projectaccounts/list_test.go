package projectaccounts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAccountsUsesReadScopeAndDecodesOnboardingState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/projects/project-1/accounts" {
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+accountReadScope {
			t.Errorf("list must use the read scope, got %q", request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"accounts":[{"accountId":"a-1","projectId":"project-1","awsAccountId":"000000000000","status":"Inactive","onboardedAt":"2026-09-30T12:00:00+00:00","onboardingRevision":"r1"},{"accountId":"a-2","status":"OnBoarding"}]}`)
	}))
	defer server.Close()

	accounts, err := testAccountClient(t, server.URL).ListAccounts(context.Background(), "project-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].OnboardedAt != "2026-09-30T12:00:00+00:00" || accounts[0].OnboardingRevision != "r1" || accounts[1].OnboardedAt != "" {
		t.Fatalf("accounts=%#v", accounts)
	}
}

func TestListAccountsRejectsAnAccountWithoutID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		_, _ = io.WriteString(w, `{"accounts":[{"status":"Active"}]}`)
	}))
	defer server.Close()

	if _, err := testAccountClient(t, server.URL).ListAccounts(context.Background(), "project-1"); err == nil {
		t.Fatal("expected an error")
	}
}
