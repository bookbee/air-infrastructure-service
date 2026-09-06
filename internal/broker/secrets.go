// Package broker turns "I want resource X" into a scoped credential.
package broker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/bookbee/air-infrastructure-service/internal/config"
)

// StoreSecret is resolved credential material for one store. It is not a wire
// type: the broker maps it onto a CredentialGrant.
type StoreSecret struct {
	Username   string
	Password   string
	URI        string
	TTLSeconds int
}

// SecretBackend is where the broker reads credential material. Only the broker
// calls it. The three implementations are shaped identically so swapping one
// in is configuration, not a rewrite.
type SecretBackend interface {
	Get(resource, service string) (StoreSecret, error)
}

// EnvSecretBackend serves static credentials straight from settings. The local
// and default backend.
type EnvSecretBackend struct{ s config.Settings }

// NewEnvSecretBackend builds the static backend.
func NewEnvSecretBackend(s config.Settings) *EnvSecretBackend { return &EnvSecretBackend{s: s} }

// Get returns the credential material for a resource. An unknown resource
// yields an empty secret rather than an error: the registry, not the secret
// backend, decides what exists, and it has already been consulted.
func (b *EnvSecretBackend) Get(resource, _ string) (StoreSecret, error) {
	switch resource {
	case "postgres":
		return StoreSecret{Username: b.s.Postgres.User, Password: b.s.Postgres.Password}, nil
	case "redis":
		return StoreSecret{URI: b.s.Redis.URL}, nil
	case "mongo":
		return StoreSecret{URI: b.s.Mongo.URI}, nil
	}
	return StoreSecret{}, nil
}

// FileSecretBackend reads a JSON file of {resource: {username, password, uri}}.
// Useful for a mounted secret file without a full secrets manager.
type FileSecretBackend struct {
	data map[string]struct {
		Username string `json:"username"`
		Password string `json:"password"`
		URI      string `json:"uri"`
	}
}

// NewFileSecretBackend loads the secret file. A missing or malformed file is
// an error at construction, so a misconfigured deployment fails at boot rather
// than on the first credential request.
func NewFileSecretBackend(path string) (*FileSecretBackend, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path, by design
	if err != nil {
		return nil, fmt.Errorf("reading secrets %s: %w", path, err)
	}
	b := &FileSecretBackend{}
	if err := json.Unmarshal(raw, &b.data); err != nil {
		return nil, fmt.Errorf("parsing secrets %s: %w", path, err)
	}
	return b, nil
}

// Get returns the file's entry for a resource, empty if it has none.
func (b *FileSecretBackend) Get(resource, _ string) (StoreSecret, error) {
	entry := b.data[resource]
	return StoreSecret{Username: entry.Username, Password: entry.Password, URI: entry.URI}, nil
}

// ErrVaultNotImplemented is returned by VaultSecretBackend.
//
// Ported as the stub it is in Python. Dynamic database credentials with leases
// and rotation are the fill-in point here; the type exists so that work is a
// config swap rather than a new seam.
var ErrVaultNotImplemented = errors.New("VaultSecretBackend is a fill-in point, not implemented")

// VaultSecretBackend is the unimplemented leased-credential backend.
type VaultSecretBackend struct{ s config.Settings }

// Get always fails. See ErrVaultNotImplemented.
func (b *VaultSecretBackend) Get(_, _ string) (StoreSecret, error) {
	return StoreSecret{}, ErrVaultNotImplemented
}

// DefaultFileSecretPath is where the file backend looks when selected.
const DefaultFileSecretPath = "deploy/secrets/secrets.json"

// BuildSecretBackend selects the backend named in settings.
func BuildSecretBackend(s config.Settings) (SecretBackend, error) {
	switch s.Security.SecretBackend {
	case "vault":
		return &VaultSecretBackend{s: s}, nil
	case "file":
		return NewFileSecretBackend(DefaultFileSecretPath)
	default:
		return NewEnvSecretBackend(s), nil
	}
}
