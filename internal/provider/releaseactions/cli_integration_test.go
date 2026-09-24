package releaseactions_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type terraformCLIWorkspace struct {
	t             *testing.T
	terraFormPath string
	dir           string
	env           []string
}

func newTerraformCLIWorkspace(t *testing.T, config string) *terraformCLIWorkspace {
	t.Helper()
	terraformPath, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform 1.16.3 is required for release-action CLI integration coverage")
	}
	versionOutput, err := exec.Command(terraformPath, "version", "-json").Output()
	if err != nil {
		t.Fatalf("read Terraform version: %v", err)
	}
	var version struct {
		TerraformVersion string `json:"terraform_version"`
	}
	if json.Unmarshal(versionOutput, &version) != nil || version.TerraformVersion != "1.16.3" {
		t.Skipf("release-action CLI integration coverage is pinned to Terraform 1.16.3, found %q", version.TerraformVersion)
	}

	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mirrorDir := filepath.Join(root, "mirror", "registry.terraform.io", "elva-labs", "vew", "0.1.0", runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	providerBinary := filepath.Join(mirrorDir, "terraform-provider-vew_v0.1.0")
	build := exec.Command("go", "build", "-o", providerBinary, "./cmd/terraform-provider-vew")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOCACHE="+filepath.Join(root, "go-build"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, output)
	}

	cliConfig := fmt.Sprintf(`provider_installation {
  filesystem_mirror {
    path    = %q
    include = ["registry.terraform.io/elva-labs/vew"]
  }
  direct {
    exclude = ["registry.terraform.io/elva-labs/vew"]
  }
}
`, filepath.Join(root, "mirror"))
	cliConfigPath := filepath.Join(root, "terraform.rc")
	if err := os.WriteFile(cliConfigPath, []byte(cliConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := `terraform {
  required_providers {
    vew = {
      source  = "elva-labs/vew"
      version = "0.1.0"
    }
  }
}
` + config
	if err := os.WriteFile(filepath.Join(root, "main.tf"), []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := &terraformCLIWorkspace{
		t: t, terraFormPath: terraformPath, dir: root,
		env: append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfigPath, "TF_IN_AUTOMATION=1"),
	}
	workspace.run("init", "-backend=false", "-input=false", "-no-color")
	return workspace
}

func (w *terraformCLIWorkspace) run(arguments ...string) string {
	w.t.Helper()
	command := exec.Command(w.terraFormPath, arguments...)
	command.Dir = w.dir
	command.Env = w.env
	output, err := command.CombinedOutput()
	if err != nil {
		w.t.Fatalf("terraform %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func TestDirectInvokeExcludesUnrelatedPipelineProtocol(t *testing.T) {
	fake := newReleaseProtocolServer(t)
	workspace := newTerraformCLIWorkspace(t, fake.providerConfig()+`
action "vew_component_version_release" "manual" {
  config {
    project_id   = "project"
    component_id = "component"
    version_id   = "version"
  }
}

resource "vew_pipeline" "unrelated" {
  project_id           = "project"
  name                 = "must-not-apply"
  description          = "direct action invocation excludes resources"
  recipe_id            = "recipe"
  recipe_version_id    = "recipe-version"
  build_instance_types = ["m8i.2xlarge"]
  schedule             = "0 0 * * ? *"
}
`)
	workspace.run("apply", "-invoke=action.vew_component_version_release.manual", "-auto-approve", "-input=false", "-no-color")
	_, _, releases, pipelines := fake.snapshot()
	if releases != 1 || pipelines != 0 {
		t.Fatalf("direct invocation release/pipeline calls = %d/%d, want 1/0", releases, pipelines)
	}
}

var releaseAcceptanceGateNames = []string{
	"TF_ACC",
	"VEW_ACC_RELEASE_ACTIONS",
	"VEW_API_URL",
	"VEW_TOKEN_URL",
	"VEW_CLIENT_ID",
	"VEW_CLIENT_SECRET",
	"VEW_TEST_PROJECT_ID",
	"VEW_TEST_COMPONENT_ID",
	"VEW_TEST_COMPONENT_VERSION_ID",
	"VEW_TEST_RECIPE_ID",
	"VEW_TEST_RECIPE_VERSION_ID",
}

func missingReleaseAcceptanceGates(getenv func(string) string) []string {
	var missing []string
	for _, name := range releaseAcceptanceGateNames {
		value := strings.TrimSpace(getenv(name))
		if (name == "TF_ACC" || name == "VEW_ACC_RELEASE_ACTIONS") && value != "1" || value == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestReleaseAcceptanceGateDocumentsDisposableFixtureContract(t *testing.T) {
	missing := missingReleaseAcceptanceGates(func(name string) string {
		if name == "TF_ACC" || name == "VEW_ACC_RELEASE_ACTIONS" {
			return "1"
		}
		if name == "VEW_TEST_COMPONENT_VERSION_ID" || name == "VEW_TEST_RECIPE_VERSION_ID" {
			return ""
		}
		return "set"
	})
	if strings.Join(missing, ",") != "VEW_TEST_COMPONENT_VERSION_ID,VEW_TEST_RECIPE_VERSION_ID" {
		t.Fatalf("missing gates = %v", missing)
	}
}

func TestAccReleaseActionsLiveDisposableVersions(t *testing.T) {
	if missing := missingReleaseAcceptanceGates(os.Getenv); len(missing) != 0 {
		t.Skipf("release-action acceptance requires %s and disposable VALIDATED component/recipe version fixtures; the recipe fixture's component prerequisites must be released or include the component fixture", strings.Join(missing, ", "))
	}

	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	componentID := strings.TrimSpace(os.Getenv("VEW_TEST_COMPONENT_ID"))
	componentVersionID := strings.TrimSpace(os.Getenv("VEW_TEST_COMPONENT_VERSION_ID"))
	recipeID := strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_ID"))
	recipeVersionID := strings.TrimSpace(os.Getenv("VEW_TEST_RECIPE_VERSION_ID"))
	config := fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}

action "vew_component_version_release" "manual" {
  config {
    project_id   = %q
    component_id = %q
    version_id   = %q
  }
}

action "vew_recipe_version_release" "manual" {
  config {
    project_id = %q
    recipe_id  = %q
    version_id = %q
  }
}

resource "terraform_data" "component_after_create" {
  lifecycle {
    action_trigger {
      events     = [after_create]
      actions    = [action.vew_component_version_release.manual]
      on_failure = halt
    }
  }
}

resource "terraform_data" "recipe_after_create" {
  depends_on = [terraform_data.component_after_create]
  lifecycle {
    action_trigger {
      events     = [after_create]
      actions    = [action.vew_recipe_version_release.manual]
      on_failure = halt
    }
  }
}
`,
		strings.TrimSpace(os.Getenv("VEW_API_URL")), strings.TrimSpace(os.Getenv("VEW_TOKEN_URL")),
		strings.TrimSpace(os.Getenv("VEW_CLIENT_ID")), strings.TrimSpace(os.Getenv("VEW_CLIENT_SECRET")),
		projectID, componentID, componentVersionID, projectID, recipeID, recipeVersionID,
	)
	workspace := newTerraformCLIWorkspace(t, config)
	for range 2 {
		workspace.run("apply", "-invoke=action.vew_component_version_release.manual", "-auto-approve", "-input=false", "-no-color")
	}
	for range 2 {
		workspace.run("apply", "-invoke=action.vew_recipe_version_release.manual", "-auto-approve", "-input=false", "-no-color")
	}
	workspace.run("apply", "-auto-approve", "-input=false", "-no-color")
}
