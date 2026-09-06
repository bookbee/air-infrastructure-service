package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the counters this service exports.
//
// The names follow the service's own namespace, matching how every other AIR
// service names its metrics. They live on an explicit registry rather than the
// global default so a test can build an isolated set and assert on it.
type Metrics struct {
	registry *prometheus.Registry

	requests    *prometheus.CounterVec
	credsIssued *prometheus.CounterVec
	rateLimited *prometheus.CounterVec
}

// NewMetrics builds the counters and registers them, along with the standard
// Go runtime and process collectors an operator expects on any /metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "air_infrastructure_service_requests_total",
			Help: "HTTP requests",
		}, []string{"route", "method", "status"}),
		credsIssued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "air_infrastructure_service_credentials_issued_total",
			Help: "Credentials issued",
		}, []string{"resource"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "air_infrastructure_service_ratelimited_total",
			Help: "Requests rejected by the rate limiter",
		}, []string{"service", "resource"}),
	}
	m.registry.MustRegister(
		m.requests, m.credsIssued, m.rateLimited,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// ObserveRequest counts one served request.
func (m *Metrics) ObserveRequest(route, method, status string) {
	m.requests.WithLabelValues(route, method, status).Inc()
}

// CredentialIssued counts one issued credential. Satisfies broker.Metrics.
func (m *Metrics) CredentialIssued(resource string) {
	m.credsIssued.WithLabelValues(resource).Inc()
}

// RateLimited counts one rejected request.
func (m *Metrics) RateLimited(service, resource string) {
	m.rateLimited.WithLabelValues(service, resource).Inc()
}

// Handler serves the exposition format at /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}
