package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requestsTotal  *prometheus.CounterVec
	latencySeconds *prometheus.HistogramVec
	costUSDTotal   *prometheus.CounterVec
	registry       *prometheus.Registry
}

func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apprentice_proxy_requests_total",
			Help: "Total number of proxy requests.",
		},
		[]string{"route", "pattern_id", "model", "status"},
	)
	reg.MustRegister(requestsTotal)

	latencySeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "apprentice_proxy_latency_seconds",
			Help: "Request latency in seconds.",
			Buckets: []float64{
				.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120,
			},
		},
		[]string{"route", "pattern_id"},
	)
	reg.MustRegister(latencySeconds)

	costUSDTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apprentice_proxy_cost_usd_total",
			Help: "Total estimated cost in USD.",
		},
		[]string{"route", "model"},
	)
	reg.MustRegister(costUSDTotal)

	return &Metrics{
		requestsTotal:  requestsTotal,
		latencySeconds: latencySeconds,
		costUSDTotal:   costUSDTotal,
		registry:       reg,
	}
}

func (m *Metrics) Observe(route, patternID, model string, status int, latency time.Duration, costUSD float64) {
	statusStr := fmt.Sprintf("%d", status)
	if status >= 200 {
		statusStr = fmt.Sprintf("%dxx", status/100)
	}
	m.requestsTotal.WithLabelValues(route, patternID, model, statusStr).Inc()
	m.latencySeconds.WithLabelValues(route, patternID).Observe(latency.Seconds())
	if costUSD >= 0 {
		m.costUSDTotal.WithLabelValues(route, model).Add(costUSD)
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}
