package projectaccounts_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	rootprovider "github.com/elva-labs/terraform-provider-vew/internal/provider"
	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	vewaccounts "github.com/elva-labs/terraform-provider-vew/internal/vew/projectaccounts"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	testresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	accProjectAccountAddress  = "vew_project_account.disposable"
	awsSideEffectConfirmation = "I_CONFIRM_REAL_AWS_ONBOARDING"
)

var acceptanceAWSRegion = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-[0-9]+$`)

func projectAccountAcceptanceMissingGates(getenv func(string) string) []string {
	var missing []string
	for _, name := range []string{
		"TF_ACC", "VEW_ACC_PROJECT_ACCOUNT", "VEW_API_URL", "VEW_PROJECTS_API_URL",
		"VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID",
		"VEW_TEST_AWS_ACCOUNT_ID", "VEW_TEST_TECHNOLOGY_ID", "VEW_TEST_ACCOUNT_TYPE",
		"VEW_TEST_ACCOUNT_STAGE", "VEW_TEST_ACCOUNT_REGION", "VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS",
	} {
		value := strings.TrimSpace(getenv(name))
		if value == "" || ((name == "TF_ACC" || name == "VEW_ACC_PROJECT_ACCOUNT") && value != "1") {
			missing = append(missing, name)
		}
	}
	accountID := strings.TrimSpace(getenv("VEW_TEST_AWS_ACCOUNT_ID"))
	if accountID != "" && !twelveDigits(accountID) {
		missing = append(missing, "VEW_TEST_AWS_ACCOUNT_ID (must be exactly 12 digits)")
	}
	accountType := strings.TrimSpace(getenv("VEW_TEST_ACCOUNT_TYPE"))
	if accountType != "" && accountType != "USER" && accountType != "TOOLCHAIN" {
		missing = append(missing, "VEW_TEST_ACCOUNT_TYPE (must be USER or TOOLCHAIN)")
	}
	stage := strings.TrimSpace(getenv("VEW_TEST_ACCOUNT_STAGE"))
	if stage != "" && stage != "dev" && stage != "qa" && stage != "prod" {
		missing = append(missing, "VEW_TEST_ACCOUNT_STAGE (must be dev, qa, or prod)")
	}
	region := strings.TrimSpace(getenv("VEW_TEST_ACCOUNT_REGION"))
	if region != "" && !acceptanceAWSRegion.MatchString(region) {
		missing = append(missing, "VEW_TEST_ACCOUNT_REGION (must be an AWS region)")
	}
	if confirmation := strings.TrimSpace(getenv("VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS")); confirmation != "" && confirmation != awsSideEffectConfirmation {
		missing = append(missing, "VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS (must equal "+awsSideEffectConfirmation+")")
	}
	return missing
}

func twelveDigits(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func TestProjectAccountAcceptanceGate(t *testing.T) {
	missing := projectAccountAcceptanceMissingGates(func(string) string { return "" })
	want := []string{"TF_ACC", "VEW_ACC_PROJECT_ACCOUNT", "VEW_API_URL", "VEW_PROJECTS_API_URL", "VEW_TOKEN_URL", "VEW_CLIENT_ID", "VEW_CLIENT_SECRET", "VEW_TEST_PROJECT_ID", "VEW_TEST_AWS_ACCOUNT_ID", "VEW_TEST_TECHNOLOGY_ID", "VEW_TEST_ACCOUNT_TYPE", "VEW_TEST_ACCOUNT_STAGE", "VEW_TEST_ACCOUNT_REGION", "VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS"}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("missing gates = %v, want %v", missing, want)
	}
	valid := map[string]string{
		"TF_ACC": "1", "VEW_ACC_PROJECT_ACCOUNT": "1", "VEW_API_URL": "https://api.example.invalid/clients/packaging/v1",
		"VEW_PROJECTS_API_URL": "https://api.example.invalid/clients/projects/v1", "VEW_TOKEN_URL": "https://auth.example.invalid/token",
		"VEW_CLIENT_ID": "client", "VEW_CLIENT_SECRET": "secret", "VEW_TEST_PROJECT_ID": "project-1",
		"VEW_TEST_AWS_ACCOUNT_ID": "123456789012", "VEW_TEST_TECHNOLOGY_ID": "technology-1", "VEW_TEST_ACCOUNT_TYPE": "USER",
		"VEW_TEST_ACCOUNT_STAGE": "dev", "VEW_TEST_ACCOUNT_REGION": "eu-west-1", "VEW_CONFIRM_AWS_ACCOUNT_SIDE_EFFECTS": awsSideEffectConfirmation,
	}
	if got := projectAccountAcceptanceMissingGates(func(name string) string { return valid[name] }); len(got) != 0 {
		t.Fatalf("valid explicit fixture rejected: %v", got)
	}
}

func TestAccProjectAccountResourceLiveDisposable(t *testing.T) {
	if missing := projectAccountAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
		t.Skipf("project-account live acceptance requires: %s; it onboards a real AWS account and may change AWS resources or incur charges", strings.Join(missing, ", "))
	}
	projectID := strings.TrimSpace(os.Getenv("VEW_TEST_PROJECT_ID"))
	awsAccountID := strings.TrimSpace(os.Getenv("VEW_TEST_AWS_ACCOUNT_ID"))
	technologyID := strings.TrimSpace(os.Getenv("VEW_TEST_TECHNOLOGY_ID"))
	accountType := strings.TrimSpace(os.Getenv("VEW_TEST_ACCOUNT_TYPE"))
	stage := strings.TrimSpace(os.Getenv("VEW_TEST_ACCOUNT_STAGE"))
	region := strings.TrimSpace(os.Getenv("VEW_TEST_ACCOUNT_REGION"))
	name := fmt.Sprintf("tf-acc-project-account-%d", time.Now().UTC().UnixNano())
	testresource.Test(t, testresource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"vew": providerserver.NewProtocol6WithError(rootprovider.New("test")())},
		PreCheck: func() {
			if missing := projectAccountAcceptanceMissingGates(os.Getenv); len(missing) > 0 {
				t.Fatalf("project-account live acceptance requires: %s", strings.Join(missing, ", "))
			}
		},
		CheckDestroy: func(state *terraform.State) error {
			config := vew.Config{APIURL: os.Getenv("VEW_PROJECTS_API_URL"), TokenURL: os.Getenv("VEW_TOKEN_URL"), ClientID: os.Getenv("VEW_CLIENT_ID"), ClientSecret: os.Getenv("VEW_CLIENT_SECRET")}
			write, err := vew.NewTransportWithScopes(config, "clients/projects/account.write")
			if err != nil {
				return err
			}
			read, err := vew.NewTransportWithScopes(config, "clients/projects/account.read")
			if err != nil {
				return err
			}
			api := vewaccounts.NewClient(write, read)
			for _, resource := range state.RootModule().Resources {
				if resource.Type != "vew_project_account" || resource.Primary == nil || resource.Primary.ID == "" {
					continue
				}
				remote, err := api.GetAccount(context.Background(), projectID, resource.Primary.ID)
				if err != nil {
					return fmt.Errorf("check deactivated disposable project account %q: %w", resource.Primary.ID, err)
				}
				if !strings.EqualFold(remote.Status, "INACTIVE") && !strings.EqualFold(remote.Status, "ARCHIVED") {
					return fmt.Errorf("disposable project account %q status = %q after Terraform delete, want INACTIVE or ARCHIVED", resource.Primary.ID, remote.Status)
				}
			}
			return nil
		},
		Steps: []testresource.TestStep{
			{Config: projectAccountAcceptanceConfig(projectID, awsAccountID, technologyID, accountType, stage, region, name), Check: testresource.ComposeTestCheckFunc(testresource.TestCheckResourceAttrSet(accProjectAccountAddress, "id"), testresource.TestCheckResourceAttr(accProjectAccountAddress, "project_id", projectID), testresource.TestCheckResourceAttr(accProjectAccountAddress, "aws_account_id", awsAccountID), testresource.TestCheckResourceAttr(accProjectAccountAddress, "account_type", accountType), testresource.TestCheckResourceAttr(accProjectAccountAddress, "technology_id", technologyID), testresource.TestCheckResourceAttr(accProjectAccountAddress, "stage", stage), testresource.TestCheckResourceAttr(accProjectAccountAddress, "region", region))},
			{ResourceName: accProjectAccountAddress, ImportState: true, ImportStateIdFunc: projectAccountAcceptanceImportID(projectID), ImportStateVerify: true},
		},
	})
}

func projectAccountAcceptanceConfig(projectID, awsAccountID, technologyID, accountType, stage, region, name string) string {
	return fmt.Sprintf(`resource "vew_project_account" "disposable" {
  project_id     = %q
  aws_account_id = %q
  account_type   = %q
  name           = %q
  description    = "Disposable Terraform project-account acceptance fixture"
  technology_id  = %q
  stage          = %q
  region         = %q

  timeouts = {
    create = "2h"
    update = "2h"
  }
}
`, projectID, awsAccountID, accountType, name, technologyID, stage, region)
}

func projectAccountAcceptanceImportID(projectID string) testresource.ImportStateIdFunc {
	return func(state *terraform.State) (string, error) {
		resource := state.RootModule().Resources[accProjectAccountAddress]
		if resource == nil || resource.Primary == nil || resource.Primary.ID == "" {
			return "", fmt.Errorf("disposable project-account state is missing an ID")
		}
		return projectID + "/" + resource.Primary.ID, nil
	}
}
