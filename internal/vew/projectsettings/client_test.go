package projectsettings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func transport(t *testing.T, serverURL, scope string) *vew.Transport {
	t.Helper()
	tr, err := vew.NewTransportWithScopes(vew.Config{
		APIURL: serverURL, TokenURL: serverURL + "/oauth/token", ClientID: "client", ClientSecret: "secret",
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

type recorded struct{ method, path, auth, body string }

func server(t *testing.T, status int, response string, calls *[]recorded) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, recorded{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), string(body)})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
}

func client(t *testing.T, url string) *Client {
	return NewClient(transport(t, url, "clients/projects/program.write"), transport(t, url, "clients/projects/program.read"))
}

func TestManagementPutGetDelete(t *testing.T) {
	var calls []recorded
	s := server(t, http.StatusOK, `{"projectId":"p1","managedBy":"terraform","source":"repo/path"}`, &calls)
	defer s.Close()
	c := client(t, s.URL)
	ctx := context.Background()
	if err := c.PutManagement(ctx, "p1", ManagementInput{ManagedBy: "terraform", Source: "repo/path"}); err != nil {
		t.Fatal(err)
	}
	m, err := c.GetManagement(ctx, "p1")
	if err != nil || m.Source != "repo/path" {
		t.Fatalf("get = %#v, %v", m, err)
	}
	if err := c.DeleteManagement(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	want := []recorded{
		{"PUT", "/projects/p1/management", "Bearer clients/projects/program.write", `{"managedBy":"terraform","source":"repo/path"}`},
		{"GET", "/projects/p1/management", "Bearer clients/projects/program.read", ""},
		{"DELETE", "/projects/p1/management", "Bearer clients/projects/program.write", ""},
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("call %d = %#v, want %#v", i, calls[i], w)
		}
	}
}

func TestWorkbenchLifecycleOmitsUnsetDefaults(t *testing.T) {
	var calls []recorded
	s := server(t, http.StatusOK, `{"projectId":"p1","alwaysOn":false,"idleStopMinutes":null,"nightlyStop":null,"weekendStop":null,"allowUserDisableNightlyStop":true,"allowUserIdleTimeout":true,"userIdleTimeoutMinMinutes":10,"userIdleTimeoutMaxMinutes":480}`, &calls)
	defer s.Close()
	c := client(t, s.URL)
	input := WorkbenchLifecycle{AllowUserDisableNightlyStop: true, AllowUserIdleTimeout: true, UserIdleTimeoutMinMinutes: 10, UserIdleTimeoutMaxMinutes: 480}
	if err := c.PutWorkbenchLifecycle(context.Background(), "p1", input); err != nil {
		t.Fatal(err)
	}
	if want := `{"alwaysOn":false,"allowUserDisableNightlyStop":true,"allowUserIdleTimeout":true,"userIdleTimeoutMinMinutes":10,"userIdleTimeoutMaxMinutes":480}`; calls[0].body != want {
		t.Fatalf("body = %s", calls[0].body)
	}
	got, err := c.GetWorkbenchLifecycle(context.Background(), "p1")
	if err != nil || got.IdleStopMinutes != nil || got.NightlyStop != nil || got.UserIdleTimeoutMinMinutes != 10 {
		t.Fatalf("get = %#v, %v", got, err)
	}
}

func TestDeleteTreatsNotFoundAsDone(t *testing.T) {
	var calls []recorded
	s := server(t, http.StatusNotFound, `{"code":"NOT_FOUND"}`, &calls)
	defer s.Close()
	c := client(t, s.URL)
	if err := c.DeleteWorkbenchLifecycle(context.Background(), "p1"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := c.GetManagement(context.Background(), "p1"); !vew.IsNotFound(err) {
		t.Fatalf("get = %v, want not found", err)
	}
}
