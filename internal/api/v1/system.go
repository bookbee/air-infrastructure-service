// Package v1 holds the version-1 HTTP handlers.
package v1

import (
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/api"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// health is liveness: the process is up. It never consults a dependency, so a
// container healthcheck cannot restart a working process because a store
// blinked.
func health(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		api.WriteJSON(w, http.StatusOK,
			schema.Health{Status: "ok", Version: constants.Version}, d.Logger(r))
		return nil
	}
}

// ready is readiness. The control plane needs no store connection to answer,
// so this is always ready once the process is serving.
//
// Store health is still reported, because that is the signal an operator
// wants — it is reporting, not gating. A red store fails the consumer's own
// connection, not this process: the broker hands out an endpoint and
// credentials and steps aside. Gating here would pull the broker out of
// rotation at exactly the moment a consumer needs fresh credentials.
func ready(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		stores := d.Probe(r.Context(), d.Settings)
		api.WriteJSON(w, http.StatusOK,
			schema.Ready{Status: "ready", Stores: stores}, d.Logger(r))
		return nil
	}
}

// capabilities reports what this build can broker.
func capabilities(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if _, err := d.RequirePrincipal(r); err != nil {
			return err
		}
		api.WriteJSON(w, http.StatusOK, schema.Capabilities{
			Version:       constants.Version,
			Environment:   d.Settings.Environment,
			ResourceKinds: constants.ResourceKinds(),
		}, d.Logger(r))
		return nil
	}
}
