package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/controlapi"
	"github.com/luckymaomi/llmgateway/internal/credentialprobe"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/publicapi"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	responseowner "github.com/luckymaomi/llmgateway/internal/responses"
	"github.com/luckymaomi/llmgateway/internal/security"
	"github.com/luckymaomi/llmgateway/internal/store"
	webassets "github.com/luckymaomi/llmgateway/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Application struct {
	config      config.Config
	logger      *slog.Logger
	connections *store.Connections
	workflow    *requestflow.Service
	publicAPI   *publicapi.API
	server      *http.Server
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Application, error) {
	connections, err := store.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}

	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	identityService, err := identity.NewService(store.NewIdentityRepository(connections.Postgres), cfg.Security.SessionPepper, cfg.Security.APIKeyPepper)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize identity service: %w", err)
	}
	envelope, err := security.NewEnvelopeCipher(cfg.Security.ActiveMasterKeyVersion, cfg.Security.MasterKeys)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize envelope cipher: %w", err)
	}
	urlValidator, err := security.NewURLValidator(security.SSRFPolicy{AllowedPrivatePrefixes: cfg.Security.AllowedPrivatePrefixes, AllowedResolvedPrefixes: cfg.Security.AllowedResolvedPrefixes, AllowLoopback: cfg.Profile == config.ProfileTest, MaxRedirects: 5})
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize outbound URL policy: %w", err)
	}
	registryService, err := registry.NewService(store.NewRegistryRepository(connections), envelope, urlValidator)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize registry service: %w", err)
	}
	rootCAs, err := providerRootCAs(cfg.Security.ProviderCABundleFile)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("load provider CA bundle for credential probes: %w", err)
	}
	probeExecutor, err := credentialprobe.New(security.SSRFPolicy{
		AllowedPrivatePrefixes: cfg.Security.AllowedPrivatePrefixes, AllowedResolvedPrefixes: cfg.Security.AllowedResolvedPrefixes,
		AllowLoopback: cfg.Profile == config.ProfileTest, MaxRedirects: 5, RootCAs: rootCAs,
	}, cfg.ProviderProbe.Timeout, cfg.ProviderProbe.MaxResponseBytes)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize credential probe: %w", err)
	}
	registryService.WithCredentialProbeExecutor(probeExecutor)
	configurationService, err := configuration.NewService(store.NewConfigurationRepository(connections))
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize configuration service: %w", err)
	}
	loginGuard := store.NewLoginGuard(connections.Valkey, cfg.Security.LoginAccountAttempts, cfg.Security.LoginAddressAttempts, cfg.Security.LoginWindow)
	quotaService, err := quota.NewService(store.NewQuotaRepository(connections))
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize quota service: %w", err)
	}
	quotaAPI := controlapi.NewQuotaAPI(quotaService, identityService, registryService, logger)
	controlAPI := controlapi.New(identityService, registryService, configurationService, loginGuard, cfg.Security, logger).WithQuotaAPI(quotaAPI)
	workflow, err := newRequestWorkflow(cfg, connections, registryService, quotaService)
	if err != nil {
		connections.Close()
		return nil, err
	}
	controlAPI.WithPlaygroundWorkflow(workflow)
	responseService, err := responseowner.NewService(store.NewResponseRepository(connections), envelope)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize response service: %w", err)
	}
	publicAPI := publicapi.New(identityService, workflow, logger, responseService)

	assets, embedded := webassets.Assets()
	routerAssets := []fs.FS(nil)
	if embedded {
		routerAssets = append(routerAssets, assets)
	}
	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           httpserver.NewRouter(cfg, logger, connections, metricsRegistry, controlAPI.Routes(), publicAPI.Routes(), routerAssets...),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	return &Application{config: cfg, logger: logger, connections: connections, workflow: workflow, publicAPI: publicAPI, server: server}, nil
}

func (a *Application) Run(ctx context.Context) error {
	go a.runRequestRecovery(ctx)
	go a.publicAPI.RunResponseWorker(ctx, publicapi.ResponseWorkerConfig{
		PollInterval: a.config.Responses.PollInterval, HeartbeatInterval: a.config.Responses.HeartbeatInterval,
		StaleAfter: a.config.Responses.StaleAfter, RecoveryBatchSize: a.config.Responses.RecoveryBatchSize,
		MaxWorkers: a.config.Responses.MaxWorkers,
	})
	errorChannel := make(chan error, 1)
	go func() {
		a.logger.Info("gateway listening", "address", a.server.Addr, "profile", a.config.Profile)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorChannel <- err
			return
		}
		errorChannel <- nil
	}()

	select {
	case err := <-errorChannel:
		_ = a.connections.Close()
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.config.HTTP.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		_ = a.server.Close()
		_ = a.connections.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	select {
	case err := <-errorChannel:
		if closeErr := a.connections.Close(); closeErr != nil {
			return fmt.Errorf("close valkey: %w", closeErr)
		}
		return err
	case <-time.After(time.Second):
		_ = a.connections.Close()
		return errors.New("HTTP server did not stop after shutdown")
	}
}

func (a *Application) runRequestRecovery(ctx context.Context) {
	run := func() {
		staleBefore := time.Now().UTC().Add(-a.config.RequestFlow.ExecutionStaleAfter)
		result, err := a.workflow.RecoverOnce(ctx, staleBefore, a.config.RequestFlow.RecoveryBatchSize)
		if err != nil {
			a.logger.Error("request recovery failed", "error", err)
			return
		}
		if result.Settled > 0 || result.Released > 0 || result.Uncertain > 0 {
			a.logger.Info("request recovery completed", "settled", result.Settled, "released", result.Released, "uncertain", result.Uncertain)
		}
	}
	run()
	ticker := time.NewTicker(a.config.RequestFlow.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
