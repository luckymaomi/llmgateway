package controlapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

const (
	sessionCookieName = "llmgateway_session"
	csrfCookieName    = "llmgateway_csrf"
)

type identityService interface {
	IsBootstrapped(context.Context) (bool, error)
	Bootstrap(context.Context, string) (identity.BootstrapCredentials, error)
	Register(context.Context, string, string, string, string) (identity.User, error)
	Login(context.Context, string, string) (identity.SessionCredentials, error)
	AuthenticateSession(context.Context, string) (identity.Principal, error)
	UserDisplayNames(context.Context, identity.Principal, []uuid.UUID) (map[uuid.UUID]string, error)
	VerifyCSRF(identity.Principal, string) bool
	Logout(context.Context, identity.Principal) error
	ChangePassword(context.Context, identity.Principal, string, string, string) (identity.SessionRevocation, error)
	ListUsers(context.Context, identity.Principal, *identity.Status, identity.Page) (identity.UserPage, error)
	SetUserStatus(context.Context, identity.Principal, uuid.UUID, identity.Status) (identity.User, error)
	ResetMemberPassword(context.Context, identity.Principal, uuid.UUID, string, identity.MutationRequest) (identity.SessionRevocation, error)
	RevokeUserSessions(context.Context, identity.Principal, uuid.UUID, string) (identity.SessionRevocation, error)
	CreateInvitation(context.Context, identity.Principal, time.Time, identity.MutationRequest) (identity.Invitation, error)
	ListInvitations(context.Context, identity.Principal, identity.Page) ([]identity.Invitation, error)
	RevokeInvitation(context.Context, identity.Principal, uuid.UUID) error
	CreateGatewayKey(context.Context, identity.Principal, uuid.UUID, string, []uuid.UUID, *time.Time, identity.MutationRequest) (identity.GatewayKey, error)
	ReplaceGatewayKey(context.Context, identity.Principal, uuid.UUID, identity.MutationRequest) (identity.GatewayKey, error)
	ListGatewayKeys(context.Context, identity.Principal, uuid.UUID) ([]identity.GatewayKey, error)
	RevokeGatewayKey(context.Context, identity.Principal, uuid.UUID) error
}

type registryService interface {
	InstallProviderPreset(context.Context, identity.Principal, string, registry.MutationRequest) (registry.ProviderPresetInstallation, error)
	CreateProvider(context.Context, identity.Principal, registry.Provider, registry.MutationRequest) (registry.Provider, error)
	UpdateProvider(context.Context, identity.Principal, registry.Provider, registry.MutationRequest) (registry.Provider, error)
	SetProviderEnabled(context.Context, identity.Principal, uuid.UUID, bool, time.Time, registry.MutationRequest) (registry.Provider, error)
	ListProviders(context.Context, identity.Principal) ([]registry.Provider, error)
	GetProvider(context.Context, identity.Principal, uuid.UUID) (registry.Provider, error)
	CreateModel(context.Context, identity.Principal, registry.Model) (registry.Model, error)
	UpdateModel(context.Context, identity.Principal, registry.Model) (registry.Model, error)
	ListModels(context.Context, identity.Principal) ([]registry.Model, error)
	CreateCredential(context.Context, identity.Principal, registry.NewCredential, string, registry.MutationRequest) (registry.Credential, error)
	UpdateCredential(context.Context, identity.Principal, registry.CredentialChange, string, registry.MutationRequest) (registry.Credential, error)
	SetCredentialEnabled(context.Context, identity.Principal, uuid.UUID, bool, time.Time, registry.MutationRequest) (registry.Credential, error)
	ProbeCredential(context.Context, identity.Principal, uuid.UUID, uuid.UUID, string) (registry.CredentialProbeExecution, registry.Credential, error)
	ListCredentials(context.Context, identity.Principal) ([]registry.Credential, error)
}

type configurationService interface {
	CreateRevision(context.Context, identity.Principal, configuration.MutationRequest) (configuration.Revision, error)
	Active(context.Context, identity.Principal) (configuration.Active, error)
	ActiveCatalog(context.Context, identity.Principal) (configuration.Active, configuration.Catalog, error)
	ListRevisions(context.Context, identity.Principal, int32, int32) ([]configuration.Revision, error)
	Publish(context.Context, identity.Principal, uuid.UUID, int64, configuration.MutationAction, configuration.MutationRequest) (configuration.Active, error)
}

type LoginGuard interface {
	Check(context.Context, string, string) (time.Duration, error)
	Reset(context.Context, string) error
}

type gatewayKeyTestWorkflow interface {
	Models(context.Context, uuid.UUID) ([]requestflow.Model, error)
	Stream(context.Context, requestflow.ChatCommand, requestflow.StreamSink) *canonical.Error
}

