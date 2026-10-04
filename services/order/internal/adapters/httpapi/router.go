package httpapi

import (
	"context"
	"net/http"
	"time"
)

type ReadinessCheck func(context.Context) error

func NewRouter(orderHandler *OrderHandler, readiness ReadinessCheck) http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("POST /v1/orders", orderHandler.Create)
	router.HandleFunc("GET /health/live", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "UP"})
	})
	router.HandleFunc("GET /health/ready", func(response http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
		defer cancel()
		if err := readiness(ctx); err != nil {
			writeError(response, http.StatusServiceUnavailable, "NOT_READY", "service dependency is unavailable")
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "READY"})
	})
	return router
}

