// Package config loads and validates all server configuration:
// the subscriptions CSV (FR-001a), the own-proxies YAML (FR-006),
// the tokens JSON (FR-019), and ServerConfig (env vars).
package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Env is a small abstraction over os.Getenv so tests can inject fakes.
type Env interface {
	Getenv(key string) string
}

// ServerConfig holds runtime configuration loaded once from process env vars.
// Per data-model.md §ServerConfig.
type ServerConfig struct {
	// File paths (all required)
	SubscriptionsCSVPath     string
	OwnProxiesYAMLPath       string
	TokensPath               string
	ServedConfigTemplatePath string
	CacheDir                 string

	// Defaults applied when CSV row omits the corresponding column
	DefaultTTLSeconds                 int
	DefaultStaleOnErrorSeconds        int
	DefaultProfileUpdateIntervalHours int

	// Bootstrap behavior
	BootstrapMaxAttemptsPerSource int
	BootstrapAttemptDelaySeconds  int

	// Server
	Port             int
	LogLevel         slog.Level
	ProxiesGroupName string

	// FallbackRuleTarget is the target of the single server-emitted MATCH rule
	// appended at the end of the merged rules block (FR-010).
	// Defaults to "auto"; overridable via FALLBACK_RULE_TARGET env var.
	// Not validated — passed through verbatim per spec Assumption A7a.
	FallbackRuleTarget string

	// Upstream HTTP client. Most subscription providers content-negotiate
	// by User-Agent — with a generic UA they serve V2Ray-style base64 URL
	// lists; with a Clash-flavored UA they serve the Clash YAML config.
	// Defaults to fetcher.DefaultUserAgent ("mihomo/v1.18.0").
	UpstreamUserAgent string

	// CustomRulesPath is the folder containing custom rule YAML files (FR-001).
	// Defaults to "./custom-rules/"; overridable via CUSTOM_RULES_PATH env var.
	CustomRulesPath string

	// AllowedUserAgentPrefixes restricts access to clients whose User-Agent
	// header starts with one of these prefixes (FR-018/FR-019).
	// Nil or empty means no filtering (all requests accepted).
	// Parsed from HONKAI_RULE_CLIENT_UA as comma-separated list.
	AllowedUserAgentPrefixes []string

	// URLPathPrefix mounts the subscription handler under a non-root path
	// (009). Empty (default) means the handler is at "/". When set to e.g.
	// "/abc123", the subscription route is at "/abc123/" and "/health"
	// stays at root. Trailing slashes are normalized.
	URLPathPrefix string

	// 011: persistent today-zero snapshot file path on the PVC.
	// Default: "/data/today-zero.json". Override via TODAY_ZERO_PATH.
	TodayZeroPath string

	// 011: timezone for the daily-budget boundary (per FR-010).
	// Default: "America/Toronto". Override via DAILY_BUDGET_TIMEZONE.
	// Validated at Load time via time.LoadLocation; loud-fail on bad
	// input per Constitution Principle III.
	DailyBudgetTimezone string

	// BudgetLocation is the parsed *time.Location resolved at Load time
	// from DailyBudgetTimezone. Convenience field so callers don't
	// re-parse on every request.
	BudgetLocation *time.Location

	// URLTestParams holds the five health-check fields written into every
	// auto-emitted _region_* / _continent_* proxy group per 012 FR-001..
	// FR-004. Loaded from URL_TEST_* env vars; empty / unset → defaults.
	URLTestParams URLTestParams

	// LoadBalanceParams holds the six load-balance fields written into every
	// auto-emitted _lb_region_* / _lb_continent_* proxy group per 014 FR-001..
	// FR-005. Loaded from LOAD_BALANCE_* env vars; empty / unset → defaults.
	// Independent namespace from URLTestParams (014 Decision 1) — the two
	// probe sets describe semantically different behaviors.
	LoadBalanceParams LoadBalanceParams
}

