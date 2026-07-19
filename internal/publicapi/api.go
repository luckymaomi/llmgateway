package publicapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

type Identity interface {
	AuthenticateGatewayKey(context.Context, string) (identity.GatewayPrincipal, error)
}

type Workflow interface {
	Models(context.Context, uuid.UUID) ([]requestflow.Model, error)
	Chat(context.Context, requestflow.ChatCommand) (requestflow.ChatResult, *canonical.Error)
	Stream(context.Context, requestflow.ChatCommand, requestflow.StreamSink) *canonical.Error
}

type API struct {
	identity  Identity
	workflow  Workflow
	responses ResponseStore
	logger    *slog.Logger
	running   sync.Map
}

type StoredResponse struct {
	RequestID uuid.UUID
	Status    string
	Input     json.RawMessage
	Output    json.RawMessage
	Error     json.RawMessage
}

type ResponseStore interface {
	Begin(context.Context, uuid.UUID, uuid.UUID, json.RawMessage) error
	SaveCompleted(context.Context, uuid.UUID, uuid.UUID, json.RawMessage, json.RawMessage) error
	Complete(context.Context, uuid.UUID, json.RawMessage) error
	Fail(context.Context, uuid.UUID, json.RawMessage) error
	Get(context.Context, uuid.UUID, uuid.UUID) (StoredResponse, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	RequestCancellation(context.Context, uuid.UUID, uuid.UUID) error
}

func New(identityService Identity, workflow Workflow, logger *slog.Logger, stores ...ResponseStore) *API {
	api := &API{identity: identityService, workflow: workflow, logger: logger}
	if len(stores) > 0 {
		api.responses = stores[0]
	}
	return api
}

func (a *API) Routes() http.Handler {
	router := chi.NewRouter()
	router.Use(a.authenticate)
	router.Get("/models", a.models)
	router.Post("/chat/completions", a.chatCompletions)
	router.Post("/responses", a.createResponse)
	router.Get("/responses/{responseID}", a.getResponse)
	router.Delete("/responses/{responseID}", a.deleteResponse)
	router.Get("/responses/{responseID}/input_items", a.responseInputItems)
	router.Post("/responses/{responseID}/cancel", a.cancelResponse)
	return router
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) identity.GatewayPrincipal {
	principal, _ := ctx.Value(principalContextKey{}).(identity.GatewayPrincipal)
	return principal
}
