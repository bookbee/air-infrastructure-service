package broker

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/policy"
	"github.com/bookbee/air-infrastructure-service/internal/registry"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// ErrResourceNotFound and ErrPolicyDenied are the two failures the API layer
// distinguishes, mapped to 404 and 403. They are sentinels so handlers match
// with errors.Is rather than on a string code.
var (
	ErrResourceNotFound = errors.New("resource not found")
	ErrPolicyDenied     = errors.New("policy denied")
)

// Error is a broker failure carrying the message a caller sees. The sentinel
// is wrapped rather than concatenated: fmt.Errorf("%s: %w", detail, sentinel)
// would append "resource not found" to the detail served in the problem body,
// which is noise no consumer ever saw from the Python service.
type Error struct {
	Detail string
	kind   error
}

func (e *Error) Error() string { return e.Detail }

// Unwrap exposes the sentinel to errors.Is.
func (e *Error) Unwrap() error { return e.kind }

// Metrics is the counter the broker increments. An interface so the broker
// does not depend on the observability package, and so a test can assert on
// issuance without scraping a registry.
type Metrics interface {
	CredentialIssued(resource string)
}

// Broker issues scoped credentials. It is never on the data path: it returns
// an endpoint plus credentials and steps aside, which is why a store being
// down does not stop it working.
type Broker struct {
	registry *registry.Registry
	policy   *policy.Engine
	secrets  SecretBackend
	metrics  Metrics
	log      *slog.Logger
}

// New builds a broker.
func New(r *registry.Registry, p *policy.Engine, s SecretBackend, m Metrics, log *slog.Logger) *Broker {
	return &Broker{registry: r, policy: p, secrets: s, metrics: m, log: log}
}

// Issue resolves a credential grant for a principal, or reports why not.
func (b *Broker) Issue(p schema.Principal, resourceName string, _ schema.CredentialRequest) (schema.CredentialGrant, error) {
	resource, ok := b.registry.Get(resourceName)
	if !ok {
		return schema.CredentialGrant{}, &Error{
			Detail: fmt.Sprintf("unknown resource '%s'", resourceName),
			kind:   ErrResourceNotFound,
		}
	}

	if decision := b.policy.Decide(p, resourceName, constants.ActionConnect); !decision.Allow {
		return schema.CredentialGrant{}, &Error{Detail: decision.Reason, kind: ErrPolicyDenied}
	}

	secret, err := b.secrets.Get(resourceName, p.Service)
	if err != nil {
		return schema.CredentialGrant{}, fmt.Errorf("resolving secret for '%s': %w", resourceName, err)
	}

	grant := schema.CredentialGrant{
		Resource: resource.Name,
		Kind:     resource.Kind,
		Endpoint: resource.Endpoint,
		Username: optional(secret.Username),
		Password: optional(secret.Password),
		URI:      optional(secret.URI),
		// Static in local; a leased backend sets this.
		ExpiresAt: nil,
	}

	b.metrics.CredentialIssued(resourceName)
	b.log.Info("credential.issued",
		"service", p.Service,
		"resource", resourceName,
		"auth_mode", resource.AuthMode,
	)
	return grant, nil
}

// optional maps an unset credential field to JSON null, the way an absent
// Optional[str] serialised in Python — a store that authenticates by URI has
// no username, and reporting "" there would look like an empty credential
// rather than an inapplicable one.
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
