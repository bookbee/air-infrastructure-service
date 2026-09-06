// Package security turns a credential into an authenticated principal.
package security

import "github.com/bookbee/air-infrastructure-service/internal/schema"

// Verifier resolves an X-API-Key to a principal. Prod swaps this for a
// verifier backed by the secret store or an mTLS identity; the contract stays
// the same, which is why the API layer depends on this interface and not on
// APIKeyVerifier.
type Verifier interface {
	Verify(token string) (schema.Principal, bool)
}

// APIKeyVerifier maps static service tokens to principals. Local only: in prod
// the tokens come from the secret backend.
type APIKeyVerifier struct {
	tokens map[string]string
}

// NewAPIKeyVerifier copies tokens, so a later mutation of the caller's map
// cannot change who is authenticated.
func NewAPIKeyVerifier(tokens map[string]string) *APIKeyVerifier {
	copied := make(map[string]string, len(tokens))
	for k, v := range tokens {
		copied[k] = v
	}
	return &APIKeyVerifier{tokens: copied}
}

// Verify resolves a token. An empty or unknown token is not authenticated.
func (v *APIKeyVerifier) Verify(token string) (schema.Principal, bool) {
	if token == "" {
		return schema.Principal{}, false
	}
	service, ok := v.tokens[token]
	if !ok {
		return schema.Principal{}, false
	}
	// Only a prefix of the token is retained, so a log line or an echoed
	// principal cannot leak the credential itself.
	id := token
	if len(id) > 8 {
		id = id[:8]
	}
	return schema.Principal{Service: service, TokenID: &id}, true
}
