package projectaccounts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOnboardingRevisionIsOmittedWhenUnset(t *testing.T) {
	for name, value := range map[string]any{
		"create": AccountInput{AWSAccountID: "000000000000", AccountType: "USER", Name: "n", Description: "d", TechnologyID: "t", Stage: "dev", Region: "eu-west-1"},
		"update": UpdateAccountInput{AccountType: "USER", Name: "n", Description: "d", TechnologyID: "t", Stage: "dev", Region: "eu-west-1"},
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "onboardingRevision") {
			t.Fatalf("%s body sends an unset revision: %s", name, body)
		}
	}
	body, _ := json.Marshal(UpdateAccountInput{OnboardingRevision: "r2"})
	if !strings.Contains(string(body), `"onboardingRevision":"r2"`) {
		t.Fatalf("set revision missing: %s", body)
	}
}
