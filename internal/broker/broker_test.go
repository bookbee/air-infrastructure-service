package broker_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/bookbee/air-infrastructure-service/internal/broker"
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/policy"
	"github.com/bookbee/air-infrastructure-service/internal/registry"
	"github.com/bookbee/air-infrastructure-service/internal/schema"
)

type countingMetrics struct{ issued map[string]int }

func (c *countingMetrics) CredentialIssued(resource string) { c.issued[resource]++ }

func newBroker(t *testing.T, defaultAllow bool) (*broker.Broker, *countingMetrics) {
	t.Helper()
	s := config.Default()
	s.Postgres.Password = "localdevpass"

	doc := schema.PolicyDoc{
		DefaultAllow:         defaultAllow,
		DefaultRatePerMinute: 120,
		Services: map[string]schema.ServicePolicy{
			"air-client": {Resources: map[string][]string{"redis": {"connect"}}},
		},
	}
	m := &countingMetrics{issued: map[string]int{}}
	return broker.New(
		registry.FromSettings(s),
		policy.New(doc),
		broker.NewEnvSecretBackend(s),
		m,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	), m
}

func TestIssueURIStoreLeavesUsernameNull(t *testing.T) {
	b, metrics := newBroker(t, false)

	grant, err := b.Issue(schema.Principal{Service: "air-client"}, "redis", schema.CredentialRequest{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if grant.URI == nil || *grant.URI == "" {
		t.Fatal("a redis grant must carry a uri")
	}
	// redis authenticates by URI: an empty username would read as an empty
	// credential rather than an inapplicable field.
	if grant.Username != nil {
		t.Errorf("username should be null for a uri-auth store, got %q", *grant.Username)
	}
	if grant.ExpiresAt != nil {
		t.Error("the static backend issues grants that do not expire")
	}
	if metrics.issued["redis"] != 1 {
		t.Errorf("issuance should be counted once, got %d", metrics.issued["redis"])
	}
}

func TestIssuePasswordStoreCarriesCredentials(t *testing.T) {
	b, _ := newBroker(t, true) // default-allow, so postgres is reachable

	grant, err := b.Issue(schema.Principal{Service: "air-client"}, "postgres", schema.CredentialRequest{})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if grant.Username == nil || *grant.Username != "air" {
		t.Errorf("username: got %v, want air", grant.Username)
	}
	if grant.Password == nil || *grant.Password != "localdevpass" {
		t.Error("password should come from settings")
	}
	if got := grant.Endpoint["port"]; got != 6432 {
		t.Errorf("postgres endpoint should point at pgbouncer: got %v, want 6432", got)
	}
}

func TestUnknownResourceIsDistinguishableFromADenial(t *testing.T) {
	b, _ := newBroker(t, false)

	_, err := b.Issue(schema.Principal{Service: "air-client"}, "nope", schema.CredentialRequest{})
	if !errors.Is(err, broker.ErrResourceNotFound) {
		t.Fatalf("want ErrResourceNotFound, got %v", err)
	}

	_, err = b.Issue(schema.Principal{Service: "air-client"}, "postgres", schema.CredentialRequest{})
	if !errors.Is(err, broker.ErrPolicyDenied) {
		t.Fatalf("want ErrPolicyDenied, got %v", err)
	}
}

func TestNothingIsIssuedWhenPolicyDenies(t *testing.T) {
	b, metrics := newBroker(t, false)
	if _, err := b.Issue(schema.Principal{Service: "ghost"}, "redis", schema.CredentialRequest{}); err == nil {
		t.Fatal("an unknown service must be denied under default-deny")
	}
	if len(metrics.issued) != 0 {
		t.Errorf("a denied request must not count as an issuance, got %v", metrics.issued)
	}
}

func TestVaultBackendIsAnHonestStub(t *testing.T) {
	s := config.Default()
	s.Security.SecretBackend = "vault"
	backend, err := broker.BuildSecretBackend(s)
	if err != nil {
		t.Fatalf("BuildSecretBackend: %v", err)
	}
	if _, err := backend.Get("postgres", "air-client"); !errors.Is(err, broker.ErrVaultNotImplemented) {
		t.Errorf("the vault backend should report that it is a fill-in point, got %v", err)
	}
}
