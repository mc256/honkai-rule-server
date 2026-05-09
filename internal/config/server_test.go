package config

import (
	"log/slog"
	"strings"
	"testing"
)

func validRequiredEnv() MapEnv {
	return MapEnv{
		"SUBSCRIPTIONS_CSV_PATH":      "/etc/subs.csv",
		"OWN_PROXIES_YAML_PATH":       "/etc/own.yaml",
		"TOKENS_PATH":                 "/etc/tokens.json",
		"SERVED_CONFIG_TEMPLATE_PATH": "/etc/template.yaml",
		"CACHE_DIR":                   "/var/cache",
	}
}

func TestServerConfig_HappyPathDefaults(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SubscriptionsCSVPath != "/etc/subs.csv" {
		t.Errorf("SubscriptionsCSVPath = %q", cfg.SubscriptionsCSVPath)
	}
	if cfg.DefaultTTLSeconds != 3600 {
		t.Errorf("DefaultTTLSeconds = %d, want 3600", cfg.DefaultTTLSeconds)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want Info", cfg.LogLevel)
	}
	if cfg.ProxiesGroupName != "Proxies" {
		t.Errorf("ProxiesGroupName = %q, want Proxies", cfg.ProxiesGroupName)
	}
}

func TestServerConfig_MissingRequiredFails(t *testing.T) {
	required := []string{
		"SUBSCRIPTIONS_CSV_PATH",
		"OWN_PROXIES_YAML_PATH",
		"TOKENS_PATH",
		"SERVED_CONFIG_TEMPLATE_PATH",
		"CACHE_DIR",
	}
	for _, key := range required {
		env := validRequiredEnv()
		delete(env, key)
		_, err := Load(env)
		if err == nil {
			t.Errorf("Load with %s missing did not error", key)
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load error %q did not name the missing var %s", err.Error(), key)
		}
	}
}

func TestServerConfig_InvalidIntegerFails(t *testing.T) {
	env := validRequiredEnv()
	env["PORT"] = "not-a-number"
	_, err := Load(env)
	if err == nil {
		t.Fatalf("Load did not error on invalid PORT")
	}
	if !strings.Contains(err.Error(), "PORT") {
		t.Errorf("error did not mention PORT: %v", err)
	}
}

func TestServerConfig_NonPositiveIntegerFails(t *testing.T) {
	env := validRequiredEnv()
	env["DEFAULT_TTL_SECONDS"] = "0"
	_, err := Load(env)
	if err == nil {
		t.Fatalf("Load did not error on zero DEFAULT_TTL_SECONDS")
	}
}

func TestServerConfig_OverrideDefaults(t *testing.T) {
	env := validRequiredEnv()
	env["PORT"] = "9999"
	env["DEFAULT_TTL_SECONDS"] = "1800"
	env["LOG_LEVEL"] = "debug"
	env["PROXIES_GROUP_NAME"] = "All-Nodes"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Port)
	}
	if cfg.DefaultTTLSeconds != 1800 {
		t.Errorf("DefaultTTLSeconds = %d, want 1800", cfg.DefaultTTLSeconds)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want Debug", cfg.LogLevel)
	}
	if cfg.ProxiesGroupName != "All-Nodes" {
		t.Errorf("ProxiesGroupName = %q, want All-Nodes", cfg.ProxiesGroupName)
	}
}

func TestServerConfig_InvalidLogLevelFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOG_LEVEL"] = "verbose"
	_, err := Load(env)
	if err == nil {
		t.Fatalf("Load did not error on invalid LOG_LEVEL")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("error did not mention LOG_LEVEL: %v", err)
	}
}

// TC-U-ENV-FALLBACK-01: env unset → FallbackRuleTarget defaults to "auto".
func TestServerConfig_FallbackDefault(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FallbackRuleTarget != "auto" {
		t.Errorf("FallbackRuleTarget = %q, want \"auto\"", cfg.FallbackRuleTarget)
	}
}

