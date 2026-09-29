package projectaccounts

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

const (
	accountWriteScope = "clients/projects/account.write"
	accountReadScope  = "clients/projects/account.read"
)

func accountTestTransport(t *testing.T, baseURL, scope string) *vew.Transport {
	t.Helper()
	transport, err := vew.NewTransportWithScopes(vew.Config{APIURL: baseURL, TokenURL: baseURL + "/oauth/token", ClientID: "id", ClientSecret: "secret"}, scope)
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func accountToken(w http.ResponseWriter, request *http.Request) bool {
	if request.URL.Path != "/oauth/token" {
		return false
	}
	_, _ = io.WriteString(w, `{"access_token":"`+request.FormValue("scope")+`","expires_in":3600}`)
	return true
}

func testAccountClient(t *testing.T, serverURL string) *Client {
	return NewClient(accountTestTransport(t, serverURL, accountWriteScope), accountTestTransport(t, serverURL, accountReadScope))
}

func TestCreateAccountUsesWriteScopeExactRequestAndRetryAfter(t *testing.T) {
	const key = "9f135a8d-47f2-4a8a-bc2a-8cba77df05b6"
	input := AccountInput{AWSAccountID: "000000000000", AccountType: "USER", Name: "production", Description: "primary account", TechnologyID: "tech-1", Stage: "prod", Region: "eu-north-1"}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		calls++
		if request.Method != http.MethodPost || request.URL.EscapedPath() != "/projects/proj%2Fone/accounts" {
			t.Errorf("request = %s %s", request.Method, request.URL.EscapedPath())
		}
		if request.Header.Get("Authorization") != "Bearer "+accountWriteScope || request.Header.Get("Idempotency-Key") != key {
			t.Errorf("headers = %#v", request.Header)
		}
		var got map[string]any
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		want := map[string]any{"awsAccountId": "000000000000", "accountType": "USER", "name": "production", "description": "primary account", "technologyId": "tech-1", "stage": "prod", "region": "eu-north-1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("body = %#v", got)
		}
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accountId":"internal-account-id"}`)
	}))
	defer server.Close()
	result, err := testAccountClient(t, server.URL).CreateAccount(context.Background(), "proj/one", input, key)
	if err != nil || result.ID != "internal-account-id" || result.RetryAfter != 5*time.Second || calls != 1 {
		t.Fatalf("create = %#v, %v; calls=%d", result, err, calls)
	}
}

func TestCreateAccountRetryReusesKeyAndByteIdenticalBody(t *testing.T) {
	var keys []string
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		keys, bodies = append(keys, request.Header.Get("Idempotency-Key")), append(bodies, body)
		if len(keys) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accountId":"accepted-id"}`)
	}))
	defer server.Close()
	result, err := testAccountClient(t, server.URL).CreateAccount(context.Background(), "project", AccountInput{AWSAccountID: "000000000000", Name: "name"}, "2f135a8d-47f2-4a8a-bc2a-8cba77df05b6")
	if err != nil || result.ID != "accepted-id" {
		t.Fatalf("create = %#v, %v", result, err)
	}
	if !reflect.DeepEqual(keys, []string{"2f135a8d-47f2-4a8a-bc2a-8cba77df05b6", "2f135a8d-47f2-4a8a-bc2a-8cba77df05b6"}) || len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("retry keys/bodies = %q / %q", keys, bodies)
	}
}

func TestGetAccountUsesReadScopeDecodesSafeShapeAndRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		if request.Method != http.MethodGet || request.URL.EscapedPath() != "/projects/project%2Fone/accounts/internal%2Fid" {
			t.Errorf("request = %s %s", request.Method, request.URL.EscapedPath())
		}
		if request.Header.Get("Authorization") != "Bearer "+accountReadScope || request.Header.Get("Idempotency-Key") != "" {
			t.Errorf("headers = %#v", request.Header)
		}
		w.Header().Set("Retry-After", "3")
		_, _ = io.WriteString(w, `{"accountId":"internal/id","projectId":"project/one","awsAccountId":"000000000000","accountType":"USER","name":"prod","description":"primary","technologyId":"technology","stage":"prod","region":"eu-north-1","status":"OnBoarding","lastOnboardingResult":"Pending","lastOnboardingError":"","createDate":"2026-09-01T00:00:00Z","lastUpdateDate":"2026-09-02T00:00:00Z"}`)
	}))
	defer server.Close()
	account, err := testAccountClient(t, server.URL).GetAccount(context.Background(), "project/one", "internal/id")
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "internal/id" || account.ProjectID != "project/one" || account.AWSAccountID != "000000000000" || account.Name != "prod" || account.Status != "OnBoarding" || account.LastOnboardingResult != "Pending" || account.CreatedAt.IsZero() || account.UpdatedAt.IsZero() || account.RetryAfter != 3*time.Second {
		t.Fatalf("account = %#v", account)
	}
	if strings.Contains(strings.ToLower(strings.Join([]string{account.ID, account.Name, account.Description}, " ")), "unsafe") {
		t.Fatalf("unsafe parameters leaked into typed account: %#v", account)
	}
}

func TestUpdateAccountReturnsStableIDAndRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if accountToken(w, request) {
			return
		}
		if request.Method != http.MethodPut || request.URL.Path != "/projects/project/accounts/account-id" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var got map[string]any
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		want := map[string]any{"accountType": "TOOLCHAIN", "name": "updated", "description": "description", "technologyId": "technology", "stage": "qa", "region": "eu-west-1"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("update body = %#v", got)
		}
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"accountId":"account-id"}`)
	}))
	defer server.Close()
	result, err := testAccountClient(t, server.URL).UpdateAccount(context.Background(), "project", "account-id", UpdateAccountInput{AccountType: "TOOLCHAIN", Name: "updated", Description: "description", TechnologyID: "technology", Stage: "qa", Region: "eu-west-1"})
	if err != nil || result.ID != "account-id" || result.RetryAfter != 9*time.Second {
		t.Fatalf("update = %#v, %v", result, err)
	}
}

func TestDeactivateAccountTreatsMissingAsSuccessAndSanitizesAPIProblems(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if accountToken(w, request) {
					return
				}
				if request.Method != http.MethodDelete || request.URL.Path != "/projects/project/accounts/account-id" {
					t.Errorf("request = %s %s", request.Method, request.URL.Path)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"title":"unsafe title","detail":"sensitive account parameter: secret-value","code":"ACCOUNT_IN_USE","requestId":"req-123","retryable":true}`)
			}))
			defer server.Close()
			err := testAccountClient(t, server.URL).DeactivateAccount(context.Background(), "project", "account-id")
			if status == http.StatusNotFound {
				if err != nil {
					t.Fatalf("deactivate missing = %v", err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "unsafe title") {
				t.Fatalf("unsafe or missing error = %v", err)
			}
			var apiErr *vew.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != status || apiErr.Problem.Code != "ACCOUNT_IN_USE" || apiErr.Problem.RequestID != "req-123" || !apiErr.Problem.Retryable || apiErr.Problem.Detail != "" || apiErr.Problem.Title != "" {
				t.Fatalf("sanitized API error = %#v, %v", apiErr, err)
			}
		})
	}
}

func TestAccountMethodsRejectEmptyIDsBeforeNetwork(t *testing.T) {
	client := NewClient(nil, nil)
	if _, err := client.CreateAccount(context.Background(), "", AccountInput{}, "key"); err == nil {
		t.Fatal("accepted empty project ID on create")
	}
	if _, err := client.GetAccount(context.Background(), "project", ""); err == nil {
		t.Fatal("accepted empty account ID on read")
	}
	if _, err := client.UpdateAccount(context.Background(), "project", "", UpdateAccountInput{}); err == nil {
		t.Fatal("accepted empty account ID on update")
	}
	if err := client.DeactivateAccount(context.Background(), "project", ""); err == nil {
		t.Fatal("accepted empty account ID on deactivate")
	}
	for _, key := range []string{"", "not-a-uuid", "9f135a8d-47f2-4a8a-7cba-8cba77df05b6"} {
		if _, err := client.CreateAccount(context.Background(), "project", AccountInput{}, key); err == nil {
			t.Fatalf("accepted invalid idempotency key %q", key)
		}
	}
}

func TestSafeAccountErrorPreservesContextCancellation(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		err := safeAccountError("read", sentinel)
		if !errors.Is(err, sentinel) {
			t.Fatalf("safeAccountError(%v) = %v; errors.Is lost the sentinel", sentinel, err)
		}
	}
}