// URLTestParams holds the five Mihomo url-test health-check fields the
// server emits on every auto-region / auto-continent proxy group per
// 012 FR-003 / FR-004. Loaded by Load() from the five URL_TEST_* env
// vars and validated per FR-004a (loud-fail per Constitution Principle III).
type URLTestParams struct {
	URL             string // YAML field "url".              Default: https://www.gstatic.com/generate_204.
	IntervalSeconds int    // YAML field "interval".         Default: 10. Must be >= 1.
	TimeoutMS       int    // YAML field "timeout".          Default: 3000. Must be >= 1.
	MaxFailedTimes  int    // YAML field "max-failed-times". Default: 3. Must be >= 1.
	Lazy            bool   // YAML field "lazy".             Default: true.
}

// LoadBalanceParams holds the six Mihomo load-balance fields the server emits
// on every auto-emitted _lb_region_* / _lb_continent_* proxy group per 014
// FR-001..FR-005. Loaded by Load() from the six LOAD_BALANCE_* env vars and
// validated per FR-005 (loud-fail per Constitution Principle III).
type LoadBalanceParams struct {
	URL             string // YAML field "url".              Default: https://www.gstatic.com/generate_204.
	IntervalSeconds int    // YAML field "interval".         Default: 300. Must be >= 1.
	TimeoutMS       int    // YAML field "timeout".          Default: 1500. Must be >= 1.
	MaxFailedTimes  int    // YAML field "max-failed-times". Default: 3. Must be >= 1.
	Lazy            bool   // YAML field "lazy".             Default: true.
	Strategy        string // YAML field "strategy".         Default: "round-robin".
	// Strategy must be one of: round-robin, consistent-hashing, sticky-sessions
	// (Mihomo's three supported values; case-sensitive).
}

// validLoadBalanceStrategies lists the Mihomo-supported strategy values for
// load-balance proxy groups (014 FR-005).
var validLoadBalanceStrategies = map[string]struct{}{
	"round-robin":        {},
	"consistent-hashing": {},
	"sticky-sessions":    {},
}

