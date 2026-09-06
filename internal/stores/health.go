// Package stores probes the data stores for readiness reporting.
//
// This service brokers access but does not own store drivers, so a probe is a
// lightweight TCP connect rather than a driver-level ping. Deep per-store
// checks belong in the consumer, which is the process that actually holds a
// connection.
package stores

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/bookbee/air-infrastructure-service/internal/config"
)

// probeTimeout bounds one dial. Readiness is polled by an orchestrator on a
// short interval, so a probe that outlives the poll is worse than a miss.
const probeTimeout = 2 * time.Second

// Probe reports each store as "ok" or "down". It never returns an error: this
// feeds a readiness body that must render something useful at exactly the
// moment a dependency is misbehaving.
func Probe(ctx context.Context, s config.Settings) map[string]string {
	redisHost, redisPort := hostPort(s.Redis.URL, 6379)
	mongoHost, mongoPort := hostPort(s.Mongo.URI, 27017)

	targets := []struct {
		name string
		host string
		port int
	}{
		{"redis", redisHost, redisPort},
		{"postgres", s.Postgres.Host, s.Postgres.Port},
		{"mongo", mongoHost, mongoPort},
	}

	results := make(map[string]string, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := "down"
			if dialOK(ctx, t.host, t.port) {
				status = "ok"
			}
			mu.Lock()
			results[t.name] = status
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

func dialOK(ctx context.Context, host string, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	// A close that fails says nothing about whether the store is reachable —
	// the successful dial already answered that.
	_ = conn.Close()
	return true
}

// hostPort pulls the host and port out of a store URL, falling back to the
// store's well-known port when the URL omits one.
func hostPort(raw string, defaultPort int) (string, int) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "localhost", defaultPort
	}
	port := defaultPort
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	return u.Hostname(), port
}
