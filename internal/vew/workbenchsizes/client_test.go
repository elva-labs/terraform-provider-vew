package workbenchsizes

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func server(t *testing.T, responses map[string]string, status int, calls *[]recorded) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, recorded{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), string(body)})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, responses[r.Method])
	}))
}

func client(t *testing.T, url string) *Client {
	return NewClient(
		transport(t, url, "clients/provisioning/workbench_size.write"),
		transport(t, url, "clients/provisioning/workbench_size.read"),
	)
}

func TestPutListDelete(t *testing.T) {
	var calls []recorded
	s := server(t, map[string]string{
		http.MethodPut:    `{"grant":{"userId":"U1","sizes":["disk-500","standard-m"],"updatedBy":"t","updatedAt":"now"}}`,
		http.MethodGet:    `{"catalog":[{"key":"standard-m","kind":"size","label":"M","alwaysAllowed":false}],"grants":[{"userId":"U1","sizes":["standard-m"],"updatedBy":"t","updatedAt":"now"}]}`,
		http.MethodDelete: `{}`,
	}, http.StatusOK, &calls)
	defer s.Close()
	c := client(t, s.URL)
	ctx := context.Background()

	g, err := c.Put(ctx, "p1", "user@saab", []string{"standard-m", "disk-500"}, "user@saab")
	if err != nil || len(g.Sizes) != 2 {
		t.Fatalf("put = %#v, %v", g, err)
	}
	got, err := c.Get(ctx, "p1", "U1")
	if err != nil || got.Sizes[0] != "standard-m" {
		t.Fatalf("get = %#v, %v", got, err)
	}
	if _, err := c.Get(ctx, "p1", "U2"); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("missing grant = %v", err)
	}
	if err := c.Delete(ctx, "p1", "U1"); err != nil {
		t.Fatal(err)
	}

	if calls[0].method != http.MethodPut || calls[0].path != "/projects/p1/workbench-sizes/users/user@saab" {
		t.Fatalf("put call = %#v", calls[0])
	}
	if calls[0].auth != "Bearer clients/provisioning/workbench_size.write" {
		t.Fatalf("put scope = %s", calls[0].auth)
	}
	if !strings.Contains(calls[0].body, `"sizes":["standard-m","disk-500"]`) || !strings.Contains(calls[0].body, `"userEmail":"user@saab"`) {
		t.Fatalf("put body = %s", calls[0].body)
	}
	if calls[1].auth != "Bearer clients/provisioning/workbench_size.read" || calls[1].path != "/projects/p1/workbench-sizes" {
		t.Fatalf("get call = %#v", calls[1])
	}
	if calls[3].method != http.MethodDelete || calls[3].path != "/projects/p1/workbench-sizes/users/U1" {
		t.Fatalf("delete call = %#v", calls[3])
	}
}

func TestPutSendsAnEmptyListNotNull(t *testing.T) {
	var calls []recorded
	s := server(t, map[string]string{http.MethodPut: `{"grant":{"userId":"U1","sizes":[],"updatedBy":"t","updatedAt":"now"}}`}, http.StatusOK, &calls)
	defer s.Close()
	if _, err := client(t, s.URL).Put(context.Background(), "p1", "U1", nil, ""); err != nil {
		t.Fatal(err)
	}
	if calls[0].body != `{"sizes":[]}` {
		t.Fatalf("body = %s", calls[0].body)
	}
}

func TestDeleteIgnoresNotFound(t *testing.T) {
	var calls []recorded
	s := server(t, map[string]string{http.MethodDelete: `{"message":"not found"}`}, http.StatusNotFound, &calls)
	defer s.Close()
	if err := client(t, s.URL).Delete(context.Background(), "p1", "U1"); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyIDsAreRefused(t *testing.T) {
	c := NewClient(nil, nil)
	if _, err := c.Put(context.Background(), "p1", " ", nil, ""); err == nil {
		t.Fatal("empty user id accepted")
	}
	if _, err := c.List(context.Background(), ""); err == nil {
		t.Fatal("empty project id accepted")
	}
}
