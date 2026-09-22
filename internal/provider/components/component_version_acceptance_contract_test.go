package components_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func TestAcceptanceMakeTargetsKeepSecretsPrivateAndSelectTests(t *testing.T) {
	for _, target := range []struct {
		name, test string
		budget     time.Duration
	}{
		{"testacc", "TestAccComponentResource", 20 * time.Minute},
		{"testacc-component-version", "TestAccComponentVersionResource", 3 * time.Hour},
	} {
		t.Run(target.name, func(t *testing.T) {
			cmd := exec.Command("make", "-n", target.name, "TF_ACC=", "VEW_ACC_COMPONENT_VERSION=")
			cmd.Dir = "../../.."
			cmd.Env = append(os.Environ(), "VEW_CLIENT_SECRET=acceptance-secret-sentinel")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(output), "acceptance-secret-sentinel") {
				t.Error("Make printed the client secret")
			}
			fields := strings.Fields(string(output))
			var pkg, selector string
			var timeout time.Duration
			for i, field := range fields {
				if field == "test" && i+1 < len(fields) {
					pkg = fields[i+1]
				}
				if field == "-run" && i+1 < len(fields) {
					selector = strings.Trim(fields[i+1], "'")
				}
				if field == "-timeout" && i+1 < len(fields) {
					timeout, _ = time.ParseDuration(fields[i+1])
				}
			}
			if pkg != "./internal/provider/components" || selector != "^"+target.test+"$" {
				t.Errorf("wrong acceptance selection: package %q, selector %q", pkg, selector)
			}
			if timeout < target.budget {
				t.Errorf("process timeout %v cannot accommodate operation budget %v", timeout, target.budget)
			}
		})
	}
}

func TestComponentVersionPublishedFixturesMeetS2SDefinitionContract(t *testing.T) {
	example, err := os.ReadFile("../../../examples/resources/vew_component_version/resource.tf")
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"example":    string(example),
		"acceptance": testAccComponentVersionConfig("project", "disposable", "description", "notes"),
	} {
		t.Run(name, func(t *testing.T) {
			file, diagnostics := hclsyntax.ParseConfig([]byte(source), name+".tf", hcl.InitialPos)
			if diagnostics.HasErrors() {
				t.Fatal(diagnostics)
			}
			for _, block := range file.Body.(*hclsyntax.Body).Blocks {
				if block.Type != "resource" || block.Labels[0] != "vew_component_version" {
					continue
				}
				value, diagnostics := block.Body.Attributes["definition_json"].Expr.Value(&hcl.EvalContext{Functions: map[string]function.Function{"jsonencode": stdlib.JSONEncodeFunc}})
				if diagnostics.HasErrors() {
					t.Fatal(diagnostics)
				}
				var definition struct {
					SchemaVersion string `json:"schemaVersion"`
					Phases        []struct {
						Name  string
						Steps []struct {
							Name, Action string
							Inputs       struct{ Commands []string }
						}
					}
				}
				if err := json.Unmarshal([]byte(value.AsString()), &definition); err != nil {
					t.Fatal(err)
				}
				if definition.SchemaVersion != "1.0" || len(definition.Phases) == 0 {
					t.Fatal("definition requires schemaVersion 1.0 and at least one phase")
				}
				for _, phase := range definition.Phases {
					if phase.Name != "build" || len(phase.Steps) == 0 {
						t.Fatal("definition requires a build phase with steps")
					}
					for _, step := range phase.Steps {
						if step.Name == "" || step.Action != "ExecuteBash" || len(step.Inputs.Commands) != 1 || step.Inputs.Commands[0] != "true" {
							t.Fatal("definition must run a harmless ExecuteBash true step")
						}
					}
				}
				return
			}
			t.Fatal("component version resource missing")
		})
	}
}
