package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/broker"
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/observability"
	"github.com/bookbee/air-infrastructure-service/internal/policy"
	"github.com/bookbee/air-infrastructure-service/internal/registry"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
	"github.com/bookbee/air-infrastructure-service/internal/security"
)

// Deps is everything the handlers need, assembled once at boot. The Python
// hung these off app.state; a struct makes the same set explicit and lets a
// test substitute any one of them.
type Deps struct {
	Settings config.Settings
	Registry *registry.Registry
	Policy   *policy.Engine
	Broker   *broker.Broker
	Verifier security.Verifier
	Limiter  *policy.RateLimiter
	Metrics  *observability.Metrics
	Log      *slog.Logger

	// Probe reports store health for readiness. A field rather than a direct
	// call so a test can drive readiness without a live store — the same seam
	// the Python tests monkeypatch.
	Probe func(context.Context, config.Settings) map[string]string
}

// Logger returns the request-scoped logger, which already carries the request id.
func (d *Deps) Logger(r *http.Request) *slog.Logger {
	return observability.LoggerFrom(r.Context(), d.Log)
}

// RequirePrincipal authenticates the caller from the X-API-Key header.
func (d *Deps) RequirePrincipal(r *http.Request) (schema.Principal, error) {
	principal, ok := d.Verifier.Verify(r.Header.Get(constants.HeaderAPIKey))
	if !ok {
		return schema.Principal{}, Unauthorized("missing or invalid X-API-Key")
	}
	return principal, nil
}

// EnforcePolicy applies the RBAC decision for (principal, resource, action).
func (d *Deps) EnforcePolicy(p schema.Principal, resource string, action constants.Action) error {
	if decision := d.Policy.Decide(p, resource, action); !decision.Allow {
		return Forbidden(decision.Reason)
	}
	return nil
}

// EnforceRate applies the caller's token bucket for a resource.
//
// NOTE: nothing calls this. `rate_per_minute` in the policy document is
// therefore inert, and air_infrastructure_service_ratelimited_total never
// leaves zero.
//
// It is left unwired deliberately rather than by oversight: turning enforcement
// on would start rejecting sibling traffic that is accepted today, so it is a
// decision for the team, not a loose end to tidy. Wiring it into
// issueCredentials is a two-line change; see docs/overview.md "Current Gaps".
func (d *Deps) EnforceRate(p schema.Principal, resource string) error {
	if d.Limiter.Check(p.Service, resource, d.Policy.RatePerMinute(p)) {
		return nil
	}
	d.Metrics.RateLimited(p.Service, resource)
	return RateLimited("rate limit exceeded for '" + resource + "'")
}

// HandlerFunc is a handler that may fail. Returning an error rather than
// writing one keeps every failure on a single path through WriteProblem, so no
// handler can invent a response shape of its own.
type HandlerFunc func(http.ResponseWriter, *http.Request) error

// Handle adapts a HandlerFunc to http.Handler, rendering any error as
// problem+json and turning a panic into a 500 rather than a dropped
// connection.
func (d *Deps) Handle(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := d.Logger(r)
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic", "value", rec, "path", r.URL.Path)
				WriteProblem(w, r, Internal("unexpected error"), log)
			}
		}()
		if err := h(w, r); err != nil {
			WriteProblem(w, r, err, log)
		}
	})
}
