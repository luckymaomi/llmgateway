package httpserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewHTTPMetrics(registry prometheus.Registerer) *HTTPMetrics {
	metrics := &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "llmgateway",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Completed HTTP requests.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "llmgateway",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	registry.MustRegister(metrics.requests, metrics.duration)
	return metrics
}

func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(response.status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(started).Seconds())
	})
}
