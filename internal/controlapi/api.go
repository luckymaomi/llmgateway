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
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/luckymaomi/llmgateway/internal/subscription"
)

const (
	sessionCookieName = "llmgateway_session"
	csrfCookieName    = "llmgateway_csrf"
)

type identityService interface {
	IsBootstrapped(context.Context) (bool, error)
	Bootstrap(context.Context, string) (identity.BootstrapCredentials, error)
	Login(context.Context, string, string) (identity.SessionCredentials, error)
	AuthenticateSession(context.Context, string) (identity.Principal, error)
	VerifyCSRF(identity.Principal, string) bool
	Logout(context.Context, identity.Principal) error
	ChangePassword(context.Context, identity.Principal, string, string, string) (identity.SessionRevocation, error)
	ListUsers(context.Context, identity.Principal, *identity.Status, string, identity.Page) (identity.UserPage, error)
	CreateMember(context.Context, identity.Principal, string, string, identity.MutationRequest) (identity.MemberCredentials, error)
	UpdateMember(context.Context, identity.Principal, identity.MemberChange, identity.MutationRequest) (identity.User, error)
	SetUserStatus(context.Context, identity.Principal, uuid.UUID, identity.Status, identity.MutationRequest) (identity.User, error)
	DeleteMember(context.Context, identity.Principal, uuid.UUID, identity.MutationRequest) (identity.User, error)
	ResetMemberPassword(context.Context, identity.Principal, uuid.UUID, string, identity.MutationRequest) (identity.SessionRevocation, error)
	RevokeUserSessions(context.Context, identity.Principal, uuid.UUID, string) (identity.SessionRevocation, error)
	CreateGatewayKey(context.Context, identity.Principal, uuid.UUID, string, []uuid.UUID, *time.Time, identity.MutationRequest) (identity.GatewayKey, error)
	ReplaceGatewayKey(context.Context, identity.Principal, uuid.UUID, identity.MutationRequest) (identity.GatewayKey, error)
	ListGatewayKeys(context.Context, identity.Principal, uuid.UUID) ([]identity.GatewayKey, error)
	RevokeGatewayKey(context.Context, identity.Principal, uuid.UUID) error
}

type registryService interface {
	ListProviders(context.Context, identity.Principal) ([]registry.Provider, error)
	GetProvider(context.Context, identity.Principal, uuid.UUID) (registry.Provider, error)
	ListModels(context.Context, identity.Principal) ([]registry.Model, error)
	CreateResourcePool(context.Context, identity.Principal, registry.NewResourcePool, registry.MutationRequest) (registry.ResourcePool, error)
	UpdateResourcePool(context.Context, identity.Principal, registry.ResourcePoolChange, registry.MutationRequest) (registry.ResourcePool, error)
	SetResourcePoolStatus(context.Context, identity.Principal, uuid.UUID, registry.ResourcePoolStatus, time.Time, registry.MutationRequest) (registry.ResourcePool, error)
	ListResourcePools(context.Context, identity.Principal, bool) ([]registry.ResourcePool, error)
	GetResourcePool(context.Context, identity.Principal, uuid.UUID) (registry.ResourcePool, error)
	CreateCredential(context.Context, identity.Principal, registry.NewCredential, string, registry.MutationRequest) (registry.Credential, error)
	ImportCredentials(context.Context, identity.Principal, uuid.UUID, []registry.CredentialBatchItem, []registry.CredentialModelBinding, *int32, *int64, *int32, registry.MutationRequest) ([]registry.CredentialBatchResult, error)
	UpdateCredential(context.Context, identity.Principal, registry.CredentialChange, string, registry.MutationRequest) (registry.Credential, error)
	SetCredentialStatus(context.Context, identity.Principal, uuid.UUID, registry.CredentialStatus, time.Time, registry.MutationRequest) (registry.Credential, error)
	RetireCredential(context.Context, identity.Principal, uuid.UUID, time.Time, registry.MutationRequest) (registry.Credential, error)
	ProbeCredential(context.Context, identity.Principal, uuid.UUID, uuid.UUID, string) (registry.CredentialProbeExecution, registry.Credential, error)
	ListCredentials(context.Context, identity.Principal, bool) ([]registry.Credential, error)
}

