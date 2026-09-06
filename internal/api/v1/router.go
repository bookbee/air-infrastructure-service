package v1

import (
	"net/http"

	"github.com/bookbee/air-infrastructure-service/internal/api"
)

// Mount registers the v1 routes on mux under /v1.
//
// Method-qualified patterns mean a wrong verb is a 405 from the mux rather
// than a handler that has to check, and {name} is extracted by the mux rather
// than by parsing the path.
func Mount(mux *http.ServeMux, d *api.Deps) {
	mux.Handle("GET /v1/health", d.Handle(health(d)))
	mux.Handle("GET /v1/ready", d.Handle(ready(d)))
	mux.Handle("GET /v1/capabilities", d.Handle(capabilities(d)))

	mux.Handle("GET /v1/resources", d.Handle(listResources(d)))
	mux.Handle("POST /v1/resources/{name}/credentials", d.Handle(issueCredentials(d)))

	mux.Handle("GET /v1/admin/registry", d.Handle(dumpRegistry(d)))
	mux.Handle("GET /v1/admin/policy", d.Handle(dumpPolicy(d)))
}