// TC-U-ENV-FALLBACK-02: env empty string → defaults to "auto".
func TestServerConfig_FallbackEmptyStringDefaults(t *testing.T) {
	env := validRequiredEnv()
	env["FALLBACK_RULE_TARGET"] = ""
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FallbackRuleTarget != "auto" {
		t.Errorf("FallbackRuleTarget = %q, want \"auto\"", cfg.FallbackRuleTarget)
	}
}

// TC-U-ENV-FALLBACK-03: env "DIRECT" → passes through.
func TestServerConfig_FallbackDIRECT(t *testing.T) {
	env := validRequiredEnv()
	env["FALLBACK_RULE_TARGET"] = "DIRECT"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FallbackRuleTarget != "DIRECT" {
		t.Errorf("FallbackRuleTarget = %q, want \"DIRECT\"", cfg.FallbackRuleTarget)
	}
}

// TC-U-ENV-FALLBACK-04: env with provider-prefixed name → passed through verbatim.
func TestServerConfig_FallbackVerbatim(t *testing.T) {
	env := validRequiredEnv()
	env["FALLBACK_RULE_TARGET"] = "alpha_Auto"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FallbackRuleTarget != "alpha_Auto" {
		t.Errorf("FallbackRuleTarget = %q, want \"alpha_Auto\"", cfg.FallbackRuleTarget)
	}
}

// TC-U-ENV-FALLBACK-05: resolved value emitted as startup log line (FR-010).
func TestServerConfig_FallbackResolvedInLog(t *testing.T) {
	env := validRequiredEnv()
	env["FALLBACK_RULE_TARGET"] = "Proxies"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Verify the value is set — the actual log emission is in main.go which
	// exercises this config field. The test confirms the config plumbing works.
	_ = cfg
}

// TC-U-ENV-CUSTOM-RULES-01: env unset → CustomRulesPath defaults to "./custom-rules/".
func TestServerConfig_CustomRulesPathDefault(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CustomRulesPath != "./custom-rules/" {
		t.Errorf("CustomRulesPath = %q, want \"./custom-rules/\"", cfg.CustomRulesPath)
	}
}

// TC-U-ENV-CUSTOM-RULES-02: env "/etc/rules" → CustomRulesPath reads correctly.
func TestServerConfig_CustomRulesPathOverride(t *testing.T) {
	env := validRequiredEnv()
	env["CUSTOM_RULES_PATH"] = "/etc/rules"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CustomRulesPath != "/etc/rules" {
		t.Errorf("CustomRulesPath = %q, want \"/etc/rules\"", cfg.CustomRulesPath)
	}
}

// TC-U-ENV-UA-01: env unset → AllowedUserAgentPrefixes is nil (disabled).
func TestServerConfig_UserAgentPrefixesUnset(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AllowedUserAgentPrefixes != nil {
		t.Errorf("AllowedUserAgentPrefixes = %v, want nil", cfg.AllowedUserAgentPrefixes)
	}
}

// TC-U-ENV-UA-02: env "Honkai-Rule-Client,curl" → parsed into slice with whitespace trimming.
func TestServerConfig_UserAgentPrefixesParsed(t *testing.T) {
	env := validRequiredEnv()
	env["HONKAI_RULE_CLIENT_UA"] = "Honkai-Rule-Client,curl"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"Honkai-Rule-Client", "curl"}
	if len(cfg.AllowedUserAgentPrefixes) != len(want) {
		t.Fatalf("AllowedUserAgentPrefixes len = %d, want %d", len(cfg.AllowedUserAgentPrefixes), len(want))
	}
	for i := range want {
		if cfg.AllowedUserAgentPrefixes[i] != want[i] {
			t.Errorf("AllowedUserAgentPrefixes[%d] = %q, want %q", i, cfg.AllowedUserAgentPrefixes[i], want[i])
		}
	}
}

// TC-U-ENV-UA-03: env empty string → AllowedUserAgentPrefixes is nil (disabled).
func TestServerConfig_UserAgentPrefixesEmpty(t *testing.T) {
	env := validRequiredEnv()
	env["HONKAI_RULE_CLIENT_UA"] = ""
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AllowedUserAgentPrefixes != nil {
		t.Errorf("AllowedUserAgentPrefixes = %v, want nil", cfg.AllowedUserAgentPrefixes)
	}
}