type subscriptionService interface {
	PublishPlan(context.Context, identity.Principal, subscription.PlanDraft, subscription.MutationRequest) (subscription.ServicePlan, error)
	SetPlanStatus(context.Context, identity.Principal, uuid.UUID, subscription.PlanStatus, subscription.MutationRequest) (subscription.ServicePlan, error)
	ListPlans(context.Context, identity.Principal, bool) ([]subscription.ServicePlan, error)
	CreateSubscription(context.Context, identity.Principal, subscription.NewSubscription, subscription.MutationRequest) (subscription.Subscription, error)
	UpdateSubscription(context.Context, identity.Principal, subscription.SubscriptionChange, subscription.MutationRequest) (subscription.Subscription, error)
	SetSubscriptionStatus(context.Context, identity.Principal, uuid.UUID, subscription.SubscriptionStatus, time.Time, subscription.MutationRequest) (subscription.Subscription, error)
	ListSubscriptions(context.Context, identity.Principal, subscription.Query) (subscription.Page, error)
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
	subscriptions  subscriptionService
	loginGuard     LoginGuard
	config         config.Security
	logger         *slog.Logger
	quota          *QuotaAPI
	costing        *CostingAPI
	gatewayKeyTest gatewayKeyTestWorkflow
	siteProfile    *SiteProfileAPI
	operations     *OperationsAPI
}

func New(identity identityService, registry registryService, subscriptions subscriptionService, loginGuard LoginGuard, securityConfig config.Security, logger *slog.Logger) *API {
	return &API{identity: identity, registry: registry, subscriptions: subscriptions, loginGuard: loginGuard, config: securityConfig, logger: logger}
}

func (a *API) WithSiteProfileAPI(value *SiteProfileAPI) *API { a.siteProfile = value; return a }
func (a *API) WithOperationsAPI(value *OperationsAPI) *API   { a.operations = value; return a }
func (a *API) WithCostingAPI(value *CostingAPI) *API         { a.costing = value; return a }
func (a *API) WithQuotaAPI(value *QuotaAPI) *API             { a.quota = value; return a }
func (a *API) WithGatewayKeyTestWorkflow(value gatewayKeyTestWorkflow) *API {
	a.gatewayKeyTest = value
	return a
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

		control.Group(func(authenticated chi.Router) {
			authenticated.Use(a.authenticate)
			authenticated.Get("/session", a.session)
			authenticated.With(a.requireCSRF).Delete("/session", a.logout)
			authenticated.With(a.requireCSRF).Post("/password", a.changePassword)
			a.registerAccessRoutes(authenticated)
			a.registerRegistryRoutes(authenticated)
			a.registerSubscriptionRoutes(authenticated)
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
	router.With(a.requireAdministrator).Get("/members", a.listUsers)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/members", a.createMember)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/members/{userID}", a.updateMember)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/members/{userID}/status", a.setUserStatus)
	router.With(a.requireAdministrator, a.requireCSRF).Delete("/members/{userID}", a.deleteMember)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/members/{userID}/password", a.resetMemberPassword)
	router.Get("/keys", a.listKeys)
	router.With(a.requireCSRF).Post("/keys", a.createKey)
	router.With(a.requireCSRF).Post("/keys/{keyID}/revoke", a.revokeKey)
	router.With(a.requireCSRF).Post("/keys/{keyID}/replacement", a.replaceKey)
}

func (a *API) registerRegistryRoutes(router chi.Router) {
	router.With(a.requireProviderAdministrator).Get("/providers", a.listProviders)
	router.With(a.requireProviderAdministrator).Get("/providers/{providerID}", a.getProvider)
	router.Get("/models", a.listModels)
	router.With(a.requireProviderAdministrator).Get("/resource-pools", a.listResourcePools)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/resource-pools", a.createResourcePool)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/resource-pools/{resourcePoolID}", a.updateResourcePool)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/resource-pools/{resourcePoolID}/status", a.setResourcePoolStatus)
	router.With(a.requireProviderAdministrator).Get("/credentials", a.listCredentials)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/credentials", a.createCredential)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/credentials/batch", a.importCredentials)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/credentials/{credentialID}", a.updateCredential)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Put("/credentials/{credentialID}/status", a.setCredentialStatus)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Delete("/credentials/{credentialID}", a.retireCredential)
	router.With(a.requireProviderAdministrator, a.requireCSRF).Post("/credentials/{credentialID}/probe", a.probeCredential)
}

func (a *API) registerSubscriptionRoutes(router chi.Router) {
	router.With(a.requireAdministrator).Get("/plans", a.listPlans)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/plans", a.publishPlan)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/plans/{planID}", a.publishPlan)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/plans/{planID}/status", a.setPlanStatus)
	router.Get("/subscriptions", a.listSubscriptions)
	router.With(a.requireAdministrator, a.requireCSRF).Post("/subscriptions", a.createSubscription)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/subscriptions/{subscriptionID}", a.updateSubscription)
	router.With(a.requireAdministrator, a.requireCSRF).Put("/subscriptions/{subscriptionID}/status", a.setSubscriptionStatus)
}
