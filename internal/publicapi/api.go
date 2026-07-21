package publicapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	responseowner "github.com/luckymaomi/llmgateway/internal/responses"
)

type Identity interface {
	AuthenticateGatewayKey(context.Context, string) (identity.GatewayPrincipal, error)
	GatewayPrincipalByID(context.Context, uuid.UUID) (identity.GatewayPrincipal, error)
}

type Catalog interface {
	Models(context.Context, uuid.UUID) ([]requestflow.Model, error)
}

type Workflow interface {
	Catalog
	Chat(context.Context, requestflow.ChatCommand) (requestflow.ChatResult, *canonical.Error)
	Stream(context.Context, requestflow.ChatCommand, requestflow.StreamSink) *canonical.Error
}

type API struct {
	identity     Identity
	catalog      Catalog
	workflow     Workflow
	responses    ResponseStore
	logger       *slog.Logger
	running      sync.Map
	responseWake chan struct{}
}

type ResponseStore interface {
	Begin(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *uuid.UUID, json.RawMessage) error
	SaveCompleted(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, *uuid.UUID, json.RawMessage, json.RawMessage) error
	Complete(context.Context, uuid.UUID, json.RawMessage) error
	Fail(context.Context, uuid.UUID, json.RawMessage) error
	Get(context.Context, uuid.UUID, uuid.UUID) (responseowner.Record, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	RequestCancellation(context.Context, uuid.UUID, uuid.UUID) error
	Enqueue(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, *string, []byte, json.RawMessage, json.RawMessage) (responseowner.Record, error)
	ClaimNext(context.Context, uuid.UUID, time.Time) (responseowner.Claim, responseowner.Record, error)
	Heartbeat(context.Context, responseowner.Claim) error
	LinkRequest(context.Context, responseowner.Claim, uuid.UUID) error
	StageOutput(context.Context, responseowner.Claim, uuid.UUID, json.RawMessage) error
	CompleteClaim(context.Context, responseowner.Claim, uuid.UUID) error
	TerminateClaim(context.Context, responseowner.Claim, *uuid.UUID, responseowner.Status, json.RawMessage) error
	RecoverOnce(context.Context, int32) (int, error)
}

func New(identityService Identity, workflow Workflow, logger *slog.Logger, stores ...ResponseStore) *API {
	api := &API{identity: identityService, catalog: workflow, workflow: workflow, logger: logger, responseWake: make(chan struct{}, 1)}
	if len(stores) > 0 {
		api.responses = stores[0]
	}
	return api
}

func NewModels(identityService Identity, catalog Catalog, logger *slog.Logger) *API {
	return &API{identity: identityService, catalog: catalog, logger: logger}
}

func (a *API) ModelRoutes() http.Handler {
	router := chi.NewRouter()
	router.Use(a.authenticate)
	router.Get("/models", a.models)
	return router
}

func (a *API) Routes() http.Handler {
	router := chi.NewRouter()
	router.Use(a.authenticate)
	router.Get("/models", a.models)
	router.Post("/chat/completions", a.chatCompletions)
	if a.responses != nil {
		router.Post("/responses", a.createResponse)
		router.Get("/responses/{responseID}", a.getResponse)
		router.Delete("/responses/{responseID}", a.deleteResponse)
		router.Get("/responses/{responseID}/input_items", a.responseInputItems)
		router.Post("/responses/{responseID}/cancel", a.cancelResponse)
	}
	return router
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) identity.GatewayPrincipal {
	principal, _ := ctx.Value(principalContextKey{}).(identity.GatewayPrincipal)
	return principal
}
