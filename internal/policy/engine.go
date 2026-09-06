// Package policy answers who may reach which resource, and how fast.
package policy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// Engine is RBAC over (service, resource, action).
//
// It fails open only when the document sets default_allow, a local
// convenience. Production documents set it false and every access needs an
// explicit rule.
//
// The document is immutable after load — editing the YAML needs a restart, as
// it did in Python — so the engine is safe to share across request goroutines.
type Engine struct {
	doc schema.PolicyDoc
}

// New wraps an already-parsed document. Used by tests and by callers that
// build a document in memory.
func New(doc schema.PolicyDoc) *Engine { return &Engine{doc: doc} }

// Load reads a policy document from disk.
//
// A missing file yields a deny-by-default empty policy rather than an error:
// if a production path is wrong, refusing every request is the safe failure,
// and a control plane that boots and denies is easier to diagnose than one
// that crash-loops.
func Load(path string, defaultRatePerMinute int) (*Engine, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path, by design
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return New(schema.PolicyDoc{
				DefaultAllow:         false,
				DefaultRatePerMinute: defaultRatePerMinute,
				Services:             map[string]schema.ServicePolicy{},
			}), nil
		}
		return nil, fmt.Errorf("reading policy %s: %w", path, err)
	}

	// Pre-set so a document that omits the key inherits the configured default,
	// matching the Python's raw.setdefault(...) before validation.
	doc := schema.PolicyDoc{DefaultRatePerMinute: defaultRatePerMinute}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing policy %s: %w", path, err)
	}
	if doc.Services == nil {
		doc.Services = map[string]schema.ServicePolicy{}
	}
	return New(doc), nil
}

// Doc returns the loaded document, for GET /v1/admin/policy.
func (e *Engine) Doc() schema.PolicyDoc { return e.doc }

// Decide resolves (service, resource, action) to allow or deny with a reason.
func (e *Engine) Decide(p schema.Principal, resource string, action constants.Action) schema.Decision {
	sp, ok := e.doc.Services[p.Service]
	if !ok {
		return e.fallback(fmt.Sprintf("no policy for service '%s'", p.Service))
	}
	allowed, ok := sp.Resources[resource]
	if !ok {
		return e.fallback(fmt.Sprintf("'%s' has no grant on '%s'", p.Service, resource))
	}
	for _, a := range allowed {
		if a == string(action) {
			return schema.Decision{Allow: true, Reason: "explicit grant"}
		}
	}
	return e.fallback(fmt.Sprintf("'%s' may not '%s' on '%s'", p.Service, action, resource))
}

// CanSee reports whether a resource appears in the caller's listing.
//
// Discovery is driven by explicit grants only — deliberately independent of
// default_allow, which governs enforcement — so a listing reflects what a
// service is declared to use even in a local default-allow environment.
func (e *Engine) CanSee(p schema.Principal, resource string) bool {
	sp, ok := e.doc.Services[p.Service]
	if !ok {
		return false
	}
	_, granted := sp.Resources[resource]
	return granted
}

// RatePerMinute is the caller's limit: their override, else the document default.
func (e *Engine) RatePerMinute(p schema.Principal) int {
	if sp, ok := e.doc.Services[p.Service]; ok && sp.RatePerMinute != nil {
		return *sp.RatePerMinute
	}
	return e.doc.DefaultRatePerMinute
}

func (e *Engine) fallback(reason string) schema.Decision {
	if e.doc.DefaultAllow {
		return schema.Decision{Allow: true, Reason: fmt.Sprintf("default-allow (%s)", reason)}
	}
	return schema.Decision{Allow: false, Reason: reason}
}