// Load reads ServerConfig from env. Required env vars must be set;
// integer-valued vars must parse; LOG_LEVEL must be a recognized name.
func Load(env Env) (*ServerConfig, error) {
	cfg := &ServerConfig{
		// Defaults; overwritten below if env is set.
		DefaultTTLSeconds:                 3600,
		DefaultStaleOnErrorSeconds:        86400,
		DefaultProfileUpdateIntervalHours: 12,
		BootstrapMaxAttemptsPerSource:     3,
		BootstrapAttemptDelaySeconds:      5,
		Port:                              8080,
		LogLevel:                          slog.LevelInfo,
		ProxiesGroupName:                  "Proxies",
		FallbackRuleTarget:                "auto",
		URLTestParams: URLTestParams{
			URL:             "https://www.gstatic.com/generate_204",
			IntervalSeconds: 10,
			TimeoutMS:       3000,
			MaxFailedTimes:  3,
			Lazy:            true,
		},
		LoadBalanceParams: LoadBalanceParams{
			URL:             "https://www.gstatic.com/generate_204",
			IntervalSeconds: 300,
			TimeoutMS:       1500,
			MaxFailedTimes:  3,
			Lazy:            true,
			Strategy:        "round-robin",
		},
		TodayZeroPath:       "/data/today-zero.json",
		DailyBudgetTimezone: "America/Toronto",
	}

	required := []struct {
		key string
		dst *string
	}{
		{"SUBSCRIPTIONS_CSV_PATH", &cfg.SubscriptionsCSVPath},
		{"OWN_PROXIES_YAML_PATH", &cfg.OwnProxiesYAMLPath},
		{"TOKENS_PATH", &cfg.TokensPath},
		{"SERVED_CONFIG_TEMPLATE_PATH", &cfg.ServedConfigTemplatePath},
		{"CACHE_DIR", &cfg.CacheDir},
	}
	for _, r := range required {
		v := env.Getenv(r.key)
		if v == "" {
			return nil, fmt.Errorf("required env var %s is unset", r.key)
		}
		*r.dst = v
	}

	intVars := []struct {
		key string
		dst *int
	}{
		{"DEFAULT_TTL_SECONDS", &cfg.DefaultTTLSeconds},
		{"DEFAULT_STALE_ON_ERROR_SECONDS", &cfg.DefaultStaleOnErrorSeconds},
		{"DEFAULT_PROFILE_UPDATE_INTERVAL_HOURS", &cfg.DefaultProfileUpdateIntervalHours},
		{"BOOTSTRAP_MAX_ATTEMPTS_PER_SOURCE", &cfg.BootstrapMaxAttemptsPerSource},
		{"BOOTSTRAP_ATTEMPT_DELAY_SECONDS", &cfg.BootstrapAttemptDelaySeconds},
		{"PORT", &cfg.Port},
	}
	for _, r := range intVars {
		v := env.Getenv(r.key)
		if v == "" {
			continue // keep default
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("env var %s = %q is not a valid integer: %w", r.key, v, err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("env var %s = %d must be positive", r.key, n)
		}
		*r.dst = n
	}

	if v := env.Getenv("LOG_LEVEL"); v != "" {
		lvl, err := parseLevel(v)
		if err != nil {
			return nil, err
		}
		cfg.LogLevel = lvl
	}

	if v := env.Getenv("PROXIES_GROUP_NAME"); v != "" {
		cfg.ProxiesGroupName = v
	}

	if v := env.Getenv("UPSTREAM_USER_AGENT"); v != "" {
		cfg.UpstreamUserAgent = v
	}

	if v := env.Getenv("FALLBACK_RULE_TARGET"); v != "" {
		cfg.FallbackRuleTarget = v
	}

	// Custom rules folder path (defaults to ./custom-rules/)
	if v := env.Getenv("CUSTOM_RULES_PATH"); v != "" {
		cfg.CustomRulesPath = v
	} else {
		cfg.CustomRulesPath = "./custom-rules/"
	}

	// 009 FR-023: optional URL path prefix for the subscription handler.
	// Normalize to leading "/", no trailing "/" (empty stays empty).
	if v := env.Getenv("URL_PATH_PREFIX"); v != "" {
		if !strings.HasPrefix(v, "/") {
			v = "/" + v
		}
		v = strings.TrimRight(v, "/")
		cfg.URLPathPrefix = v
	}

	// 012 FR-004 + FR-004a: URL_TEST_* params for auto-emitted
	// _region_* / _continent_* groups. Empty / unset → keep defaults.
	// Invalid values accumulate into a single error so the operator
	// sees all problems at once rather than fixing them one-by-one.
	var urlTestErrs []string
	if v := env.Getenv("URL_TEST_URL"); v != "" {
		cfg.URLTestParams.URL = v
	}
	parsePositiveInt := func(key string, dst *int) {
		v := env.Getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			urlTestErrs = append(urlTestErrs, fmt.Sprintf("%s=%q (must be a positive integer)", key, v))
			return
		}
		if n < 1 {
			urlTestErrs = append(urlTestErrs, fmt.Sprintf("%s=%d (must be >= 1)", key, n))
			return
		}
		*dst = n
	}
	parsePositiveInt("URL_TEST_INTERVAL_SECONDS", &cfg.URLTestParams.IntervalSeconds)
	parsePositiveInt("URL_TEST_TIMEOUT_MS", &cfg.URLTestParams.TimeoutMS)
	parsePositiveInt("URL_TEST_MAX_FAILED_TIMES", &cfg.URLTestParams.MaxFailedTimes)
	if v := env.Getenv("URL_TEST_LAZY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			urlTestErrs = append(urlTestErrs, fmt.Sprintf("URL_TEST_LAZY=%q (must be true or false)", v))
		} else {
			cfg.URLTestParams.Lazy = b
		}
	}
	if len(urlTestErrs) > 0 {
		return nil, fmt.Errorf("URLTestParams validation failed: %s", strings.Join(urlTestErrs, "; "))
	}

	// 014 FR-004 + FR-005: LOAD_BALANCE_* params for auto-emitted
	// _lb_region_* / _lb_continent_* groups. Empty / unset → keep defaults.
	// Invalid values accumulate into a single error so the operator sees all
	// problems at once rather than fixing them one-by-one. Mirrors the
	// URL_TEST_* pattern above; namespace is independent (014 Decision 1).
	var loadBalanceErrs []string
	if v := env.Getenv("LOAD_BALANCE_URL"); v != "" {
		cfg.LoadBalanceParams.URL = v
	}
	parseLBPositiveInt := func(key string, dst *int) {
		v := env.Getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			loadBalanceErrs = append(loadBalanceErrs, fmt.Sprintf("%s=%q (must be a positive integer)", key, v))
			return
		}
		if n < 1 {
			loadBalanceErrs = append(loadBalanceErrs, fmt.Sprintf("%s=%d (must be >= 1)", key, n))
			return
		}
		*dst = n
	}
	parseLBPositiveInt("LOAD_BALANCE_INTERVAL_SECONDS", &cfg.LoadBalanceParams.IntervalSeconds)
	parseLBPositiveInt("LOAD_BALANCE_TIMEOUT_MS", &cfg.LoadBalanceParams.TimeoutMS)
	parseLBPositiveInt("LOAD_BALANCE_MAX_FAILED_TIMES", &cfg.LoadBalanceParams.MaxFailedTimes)
	if v := env.Getenv("LOAD_BALANCE_LAZY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			loadBalanceErrs = append(loadBalanceErrs, fmt.Sprintf("LOAD_BALANCE_LAZY=%q (must be true or false)", v))
		} else {
			cfg.LoadBalanceParams.Lazy = b
		}
	}
	if v := env.Getenv("LOAD_BALANCE_STRATEGY"); v != "" {
		if _, ok := validLoadBalanceStrategies[v]; !ok {
			loadBalanceErrs = append(loadBalanceErrs, fmt.Sprintf("LOAD_BALANCE_STRATEGY=%q (must be round-robin, consistent-hashing, or sticky-sessions)", v))
		} else {
			cfg.LoadBalanceParams.Strategy = v
		}
	}
	if len(loadBalanceErrs) > 0 {
		return nil, fmt.Errorf("LoadBalanceParams validation failed: %s", strings.Join(loadBalanceErrs, "; "))
	}

	// 011: today-zero snapshot file path + budget timezone.
	if v := env.Getenv("TODAY_ZERO_PATH"); v != "" {
		cfg.TodayZeroPath = v
	}
	if v := env.Getenv("DAILY_BUDGET_TIMEZONE"); v != "" {
		cfg.DailyBudgetTimezone = v
	}
	loc, err := time.LoadLocation(cfg.DailyBudgetTimezone)
	if err != nil {
		return nil, fmt.Errorf("DAILY_BUDGET_TIMEZONE=%q is not a valid IANA timezone: %w", cfg.DailyBudgetTimezone, err)
	}
	cfg.BudgetLocation = loc

	// User-Agent filtering (comma-separated prefixes)
	if v := env.Getenv("HONKAI_RULE_CLIENT_UA"); v != "" {
		parts := strings.Split(v, ",")
		prefixes := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				prefixes = append(prefixes, p)
			}
		}
		if len(prefixes) > 0 {
			cfg.AllowedUserAgentPrefixes = prefixes
		}
	}

	return cfg, nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unrecognized LOG_LEVEL %q (want one of: debug, info, warn, error)", s)
	}
}

// MapEnv is a test-friendly Env backed by a map.
type MapEnv map[string]string

// Getenv satisfies Env.
func (m MapEnv) Getenv(key string) string { return m[key] }

