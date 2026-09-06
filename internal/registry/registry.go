// Package registry is the source of truth for resources and their
// per-environment endpoints.
package registry

import (
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

// Registry maps a resource name to what the broker advertises for it. Built
// from settings today (endpoints differ by environment via config); a future
// version can load a richer registry document without changing consumers.
//
// It is read-only after construction, so it needs no locking and is safe to
// share across request goroutines.
type Registry struct {
	byName map[string]schema.Resource
	order  []string
}

// FromSettings builds the registry the local stack advertises.
func FromSettings(s config.Settings) *Registry {
	pgbouncer := "pgbouncer"
	resources := map[string]schema.Resource{
		"redis": {
			Name:     "redis",
			Kind:     constants.KindRedis,
			Endpoint: schema.Endpoint{"url": s.Redis.URL},
			AuthMode: constants.AuthModeURI,
		},
		"postgres": {
			Name: "postgres",
			Kind: constants.KindPostgres,
			Endpoint: schema.Endpoint{
				"host": s.Postgres.Host,
				"port": s.Postgres.Port,
				"db":   s.Postgres.DB,
			},
			AuthMode: constants.AuthModePassword,
			Pooler:   &pgbouncer,
		},
		"mongo": {
			Name:     "mongo",
			Kind:     constants.KindMongo,
			Endpoint: schema.Endpoint{"uri": s.Mongo.URI, "db": s.Mongo.DB},
			AuthMode: constants.AuthModeURI,
		},
	}
	// Fixed order, so a listing is stable across calls rather than following Go's
	// randomised map iteration — a response that reshuffles between identical
	// requests is a needless diff for anything comparing them.
	return &Registry{byName: resources, order: []string{"redis", "postgres", "mongo"}}
}

// Get returns the resource and whether it is registered.
func (r *Registry) Get(name string) (schema.Resource, bool) {
	res, ok := r.byName[name]
	return res, ok
}

// All returns every resource, in registration order.
func (r *Registry) All() []schema.Resource {
	out := make([]schema.Resource, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}
