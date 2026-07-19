package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Readiness interface {
	Ready(context.Context) error
}

func NewRouter(cfg config.Config, logger *slog.Logger, readiness Readiness, registry *prometheus.Registry, controlAPI http.Handler) http.Handler {
	router := chi.NewRouter()
	metrics := NewHTTPMetrics(registry)
	router.Use(RequestID)
	router.Use(SecurityHeaders)
	router.Use(Recover(logger))
	router.Use(AccessLog(logger))
	router.Use(metrics.Middleware)

	router.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})
	router.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := readiness.Ready(ctx); err != nil {
			WriteProblem(w, Problem{Type: "about:blank", Title: "Service unavailable", Status: http.StatusServiceUnavailable, Detail: "Required storage is unavailable.", Code: "not_ready", RequestID: RequestIDFromContext(r.Context())})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	router.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if controlAPI != nil {
		router.Mount("/api", controlAPI)
	}

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, Problem{Type: "about:blank", Title: "Not found", Status: http.StatusNotFound, Code: "not_found", RequestID: RequestIDFromContext(r.Context())})
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, Problem{Type: "about:blank", Title: "Method not allowed", Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", RequestID: RequestIDFromContext(r.Context())})
	})

	return http.MaxBytesHandler(router, cfg.HTTP.MaxBodyBytes)
}