type API struct {
	identity       identityService
	registry       registryService
	configuration  configurationService
	loginGuard     LoginGuard
	config         config.Security
	logger         *slog.Logger
	quota          *QuotaAPI
	costing        *CostingAPI
	gatewayKeyTest gatewayKeyTestWorkflow
	siteProfile    *SiteProfileAPI
	operations     *OperationsAPI
}

func (a *API) WithSiteProfileAPI(siteProfileAPI *SiteProfileAPI) *API {
	a.siteProfile = siteProfileAPI
	return a
}

func (a *API) WithOperationsAPI(operationsAPI *OperationsAPI) *API {
	a.operations = operationsAPI
	return a
}

func (a *API) WithCostingAPI(costingAPI *CostingAPI) *API {
	a.costing = costingAPI
	return a
}

func (a *API) WithQuotaAPI(quotaAPI *QuotaAPI) *API {
	a.quota = quotaAPI
	return a
}

func (a *API) WithGatewayKeyTestWorkflow(workflow gatewayKeyTestWorkflow) *API {
	a.gatewayKeyTest = workflow
	return a
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
		if a.siteProfile != nil {
			control.Get("/site-profile", a.siteProfile.get)
		}
		control.Get("/setup/status", a.setupStatus)
		control.Post("/setup", a.bootstrap)
		control.Post("/session", a.login)
		control.Post("/registrations", a.register)

		control.Group(func(authenticated chi.Router) {
			authenticated.Use(a.authenticate)
			authenticated.Get("/session", a.session)
			authenticated.With(a.requireCSRF).Delete("/session", a.logout)
			authenticated.With(a.requireCSRF).Post("/password", a.changePassword)

			a.registerAccessRoutes(authenticated)
			a.registerRegistryRoutes(authenticated)
			a.registerConfigurationRoutes(authenticated)
			if a.operations != nil {
				a.operations.RegisterRoutes(authenticated)
			}
			if a.siteProfile != nil {
				a.siteProfile.RegisterAuthenticatedRoutes(authenticated, a.requireAdministrator, a.requireCSRF)
			}
			if a.quota != nil {
				a.quota.RegisterRoutes(authenticated, a.requireAdministrator, a.requireCSRF)
			}
			if a.costing != nil {
				a.costing.RegisterRoutes(authenticated, a.requireAdministrator, a.requireCSRF)
			}
			if a.gatewayKeyTest != nil {
				a.registerGatewayKeyTestRoutes(authenticated)
			}
		})
	})
	return router
}

func (a *API) registerAccessRoutes(router chi.Router) {
	router.With(a.requireAdministrator).Get("/users", a.listUsers)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/users/{userID}/review", a.reviewUser)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/users/{userID}/password", a.resetMemberPassword)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/users/{userID}/sessions/revoke", a.revokeUserSessions)
	router.With(a.requireAdministrator).Get("/invitations", a.listInvitations)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/invitations", a.createInvitation)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/invitations/{invitationID}/revoke", a.revokeInvitation)
	router.Get("/keys", a.listKeys)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/keys", a.createKey)
	router.With(a.requireCSRF).Post("/keys/{keyID}/revoke", a.revokeKey)
	router.With(a.requireCSRF).Post("/keys/{keyID}/replacement", a.replaceKey)
}

func (a *API) registerRegistryRoutes(router chi.Router) {
	router.With(a.requireProviderAdministrator).Get("/provider-kinds", a.listProviderKinds)
	router.With(a.requireProviderAdministrator).Get("/provider-presets", a.listProviderPresets)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/provider-presets/{presetID}/install", a.installProviderPreset)
	router.With(a.requireProviderAdministrator).Get("/providers", a.listProviders)
	router.With(a.requireProviderAdministrator).Get("/providers/{providerID}", a.getProvider)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/providers", a.createProvider)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/providers/{providerID}", a.updateProvider)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/providers/{providerID}/status", a.setProviderStatus)

	router.With(a.requireProviderAdministrator).Get("/models", a.listModels)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/models", a.createModel)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/models/{modelID}", a.updateModel)

	router.With(a.requireProviderAdministrator).Get("/credentials", a.listCredentials)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/credentials", a.createCredential)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/credentials/{credentialID}", a.updateCredential)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/credentials/{credentialID}/status", a.setCredentialStatus)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/credentials/{credentialID}/probe", a.probeCredential)
}

func (a *API) registerConfigurationRoutes(router chi.Router) {
	router.With(a.requireProviderAdministrator).Get("/configuration/active", a.getActiveConfiguration)
	router.With(a.requireProviderAdministrator).Get("/configuration/revisions", a.listConfigurationRevisions)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/configuration/revisions", a.captureConfigurationRevision)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/validate", a.validateConfigurationRevision)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/publish", a.publishConfigurationRevision)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/configuration/revisions/{revisionID}/rollback", a.rollbackConfigurationRevision)
}
