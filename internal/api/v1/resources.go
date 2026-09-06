package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/api"
	"github.com/bookbee/air-infrastructure-service/internal/broker"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// listResources returns the resources the caller is declared to use.
// Visibility follows explicit grants, not the enforcement default.
func listResources(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		principal, err := d.RequirePrincipal(r)
		if err != nil {
			return err
		}
		all := d.Registry.All()
		visible := make([]schema.Resource, 0, len(all))
		for _, res := range all {
			if d.Policy.CanSee(principal, res.Name) {
				visible = append(visible, res)
			}
		}
		api.WriteJSON(w, http.StatusOK, schema.ResourceList{Resources: visible}, d.Logger(r))
		return nil
	}
}

// issueCredentials is the one request that matters: "give me a connection to X".
func issueCredentials(d *api.Deps) api.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		principal, err := d.RequirePrincipal(r)
		if err != nil {
			return err
		}
		name := r.PathValue("name")

		req, err := decodeCredentialRequest(r)
		if err != nil {
			return err
		}

		grant, err := d.Broker.Issue(principal, name, req)
		switch {
		case err == nil:
		case errors.Is(err, broker.ErrResourceNotFound):
			return api.ResourceNotFound(err.Error())
		case errors.Is(err, broker.ErrPolicyDenied):
			return api.Forbidden(err.Error())
		default:
			// A secret backend that cannot answer is a dependency failure, not a
			// client error: the request was well-formed and authorized.
			return api.DependencyUnavailable(err.Error())
		}

		api.WriteJSON(w, http.StatusOK, grant, d.Logger(r))
		return nil
	}
}

// decodeCredentialRequest reads the optional body. An absent or empty body is
// the common case — consumers post `{}` — so it is not an error; anything that
// is present must still be valid JSON.
func decodeCredentialRequest(r *http.Request) (schema.CredentialRequest, error) {
	var req schema.CredentialRequest
	if r.Body == nil {
		return req, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		return req, api.ValidationError("could not read request body")
	}
	if len(body) == 0 {
		return req, nil
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, api.ValidationError(err.Error())
	}
	return req, nil
}
