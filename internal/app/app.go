// Package app wires the service together and serves it.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/api"
	v1 "github.com/bookbee/air-infrastructure-service/internal/api/v1"
	"github.com/bookbee/air-infrastructure-service/internal/broker"
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/observability"
	"github.com/bookbee/air-infrastructure-service/internal/policy"
	"github.com/bookbee/air-infrastructure-service/internal/registry"
	"github.com/bookbee/air-infrastructure-service/internal/security"
	"github.com/bookbee/air-infrastructure-service/internal/stores"
)

// New assembles the handler and the dependencies behind it.
//
// Everything that can fail — an unparseable policy document, a missing secret
// file — fails here, at boot, rather than on the first request that needs it.
func New(s config.Settings, log *slog.Logger) (http.Handler, *api.Deps, error) {
	reg := registry.FromSettings(s)

	pol, err := policy.Load(s.Policy.ConfigPath, s.Policy.DefaultRatePerMinute)
	if err != nil {
		return nil, nil, fmt.Errorf("loading policy: %w", err)
	}

	secrets, err := broker.BuildSecretBackend(s)
	if err != nil {
		return nil, nil, fmt.Errorf("building secret backend: %w", err)
	}

	metrics := observability.NewMetrics()
	deps := &api.Deps{
		Settings: s,
		Registry: reg,
		Policy:   pol,
		Broker:   broker.New(reg, pol, secrets, metrics, log),
		Verifier: security.NewAPIKeyVerifier(s.Security.ServiceTokens),
		Limiter:  policy.NewRateLimiter(s.Policy.DefaultRatePerMinute),
		Metrics:  metrics,
		Log:      log,
		Probe:    stores.Probe,
	}

	return Handler(deps), deps, nil
}

// Handler builds the routed handler for an already-assembled Deps. Separate
// from New so a test can supply its own dependencies.
func Handler(d *api.Deps) http.Handler {
	mux := http.NewServeMux()
	v1.Mount(mux, d)

	// Unauthenticated and outside /v1, matching the Python: a scrape must not
	// need a credential, and the path is fixed by every Prometheus config in
	// the estate.
	mux.Handle("GET /metrics", d.Metrics.Handler())

	// The mux's own 404 and 405 bodies are plain text. This keeps the shape the
	// Python framework produced for the same cases, so a client that parses an
	// error body sees no difference between the two implementations.
	mux.HandleFunc("/", notFound(d.Log))

	return api.Observe(mux, d.Metrics, d.Log)
}

func notFound(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(map[string]string{"detail": "Not Found"}); err != nil {
			log.Error("not_found_encode_failed", "error", err.Error())
		}
	}
}

// Probe is what the container healthcheck runs: the binary asking its own
// /v1/health whether it is serving. Living in the binary means the runtime
// image needs no shell and no curl.
func Probe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // response body, nothing to salvage
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned HTTP %d", resp.StatusCode)
	}
	return nil
}
