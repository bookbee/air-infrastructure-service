package stores

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/bookbee/air-infrastructure-service/internal/config"
)

func TestHostPort(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		defaultPort int
		wantHost    string
		wantPort    int
	}{
		{"redis with credentials and port", "redis://:localdevpass@redis:6379/0", 6379, "redis", 6379},
		{"mongo with credentials", "mongodb://air:localdevpass@mongo:27017", 27017, "mongo", 27017},
		{"port omitted falls back", "redis://redis/0", 6379, "redis", 6379},
		{"non-default port is honoured", "redis://redis:6380/0", 6379, "redis", 6380},
		// A malformed or hostless URL must not crash a readiness probe; it
		// degrades to the local default and simply reports the store down.
		{"empty string", "", 6379, "localhost", 6379},
		{"not a url", "¬a url", 27017, "localhost", 27017},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port := hostPort(tc.raw, tc.defaultPort)
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("got %s:%d, want %s:%d", host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestProbeReportsUpAndDown(t *testing.T) {
	// A listener with nothing behind it is enough: the probe is a TCP connect,
	// deliberately not a driver-level ping.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close() //nolint:errcheck
	addr := listener.Addr().(*net.TCPAddr)

	s := config.Default()
	s.Redis.URL = "redis://127.0.0.1:" + strconv.Itoa(addr.Port) + "/0"
	// Port 1 on loopback: reserved, and nothing in this estate binds it.
	s.Postgres.Host, s.Postgres.Port = "127.0.0.1", 1
	s.Mongo.URI = "mongodb://127.0.0.1:1"

	got := Probe(context.Background(), s)
	if got["redis"] != "ok" {
		t.Errorf("redis: got %q, want ok", got["redis"])
	}
	for _, name := range []string{"postgres", "mongo"} {
		if got[name] != "down" {
			t.Errorf("%s: got %q, want down", name, got[name])
		}
	}
	if len(got) != 3 {
		t.Errorf("every store must be reported, got %v", got)
	}
}