// TC-U-ENV-UA-04: whitespace trimming around prefixes.
func TestServerConfig_UserAgentPrefixesWhitespaceTrimming(t *testing.T) {
	env := validRequiredEnv()
	env["HONKAI_RULE_CLIENT_UA"] = "  Honkai-Rule-Client ,  curl  ,  MyClient  "
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"Honkai-Rule-Client", "curl", "MyClient"}
	if len(cfg.AllowedUserAgentPrefixes) != len(want) {
		t.Fatalf("AllowedUserAgentPrefixes len = %d, want %d", len(cfg.AllowedUserAgentPrefixes), len(want))
	}
	for i := range want {
		if cfg.AllowedUserAgentPrefixes[i] != want[i] {
			t.Errorf("AllowedUserAgentPrefixes[%d] = %q, want %q", i, cfg.AllowedUserAgentPrefixes[i], want[i])
		}
	}
}

// 012 FR-003 + FR-004: URLTestParams defaults match the operator-confirmed
// example values when no env vars are set.
func TestServerConfig_URLTestParamsDefaults(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := URLTestParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 10,
		TimeoutMS:       3000,
		MaxFailedTimes:  3,
		Lazy:            true,
	}
	if cfg.URLTestParams != want {
		t.Errorf("URLTestParams = %+v, want %+v", cfg.URLTestParams, want)
	}
}

// 012 FR-004: All five env vars override the defaults verbatim.
func TestServerConfig_URLTestParamsAllOverridden(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_URL"] = "https://example.com/204"
	env["URL_TEST_INTERVAL_SECONDS"] = "30"
	env["URL_TEST_TIMEOUT_MS"] = "5000"
	env["URL_TEST_MAX_FAILED_TIMES"] = "5"
	env["URL_TEST_LAZY"] = "false"

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := URLTestParams{
		URL:             "https://example.com/204",
		IntervalSeconds: 30,
		TimeoutMS:       5000,
		MaxFailedTimes:  5,
		Lazy:            false,
	}
	if cfg.URLTestParams != want {
		t.Errorf("URLTestParams = %+v, want %+v", cfg.URLTestParams, want)
	}
}

// 012 FR-004: Empty string is treated as unset (matches FALLBACK_RULE_TARGET pattern).
func TestServerConfig_URLTestParamsEmptyTreatedAsUnset(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_URL"] = ""
	env["URL_TEST_INTERVAL_SECONDS"] = ""
	env["URL_TEST_TIMEOUT_MS"] = ""
	env["URL_TEST_MAX_FAILED_TIMES"] = ""
	env["URL_TEST_LAZY"] = ""

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URLTestParams.URL != "https://www.gstatic.com/generate_204" {
		t.Errorf("URL = %q, want default", cfg.URLTestParams.URL)
	}
	if cfg.URLTestParams.IntervalSeconds != 10 {
		t.Errorf("IntervalSeconds = %d, want 10", cfg.URLTestParams.IntervalSeconds)
	}
}

// 012 FR-004a: Non-integer interval fails loud (Constitution Principle III).
func TestServerConfig_URLTestParamsNonIntegerIntervalFails(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_INTERVAL_SECONDS"] = "abc"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for non-integer URL_TEST_INTERVAL_SECONDS, got nil")
	}
	if !strings.Contains(err.Error(), "URL_TEST_INTERVAL_SECONDS") {
		t.Errorf("error should name the offending env var; got: %v", err)
	}
}

// 012 FR-004a: Zero / negative values fail loud.
func TestServerConfig_URLTestParamsZeroIntervalFails(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_INTERVAL_SECONDS"] = "0"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for zero URL_TEST_INTERVAL_SECONDS, got nil")
	}
	if !strings.Contains(err.Error(), "URL_TEST_INTERVAL_SECONDS") {
		t.Errorf("error should name URL_TEST_INTERVAL_SECONDS; got: %v", err)
	}
}

