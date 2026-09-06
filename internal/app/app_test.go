package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bookbee/air-infrastructure-service/internal/app"
	"github.com/bookbee/air-infrastructure-service/internal/config"
	"github.com/bookbee/air-infrastructure-service/internal/constants"
)

// The two tokens the Python test suite uses, so a failure here reads the same
// way as a failure there.
const (
	tokenClassifier = "t-sent"
	tokenClient     = "t-client"
)

var (
	allStoresUp   = map[string]string{"redis": "ok", "postgres": "ok", "mongo": "ok"}
	allStoresDown = map[string]string{"redis": "down", "postgres": "down", "mongo": "down"}
)

// newServer builds the full handler over the repo's real policy document.
//
// The store probe is always stubbed — with allStoresUp when the caller passes
// nil. The real probe dials localhost, so leaving it in place would make these
// tests depend on whether the developer happens to have the stack running:
// slower, and a different code path covered on different machines. Readiness
// behaviour is asserted through the stub, which is the seam the Python tests
// monkeypatch for the same reason.
func newServer(t *testing.T, stores map[string]string) *httptest.Server {
	t.Helper()

	s := config.Default()
	s.Security.ServiceTokens = map[string]string{
		tokenClassifier: "air-classifier",
		tokenClient:     "air-client",
	}
	s.Policy.ConfigPath = filepath.Join("..", "..", "deploy", "policy", "policy.local.yaml")

	// Discarded rather than printed: these tests assert on responses, and a
	// passing run should be quiet.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The handler app.New returns is discarded: the probe is replaced below and
	// the handler rebuilt from the same deps, so building it twice is deliberate.
	_, deps, err := app.New(s, log)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if stores == nil {
		stores = allStoresUp
	}
	deps.Probe = func(context.Context, config.Settings) map[string]string { return stores }
	handler := app.Handler(deps)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, token string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, body)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if token != "" {
		req.Header.Set(constants.HeaderAPIKey, token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return resp, raw
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding %q: %v", raw, err)
	}
	return out
}

func TestHealth(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodGet, "/v1/health", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if got := decode(t, raw)["status"]; got != "ok" {
		t.Errorf("status field: got %v, want ok", got)
	}
}

func TestCapabilitiesRequiresKey(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodGet, "/v1/capabilities", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("content-type: got %q, want application/problem+json", ct)
	}
	if got := decode(t, raw)["code"]; got != "unauthorized" {
		t.Errorf("code: got %v, want unauthorized", got)
	}
}

func TestCapabilitiesOK(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodGet, "/v1/capabilities", tokenClassifier, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	body := decode(t, raw)
	if body["environment"] != "local" {
		t.Errorf("environment: got %v, want local", body["environment"])
	}
	kinds, _ := body["resource_kinds"].([]any)
	if !containsAny(kinds, "postgres") {
		t.Errorf("resource_kinds should include postgres, got %v", kinds)
	}
}

func TestIssueRedisCredentials(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodPost, "/v1/resources/redis/credentials",
		tokenClassifier, strings.NewReader("{}"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body %s)", resp.StatusCode, raw)
	}
	grant := decode(t, raw)
	if grant["resource"] != "redis" {
		t.Errorf("resource: got %v, want redis", grant["resource"])
	}
	if uri, _ := grant["uri"].(string); uri == "" {
		t.Error("a redis grant must carry a uri")
	}
	// Static grant: nil, not omitted, so a consumer can tell "no lease" from
	// "field missing".
	if _, present := grant["expires_at"]; !present {
		t.Error("expires_at must be present as null, not omitted")
	}
}

func TestCredentialsWithNoBodyAreAccepted(t *testing.T) {
	// The body is optional; a consumer that posts nothing must still be served.
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodPost, "/v1/resources/redis/credentials", tokenClassifier, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body %s)", resp.StatusCode, raw)
	}
}

