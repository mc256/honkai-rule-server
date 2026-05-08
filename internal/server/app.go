package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/mc256/honkai-rule-server/internal/auth"
	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/output"
	"github.com/mc256/honkai-rule-server/internal/server/routes"
)

// App holds all the runtime dependencies the HTTP server needs. It owns
// the *http.Server lifecycle and provides Run / Shutdown.
type App struct {
	cfg         *config.ServerConfig
	tokens      *config.TokenStore
	cache       *fetcher.Cache
	coordinator *fetcher.Coordinator
	pipeline    *merge.Pipeline
	adapter     *output.SubscriptionMode
	clock       clock.Clock
	logger      *slog.Logger

	httpSrv *http.Server
}

// NewApp constructs an App from already-initialized dependencies. main.go
// is responsible for wiring (env → config → loaders → cache → fetcher → ...).
func NewApp(
	cfg *config.ServerConfig,
	tokens *config.TokenStore,
	cache *fetcher.Cache,
	coord *fetcher.Coordinator,
	pipeline *merge.Pipeline,
	adapter *output.SubscriptionMode,
	clk clock.Clock,
	log *slog.Logger,
) *App {
	return &App{
		cfg:         cfg,
		tokens:      tokens,
		cache:       cache,
		coordinator: coord,
		pipeline:    pipeline,
		adapter:     adapter,
		clock:       clk,
		logger:      log,
	}
}

// buildMux wires the HTTP routes. Exposed for testing.
func (a *App) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	subscription := routes.Subscription(routes.SubscriptionDeps{
		Coordinator: a.coordinator,
		Pipeline:    a.pipeline,
		Adapter:     a.adapter,
		Logger:      a.logger,
	})

	// Wrap with token authentication (inner)
	subHandler := auth.RequireToken(a.tokens, a.logger)(subscription)

	// Wrap with UA filtering if configured (outer, runs first)
	if len(a.cfg.AllowedUserAgentPrefixes) > 0 {
		subHandler = auth.RequireUserAgent(a.cfg.AllowedUserAgentPrefixes, a.logger)(subHandler)
	}

	// 009 FR-023: optionally mount the subscription handler under a URL
	// prefix. Configured via URL_PATH_PREFIX env var; "" = root.
	if p := a.cfg.URLPathPrefix; p != "" {
		mux.Handle("GET "+p, http.StripPrefix(p, subHandler))
		mux.Handle("GET "+p+"/{$}", http.StripPrefix(p, subHandler))
	} else {
		mux.Handle("GET /{$}", subHandler)
	}

	// /health is unauthenticated by design — exposed only on the cluster-
	// internal Service in production K8s, never via the public Ingress
	// (per contracts/health.openapi.yaml + README §Deploy).
	mux.Handle("GET /health", routes.Health(routes.HealthDeps{
		Coordinator: a.coordinator,
		Pipeline:    a.pipeline,
		Logger:      a.logger,
	}))

	return mux
}

// Run blocks until ctx is canceled. It starts the HTTP listener immediately
// (per FR-003b, the subscription handler returns 503 until the coordinator's
// Ready channel closes). On ctx cancellation, gracefully drains in-flight
// requests with a 10-second deadline.
func (a *App) Run(ctx context.Context) error {
	addr := net.JoinHostPort("", strconv.Itoa(a.cfg.Port))
	a.httpSrv = &http.Server{
		Addr:              addr,
		Handler:           a.buildMux(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		a.logger.Info("server listening", "port", a.cfg.Port)
		err := a.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received; draining HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	case err, ok := <-listenErr:
		if ok && err != nil {
			return fmt.Errorf("http listen: %w", err)
		}
		return nil
	}
}

// Address returns the resolved listen address (host:port). Useful for tests
// that need to know the bound port (when Port==0, the OS assigns one).
func (a *App) Address() string {
	if a.httpSrv == nil {
		return ""
	}
	return a.httpSrv.Addr
}
