package security_test

import (
	"testing"

	"github.com/bookbee/air-infrastructure-service/internal/security"
)

func TestVerify(t *testing.T) {
	v := security.NewAPIKeyVerifier(map[string]string{"air-classifier-dev": "air-classifier"})

	p, ok := v.Verify("air-classifier-dev")
	if !ok {
		t.Fatal("a known token must authenticate")
	}
	if p.Service != "air-classifier" {
		t.Errorf("service: got %q, want air-classifier", p.Service)
	}
	if p.TokenID == nil || *p.TokenID != "air-clas" {
		t.Errorf("only a token prefix should be retained, got %v", p.TokenID)
	}

	for _, token := range []string{"", "wrong"} {
		if _, ok := v.Verify(token); ok {
			t.Errorf("token %q must not authenticate", token)
		}
	}
}

func TestTokensAreCopiedAtConstruction(t *testing.T) {
	tokens := map[string]string{"t": "air-client"}
	v := security.NewAPIKeyVerifier(tokens)

	// Mutating the caller's map must not change who is authenticated.
	tokens["t"] = "somebody-else"
	delete(tokens, "t")

	p, ok := v.Verify("t")
	if !ok || p.Service != "air-client" {
		t.Errorf("verifier should hold its own copy, got %q ok=%v", p.Service, ok)
	}
}