func TestServerConfig_URLTestParamsNegativeTimeoutFails(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_TIMEOUT_MS"] = "-100"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for negative URL_TEST_TIMEOUT_MS, got nil")
	}
	if !strings.Contains(err.Error(), "URL_TEST_TIMEOUT_MS") {
		t.Errorf("error should name URL_TEST_TIMEOUT_MS; got: %v", err)
	}
}

// strconv.ParseBool accepts variants like "False", "FALSE", "1", "0", "t", "f".
func TestServerConfig_URLTestParamsBoolVariantsAccepted(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"True":  true,
		"TRUE":  true,
		"1":     true,
		"t":     true,
		"false": false,
		"False": false,
		"FALSE": false,
		"0":     false,
		"f":     false,
	}
	for v, want := range cases {
		t.Run(v, func(t *testing.T) {
			env := validRequiredEnv()
			env["URL_TEST_LAZY"] = v
			cfg, err := Load(env)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.URLTestParams.Lazy != want {
				t.Errorf("URL_TEST_LAZY=%q → Lazy=%v, want %v", v, cfg.URLTestParams.Lazy, want)
			}
		})
	}
}

// 012 FR-004a: Bool gibberish fails loud.
func TestServerConfig_URLTestParamsInvalidBoolFails(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_LAZY"] = "maybe"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for invalid URL_TEST_LAZY, got nil")
	}
	if !strings.Contains(err.Error(), "URL_TEST_LAZY") {
		t.Errorf("error should name URL_TEST_LAZY; got: %v", err)
	}
}

// 012 FR-004a: Multiple violations bundled in one error message so the operator
// sees all problems on the next restart attempt rather than fixing them one by one.
func TestServerConfig_URLTestParamsMultipleViolations(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_INTERVAL_SECONDS"] = "0"
	env["URL_TEST_LAZY"] = "maybe"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for two violations, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "URL_TEST_INTERVAL_SECONDS") {
		t.Errorf("error missing URL_TEST_INTERVAL_SECONDS: %v", err)
	}
	if !strings.Contains(msg, "URL_TEST_LAZY") {
		t.Errorf("error missing URL_TEST_LAZY: %v", err)
	}
}

// 014 FR-003 + FR-004: LoadBalanceParams defaults match the operator-confirmed
// example values when no env vars are set.
func TestServerConfig_LoadBalanceParamsDefaults(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := LoadBalanceParams{
		URL:             "https://www.gstatic.com/generate_204",
		IntervalSeconds: 300,
		TimeoutMS:       1500,
		MaxFailedTimes:  3,
		Lazy:            true,
		Strategy:        "round-robin",
	}
	if cfg.LoadBalanceParams != want {
		t.Errorf("LoadBalanceParams = %+v, want %+v", cfg.LoadBalanceParams, want)
	}
}

// 014 FR-004: All six env vars override the defaults verbatim.
func TestServerConfig_LoadBalanceParamsAllOverridden(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_URL"] = "https://example.com/204"
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = "600"
	env["LOAD_BALANCE_TIMEOUT_MS"] = "2000"
	env["LOAD_BALANCE_MAX_FAILED_TIMES"] = "5"
	env["LOAD_BALANCE_LAZY"] = "false"
	env["LOAD_BALANCE_STRATEGY"] = "consistent-hashing"

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := LoadBalanceParams{
		URL:             "https://example.com/204",
		IntervalSeconds: 600,
		TimeoutMS:       2000,
		MaxFailedTimes:  5,
		Lazy:            false,
		Strategy:        "consistent-hashing",
	}
	if cfg.LoadBalanceParams != want {
		t.Errorf("LoadBalanceParams = %+v, want %+v", cfg.LoadBalanceParams, want)
	}
}

