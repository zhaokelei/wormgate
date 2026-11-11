package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the Prometheus surface of the server.
type Metrics struct {
	registry *prometheus.Registry

	controlSessions   prometheus.Gauge
	tunnels           prometheus.Gauge
	clientSessions    prometheus.Counter
	publicConns       *prometheus.CounterVec
	traffic           *prometheus.CounterVec
	httpResponses     *prometheus.CounterVec
	registrationsFail prometheus.Counter
}

func newMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		registry: reg,
		controlSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wormgate_control_sessions",
			Help: "Currently authenticated control sessions.",
		}),
		tunnels: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wormgate_tunnels",
			Help: "Currently registered tunnels.",
		}),
		clientSessions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wormgate_client_sessions_total",
			Help: "Control sessions established since start.",
		}),
		publicConns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wormgate_public_connections_total",
			Help: "Public connections admitted, by owner.",
		}, []string{"owner"}),
		traffic: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wormgate_traffic_bytes_total",
			Help: "Tunneled bytes, by owner and direction.",
		}, []string{"owner", "direction"}),
		httpResponses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wormgate_http_responses_total",
			Help: "HTTP responses served on public hostnames, by status code.",
		}, []string{"code"}),
		registrationsFail: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wormgate_registration_failures_total",
			Help: "Tunnel registrations rejected by the server.",
		}),
	}
	reg.MustRegister(
		collectors.NewGoCollector(),
		m.controlSessions,
		m.tunnels,
		m.clientSessions,
		m.publicConns,
		m.traffic,
		m.httpResponses,
		m.registrationsFail,
	)
	return m
}

// Handler exposes the registry for the dedicated metrics listener.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
