package v1

import (
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/api"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// The admin surface is read-only. Registering a resource or editing a grant
// means editing the policy document and restarting; write endpoints are a
// standing to-do carried over from the Python service.

func dumpRegistry(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		principal, err := d.RequirePrincipal(r)
		if err != nil {
			return err
		}
		if err := d.EnforcePolicy(principal, "admin", constants.ActionAdminRead); err != nil {
			return err
		}
		api.WriteJSON(w, http.StatusOK,
			schema.ResourceList{Resources: d.Registry.All()}, d.Logger(r))
		return nil
	}
}

func dumpPolicy(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		principal, err := d.RequirePrincipal(r)
		if err != nil {
			return err
		}
		if err := d.EnforcePolicy(principal, "admin", constants.ActionAdminRead); err != nil {
			return err
		}
		api.WriteJSON(w, http.StatusOK, d.Policy.Doc(), d.Logger(r))
		return nil
	}
}
