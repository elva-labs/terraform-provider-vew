package projectaccess_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The real Terraform CLI talks only to this loopback implementation of the
// scope, project-access, orphan, and self-assignment policies.
type accessServer struct {
	server              *httptest.Server
	mu                  sync.Mutex
	assignments         map[string]string
	puts, gets, deletes int
	recoveryReads       int
}

func newAccessServer(t *testing.T) *accessServer {
	f := &accessServer{assignments: map[string]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}
func (f *accessServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/token" {
		client := r.FormValue("client_id")
		if client == "" {
			client, _, _ = r.BasicAuth()
		}
		token := base64.RawURLEncoding.EncodeToString([]byte(client + "|" + r.FormValue("scope")))
		json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 3600})
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	parts := strings.SplitN(string(decoded), "|", 2)
	if err != nil || len(parts) != 2 {
		w.WriteHeader(401)
		return
	}
	caller, scopes := parts[0], strings.Fields(parts[1])
	has := func(scope string) bool {
		for _, got := range scopes {
			if got == "clients/projects/"+scope {
				return true
			}
		}
		return false
	}
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/projects/"), "/")
	if len(path) != 3 || path[0] != "project" || path[1] != "clients" {
		w.WriteHeader(404)
		return
	}
	target := path[2]
	f.mu.Lock()
	defer f.mu.Unlock()
	allowed := f.assignments[caller] == "ACTIVE"
	switch r.Method {
	case "PUT":
		f.puts++
		orphan := true
		for _, status := range f.assignments {
			if status == "ACTIVE" {
				orphan = false
			}
		}
		if caller == target || !has("client_assignment.write") || !(allowed || has("client_assignment.bootstrap") && orphan) {
			w.WriteHeader(403)
			return
		}
		f.assignments[target] = "ACTIVE"
	case "GET":
		f.gets++
		if caller == "recovery" {
			f.recoveryReads++
		}
		if !has("client_assignment.read") || !allowed {
			w.WriteHeader(403)
			return
		}
		if _, exists := f.assignments[target]; !exists {
			w.WriteHeader(404)
			return
		}
	case "DELETE":
		f.deletes++
		if !has("client_assignment.write") || !allowed {
			w.WriteHeader(403)
			return
		}
		f.assignments[target] = "REVOKED"
	default:
		w.WriteHeader(405)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"assignment": map[string]string{"projectId": "project", "clientId": target, "status": f.assignments[target]}})
}

type accessCLI struct {
	t              *testing.T
	dir, terraform string
	env            []string
}

func newAccessCLI(t *testing.T) *accessCLI {
	t.Helper()
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform 1.16 or later required")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "terraform-provider-vew"), "./cmd/terraform-provider-vew")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, out)
	}
	rc := filepath.Join(dir, "terraform.rc")
	if err := os.WriteFile(rc, []byte(fmt.Sprintf("provider_installation {\n dev_overrides {\n \"elva-labs/vew\" = %q\n }\n direct {}\n}\n", bin)), 0600); err != nil {
		t.Fatal(err)
	}
	return &accessCLI{t: t, dir: dir, terraform: terraform, env: append(os.Environ(), "TF_CLI_CONFIG_FILE="+rc, "TF_IN_AUTOMATION=1")}
}
func (c *accessCLI) config(text string) {
	c.t.Helper()
	if err := os.WriteFile(filepath.Join(c.dir, "main.tf"), []byte("terraform {\n required_providers {\n vew = { source = \"elva-labs/vew\" }\n }\n}\n"+text), 0600); err != nil {
		c.t.Fatal(err)
	}
}
func (c *accessCLI) run(wantError bool, args ...string) string {
	c.t.Helper()
	cmd := exec.Command(c.terraform, args...)
	cmd.Dir = c.dir
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	if (err != nil) != wantError {
		c.t.Fatalf("terraform %v: %v\n%s", args, err, out)
	}
	return string(out)
}
func TestBootstrapAndManagementLifecycleCLI(t *testing.T) {
	f := newAccessServer(t)
	c := newAccessCLI(t)
	providerConfig := func(client string, bootstrap bool) string {
		return fmt.Sprintf("provider \"vew\" {\n api_url = %q\n projects_api_url = %q\n token_url = %q\n client_id = %q\n client_secret = \"test-secret\"\n project_client_bootstrap = %t\n}\n", f.server.URL, f.server.URL, f.server.URL+"/token", client, bootstrap)
	}
	c.config(providerConfig("recovery", true) + `
action "vew_project_client_bootstrap" "manager" {
  config {
    project_id = "project"
    client_id = "manager"
  }
}
`)
	c.run(false, "validate", "-no-color")
	c.run(false, "plan", "-invoke=action.vew_project_client_bootstrap.manager", "-input=false", "-no-color")
	c.run(false, "apply", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	before := f.puts
	f.mu.Unlock()
	if before != 0 {
		t.Fatal("planning or declaring the action made a grant")
	}
	c.run(false, "apply", "-invoke=action.vew_project_client_bootstrap.manager", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	if f.assignments["manager"] != "ACTIVE" || f.puts != 1 || f.gets != 0 || f.deletes != 0 {
		t.Errorf("bootstrap made unexpected requests: %+v", f.assignments)
	}
	f.mu.Unlock()
	output := c.run(true, "apply", "-invoke=action.vew_project_client_bootstrap.manager", "-auto-approve", "-input=false", "-no-color")
	if !strings.Contains(output, "HTTP 403") {
		t.Fatalf("repeat invocation diagnostic: %s", output)
	}
	c.run(false, "destroy", "-auto-approve", "-input=false", "-no-color")
	c.config(providerConfig("manager", false) + `
resource "vew_project_client_assignment" "packaging" {
  project_id = "project"
  client_id = "packaging"
}
`)
	c.run(false, "apply", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	if f.assignments["packaging"] != "ACTIVE" {
		t.Error("manager did not grant packaging access")
	}
	f.assignments["packaging"] = "REVOKED"
	f.mu.Unlock()
	c.run(false, "apply", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	if f.assignments["packaging"] != "ACTIVE" {
		t.Error("manager did not repair revoked assignment")
	}
	f.mu.Unlock()
	c.run(false, "destroy", "-auto-approve", "-input=false", "-no-color")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assignments["manager"] != "ACTIVE" || f.assignments["packaging"] != "REVOKED" || f.recoveryReads != 0 || f.deletes != 1 {
		t.Fatalf("incorrect final lifecycle: assignments=%v recoveryReads=%d deletes=%d", f.assignments, f.recoveryReads, f.deletes)
	}
}