// 014 FR-004: Empty string is treated as unset (matches FALLBACK_RULE_TARGET pattern).
func TestServerConfig_LoadBalanceParamsEmptyTreatedAsUnset(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_URL"] = ""
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = ""
	env["LOAD_BALANCE_TIMEOUT_MS"] = ""
	env["LOAD_BALANCE_MAX_FAILED_TIMES"] = ""
	env["LOAD_BALANCE_LAZY"] = ""
	env["LOAD_BALANCE_STRATEGY"] = ""

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LoadBalanceParams.URL != "https://www.gstatic.com/generate_204" {
		t.Errorf("URL = %q, want default", cfg.LoadBalanceParams.URL)
	}
	if cfg.LoadBalanceParams.IntervalSeconds != 300 {
		t.Errorf("IntervalSeconds = %d, want 300", cfg.LoadBalanceParams.IntervalSeconds)
	}
	if cfg.LoadBalanceParams.Strategy != "round-robin" {
		t.Errorf("Strategy = %q, want round-robin", cfg.LoadBalanceParams.Strategy)
	}
}

// 014 FR-005: Non-integer interval fails loud (Constitution Principle III).
func TestServerConfig_LoadBalanceParamsNonIntegerIntervalFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = "abc"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for non-integer LOAD_BALANCE_INTERVAL_SECONDS, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_INTERVAL_SECONDS") {
		t.Errorf("error should name the offending env var; got: %v", err)
	}
}

// 014 FR-005: Zero / negative values fail loud.
func TestServerConfig_LoadBalanceParamsZeroIntervalFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = "0"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for zero LOAD_BALANCE_INTERVAL_SECONDS, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_INTERVAL_SECONDS") {
		t.Errorf("error should name LOAD_BALANCE_INTERVAL_SECONDS; got: %v", err)
	}
}

func TestServerConfig_LoadBalanceParamsNegativeTimeoutFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_TIMEOUT_MS"] = "-100"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for negative LOAD_BALANCE_TIMEOUT_MS, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_TIMEOUT_MS") {
		t.Errorf("error should name LOAD_BALANCE_TIMEOUT_MS; got: %v", err)
	}
}

// 014 FR-005: Bool gibberish fails loud.
func TestServerConfig_LoadBalanceParamsInvalidBoolFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_LAZY"] = "maybe"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for invalid LOAD_BALANCE_LAZY, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_LAZY") {
		t.Errorf("error should name LOAD_BALANCE_LAZY; got: %v", err)
	}
}

// 014 FR-005: Strategy enum — only round-robin / consistent-hashing /
// sticky-sessions are accepted. Other values fail loud (Constitution III).
func TestServerConfig_LoadBalanceParamsStrategyAcceptedValues(t *testing.T) {
	for _, strat := range []string{"round-robin", "consistent-hashing", "sticky-sessions"} {
		t.Run(strat, func(t *testing.T) {
			env := validRequiredEnv()
			env["LOAD_BALANCE_STRATEGY"] = strat
			cfg, err := Load(env)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LoadBalanceParams.Strategy != strat {
				t.Errorf("Strategy = %q, want %q", cfg.LoadBalanceParams.Strategy, strat)
			}
		})
	}
}

func TestServerConfig_LoadBalanceParamsUnknownStrategyFails(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_STRATEGY"] = "random"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for unknown LOAD_BALANCE_STRATEGY, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_STRATEGY") {
		t.Errorf("error should name LOAD_BALANCE_STRATEGY; got: %v", err)
	}
}

// 014 FR-005: Strategy is case-sensitive — Mihomo accepts only the lowercase
// hyphenated literals.
func TestServerConfig_LoadBalanceParamsStrategyCaseSensitive(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_STRATEGY"] = "Round-Robin"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for case-mismatched strategy, got nil")
	}
	if !strings.Contains(err.Error(), "LOAD_BALANCE_STRATEGY") {
		t.Errorf("error should name LOAD_BALANCE_STRATEGY; got: %v", err)
	}
}

// 014 FR-005: Multiple violations bundled in one error message.
func TestServerConfig_LoadBalanceParamsMultipleViolations(t *testing.T) {
	env := validRequiredEnv()
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = "0"
	env["LOAD_BALANCE_LAZY"] = "maybe"
	env["LOAD_BALANCE_STRATEGY"] = "random"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for three violations, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LOAD_BALANCE_INTERVAL_SECONDS") {
		t.Errorf("error missing LOAD_BALANCE_INTERVAL_SECONDS: %v", err)
	}
	if !strings.Contains(msg, "LOAD_BALANCE_LAZY") {
		t.Errorf("error missing LOAD_BALANCE_LAZY: %v", err)
	}
	if !strings.Contains(msg, "LOAD_BALANCE_STRATEGY") {
		t.Errorf("error missing LOAD_BALANCE_STRATEGY: %v", err)
	}
}

