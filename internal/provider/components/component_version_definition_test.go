package components

import (
	"strings"
	"testing"
)

func TestNormalizeDefinitionRejectsInvalidJSON(t *testing.T) {
	_, err := normalizeDefinition(`{"phases":[`)
	if err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
}

func TestNormalizeDefinitionRejectsNonObjectTopLevel(t *testing.T) {
	_, err := normalizeDefinition(`[1, 2, 3]`)
	if err == nil {
		t.Fatal("expected non-object top-level JSON to be rejected")
	}
}

func TestNormalizeDefinitionRejectsTrailingJSON(t *testing.T) {
	_, err := normalizeDefinition(`{} {}`)
	if err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestNormalizeDefinitionCanonicalizesObjectsAndPreservesArrayOrder(t *testing.T) {
	got, err := normalizeDefinition(`{"z":1,"items":[{"b":2,"a":1},{"a":3}],"a":2}`)
	if err != nil {
		t.Fatalf("normalizeDefinition returned error: %v", err)
	}

	const want = `{"a":2,"items":[{"a":1,"b":2},{"a":3}],"z":1}`
	if string(got) != want {
		t.Fatalf("normalized definition = %s, want %s", got, want)
	}
}

func TestNormalizeDefinitionInjectsMissingStepDefaults(t *testing.T) {
	got, err := normalizeDefinition(`{"phases":[{"steps":[{"name":"one"},{"name":"two"}]},{"steps":[{"name":"three"}]}]}`)
	if err != nil {
		t.Fatalf("normalizeDefinition returned error: %v", err)
	}

	const want = `{"phases":[{"steps":[{"maxAttempts":1,"name":"one","onFailure":"Abort","timeoutSeconds":7200},{"maxAttempts":1,"name":"two","onFailure":"Abort","timeoutSeconds":7200}]},{"steps":[{"maxAttempts":1,"name":"three","onFailure":"Abort","timeoutSeconds":7200}]}]}`
	if string(got) != want {
		t.Fatalf("normalized definition = %s, want %s", got, want)
	}
}

func TestNormalizeDefinitionPreservesExplicitStepValues(t *testing.T) {
	got, err := normalizeDefinition(`{"phases":[{"steps":[{"timeoutSeconds":0,"onFailure":"","maxAttempts":9,"name":"explicit"}]}]}`)
	if err != nil {
		t.Fatalf("normalizeDefinition returned error: %v", err)
	}

	const want = `{"phases":[{"steps":[{"maxAttempts":9,"name":"explicit","onFailure":"","timeoutSeconds":0}]}]}`
	if string(got) != want {
		t.Fatalf("normalized definition = %s, want %s", got, want)
	}
}

func TestNormalizeDefinitionDoesNotExposeInputInErrors(t *testing.T) {
	const secret = "definition-secret-should-not-leak"
	_, err := normalizeDefinition(`{"phases":[{"secret":"` + secret + `"}`)
	if err == nil {
		t.Fatal("expected malformed definition to be rejected")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed definition input: %v", err)
	}
}
