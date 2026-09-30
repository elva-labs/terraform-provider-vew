package baseimages

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

const released = `{"architecture":"amd64","channel":"prod","osVersion":"Base OS","parameterName":"/base/prod/amd64","status":"RELEASED","projectId":"p1","imageId":"image-1","amiId":"ami-1","previousAmiId":"ami-0"}`

func TestReleaseAndGet(t *testing.T) {
	var got []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(body))
		_, _ = io.WriteString(w, released)
	}))
	defer s.Close()
	c := NewClient(transport(t, s.URL, "clients/packaging/base_image.write"), transport(t, s.URL, "clients/packaging/base_image.read"))
	b, err := c.ReleaseBaseImage(context.Background(), "amd64", "prod", ReleaseInput{ProjectID: "p1", ImageID: "image-1"})
	if err != nil || *b.AmiID != "ami-1" || *b.PreviousAmiID != "ami-0" {
		t.Fatalf("release = %#v, %v", b, err)
	}
	if _, err := c.GetBaseImage(context.Background(), "amd64", "prod"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PUT /base-images/amd64/prod Bearer clients/packaging/base_image.write {"projectId":"p1","imageId":"image-1"}`,
		`GET /base-images/amd64/prod Bearer clients/packaging/base_image.read `,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEmptySegmentsAreRejected(t *testing.T) {
	c := NewClient(nil, nil)
	if _, err := c.GetBaseImage(context.Background(), "", "prod"); err == nil {
		t.Fatal("empty architecture accepted")
	}
}
