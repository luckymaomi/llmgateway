package controlapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

const (
	sessionCookieName = "llmgateway_session"
	csrfCookieName    = "llmgateway_csrf"
)

type identityService interface {
	IsBootstrapped(context.Context) (bool, error)
	Bootstrap(context.Context, string, string, string) (identity.SessionCredentials, error)
	Register(context.Context, string, string, string, string) (identity.User, error)
	Login(context.Context, string, string) (identity.SessionCredentials, error)
	AuthenticateSession(context.Context, string) (identity.Principal, error)
	VerifyCSRF(identity.Principal, string) bool
	Logout(context.Context, identity.Principal) error
	ListUsers(context.Context, identity.Principal, *identity.Status, identity.Page) (identity.UserPage, error)
	SetUserStatus(context.Context, identity.Principal, uuid.UUID, identity.Status) (identity.User, error)
	CreateInvitation(context.Context, identity.Principal, identity.Role, time.Duration) (identity.Invitation, error)
	ListInvitations(context.Context, identity.Principal, identity.Page) ([]identity.Invitation, error)
	RevokeInvitation(context.Context, identity.Principal, uuid.UUID) error
	CreateGatewayKey(context.Context, identity.Principal, uuid.UUID, string, *time.Time) (identity.GatewayKey, error)
	ListGatewayKeys(context.Context, identity.Principal, uuid.UUID) ([]identity.GatewayKey, error)
	RevokeGatewayKey(context.Context, identity.Principal, uuid.UUID) error
}

type registryService interface {
	CreateProvider(context.Context, identity.Principal, registry.Provider) (registry.Provider, error)
	UpdateProvider(context.Context, identity.Principal, registry.Provider) (registry.Provider, error)
	SetProviderEnabled(context.Context, identity.Principal, uuid.UUID, bool, time.Time) (registry.Provider, error)
	ListProviders(context.Context, identity.Principal) ([]registry.Provider, error)
	CreateModel(context.Context, identity.Principal, registry.Model) (registry.Model, error)
	UpdateModel(context.Context, identity.Principal, registry.Model) (registry.Model, error)
	ListModels(context.Context, identity.Principal) ([]registry.Model, error)
	CreateCredential(context.Context, identity.Principal, registry.NewCredential, string) (registry.Credential, error)
	ListCredentials(context.Context, identity.Principal) ([]registry.Credential, error)
	BindCredentialModel(context.Context, identity.Principal, uuid.UUID, uuid.UUID, int32, int32) error
}

type configurationService interface {
	Active(context.Context, identity.Principal) (configuration.Active, error)
	ListRevisions(context.Context, identity.Principal, int32, int32) ([]configuration.Revision, error)
	Publish(context.Context, identity.Principal, uuid.UUID, int64) (configuration.Active, error)
}

type LoginGuard interface {
	Check(context.Context, string, string) (time.Duration, error)
	Reset(context.Context, string) error
}

type API struct {
	identity      identityService
	registry      registryService
	configuration configurationService
	loginGuard    LoginGuard
	config        config.Security
	logger        *slog.Logger
}

func New(identity identityService, registry registryService, configuration configurationService, loginGuard LoginGuard, securityConfig config.Security, logger *slog.Logger) *API {
	return &API{
		identity:      identity,
		registry:      registry,
		configuration: configuration,
		loginGuard:    loginGuard,
		config:        securityConfig,
		logger:        logger,
	}
}

func (a *API) Routes() http.Handler {
	router := chi.NewRouter()
	router.Route("/control", func(control chi.Router) {
		control.Get("/setup/status", a.setupStatus)
		control.Post("/setup", a.bootstrap)
		control.Post("/session", a.login)
		control.Post("/registrations", a.register)

		control.Group(func(authenticated chi.Router) {
			authenticated.Use(a.authenticate)
			authenticated.Get("/session", a.session)
			authenticated.With(a.requireCSRF).Delete("/session", a.logout)

			a.registerAccessRoutes(authenticated)
			a.registerRegistryRoutes(authenticated)
			a.registerConfigurationRoutes(authenticated)
			a.registerUnavailableRoutes(authenticated)
		})
	})
	return router
}

func (a *API) registerAccessRoutes(router chi.Router) {
	router.With(a.requireAdministrator).Get("/users", a.listUsers)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/users/{userID}/review", a.reviewUser)
	router.With(a.requireAdministrator).Get("/invitations", a.listInvitations)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/invitations", a.createInvitation)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/invitations/{invitationID}/revoke", a.revokeInvitation)
	router.Get("/keys", a.listKeys)
	router.With(a.requireCSRF).Post("/keys", a.createKey)
	router.With(a.requireCSRF).Post("/keys/{keyID}/revoke", a.revokeKey)
}

func (a *API) registerRegistryRoutes(router chi.Router) {
	router.With(a.requireOperator).Get("/providers", a.listProviders)
	router.With(a.requireOperator, a.requireCSRF).Post("/providers", a.createProvider)
	router.With(a.requireOperator, a.requireCSRF).Put("/providers/{providerID}", a.updateProvider)
	router.With(a.requireOperator, a.requireCSRF).Put("/providers/{providerID}/status", a.setProviderStatus)

	router.With(a.requireOperator).Get("/models", a.listModels)
	router.With(a.requireOperator, a.requireCSRF).Post("/models", a.createModel)
	router.With(a.requireOperator, a.requireCSRF).Put("/models/{modelID}", a.updateModel)

	router.With(a.requireOperator).Get("/credentials", a.listCredentials)
	router.With(a.requireOperator, a.requireCSRF).Post("/credentials", a.createCredential)
	router.With(a.requireOperator, a.requireCSRF).Put("/credentials/{credentialID}", a.unavailable("credential_updates"))
	router.With(a.requireOperator, a.requireCSRF).Put("/credentials/{credentialID}/status", a.unavailable("credential_status"))
	router.With(a.requireOperator, a.requireCSRF).Post("/credentials/{credentialID}/tests", a.unavailable("credential_tests"))
}

func (a *API) registerConfigurationRoutes(router chi.Router) {
	router.With(a.requireOperator).Get("/configuration/revisions", a.listConfigurationRevisions)
	router.With(a.requireOperator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/validate", a.validateConfigurationRevision)
	router.With(a.requireOperator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/publish", a.publishConfigurationRevision)
	router.With(a.requireOperator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/rollback", a.rollbackConfigurationRevision)
}

func (a *API) registerUnavailableRoutes(router chi.Router) {
	router.Get("/overview", a.unavailable("overview"))
	router.Get("/requests", a.unavailable("request_operations"))
	router.Get("/requests/{requestID}", a.unavailable("request_operations"))
	router.Get("/audit/events", a.unavailable("audit_events"))
	router.Get("/content-records", a.unavailable("content_records"))
	router.With(a.requireCSRF).Post("/content-records/{recordID}/access", a.unavailable("content_records"))
	router.With(a.requireCSRF).Post("/content-records/{recordID}/deletion", a.unavailable("content_records"))
	router.Get("/operations/{operationID}", a.unavailable("background_operations"))
	router.With(a.requireCSRF).Post("/operations/{operationID}/cancel", a.unavailable("background_operations"))
	router.Get("/playground/models", a.unavailable("playground"))
	router.With(a.requireCSRF).Post("/playground/runs", a.unavailable("playground"))
	router.Get("/settings/{section}", a.unavailable("settings"))
	router.With(a.requireCSRF).Put("/settings/{section}", a.unavailable("settings"))
	router.With(a.requireCSRF).Post("/settings/backups/runs", a.unavailable("backups"))
}
