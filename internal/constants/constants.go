// Package constants holds the enums shared across the service. Kept
// dependency-free so anything may import it.
package constants

// Version is the service version, reported by /v1/health and /v1/capabilities.
const Version = "0.1.0"

// Environment selects which set of endpoints the registry advertises. One
// image, environment chosen at boot.
type Environment string

const (
	EnvLocal   Environment = "local"
	EnvStaging Environment = "staging"
	EnvProd    Environment = "prod"
)

// Valid reports whether e is one of the known environments.
func (e Environment) Valid() bool {
	switch e {
	case EnvLocal, EnvStaging, EnvProd:
		return true
	}
	return false
}

// ResourceKind is the type of store behind a resource. The order below is the
// order /v1/capabilities reports, matching the Python enum's declaration order.
type ResourceKind string

const (
	KindRedis    ResourceKind = "redis"
	KindPostgres ResourceKind = "postgres"
	KindMongo    ResourceKind = "mongo"
)

// ResourceKinds lists every kind in declaration order, for /v1/capabilities.
func ResourceKinds() []ResourceKind {
	return []ResourceKind{KindRedis, KindPostgres, KindMongo}
}

// Action is the verb a policy rule grants on a resource.
type Action string

const (
	ActionConnect    Action = "connect"
	ActionRead       Action = "read"
	ActionWrite      Action = "write"
	ActionAdminRead  Action = "admin_read"
	ActionAdminWrite Action = "admin_write"
)

// AuthMode describes how a consumer authenticates to a store, so it knows
// which fields of a credential grant to use.
const (
	AuthModePassword = "password"
	AuthModeURI      = "uri"
)

// HeaderAPIKey carries the service token. HeaderRequestID is echoed back on
// every response, generated when the caller did not supply one.
const (
	HeaderAPIKey    = "X-API-Key"
	HeaderRequestID = "X-Request-ID"
)
