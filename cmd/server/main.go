// Command server is the entry point for honkai-rule-server.
//
// Wires: env → ServerConfig → loaders (subscriptions CSV, tokens JSON) →
// fetcher cache + coordinator → merge pipeline → output adapter → HTTP
// server. Handles SIGTERM/SIGINT for graceful shutdown.
//
// Per quickstart §3, all paths come from environment variables.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// 011: embed the IANA timezone database in the binary so
	// time.LoadLocation works inside the FROM-scratch runtime image
	// (which has no /usr/share/zoneinfo). Required for the
	// DAILY_BUDGET_TIMEZONE = "America/Toronto" default.
	_ "time/tzdata"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/customrules"
	"github.com/mc256/honkai-rule-server/internal/dailyspend"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/observability"
	"github.com/mc256/honkai-rule-server/internal/output"
	"github.com/mc256/honkai-rule-server/internal/server"
)

// realEnv satisfies config.Env using os.Getenv.
type realEnv struct{}

func (realEnv) Getenv(k string) string { return os.Getenv(k) }

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(realEnv{})
	if err != nil {
		return err
	}

	log := observability.New(cfg.LogLevel, os.Stdout)
	slog.SetDefault(log)
	clk := clock.RealClock{}

	// Subscriptions CSV.
	subs, err := config.LoadSubscriptions(cfg.SubscriptionsCSVPath)
	if err != nil {
		return fmt.Errorf("subscriptions: %w", err)
	}
	enabled, disabled := splitEnabled(subs)
	log.Info("loaded subscriptions CSV",
		"sources", names(enabled),
		"disabled", names(disabled),
	)

	// Tokens JSON.
	tokens := config.NewTokenStore(clk)
	if err := tokens.Load(cfg.TokensPath); err != nil {
		return fmt.Errorf("tokens: %w", err)
	}
	log.Info("loaded token store",
		"active_tokens", tokens.ActiveCount(),
		"revoked_tokens", tokens.RevokedCount(),
	)

	// Background context for fsnotify watchers and coordinator goroutines.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Hot-reload tokens on disk change. Updates channel is unbuffered + nil
	// here because we don't need to block on reload events in the main loop.
	if err := tokens.Watch(ctx, cfg.TokensPath, log, nil); err != nil {
		return fmt.Errorf("tokens watcher: %w", err)
	}

	// Cache + fetcher.
	cache := fetcher.NewCache(clk, cfg.CacheDir)
	if err := cache.LoadFromDisk(ctx, log); err != nil {
		log.Warn("cache rehydrate failed (cold start will refetch)", "err", err.Error())
	}
	httpFetcher := fetcher.NewUpstreamFetcher(30*time.Second, log)
	if cfg.UpstreamUserAgent != "" {
		httpFetcher.UserAgent = cfg.UpstreamUserAgent
	}
	log.Info("upstream fetcher configured", "user_agent", httpFetcher.UserAgent)

	// 012 FR-008: log resolved URLTestParams so the operator can confirm
	// the active health-check configuration without inspecting the served
	// body.
	log.Info("url_test_params resolved",
		"url", cfg.URLTestParams.URL,
		"interval_seconds", cfg.URLTestParams.IntervalSeconds,
		"timeout_ms", cfg.URLTestParams.TimeoutMS,
		"max_failed_times", cfg.URLTestParams.MaxFailedTimes,
		"lazy", cfg.URLTestParams.Lazy,
	)

	// Own-proxies YAML.
	own, err := config.LoadOwnProxies(cfg.OwnProxiesYAMLPath)
	if err != nil {
		return fmt.Errorf("own-proxies: %w", err)
	}
	log.Info("loaded own-proxies",
		"proxies", len(own.Proxies),
		"groups", len(own.ProxyGroups),
	)

	// Custom rules (FR-003). Folder may be missing — Load returns empty.
	customRules, err := customrules.Load(cfg.CustomRulesPath)
	if err != nil {
		return fmt.Errorf("custom rules: %w", err)
	}
	log.Info("loaded custom rules",
		"sets", len(customRules),
		"path", cfg.CustomRulesPath,
	)

	// Coordinator (per-source goroutines + bootstrap state machine).
	coord := fetcher.NewCoordinator(cache, httpFetcher, clk, fetcher.SchedulerConfig{
		DefaultTTL:                    time.Duration(cfg.DefaultTTLSeconds) * time.Second,
		DefaultStaleOnError:           time.Duration(cfg.DefaultStaleOnErrorSeconds) * time.Second,
		BootstrapMaxAttemptsPerSource: cfg.BootstrapMaxAttemptsPerSource,
		BootstrapAttemptDelay:         time.Duration(cfg.BootstrapAttemptDelaySeconds) * time.Second,
	}, log, subs)

	// Run coordinator in background (returns from Start when bootstrap done).
	go coord.Start(ctx)

	// Pipeline + output adapter.
	pipeline := merge.NewPipeline(cache, subs, own.Proxies, own.ProxyGroups, clk, cfg.DefaultProfileUpdateIntervalHours).
		WithProxiesGroupName(cfg.ProxiesGroupName).
		WithFallbackRuleTarget(cfg.FallbackRuleTarget).
		WithURLTestParams(merge.URLTestParams{
			URL:             cfg.URLTestParams.URL,
			IntervalSeconds: cfg.URLTestParams.IntervalSeconds,
			TimeoutMS:       cfg.URLTestParams.TimeoutMS,
			MaxFailedTimes:  cfg.URLTestParams.MaxFailedTimes,
			Lazy:            cfg.URLTestParams.Lazy,
		}).
		WithSnapshotter(dailyspend.NewFileSnapshotter(cfg.TodayZeroPath)).
		WithBudgetLocation(cfg.BudgetLocation).
		WithCustomRules(customRules)
	adapter, err := output.NewSubscriptionMode(cfg.ServedConfigTemplatePath)
	if err != nil {
		return err
	}

	// Wait for bootstrap to settle before opening the HTTP listener — this
	// keeps cold-start clients from getting a flood of 503s. The handler
	// itself also enforces the gate (FR-003b), so this is a soft optimization.
	bootstrapWait := time.NewTimer(time.Duration(cfg.BootstrapMaxAttemptsPerSource*cfg.BootstrapAttemptDelaySeconds+30) * time.Second)
	select {
	case <-coord.Ready():
		bootstrapWait.Stop()
		if !coord.AllSucceeded() {
			log.Warn("bootstrap completed with at least one failed source — server will return 503 until reload")
		}
	case <-bootstrapWait.C:
		log.Warn("bootstrap deadline elapsed; opening HTTP listener anyway (handler will return 503)")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Need to break the import cycle: routes imports server, but server.NewApp
	// returns from this package. Output adapter import is in this main file.
	app := server.NewApp(cfg, tokens, cache, coord, pipeline, adapter, clk, log)
	return app.Run(ctx)
}

func splitEnabled(rows []config.SubscriptionRow) (enabled, disabled []config.SubscriptionRow) {
	for _, r := range rows {
		if r.Enable {
			enabled = append(enabled, r)
		} else {
			disabled = append(disabled, r)
		}
	}
	return
}

func names(rows []config.SubscriptionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
