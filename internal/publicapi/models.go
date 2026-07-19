package publicapi

import (
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/httpserver"
)

func (a *API) models(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	models, err := a.workflow.Models(r.Context(), principal.UserID)
	if err != nil {
		a.logger.Error("list public models failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		httpserver.WriteProblem(w, httpserver.Problem{Type: "about:blank", Title: "Service unavailable", Status: http.StatusServiceUnavailable, Code: "model_catalog_unavailable", RequestID: httpserver.RequestIDFromContext(r.Context())})
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{
			"id": model.PublicName, "object": "model", "created": model.CreatedAt.Unix(), "owned_by": model.ProviderSlug,
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}
