package projectaccess

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
)

func TestClientPathsAndScopeIsolation(t *testing.T) {
	scopes := map[string]string{"POST /projects": "program.write", "GET /projects/proj-1": "program.read", "PUT /projects/proj-1": "program.write", "DELETE /projects/proj-1": "program.write", "POST /projects/proj-1/users": "assignment.write", "GET /projects/proj-1/users/ABC-123": "assignment.read", "PUT /projects/proj-1/users/ABC-123": "assignment.write", "DELETE /projects/proj-1/users/ABC-123": "assignment.write", "PUT /projects/proj-1/groups/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": "group_assignment.write", "GET /projects/proj-1/groups/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": "group_assignment.read", "DELETE /projects/proj-1/groups/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": "group_assignment.write", "PUT /projects/proj-1/clients/client-1": "client_assignment.write", "GET /projects/proj-1/clients/client-1": "client_assignment.read", "DELETE /projects/proj-1/clients/client-1": "client_assignment.write"}
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = io.WriteString(w, `{"access_token":"`+r.FormValue("scope")+`","expires_in":3600}`)
			return
		}
		key := r.Method + " " + r.URL.Path
		want, ok := scopes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			w.WriteHeader(404)
			return
		}
		seen[key] = true
		if got := r.Header.Get("Authorization"); got != "Bearer clients/projects/"+want {
			t.Errorf("%s authorization=%q", key, got)
		}
		switch key {
		case "POST /projects":
			if r.Header.Get("Idempotency-Key") != "stable-key" {
				t.Errorf("missing idempotency key")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			// An unset description is omitted, not sent as null.
			if !reflect.DeepEqual(body, map[string]any{"name": "name", "isActive": true}) {
				t.Errorf("project body %#v", body)
			}
			_, _ = io.WriteString(w, `{"projectId":"proj-1"}`)
		case "GET /projects/proj-1":
			_, _ = io.WriteString(w, `{"projectId":"proj-1","projectName":"name","projectDescription":null,"isActive":false,"createDate":"created","lastUpdateDate":"updated"}`)
		case "GET /projects/proj-1/users/ABC-123":
			_, _ = io.WriteString(w, `{"projectId":"proj-1","userId":"ABC-123","roles":["ADMIN"],"userEmail":"a@example.test"}`)
		case "GET /projects/proj-1/groups/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee":
			_, _ = io.WriteString(w, `{"projectId":"proj-1","groupId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","roles":["ADMIN"]}`)
		case "GET /projects/proj-1/clients/client-1":
			_, _ = io.WriteString(w, `{"assignment":{"projectId":"proj-1","clientId":"client-1","status":"REVOKED"}}`)
		}
	}))
	defer server.Close()
	pair := func(s string) Pair {
		cfg := vew.Config{APIURL: server.URL, TokenURL: server.URL + "/oauth/token", ClientID: "id", ClientSecret: "secret"}
		w, e := vew.NewTransportWithScopes(cfg, "clients/projects/"+s+".write")
		if e != nil {
			t.Fatal(e)
		}
		r, e := vew.NewTransportWithScopes(cfg, "clients/projects/"+s+".read")
		if e != nil {
			t.Fatal(e)
		}
		return Pair{Write: w, Read: r}
	}
	c := NewClient(pair("program"), pair("assignment"), pair("group_assignment"), pair("client_assignment"))
	ctx := context.Background()
	pid := "proj-1"
	uid := "ABC-123"
	gid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	cid := "client-1"
	if id, e := c.CreateProject(ctx, ProjectInput{Name: "name", IsActive: true}, "stable-key"); e != nil || id != pid {
		t.Fatalf("create project %q %v", id, e)
	}
	if v, e := c.GetProject(ctx, pid); e != nil || v.IsActive || v.Name != "name" {
		t.Fatalf("read project %#v %v", v, e)
	}
	if e := c.UpdateProject(ctx, pid, ProjectInput{Name: "name", IsActive: true}); e != nil {
		t.Fatal(e)
	}
	if e := c.DeactivateProject(ctx, pid); e != nil {
		t.Fatal(e)
	}
	if e := c.CreateUser(ctx, pid, uid, UserInput{Roles: []string{"ADMIN"}}); e != nil {
		t.Fatal(e)
	}
	if v, e := c.GetUser(ctx, pid, uid); e != nil || v.UserID != uid {
		t.Fatalf("read user %#v %v", v, e)
	}
	if e := c.UpdateUser(ctx, pid, uid, UserInput{Roles: []string{"ADMIN"}}); e != nil {
		t.Fatal(e)
	}
	if e := c.DeleteUser(ctx, pid, uid); e != nil {
		t.Fatal(e)
	}
	if e := c.PutGroup(ctx, pid, gid, GroupInput{Roles: []string{"ADMIN"}}); e != nil {
		t.Fatal(e)
	}
	if v, e := c.GetGroup(ctx, pid, gid); e != nil || v.GroupID != gid {
		t.Fatalf("read group %#v %v", v, e)
	}
	if e := c.DeleteGroup(ctx, pid, gid); e != nil {
		t.Fatal(e)
	}
	if e := c.ActivateClient(ctx, pid, cid); e != nil {
		t.Fatal(e)
	}
	if v, e := c.GetClient(ctx, pid, cid); e != nil || v.Status != "REVOKED" {
		t.Fatalf("read client %#v %v", v, e)
	}
	if e := c.RevokeClient(ctx, pid, cid); e != nil {
		t.Fatal(e)
	}
	if len(seen) != len(scopes) {
		t.Fatalf("saw %d of %d routes", len(seen), len(scopes))
	}
}

func TestProjectInputOmitsUnsetDescription(t *testing.T) {
	description := "text"
	off := false
	workbenchOnly := "workbench-only"
	for _, tc := range []struct {
		input ProjectInput
		want  string
	}{
		{ProjectInput{Name: "name", IsActive: true}, `{"name":"name","isActive":true}`},
		{ProjectInput{Name: "name", Description: &description, IsActive: true}, `{"name":"name","description":"text","isActive":true}`},
		// remoteSupportEnabled is sent only when set; unset keeps the project's value.
		{ProjectInput{Name: "name", IsActive: true, RemoteSupportEnabled: &off}, `{"name":"name","isActive":true,"remoteSupportEnabled":false}`},
		// experience is sent only when set; unset keeps the project's value.
		{ProjectInput{Name: "name", IsActive: true, Experience: &workbenchOnly}, `{"name":"name","isActive":true,"experience":"workbench-only"}`},
	} {
		got, err := json.Marshal(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("got %s, want %s", got, tc.want)
		}
	}
}
