// Package client is the SDK a Go consumer imports to reach brokered stores.
//
// It deliberately returns a credential grant and the connection string built
// from it, rather than a live driver client. The Python SDK this replaces
// returned ready redis/asyncpg/motor clients and paid for it with an optional
// dependency on all three; a Go consumer already has its own driver pinned,
// and handing it a DSN keeps this package dependency-free — importing it costs
// nothing and constrains nothing.
//
//	c := client.New("", "")            // reads AIR_INFRASTRUCTURE_SERVICE_URL/_TOKEN
//	dsn, err := c.PostgresDSN(ctx)     // then hand dsn to pgx, database/sql, ...
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is where the control plane listens in the local stack.
const DefaultBaseURL = "http://localhost:8080"

// Grant is an endpoint plus scoped credential material. ExpiresAt is nil for a
// static grant; a leased backend sets it.
type Grant struct {
	Resource  string         `json:"resource"`
	Kind      string         `json:"kind"`
	Endpoint  map[string]any `json:"endpoint"`
	Username  *string        `json:"username"`
	Password  *string        `json:"password"`
	URI       *string        `json:"uri"`
	ExpiresAt *time.Time     `json:"expires_at"`
}

// Client fetches credential grants. It is safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	mu     sync.Mutex
	grants map[string]Grant
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient supplies the HTTP client, for callers that need their own
// transport, timeout or instrumentation.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New builds a client. Empty arguments fall back to
// AIR_INFRASTRUCTURE_SERVICE_URL and AIR_INFRASTRUCTURE_SERVICE_TOKEN, then to
// DefaultBaseURL.
func New(baseURL, token string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = os.Getenv("AIR_INFRASTRUCTURE_SERVICE_URL")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if token == "" {
		token = os.Getenv("AIR_INFRASTRUCTURE_SERVICE_TOKEN")
	}
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		grants:  map[string]Grant{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Grant fetches a credential grant for a resource, reusing a cached one until
// it expires. A static grant (no expiry) is cached for the life of the client,
// which is what makes this cheap to call on every connection attempt.
func (c *Client) Grant(ctx context.Context, resource string) (Grant, error) {
	c.mu.Lock()
	cached, ok := c.grants[resource]
	c.mu.Unlock()
	if ok && !expired(cached) {
		return cached, nil
	}

	endpoint := fmt.Sprintf("%s/v1/resources/%s/credentials", c.baseURL, url.PathEscape(resource))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return Grant{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-API-Key", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Grant{}, fmt.Errorf("requesting %s credentials: %w", resource, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		// The status, not the body: an error body can carry a policy reason that
		// is useful in a log but should not be pasted into a caller's own error
		// surface unexamined.
		return Grant{}, fmt.Errorf("requesting %s credentials: HTTP %d", resource, resp.StatusCode)
	}

	var grant Grant
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		return Grant{}, fmt.Errorf("decoding %s grant: %w", resource, err)
	}

	c.mu.Lock()
	c.grants[resource] = grant
	c.mu.Unlock()
	return grant, nil
}

// RedisURL returns a connection URL for the brokered Redis.
func (c *Client) RedisURL(ctx context.Context) (string, error) {
	grant, err := c.Grant(ctx, "redis")
	if err != nil {
		return "", err
	}
	if grant.URI == nil {
		return "", fmt.Errorf("redis grant carried no uri")
	}
	return *grant.URI, nil
}

// MongoURI returns a connection URI for the brokered Mongo.
func (c *Client) MongoURI(ctx context.Context) (string, error) {
	grant, err := c.Grant(ctx, "mongo")
	if err != nil {
		return "", err
	}
	if grant.URI == nil {
		return "", fmt.Errorf("mongo grant carried no uri")
	}
	return *grant.URI, nil
}

// PostgresDSN returns a libpq-style URL for the brokered Postgres, pointing at
// the pooler the registry advertises.
func (c *Client) PostgresDSN(ctx context.Context) (string, error) {
	grant, err := c.Grant(ctx, "postgres")
	if err != nil {
		return "", err
	}
	host, _ := grant.Endpoint["host"].(string)
	db, _ := grant.Endpoint["db"].(string)
	port := 5432
	if p, ok := grant.Endpoint["port"].(float64); ok { // JSON numbers decode as float64
		port = int(p)
	}
	if host == "" {
		return "", fmt.Errorf("postgres grant carried no host")
	}

	dsn := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + db,
	}
	switch {
	case grant.Username != nil && grant.Password != nil:
		dsn.User = url.UserPassword(*grant.Username, *grant.Password)
	case grant.Username != nil:
		dsn.User = url.User(*grant.Username)
	}
	return dsn.String(), nil
}

func expired(g Grant) bool {
	// A static grant never expires; that is the local backend's contract, not an
	// oversight, so nil must not be read as "expired".
	return g.ExpiresAt != nil && !time.Now().Before(*g.ExpiresAt)
}
