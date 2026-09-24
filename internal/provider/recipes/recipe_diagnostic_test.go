package recipes

import (
	"errors"
	"strings"
	"testing"

	"github.com/elva-labs/terraform-provider-vew/internal/vew"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func TestRecipeDiagnosticOmitsResponseAndCredentials(t *testing.T) {
	secret := "Bearer eyJhbGciOiJIUzI1NiJ9.secret"
	apiError := &vew.APIError{Status: 403, Problem: vew.Problem{
		Code: "ACCESS_DENIED", RequestID: "request-123",
		Title: secret, Detail: `{"configuredComponentsVersions":[{"secret":"` + secret + `"}]}`,
	}}
	message := recipeAPIDiagnostic("create", apiError)
	if !strings.Contains(message, "HTTP status 403") || !strings.Contains(message, "ACCESS_DENIED") || !strings.Contains(message, "request-123") {
		t.Fatalf("missing safe API context: %q", message)
	}
	for _, value := range []string{"Bearer", "eyJhbGci", "configuredComponentsVersions", "secret"} {
		if strings.Contains(message, value) {
			t.Fatalf("diagnostic leaked %q: %q", value, message)
		}
	}
	unsafe := &vew.APIError{Status: 400, Problem: vew.Problem{
		Code: "ACCESS_DENIED\n" + secret, RequestID: "request-123 " + secret,
	}}
	message = recipeAPIDiagnostic("archive", unsafe)
	if strings.Contains(message, "ACCESS_DENIED") || strings.Contains(message, "Bearer") || strings.Contains(message, "request-123") {
		t.Fatalf("untrusted correlation leaked: %q", message)
	}
	if message := recipeAPIDiagnostic("read", errors.New(secret)); strings.Contains(message, "Bearer") {
		t.Fatalf("transport error leaked: %q", message)
	}
}

func TestRecipeVersionDiagnosticOmitsResponseAndComponents(t *testing.T) {
	secret := "Bearer eyJhbGciOiJIUzI1NiJ9.secret"
	apiError := &vew.APIError{Status: 400, Problem: vew.Problem{
		Code: "INVALID_COMPONENTS", RequestID: "request-456",
		Detail: `{"configuredComponentsVersions":[{"secret":"` + secret + `"}]}`,
	}}
	var diagnostics diag.Diagnostics
	addRecipeVersionError(&diagnostics, "update", apiError, "version-123")
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	message := diagnostics[0].Detail()
	for _, safe := range []string{"HTTP status 400", "INVALID_COMPONENTS", "request-456", "version-123"} {
		if !strings.Contains(message, safe) {
			t.Fatalf("missing %q: %q", safe, message)
		}
	}
	for _, value := range []string{"Bearer", "eyJhbGci", "configuredComponentsVersions", "secret"} {
		if strings.Contains(message, value) {
			t.Fatalf("diagnostic leaked %q: %q", value, message)
		}
	}
	var unsafeDiagnostics diag.Diagnostics
	addRecipeVersionError(&unsafeDiagnostics, "create", &vew.APIError{Status: 400, Problem: vew.Problem{
		Code: "unsafe\n" + secret, RequestID: "request-456 " + secret,
	}}, "version-123 "+secret)
	message = unsafeDiagnostics[0].Detail()
	if strings.Contains(message, "Bearer") || strings.Contains(message, "request-456") || strings.Contains(message, "version-123") {
		t.Fatalf("untrusted correlation leaked: %q", message)
	}
	var transportDiagnostics diag.Diagnostics
	addRecipeVersionError(&transportDiagnostics, "delete", errors.New(secret), "")
	if strings.Contains(transportDiagnostics[0].Detail(), "Bearer") {
		t.Fatalf("transport error leaked: %v", transportDiagnostics)
	}
}