func TestUnknownResourceIs404(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodPost, "/v1/resources/nope/credentials",
		tokenClassifier, strings.NewReader("{}"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
	if got := decode(t, raw)["code"]; got != "resource_not_found" {
		t.Errorf("code: got %v, want resource_not_found", got)
	}
}

func TestResourceListingIsScoped(t *testing.T) {
	// air-client's policy grants only redis; postgres must not appear.
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodGet, "/v1/resources", tokenClient, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	names := map[string]bool{}
	for _, r := range decode(t, raw)["resources"].([]any) {
		names[r.(map[string]any)["name"].(string)] = true
	}
	if !names["redis"] {
		t.Error("redis should be visible to air-client")
	}
	if names["postgres"] {
		t.Error("postgres must not be visible to air-client")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newServer(t, nil)
	// A counter with no observed label values exports no series, so the metric
	// only appears once something has been counted. One prior request is what
	// makes this assertion meaningful rather than accidental.
	do(t, srv, http.MethodGet, "/v1/health", "", nil)

	resp, raw := do(t, srv, http.MethodGet, "/metrics", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	for _, name := range []string{"air_infrastructure_service_requests_total"} {
		if !strings.Contains(string(raw), name) {
			t.Errorf("/metrics should export %s", name)
		}
	}
}

func TestRequestIDIsEchoedAndGenerated(t *testing.T) {
	srv := newServer(t, nil)

	resp, _ := do(t, srv, http.MethodGet, "/v1/health", "", nil)
	if got := resp.Header.Get(constants.HeaderRequestID); got == "" {
		t.Error("a generated request id must be echoed back")
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v1/health", nil)
	req.Header.Set(constants.HeaderRequestID, "caller-supplied")
	got, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer got.Body.Close() //nolint:errcheck
	if v := got.Header.Get(constants.HeaderRequestID); v != "caller-supplied" {
		t.Errorf("a supplied request id must be preserved: got %q", v)
	}
}

func TestUnknownPathIs404JSON(t *testing.T) {
	srv := newServer(t, nil)
	resp, raw := do(t, srv, http.MethodGet, "/v1/nope", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
	if got := decode(t, raw)["detail"]; got != "Not Found" {
		t.Errorf(`detail: got %v, want "Not Found"`, got)
	}
}

// --- readiness ------------------------------------------------------------
//
// Readiness reports store health but never gates on it. The broker hands out
// an endpoint and steps aside, so a red store fails the consumer's own
// connection, not this process. Gating would pull the broker out of rotation
// at exactly the moment a consumer needs fresh credentials.

func TestReadyWhenStoresAreDown(t *testing.T) {
	srv := newServer(t, allStoresDown)
	resp, raw := do(t, srv, http.MethodGet, "/v1/ready", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200 even with every store down", resp.StatusCode)
	}
	body := decode(t, raw)
	if body["status"] != "ready" {
		t.Errorf("status: got %v, want ready", body["status"])
	}
	if got := body["stores"].(map[string]any)["mongo"]; got != "down" {
		t.Errorf("store health is reported, not gating: mongo got %v, want down", got)
	}
}

func TestLivenessNeverConsultsADependency(t *testing.T) {
	srv := newServer(t, allStoresDown)
	resp, raw := do(t, srv, http.MethodGet, "/v1/health", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if got := decode(t, raw)["status"]; got != "ok" {
		t.Errorf("status: got %v, want ok", got)
	}
}

func TestTheBrokerStillIssuesCredentialsWithEveryStoreDown(t *testing.T) {
	srv := newServer(t, allStoresDown)
	resp, raw := do(t, srv, http.MethodPost, "/v1/resources/redis/credentials",
		tokenClassifier, strings.NewReader("{}"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body %s)", resp.StatusCode, raw)
	}
	if got := decode(t, raw)["resource"]; got != "redis" {
		t.Errorf("resource: got %v, want redis", got)
	}
}

func containsAny(haystack []any, needle string) bool {
	for _, v := range haystack {
		if s, ok := v.(string); ok && s == needle {
			return true
		}
	}
	return false
}

func TestMetricRouteLabelDropsTheVersionPrefix(t *testing.T) {
	// The Python service labelled by the route template with /v1 stripped, and
	// dashboards select on that value. An unmatched path keeps its full path.
	srv := newServer(t, nil)
	do(t, srv, http.MethodGet, "/v1/health", "", nil)
	do(t, srv, http.MethodPost, "/v1/resources/redis/credentials", tokenClassifier, strings.NewReader("{}"))
	do(t, srv, http.MethodGet, "/v1/nope", "", nil)

	_, raw := do(t, srv, http.MethodGet, "/metrics", "", nil)
	body := string(raw)
	for _, want := range []string{
		`route="/health"`,
		`route="/resources/{name}/credentials"`,
		`route="/v1/nope"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics should carry %s", want)
		}
	}
	if strings.Contains(body, `route="/v1/health"`) {
		t.Error(`the version prefix must be stripped from a matched route label`)
	}
}

func TestUnknownResourceDetailIsNotPollutedByTheSentinel(t *testing.T) {
	// The detail is served to callers; wrapping the sentinel into it would append
	// "resource not found" to a message the Python service kept clean.
	srv := newServer(t, nil)
	_, raw := do(t, srv, http.MethodPost, "/v1/resources/nope/credentials",
		tokenClassifier, strings.NewReader("{}"))
	if got := decode(t, raw)["detail"]; got != "unknown resource 'nope'" {
		t.Errorf("detail: got %q, want %q", got, "unknown resource 'nope'")
	}
}
