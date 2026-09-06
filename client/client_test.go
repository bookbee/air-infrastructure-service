package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bookbee/air-infrastructure-service/client"
)

func TestPostgresDSNIsBuiltFromTheGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "t-token" {
			t.Errorf("the service token must be sent, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"postgres","kind":"postgres",
			"endpoint":{"host":"pgbouncer","port":6432,"db":"air"},
			"username":"air","password":"localdevpass","uri":null,"expires_at":null}`))
	}))
	defer srv.Close()

	dsn, err := client.New(srv.URL, "t-token").PostgresDSN(context.Background())
	if err != nil {
		t.Fatalf("PostgresDSN: %v", err)
	}
	want := "postgres://air:localdevpass@pgbouncer:6432/air"
	if dsn != want {
		t.Errorf("dsn: got %q, want %q", dsn, want)
	}
}

func TestStaticGrantsAreCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"resource":"redis","kind":"redis","endpoint":{},
			"username":null,"password":null,"uri":"redis://localhost:6379/0","expires_at":null}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, "")
	for range 3 {
		if _, err := c.RedisURL(context.Background()); err != nil {
			t.Fatalf("RedisURL: %v", err)
		}
	}
	// A grant with no expiry is good for the life of the client; re-fetching it
	// on every connection would put the broker on the hot path it exists to
	// stay off.
	if calls != 1 {
		t.Errorf("a static grant should be fetched once, got %d calls", calls)
	}
}

func TestErrorStatusDoesNotLeakTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"'air-client' may not 'connect' on 'postgres'"}`))
	}))
	defer srv.Close()

	_, err := client.New(srv.URL, "").PostgresDSN(context.Background())
	if err == nil {
		t.Fatal("a 403 must be an error")
	}
	if got := err.Error(); !strings.Contains(got, "HTTP 403") || strings.Contains(got, "may not") {
		t.Errorf("error should report the status, not the body: %q", got)
	}
}
