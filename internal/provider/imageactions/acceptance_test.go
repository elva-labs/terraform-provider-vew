package imageactions_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var imageUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var reservedImagePattern = regexp.MustCompile(`VEW image build reserved image ([A-Za-z0-9][A-Za-z0-9_.:-]{0,127})\.`)

var imageAcceptanceGateNames = []string{
	"TF_ACC", "VEW_ACC_IMAGE_BUILD", "VEW_API_URL", "VEW_TOKEN_URL",
	"VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID",
	"VEW_TEST_PIPELINE_ID", "VEW_TEST_IMAGE_IDEMPOTENCY_KEY",
}

func missingImageAcceptanceGates(getenv func(string) string) []string {
	var missing []string
	for _, name := range imageAcceptanceGateNames {
		value := strings.TrimSpace(getenv(name))
		if value == "" || (name == "TF_ACC" || name == "VEW_ACC_IMAGE_BUILD") && value != "1" || name == "VEW_TEST_IMAGE_IDEMPOTENCY_KEY" && !imageUUIDPattern.MatchString(value) {
			missing = append(missing, name)
		}
	}
	return missing
}

func TestImageBuildAcceptanceGate(t *testing.T) {
	getenv := func(name string) string { return "set" }
	if got := missingImageAcceptanceGates(getenv); strings.Join(got, ",") != "TF_ACC,VEW_ACC_IMAGE_BUILD,VEW_TEST_IMAGE_IDEMPOTENCY_KEY" {
		t.Fatalf("missing gates = %v", got)
	}
	getenv = func(name string) string {
		switch name {
		case "TF_ACC", "VEW_ACC_IMAGE_BUILD":
			return "1"
		case "VEW_TEST_IMAGE_IDEMPOTENCY_KEY":
			return imageProtocolKey
		default:
			return "set"
		}
	}
	if got := missingImageAcceptanceGates(getenv); len(got) != 0 {
		t.Fatalf("complete gate rejected: %v", got)
	}
}

type imageTerraformCLIWorkspace struct {
	t             *testing.T
	terraformPath string
	dir           string
	env           []string
}

func newImageTerraformCLIWorkspace(t *testing.T, config string) *imageTerraformCLIWorkspace {
	t.Helper()
	terraformPath, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform 1.16.3 is required for image-action CLI coverage")
	}
	versionOutput, err := exec.Command(terraformPath, "version", "-json").Output()
	if err != nil {
		t.Fatalf("read Terraform version: %v", err)
	}
	var version struct {
		TerraformVersion string `json:"terraform_version"`
	}
	if json.Unmarshal(versionOutput, &version) != nil || version.TerraformVersion != "1.16.3" {
		t.Skipf("image-action CLI coverage is pinned to Terraform 1.16.3, found %q", version.TerraformVersion)
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
	workspace := &imageTerraformCLIWorkspace{
		t: t, terraformPath: terraformPath, dir: root,
		env: append(os.Environ(), "TF_CLI_CONFIG_FILE="+cliConfigPath, "TF_IN_AUTOMATION=1"),
	}
	workspace.run("init", "-backend=false", "-input=false", "-no-color")
	return workspace
}

func (w *imageTerraformCLIWorkspace) run(arguments ...string) string {
	w.t.Helper()
	command := exec.Command(w.terraformPath, arguments...)
	command.Dir = w.dir
	command.Env = w.env
	output, err := command.CombinedOutput()
	if err != nil {
		w.t.Fatalf("terraform %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func TestAccImageBuildLiveExistingPipeline(t *testing.T) {
	if missing := missingImageAcceptanceGates(os.Getenv); len(missing) != 0 {
		t.Skipf("image-build acceptance requires %s, a disposable existing pipeline, an explicit UUID key, and may incur AWS charges", strings.Join(missing, ", "))
	}
	config := fmt.Sprintf(`
provider "vew" {
  api_url       = %q
  token_url     = %q
  client_id     = %q
  client_secret = %q
}

action "vew_image_build" "manual" {
  config {
    project_id      = %q
    pipeline_id     = %q
    idempotency_key = %q
  }
}
`,
		strings.TrimSpace(os.Getenv("VEW_API_URL")), strings.TrimSpace(os.Getenv("VEW_TOKEN_URL")),
		strings.TrimSpace(os.Getenv("VEW_CLIENT_ID")), strings.TrimSpace(os.Getenv("VEW_CLIENT_SECRET")),
		strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID")), strings.TrimSpace(os.Getenv("VEW_TEST_PIPELINE_ID")),
		strings.TrimSpace(os.Getenv("VEW_TEST_IMAGE_IDEMPOTENCY_KEY")),
	)
	workspace := newImageTerraformCLIWorkspace(t, config)
	reservedID := ""
	for range 2 {
		output := workspace.run("apply", "-invoke=action.vew_image_build.manual", "-auto-approve", "-input=false", "-no-color")
		match := reservedImagePattern.FindStringSubmatch(output)
		if len(match) != 2 {
			t.Fatal("image invocation did not report a reserved image ID")
		}
		if reservedID != "" && reservedID != match[1] {
			t.Fatalf("same-key invocation returned a different image ID: %s then %s", reservedID, match[1])
		}
		reservedID = match[1]
	}
}