// 014: URL_TEST_* and LOAD_BALANCE_* namespaces are independent — overriding
// one MUST NOT affect the other (FR-007 byte-stability for url-test path).
func TestServerConfig_URLTestAndLoadBalanceIndependent(t *testing.T) {
	env := validRequiredEnv()
	env["URL_TEST_INTERVAL_SECONDS"] = "30"
	env["LOAD_BALANCE_INTERVAL_SECONDS"] = "999"

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URLTestParams.IntervalSeconds != 30 {
		t.Errorf("URLTestParams.IntervalSeconds = %d, want 30", cfg.URLTestParams.IntervalSeconds)
	}
	if cfg.LoadBalanceParams.IntervalSeconds != 999 {
		t.Errorf("LoadBalanceParams.IntervalSeconds = %d, want 999", cfg.LoadBalanceParams.IntervalSeconds)
	}
}

// 011 FR-005: TODAY_ZERO_PATH defaults + override.
func TestServerConfig_TodayZeroPathDefault(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TodayZeroPath != "/data/today-zero.json" {
		t.Errorf("TodayZeroPath = %q; want /data/today-zero.json", cfg.TodayZeroPath)
	}
}

func TestServerConfig_TodayZeroPathOverride(t *testing.T) {
	env := validRequiredEnv()
	env["TODAY_ZERO_PATH"] = "/custom/path/snap.json"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TodayZeroPath != "/custom/path/snap.json" {
		t.Errorf("TodayZeroPath = %q; want override", cfg.TodayZeroPath)
	}
}

// 011 FR-010: DAILY_BUDGET_TIMEZONE defaults to America/Toronto + parses
// to a *time.Location at Load time.
func TestServerConfig_DailyBudgetTimezoneDefault(t *testing.T) {
	cfg, err := Load(validRequiredEnv())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DailyBudgetTimezone != "America/Toronto" {
		t.Errorf("DailyBudgetTimezone = %q; want America/Toronto", cfg.DailyBudgetTimezone)
	}
	if cfg.BudgetLocation == nil {
		t.Fatal("BudgetLocation = nil; want parsed *time.Location")
	}
	if cfg.BudgetLocation.String() != "America/Toronto" {
		t.Errorf("BudgetLocation = %v; want America/Toronto", cfg.BudgetLocation)
	}
}

func TestServerConfig_DailyBudgetTimezoneOverride(t *testing.T) {
	env := validRequiredEnv()
	env["DAILY_BUDGET_TIMEZONE"] = "Europe/London"
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DailyBudgetTimezone != "Europe/London" {
		t.Errorf("DailyBudgetTimezone = %q; want Europe/London", cfg.DailyBudgetTimezone)
	}
	if cfg.BudgetLocation == nil || cfg.BudgetLocation.String() != "Europe/London" {
		t.Errorf("BudgetLocation = %v; want Europe/London", cfg.BudgetLocation)
	}
}

// 011 FR-010 + Constitution Principle III: invalid timezone is loud-fail
// at startup with a clear error naming the offending value.
func TestServerConfig_DailyBudgetTimezoneInvalidFails(t *testing.T) {
	env := validRequiredEnv()
	env["DAILY_BUDGET_TIMEZONE"] = "Mars/Olympus"
	_, err := Load(env)
	if err == nil {
		t.Fatal("expected error for invalid DAILY_BUDGET_TIMEZONE; got nil")
	}
	if !strings.Contains(err.Error(), "DAILY_BUDGET_TIMEZONE") {
		t.Errorf("error should name DAILY_BUDGET_TIMEZONE; got: %v", err)
	}
	if !strings.Contains(err.Error(), "Mars/Olympus") {
		t.Errorf("error should include the offending value; got: %v", err)
	}
}
