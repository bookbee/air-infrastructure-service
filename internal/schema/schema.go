// Package schema holds the wire types.
//
// Pointers are used wherever an absent value must serialise as JSON null
// rather than disappear from the body, so `omitempty` is deliberately absent
// from those fields: a consumer can tell "inapplicable" from "missing".
package schema

import (
	"time"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
)

// Principal is the authenticated caller. Service is the v1 unit of
// authorization; Tenant is carried now so quotas and audit can split
// per-tenant later without an API-breaking change.
type Principal struct {
	Service string  `json:"service"`
	Tenant  *string `json:"tenant"`
	TokenID *string `json:"token_id"`
}

// Problem is an RFC 9457 problem detail. Served as application/problem+json.
type Problem struct {
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Status    int     `json:"status"`
	Code      string  `json:"code"`
	Detail    *string `json:"detail"`
	RequestID *string `json:"request_id"`
}

// Endpoint is the kind-shaped connection info the broker advertises. Values
// are strings or ints (Postgres carries a numeric port), which is why this is
// not map[string]string.
type Endpoint map[string]any

// Resource is one brokered store. Endpoint never carries secrets.
type Resource struct {
	Name     string                 `json:"name"`
	Kind     constants.ResourceKind `json:"kind"`
	Endpoint Endpoint               `json:"endpoint"`
	AuthMode string                 `json:"auth_mode"`
	Pooler   *string                `json:"pooler"`
}

// ResourceList is the body of GET /v1/resources and GET /v1/admin/registry.
type ResourceList struct {
	Resources []Resource `json:"resources"`
}

// CredentialRequest is the (optional) body of a credential issuance call.
// TTLSeconds is a requested lease; the backend may clamp it.
type CredentialRequest struct {
	TTLSeconds *int `json:"ttl_seconds"`
}

// CredentialGrant is an endpoint plus scoped credential material. ExpiresAt is
// nil for a static grant (the local `env` backend); leased backends set it.
type CredentialGrant struct {
	Resource  string                 `json:"resource"`
	Kind      constants.ResourceKind `json:"kind"`
	Endpoint  Endpoint               `json:"endpoint"`
	Username  *string                `json:"username"`
	Password  *string                `json:"password"`
	URI       *string                `json:"uri"`
	ExpiresAt *time.Time             `json:"expires_at"`
}

// ServicePolicy grants one service a set of actions per resource, with an
// optional per-minute rate override.
type ServicePolicy struct {
	Resources     map[string][]string `json:"resources" yaml:"resources"`
	RatePerMinute *int                `json:"rate_per_minute" yaml:"rate_per_minute"`
}

// PolicyDoc is the whole policy document, as loaded from YAML and as served by
// GET /v1/admin/policy.
type PolicyDoc struct {
	DefaultAllow         bool                     `json:"default_allow" yaml:"default_allow"`
	DefaultRatePerMinute int                      `json:"default_rate_per_minute" yaml:"default_rate_per_minute"`
	Services             map[string]ServicePolicy `json:"services" yaml:"services"`
}

// Decision is the outcome of a policy check. Reason is surfaced to the caller
// in the problem detail on a denial, so it must not leak anything a caller
// could not already infer.
type Decision struct {
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
}

// Health is the GET /v1/health body.
type Health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Ready is the GET /v1/ready body. Stores is reported, never gated on.
type Ready struct {
	Status string            `json:"status"`
	Stores map[string]string `json:"stores"`
}

// Capabilities is the GET /v1/capabilities body.
type Capabilities struct {
	Version       string                   `json:"version"`
	Environment   constants.Environment    `json:"environment"`
	ResourceKinds []constants.ResourceKind `json:"resource_kinds"`
}

// Ptr returns a pointer to v. The wire types use pointers to distinguish an
// absent value (JSON null) from a zero one, and this keeps constructing them
// from literals readable.
func Ptr[T any](v T) *T { return &v }
