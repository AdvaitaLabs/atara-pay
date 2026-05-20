// Package metrics owns ATARA-Pay's Prometheus collectors.
//
// Three categories:
//   - HTTP: request count + latency histogram, labelled by route + status
//     class (2xx/3xx/4xx/5xx). Cardinality stays low — full status codes
//     would explode label values without adding insight.
//   - Domain events: limit violations, session-key lifecycle, webhook
//     delivery outcomes. Counters operators page on when something
//     stops moving.
//   - Rail calls: outbound CrossMint / Tempo latency + outcome.
//
// All collectors register against one Registry exposed via Handler() —
// main wires this onto /metrics. Tests can build a fresh Registry via
// NewRegistry().
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry bundles every collector + the underlying Prometheus registry.
// One per process in production; tests build their own.
type Registry struct {
	r *prometheus.Registry

	// HTTP
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec

	// Domain events
	LimitViolations   *prometheus.CounterVec
	WebhookDeliveries *prometheus.CounterVec
	SessionKeyEvents  *prometheus.CounterVec

	// Outbound rail calls
	RailCalls   *prometheus.CounterVec
	RailLatency *prometheus.HistogramVec
}

// NewRegistry builds + registers all collectors against a fresh Registry.
func NewRegistry() *Registry {
	r := prometheus.NewRegistry()
	// Standard Go + process metrics — saves operators from asking
	// "where's heap_alloc?" on day one.
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	out := &Registry{r: r}

	out.HTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atara_pay", Subsystem: "http",
			Name: "requests_total",
			Help: "Total HTTP requests served, partitioned by method, route, status class.",
		},
		[]string{"method", "route", "status_class"},
	)

	out.HTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "atara_pay", Subsystem: "http",
			Name: "request_duration_seconds",
			Help: "Time spent serving a request.",
			Buckets: []float64{
				0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
			},
		},
		[]string{"method", "route"},
	)

	out.LimitViolations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atara_pay", Subsystem: "limits",
			Name: "violations_total",
			Help: "Spending policy denials by violation type.",
		},
		[]string{"type"},
	)

	out.WebhookDeliveries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atara_pay", Subsystem: "webhooks",
			Name: "deliveries_total",
			Help: "Webhook delivery outcomes by terminal state.",
		},
		[]string{"outcome"},
	)

	out.SessionKeyEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atara_pay", Subsystem: "sessionkey",
			Name: "events_total",
			Help: "Session key lifecycle events by action.",
		},
		[]string{"action"},
	)

	out.RailCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "atara_pay", Subsystem: "rail",
			Name: "calls_total",
			Help: "Outbound rail calls (CrossMint REST, Tempo RPC) by outcome.",
		},
		[]string{"rail", "operation", "outcome"},
	)

	out.RailLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "atara_pay", Subsystem: "rail",
			Name: "call_duration_seconds",
			Help: "Outbound rail call latency.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"rail", "operation"},
	)

	r.MustRegister(
		out.HTTPRequests, out.HTTPDuration,
		out.LimitViolations, out.WebhookDeliveries, out.SessionKeyEvents,
		out.RailCalls, out.RailLatency,
	)
	return out
}

// Handler returns the http.Handler mountable on /metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.r, promhttp.HandlerOpts{})
}

// StatusClass returns a low-cardinality bucket label for an HTTP status
// code (2xx / 3xx / 4xx / 5xx / unknown). Used by the HTTP middleware to
// keep HTTPRequests counter cardinality bounded.
func StatusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "unknown"
	}
}
