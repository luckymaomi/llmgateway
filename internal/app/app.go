package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/controlapi"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
	"github.com/luckymaomi/llmgateway/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Application struct {
	config      config.Config
	logger      *slog.Logger
	connections *store.Connections
	projector   *store.ConfigProjector
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
	urlValidator, err := security.NewURLValidator(security.SSRFPolicy{AllowedPrivatePrefixes: cfg.Security.AllowedPrivatePrefixes, AllowedResolvedPrefixes: cfg.Security.AllowedResolvedPrefixes, MaxRedirects: 5})
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize outbound URL policy: %w", err)
	}
	registryService, err := registry.NewService(store.NewRegistryRepository(connections), envelope, urlValidator)
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize registry service: %w", err)
	}
	configurationService, err := configuration.NewService(store.NewConfigurationRepository(connections))
	if err != nil {
		connections.Close()
		return nil, fmt.Errorf("initialize configuration service: %w", err)
	}
	loginGuard := store.NewLoginGuard(connections.Valkey, cfg.Security.LoginAccountAttempts, cfg.Security.LoginAddressAttempts, cfg.Security.LoginWindow)
	controlAPI := controlapi.New(identityService, registryService, configurationService, loginGuard, cfg.Security, logger)

	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           httpserver.NewRouter(cfg, logger, connections, metricsRegistry, controlAPI.Routes()),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	return &Application{config: cfg, logger: logger, connections: connections, projector: store.NewConfigProjector(connections), server: server}, nil
}

func (a *Application) Run(ctx context.Context) error {
	go a.projector.Run(ctx)
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
