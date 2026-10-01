package mandatorycomponents

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

const listBody = `{"platform":"Linux","osVersion":"Golden Ubuntu","architecture":"amd64",` +
	`"prependedComponentsVersions":[{"componentId":"c1","componentName":"marker","componentVersionId":"v1","componentVersionName":"1.0.0","order":1}],` +
	`"appendedComponentsVersions":[],"lastUpdateDate":"2026-10-01T00:00:00+00:00","lastUpdatedBy":"service:client"}`

func TestPutGetAndDelete(t *testing.T) {
	var got []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("Authorization")+" "+string(body))
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.WriteString(w, listBody)
	}))
	defer s.Close()
	c := NewClient(
		transport(t, s.URL, "clients/packaging/mandatory_components_list.write"),
		transport(t, s.URL, "clients/packaging/mandatory_components_list.read"),
	)
	key := Key{Platform: "Linux", OSVersion: "Golden Ubuntu", Architecture: "amd64"}
	list, err := c.PutList(context.Background(), key, PutInput{ProjectID: "p1", Prepended: []ComponentRef{{ComponentID: "c1", ComponentVersionID: "v1"}}})
	if err != nil || len(list.Prepended) != 1 || list.Prepended[0].ComponentName != "marker" {
		t.Fatalf("put = %#v, %v", list, err)
	}
	if _, err := c.GetList(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteList(context.Background(), key, "p1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PUT /mandatory-components-lists/Linux/Golden%20Ubuntu/amd64 Bearer clients/packaging/mandatory_components_list.write {"projectId":"p1","prependedComponentsVersions":[{"componentId":"c1","componentVersionId":"v1"}],"appendedComponentsVersions":[]}`,
		`GET /mandatory-components-lists/Linux/Golden%20Ubuntu/amd64 Bearer clients/packaging/mandatory_components_list.read `,
		`DELETE /mandatory-components-lists/Linux/Golden%20Ubuntu/amd64 Bearer clients/packaging/mandatory_components_list.write {"projectId":"p1"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeleteOfAMissingListIsNotAnError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"t","expires_in":3600}`)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"NOT_FOUND","detail":"gone"}`)
	}))
	defer s.Close()
	c := NewClient(transport(t, s.URL, "w"), transport(t, s.URL, "r"))
	if err := c.DeleteList(context.Background(), Key{Platform: "Linux", OSVersion: "X", Architecture: "amd64"}, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetList(context.Background(), Key{Platform: "Linux", OSVersion: "X", Architecture: "amd64"}); !vew.IsNotFound(err) {
		t.Fatalf("get of a missing list = %v", err)
	}
}

func TestEmptyKeyIsRejected(t *testing.T) {
	c := NewClient(nil, nil)
	if _, err := c.GetList(context.Background(), Key{Platform: "Linux"}); err == nil {
		t.Fatal("expected an error for an empty key")
	}
}
