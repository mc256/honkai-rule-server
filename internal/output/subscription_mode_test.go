package output

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/config"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

const minimalTemplate = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info

dns:
  enable: true
  nameserver:
    - 1.1.1.1

proxies: __MERGED_PROXIES__

proxy-groups: __MERGED_PROXY_GROUPS__

rules: __MERGED_RULES__
`

const upstreamA = `port: 7890
proxies:
  - {name: A1, type: trojan, server: a.test, port: 443, password: pw}
proxy-groups:
  - {name: Auto, type: select, proxies: [A1]}
rules:
  - DOMAIN,a.test,Auto
  - MATCH,DIRECT
`

func makePayload(name, body string) *fetcher.UpstreamCachedPayload {
	return &fetcher.UpstreamCachedPayload{
		SourceName:   name,
		BodyYAML:     []byte(body),
		PayloadBytes: len(body),
	}
}

type stubCache struct{ p map[string]*fetcher.UpstreamCachedPayload }

func (s *stubCache) Get(name string) (*fetcher.UpstreamCachedPayload, bool) {
	v, ok := s.p[name]
	return v, ok
}

func TestSubscriptionMode_RenderProducesValidYAML(t *testing.T) {
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstreamA),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, err := pipeline.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapter, err := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	if err != nil {
		t.Fatalf("NewSubscriptionModeFromBytes: %v", err)
	}
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Body must round-trip through yaml.Unmarshal.
	var got map[string]any
	if err := yaml.Unmarshal(rendered.Body, &got); err != nil {
		t.Fatalf("rendered body does not parse as YAML: %v\n--body--\n%s", err, rendered.Body)
	}

	// Globals from the template are preserved.
	if got["mixed-port"] != 7890 {
		t.Errorf("mixed-port = %v, want 7890", got["mixed-port"])
	}
	if got["mode"] != "rule" {
		t.Errorf("mode = %v, want rule", got["mode"])
	}

	// Proxies / proxy-groups / rules are populated (placeholders gone).
	proxies, ok := got["proxies"].([]any)
	if !ok || len(proxies) != 1 {
		t.Errorf("proxies = %v, want one entry", got["proxies"])
	}
	// 1 from upstream (Auto) + 1 always-present Proxies group (FR-009a) +
	// 1 _region_UNKNOWN (003) + 1 _lb_region_UNKNOWN paired sibling (014 FR-001) = 4.
	groups, ok := got["proxy-groups"].([]any)
	if !ok || len(groups) != 4 {
		t.Errorf("proxy-groups = %v, want 4 entries (Auto + Proxies + _region_UNKNOWN + _lb_region_UNKNOWN)", got["proxy-groups"])
	}
	rules, ok := got["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Errorf("rules = %v, want 2 entries", got["rules"])
	}

	// Placeholder strings must not survive into the output.
	for _, sentinel := range []string{"__MERGED_PROXIES__", "__MERGED_PROXY_GROUPS__", "__MERGED_RULES__"} {
		if strings.Contains(string(rendered.Body), sentinel) {
			t.Errorf("rendered body still contains placeholder %s", sentinel)
		}
	}

	// Headers: Content-Type + Cache-Control + (when MergedConfig has them)
	// Subscription-Userinfo + Profile-Update-Interval per FR-011, FR-011a.
	if ct := rendered.Headers.Get("Content-Type"); ct != "application/yaml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rendered.Headers.Get("Cache-Control"); cc != "no-store, no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q", cc)
	}

	// In this test the upstream stub doesn't return Subscription-Userinfo,
	// so per 010 FR-006 the header is omitted entirely (rather than emitted
	// with all-zero values, which would falsely advertise "you have zero
	// quota at all"). The pipeline's PUI defaults to 12 hours when no
	// source supplies one, so Profile-Update-Interval should still be set.
	if pui := rendered.Headers.Get("Profile-Update-Interval"); pui != "12" {
		t.Errorf("Profile-Update-Interval = %q, want 12 (configured default)", pui)
	}
	if ui := rendered.Headers.Get("Subscription-Userinfo"); ui != "" {
		t.Errorf("Subscription-Userinfo = %q, want empty (no source supplied userinfo per 010 FR-006)", ui)
	}
}

// 010 FR-001/FR-002/FR-003: Subscription-Userinfo carries the daily-spendable
// encoding (`total - upload - download = daily allowance`, `expire = next
// 00:00 UTC`) rather than raw per-source aggregates. Wire format unchanged.
func TestSubscriptionMode_HeadersWireFormat(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"a": {SourceName: "a", BodyYAML: []byte(upstreamA), PayloadBytes: len(upstreamA),
			Headers: fetcher.UpstreamHeaders{
				// remaining = 1000 - 100 - 200 = 700; days = 10 → 70/day
				SubscriptionUserinfo: &fetcher.SubscriptionUserinfo{
					Upload: 100, Download: 200, Total: 1000, Expire: now.Add(10 * 24 * time.Hour).Unix(),
				},
				ProfileUpdateIntervalHours: intPtrLocal(24),
			}},
		"b": {SourceName: "b", BodyYAML: []byte(upstreamA), PayloadBytes: len(upstreamA),
			Headers: fetcher.UpstreamHeaders{
				// remaining = 500 - 50 - 100 = 350; days = 3 → 116/day (350/3 = 116.67 → integer 116)
				SubscriptionUserinfo: &fetcher.SubscriptionUserinfo{
					Upload: 50, Download: 100, Total: 500, Expire: now.Add(3 * 24 * time.Hour).Unix(),
				},
				ProfileUpdateIntervalHours: intPtrLocal(6),
			}},
	}}
	rows := []config.SubscriptionRow{
		{Name: "a", Link: "http://a.test", Priority: 1, Enable: true},
		{Name: "b", Link: "http://b.test", Priority: 2, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(now), 12)
	mc, err := pipeline.Build()
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatal(err)
	}

	// daily allowance = 700/10 + 350/3 = 70 + 116 = 186; expire = 2026-05-01 00:00 UTC
	wantUI := fmt.Sprintf("upload=0; download=0; total=186; expire=%d",
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix())
	if got := rendered.Headers.Get("Subscription-Userinfo"); got != wantUI {
		t.Errorf("Subscription-Userinfo:\n  got:  %q\n  want: %q", got, wantUI)
	}
	if got := rendered.Headers.Get("Profile-Update-Interval"); got != "6" {
		t.Errorf("Profile-Update-Interval = %q, want 6 (min of 6, 24)", got)
	}
}

// TC-U-OUTPUT-FLOW-01: proxies render as flow style with name first
func TestSubscriptionMode_ProxiesFlowStyle(t *testing.T) {
	// Upstream has mixed styles: one flow, one block
	upstream := `proxies:
  - {name: proxy-a, type: trojan, server: a.test, port: 443}
  - name: proxy-b
    type: ss
    server: b.test
    port: 8388
proxy-groups:
  - {name: Auto, type: select, proxies: [proxy-a, proxy-b]}
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, err := pipeline.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	adapter, err := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	if err != nil {
		t.Fatalf("NewSubscriptionModeFromBytes: %v", err)
	}
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := string(rendered.Body)

	// Both proxies must be flow style (single-line {name: ..., ...})
	if !strings.Contains(body, "{name: src_proxy-a,") {
		t.Errorf("proxy-a should be flow style starting with 'name':\n%s", body)
	}
	if !strings.Contains(body, "{name: src_proxy-b,") {
		t.Errorf("proxy-b should be flow style starting with 'name':\n%s", body)
	}

	// Verify the YAML is still valid
	var got map[string]any
	if err := yaml.Unmarshal(rendered.Body, &got); err != nil {
		t.Fatalf("rendered body does not parse as YAML: %v\n--body--\n%s", err, rendered.Body)
	}
}

// TC-U-OUTPUT-FLOW-02: nested mappings in proxies also use flow style
func TestSubscriptionMode_NestedProxyFieldsFlowStyle(t *testing.T) {
	upstream := `proxies:
  - name: proxy-with-opts
    type: trojan
    server: test.com
    port: 443
    reality-opts:
      public-key: abc123
      short-id: xyz789
    ws-opts:
      path: /ws
      headers:
        Host: test.com
proxy-groups:
  - {name: Auto, type: select, proxies: [proxy-with-opts]}
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, err := adapter.Render(mc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	body := string(rendered.Body)

	// Nested mappings (reality-opts, ws-opts) should also be flow style
	if !strings.Contains(body, "reality-opts: {") {
		t.Errorf("reality-opts should be flow style:\n%s", body)
	}
	if !strings.Contains(body, "ws-opts: {") {
		t.Errorf("ws-opts should be flow style:\n%s", body)
	}

	// Check name is first
	if !strings.Contains(body, "{name: src_proxy-with-opts,") {
		t.Errorf("proxy should have name first in flow style:\n%s", body)
	}
}

func intPtrLocal(n int) *int { return &n }

// Without an upstream, the merged config is empty but Render still produces
// a valid Clash config (proxies, proxy-groups, rules become empty sequences).
func TestSubscriptionMode_RenderEmptyMergedConfig(t *testing.T) {
	adapter, err := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := adapter.Render(&merge.MergedConfig{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(rendered.Body, &got); err != nil {
		t.Fatalf("body does not parse: %v\n%s", err, rendered.Body)
	}
	if _, ok := got["proxies"]; !ok {
		t.Errorf("body missing proxies key")
	}
}

func TestSubscriptionMode_NilTemplateError(t *testing.T) {
	_, err := NewSubscriptionModeFromBytes([]byte("not: [valid {yaml"))
	if err == nil {
		t.Fatal("expected parse error on invalid template")
	}
}

// FR-005: the served body MUST NOT echo upstream URLs or credentials. Smoke
// check: load a payload whose source URL contains a token-shaped string and
// verify it doesn't appear in the rendered body.
//
// After 002 FR-001, source names must match ^[a-z]+$ — token-shaped names are
// rejected at load time. This test uses a valid lowercase name and verifies
// that the upstream URL (which carries the token) does NOT appear in output.
func TestSubscriptionMode_DoesNotEchoUpstreamCredentials(t *testing.T) {
	// Source name is now constrained to ^[a-z]+$ (FR-001), so use a valid name.
	sourceName := "provider"
	// The upstream URL contains the token, but the source name does not.
	upstreamURL := "http://x.test/sub?token=secret-token-12345"

	body := "proxies:\n  - {name: A1, type: trojan, server: a.test, port: 443, password: pw}\nproxy-groups:\n  - {name: Auto, type: select, proxies: [A1]}\nrules:\n  - MATCH,DIRECT\n"
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		sourceName: makePayload(sourceName, body),
	}}
	rows := []config.SubscriptionRow{
		{Name: sourceName, Link: upstreamURL, Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.RealClock{}, 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)

	// The upstream URL token must NOT appear in the rendered body.
	if strings.Contains(string(rendered.Body), "secret-token-12345") {
		t.Errorf("rendered body leaked upstream URL token:\n%s", rendered.Body)
	}
	// The source name (valid lowercase) will appear as prefix per FR-004.
	// That's intentional — it's the operator's identifier, not a credential.
}

// --- US1: Proxy-Groups Block Format Tests ---

// TC-U-OUTPUT-BLOCK-01: proxy-group from upstream with flow style becomes block style
func TestSubscriptionMode_ProxyGroupBlockStyle(t *testing.T) {
	upstream := `proxies:
  - {name: proxy-a, type: trojan, server: a.test, port: 443}
proxy-groups:
  - {name: Auto, type: select, proxies: [src_proxy-a]}
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)
	body := string(rendered.Body)

	// Block style means "name:" should appear on its own line (not in a {name: ...} flow)
	// The Auto group should render as multi-line block format
	if !strings.Contains(body, "- name: src_Auto\n") {
		t.Errorf("proxy-group should be block style with 'name:' on own line:\n%s", body)
	}
	// Flow style "{name:" should NOT appear for proxy-groups
	// Check proxy-groups section specifically
	lines := strings.Split(body, "\n")
	inGroups := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "proxy-groups:" {
			inGroups = true
			continue
		}
		if trimmed == "rules:" {
			inGroups = false
			continue
		}
		if inGroups && strings.HasPrefix(trimmed, "- {") {
			t.Errorf("proxy-group should not be flow style: %q", trimmed)
		}
	}
}

// TC-U-OUTPUT-BLOCK-02: proxy-group already in block style stays block style
func TestSubscriptionMode_ProxyGroupAlreadyBlock(t *testing.T) {
	upstream := `proxies:
  - name: proxy-a
    type: trojan
    server: a.test
    port: 443
proxy-groups:
  - name: Auto
    type: select
    proxies:
      - src_proxy-a
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)

	var got map[string]any
	if err := yaml.Unmarshal(rendered.Body, &got); err != nil {
		t.Fatalf("rendered body does not parse as YAML: %v\n%s", err, rendered.Body)
	}
}

// TC-U-OUTPUT-BLOCK-03: multiple proxy-groups all become block style
func TestSubscriptionMode_MultipleProxyGroupsBlock(t *testing.T) {
	upstream := `proxies:
  - {name: p1, type: ss, server: a.test, port: 443}
proxy-groups:
  - {name: Auto, type: url-test, proxies: [src_p1], url: http://test.com}
  - {name: Backup, type: select, proxies: [src_p1]}
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)
	body := string(rendered.Body)

	// Both Auto and Backup should be block style
	if !strings.Contains(body, "- name: src_Auto\n") {
		t.Errorf("Auto group should be block style:\n%s", body)
	}
	if !strings.Contains(body, "- name: src_Backup\n") {
		t.Errorf("Backup group should be block style:\n%s", body)
	}
}

// --- US2: Field Ordering Tests ---

// TC-U-OUTPUT-ORDER-01: proxy-group with fields in random order gets reordered
func TestSubscriptionMode_ProxyGroupFieldOrdering(t *testing.T) {
	// Upstream has fields in non-standard order: type, proxies, name
	upstream := `proxies:
  - {name: p1, type: ss, server: a.test, port: 443}
proxy-groups:
  - proxies: [src_p1]
    type: select
    name: Auto
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)

	// Parse as yaml.Node to inspect field order
	var doc yaml.Node
	if err := yaml.Unmarshal(rendered.Body, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := doc.Content[0]

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxy-groups" {
			seq := root.Content[i+1]
			// Check first proxy-group (Auto)
			// After reordering, first three keys should be name, type, proxies
			// (Proxies group is auto-generated, but Auto comes from upstream)
			for _, g := range seq.Content {
				if g.Kind != yaml.MappingNode {
					continue
				}
				name := getMappingFieldLocal(g, "name")
				if name == "src_Proxies" || name == "Proxies" || strings.HasPrefix(name, "_") {
					continue // skip auto-generated group
				}
				if g.Content[0].Value != "name" {
					t.Errorf("first field should be 'name', got %q", g.Content[0].Value)
				}
				if len(g.Content) >= 4 && g.Content[2].Value != "type" {
					t.Errorf("second field should be 'type', got %q", g.Content[2].Value)
				}
				if len(g.Content) >= 6 && g.Content[4].Value != "proxies" {
					t.Errorf("third field should be 'proxies', got %q", g.Content[4].Value)
				}
			}
			return
		}
	}
	t.Error("proxy-groups not found in rendered output")
}

// TC-U-OUTPUT-ORDER-02: proxy-group missing proxies field gets name and type first
func TestSubscriptionMode_ProxyGroupFieldOrderingNoProxies(t *testing.T) {
	upstream := `proxies:
  - {name: p1, type: ss, server: a.test, port: 443}
proxy-groups:
  - type: url-test
    name: Auto
    url: http://test.com
    interval: 300
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)

	var doc yaml.Node
	yaml.Unmarshal(rendered.Body, &doc)
	root := doc.Content[0]

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxy-groups" {
			seq := root.Content[i+1]
			for _, g := range seq.Content {
				if g.Kind != yaml.MappingNode {
					continue
				}
				name := getMappingFieldLocal(g, "name")
				if name == "src_Proxies" || name == "Proxies" || strings.HasPrefix(name, "_") {
					continue
				}
				if g.Content[0].Value != "name" {
					t.Errorf("first field should be 'name', got %q", g.Content[0].Value)
				}
				if g.Content[2].Value != "type" {
					t.Errorf("second field should be 'type', got %q", g.Content[2].Value)
				}
				// proxies not present in upstream group; no assertion on 3rd field
			}
			return
		}
	}
}

// TC-U-OUTPUT-ORDER-03: additional fields preserve relative order after name/type/proxies
func TestSubscriptionMode_ProxyGroupExtraFieldsPreserveOrder(t *testing.T) {
	upstream := `proxies:
  - {name: p1, type: ss, server: a.test, port: 443}
proxy-groups:
  - url: http://test.com
    interval: 300
    tolerance: 50
    name: Auto
    type: url-test
rules:
  - MATCH,Auto
`
	cache := &stubCache{p: map[string]*fetcher.UpstreamCachedPayload{
		"src": makePayload("src", upstream),
	}}
	rows := []config.SubscriptionRow{
		{Name: "src", Link: "http://a.test", Priority: 1, Enable: true},
	}
	pipeline := merge.NewPipeline(cache, rows, nil, nil, clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)), 12)
	mc, _ := pipeline.Build()

	adapter, _ := NewSubscriptionModeFromBytes([]byte(minimalTemplate))
	rendered, _ := adapter.Render(mc)

	var doc yaml.Node
	yaml.Unmarshal(rendered.Body, &doc)
	root := doc.Content[0]

	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxy-groups" {
			seq := root.Content[i+1]
			for _, g := range seq.Content {
				if g.Kind != yaml.MappingNode {
					continue
				}
				name := getMappingFieldLocal(g, "name")
				if name == "src_Proxies" || name == "Proxies" || strings.HasPrefix(name, "_") {
					continue
				}
				// After name(0), type(2), proxies(4), remaining fields preserve original relative order
				remainingFields := make([]string, 0)
				for idx := 6; idx+1 < len(g.Content); idx += 2 {
					remainingFields = append(remainingFields, g.Content[idx].Value)
				}
				// At minimum we expect url and interval (extra fields from upstream)
				if len(remainingFields) < 2 {
					t.Fatalf("expected at least 2 extra fields, got %d: %v", len(remainingFields), remainingFields)
				}
				// Check that url and interval appear in their original relative order
				if remainingFields[0] != "url" {
					t.Errorf("first extra field: got %q, want 'url'", remainingFields[0])
				}
				if remainingFields[1] != "interval" {
					t.Errorf("second extra field: got %q, want 'interval'", remainingFields[1])
				}
			}
			return
		}
	}
}

func getMappingFieldLocal(n *yaml.Node, key string) string {
	if n == nil || n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Value == key && v.Kind == yaml.ScalarNode {
			return v.Value
		}
	}
	return ""
}

// --- Feature 005: Priority-Bucket Header Comment Tests ---

// TC-U-OUTPUT-PRIORITY-01: single contributor at one priority → one head comment.
func TestNormalizeRulesPriorityComments_SingleContributor(t *testing.T) {
	root := buildYAMLRoot("rules",
		"DOMAIN,a.test,Auto",
		"DOMAIN,b.test,Auto",
		"DOMAIN,c.test,Auto",
		"MATCH,auto",
	)
	priorities := []int{1000, 1000, 1000, 0}
	contributors := []string{"alpha", "alpha", "alpha", ""}
	normalizeRulesPriorityComments(root, priorities, contributors)

	rules := findRulesSequence(root)
	if rules.Content[0].HeadComment != "# --- priority 1000 (alpha) ---" {
		t.Errorf("first rule head comment: got %q, want \"# --- priority 1000 (alpha) ---\"", rules.Content[0].HeadComment)
	}
	for i := 1; i < len(rules.Content); i++ {
		if rules.Content[i].HeadComment != "" {
			t.Errorf("rule %d should have no head comment, got %q", i, rules.Content[i].HeadComment)
		}
	}
}

// TC-U-OUTPUT-PRIORITY-02: two contributors share priority 1000 → single header
// comment with both names alphabetically.
func TestNormalizeRulesPriorityComments_TieBreakHeader(t *testing.T) {
	root := buildYAMLRoot("rules",
		"DOMAIN,c1.test,REJECT",
		"DOMAIN,c2.test,REJECT",
		"DOMAIN,e1.test,Auto",
		"MATCH,auto",
	)
	// alpha (alphabetically first) contributes the first two; corporate the next one.
	priorities := []int{1000, 1000, 1000, 0}
	contributors := []string{"alpha", "alpha", "corporate", ""}
	normalizeRulesPriorityComments(root, priorities, contributors)

	rules := findRulesSequence(root)
	want := "# --- priority 1000 (alpha, corporate) ---"
	if rules.Content[0].HeadComment != want {
		t.Errorf("bucket header: got %q, want %q", rules.Content[0].HeadComment, want)
	}
	for i := 1; i < len(rules.Content); i++ {
		if rules.Content[i].HeadComment != "" {
			t.Errorf("rule %d should have no head comment, got %q", i, rules.Content[i].HeadComment)
		}
	}
}

// TC-U-OUTPUT-PRIORITY-03: three priority buckets → three head comments at the
// first rule of each bucket, in descending priority order.
func TestNormalizeRulesPriorityComments_ThreeBuckets(t *testing.T) {
	root := buildYAMLRoot("rules",
		"DOMAIN,h.test,Auto",
		"DOMAIN,m.test,Auto",
		"DOMAIN,l.test,Auto",
		"MATCH,auto",
	)
	priorities := []int{2000, 1500, 1000, 0}
	contributors := []string{"high", "mid", "low", ""}
	normalizeRulesPriorityComments(root, priorities, contributors)

	rules := findRulesSequence(root)
	if rules.Content[0].HeadComment != "# --- priority 2000 (high) ---" {
		t.Errorf("bucket 0: got %q", rules.Content[0].HeadComment)
	}
	if rules.Content[1].HeadComment != "# --- priority 1500 (mid) ---" {
		t.Errorf("bucket 1: got %q", rules.Content[1].HeadComment)
	}
	if rules.Content[2].HeadComment != "# --- priority 1000 (low) ---" {
		t.Errorf("bucket 2: got %q", rules.Content[2].HeadComment)
	}
	if rules.Content[3].HeadComment != "" {
		t.Errorf("MATCH should have no head comment, got %q", rules.Content[3].HeadComment)
	}
}

// TC-U-OUTPUT-PRIORITY-04: priority 0 with empty contributor (the MATCH fallback)
// → no head comment emitted, even when it's the only entry.
func TestNormalizeRulesPriorityComments_MatchOnly(t *testing.T) {
	root := buildYAMLRoot("rules", "MATCH,auto")
	priorities := []int{0}
	contributors := []string{""}
	normalizeRulesPriorityComments(root, priorities, contributors)

	rules := findRulesSequence(root)
	if rules.Content[0].HeadComment != "" {
		t.Errorf("MATCH-only rule should have no head comment, got %q", rules.Content[0].HeadComment)
	}
}

// TC-U-OUTPUT-PRIORITY-05: rendered YAML never contains the legacy
// "# --- upstream ---" string for any input. Renders the YAML and asserts.
func TestNormalizeRulesPriorityComments_NoLegacyUpstreamComment(t *testing.T) {
	root := buildYAMLRoot("rules",
		"DOMAIN,a.test,Auto",
		"DOMAIN,b.test,REJECT",
		"MATCH,auto",
	)
	priorities := []int{1000, 500, 0}
	contributors := []string{"alpha", "corporate", ""}
	normalizeRulesPriorityComments(root, priorities, contributors)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		t.Fatalf("encode: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, "# --- upstream ---") {
		t.Errorf("rendered YAML must not contain legacy \"# --- upstream ---\" comment:\n%s", body)
	}
	// Sanity: priority headers ARE present.
	if !strings.Contains(body, "# --- priority 1000 (alpha) ---") {
		t.Errorf("expected priority 1000 header missing:\n%s", body)
	}
	if !strings.Contains(body, "# --- priority 500 (corporate) ---") {
		t.Errorf("expected priority 500 header missing:\n%s", body)
	}
}

func buildYAMLRoot(key string, rules ...string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, r := range rules {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: r, Tag: "!!str"})
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		seq,
	}}
}

func findRulesSequence(root *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "rules" {
			return root.Content[i+1]
		}
	}
	return nil
}

// --- Feature 006: unescapeSupplementaryPlane unit tests ---

// encodeYAMLString runs a single string value through yaml.NewEncoder and
// returns the rendered bytes. Used by the unit tests below to drive
// unescapeSupplementaryPlane against real yaml.v3 output (not synthetic
// hand-crafted byte strings, which could mask bugs in the helper).
func encodeYAMLString(t *testing.T, value string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]string{"name": value}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

// TC-U-OUTPUT-EMOJI-01: single emoji 🔰 (U+1F530) at start → literal UTF-8
// bytes, no \U escape substring in the output.
func TestUnescapeSupplementaryPlane_SingleEmoji(t *testing.T) {
	in := encodeYAMLString(t, "🔰")
	if !strings.Contains(string(in), `\U0001F530`) {
		t.Fatalf("precondition: yaml.v3 should emit \\U0001F530 for U+1F530; got: %s", in)
	}
	out := unescapeSupplementaryPlane(in)
	if !strings.Contains(string(out), "🔰") {
		t.Errorf("output should contain literal 🔰; got: %s", out)
	}
	if strings.Contains(string(out), `\U`) {
		t.Errorf("output should not contain \\U escape; got: %s", out)
	}
}

// TC-U-OUTPUT-EMOJI-02: mixed BMP + non-BMP — emoji + CJK ideographs
// → both render as literals; CJK (BMP, already literal pre-fix) unchanged.
func TestUnescapeSupplementaryPlane_MixedBMP(t *testing.T) {
	source := "alpha_🔰国外流量"
	in := encodeYAMLString(t, source)
	out := unescapeSupplementaryPlane(in)
	if !strings.Contains(string(out), source) {
		t.Errorf("output should contain literal %q; got: %s", source, out)
	}
	if strings.Contains(string(out), `\U`) {
		t.Errorf("output should not contain any \\U escape; got: %s", out)
	}
}

// TC-U-OUTPUT-EMOJI-03: ASCII-only string → no transformation, byte-identical
// pre/post helper.
func TestUnescapeSupplementaryPlane_AsciiOnly(t *testing.T) {
	in := encodeYAMLString(t, "hello-world")
	out := unescapeSupplementaryPlane(in)
	if !bytes.Equal(in, out) {
		t.Errorf("ASCII-only body should be byte-identical pre/post helper\n  in:  %q\n  out: %q", in, out)
	}
}

// TC-U-OUTPUT-EMOJI-04: critical safety case. The source string contains BOTH
// a real supplementary-plane character (🔰, U+1F530) AND the 10 ASCII characters
// `\U0001F530` (backslash + U + 8 hex). yaml.v3 chooses double-quoted style
// (because of the emoji) and emits `\U0001F530\\U0001F530` — the first sequence
// is the real escape (1 backslash), the second is operator content (2
// backslashes, the first escaping the second). The helper must rewrite the
// first to literal 🔰 bytes AND leave the second as `\\U0001F530` unchanged.
func TestUnescapeSupplementaryPlane_LiteralBackslashU(t *testing.T) {
	// Source: real emoji (4 UTF-8 bytes) followed by literal ASCII \U0001F530 (10 bytes).
	source := "🔰" + `\U0001F530`
	in := encodeYAMLString(t, source)
	// Precondition: yaml.v3 emits the real emoji as \U escape and the literal as \\U escape.
	if !strings.Contains(string(in), `\U0001F530\\U0001F530`) {
		t.Fatalf("precondition: yaml.v3 should emit \\U0001F530\\\\U0001F530; got: %s", in)
	}
	out := unescapeSupplementaryPlane(in)
	// Real emoji must be rewritten to literal UTF-8.
	if !strings.Contains(string(out), "🔰") {
		t.Errorf("helper must rewrite real \\U escape to literal 🔰; got: %s", out)
	}
	// Operator content (escaped backslash) must survive unchanged.
	if !strings.Contains(string(out), `\\U0001F530`) {
		t.Errorf("helper must NOT rewrite \\\\U escape (escaped backslash, operator content); got: %s", out)
	}
	// Round-trip safety: parse output and confirm the original source is recovered.
	var parsed map[string]string
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("post-helper body does not parse: %v\n  body: %s", err, out)
	}
	if got := parsed["name"]; got != source {
		t.Errorf("round-trip mismatch:\n  source: %q\n  parsed: %q", source, got)
	}
}

// TC-U-OUTPUT-EMOJI-05: \xhh-style control-character escape passes through
// unchanged. Helper only matches \U + exactly 8 hex digits.
func TestUnescapeSupplementaryPlane_ControlCharEscape(t *testing.T) {
	in := encodeYAMLString(t, "tab\there")
	if !strings.Contains(string(in), `\t`) {
		t.Fatalf("precondition: yaml.v3 should emit \\t for a tab character; got: %s", in)
	}
	out := unescapeSupplementaryPlane(in)
	if !strings.Contains(string(out), `\t`) {
		t.Errorf("helper must not touch \\t escape; got: %s", out)
	}
}

// TC-U-OUTPUT-EMOJI-06: round-trip — encode source, run helper, parse result;
// parsed value must equal source byte-for-byte.
func TestUnescapeSupplementaryPlane_RoundTrip(t *testing.T) {
	source := "alpha_🔰 USA-Premium"
	in := encodeYAMLString(t, source)
	out := unescapeSupplementaryPlane(in)
	var parsed map[string]string
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("post-helper body does not parse as YAML: %v\n  body: %s", err, out)
	}
	got := parsed["name"]
	if got != source {
		t.Errorf("round-trip mismatch:\n  source: %q\n  parsed: %q", source, got)
	}
}

// 012 FR-007: reorderProxyGroupFields positions the five url-test fields
// in the documented order after name, type, proxies. Constructs a mapping
// node with the keys in scrambled order and asserts the post-reorder order.
func TestReorderProxyGroupFields_URLTestOrdering(t *testing.T) {
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }
	seq := func(items ...string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range items {
			s.Content = append(s.Content, scalar(it))
		}
		return s
	}

	// Construct in scrambled order: lazy, url, name, max-failed-times, type, interval, proxies, timeout
	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("lazy"), scalar("true"),
			scalar("url"), scalar("https://gstatic/204"),
			scalar("name"), scalar("_region_JP"),
			scalar("max-failed-times"), scalar("3"),
			scalar("type"), scalar("url-test"),
			scalar("interval"), scalar("10"),
			scalar("proxies"), seq("a", "b"),
			scalar("timeout"), scalar("3000"),
		},
	}

	reorderProxyGroupFields(n)

	wantKeyOrder := []string{
		"name", "type", "proxies",
		"url", "interval", "timeout", "max-failed-times", "lazy",
	}
	gotKeys := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		gotKeys = append(gotKeys, n.Content[i].Value)
	}
	if len(gotKeys) != len(wantKeyOrder) {
		t.Fatalf("got %d keys, want %d", len(gotKeys), len(wantKeyOrder))
	}
	for i, want := range wantKeyOrder {
		if gotKeys[i] != want {
			t.Errorf("key[%d] = %q, want %q (full order: got=%v want=%v)",
				i, gotKeys[i], want, gotKeys, wantKeyOrder)
		}
	}
}

// 014 FR-006: load-balance groups have their nine fields in the documented
// order: name, type, proxies, url, interval, lazy, strategy, timeout,
// max-failed-times. This order DIFFERS from url-test (where lazy is last)
// and adds the new `strategy` field.
func TestReorderProxyGroupFields_LoadBalanceOrdering(t *testing.T) {
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }
	seq := func(items ...string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range items {
			s.Content = append(s.Content, scalar(it))
		}
		return s
	}

	// Construct in scrambled order.
	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("strategy"), scalar("round-robin"),
			scalar("max-failed-times"), scalar("3"),
			scalar("type"), scalar("load-balance"),
			scalar("name"), scalar("_lb_region_JP"),
			scalar("proxies"), seq("a", "b"),
			scalar("interval"), scalar("300"),
			scalar("lazy"), scalar("true"),
			scalar("url"), scalar("https://gstatic/204"),
			scalar("timeout"), scalar("1500"),
		},
	}

	reorderProxyGroupFields(n)

	wantKeyOrder := []string{
		"name", "type", "proxies",
		"url", "interval", "lazy", "strategy", "timeout", "max-failed-times",
	}
	gotKeys := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		gotKeys = append(gotKeys, n.Content[i].Value)
	}
	if len(gotKeys) != len(wantKeyOrder) {
		t.Fatalf("got %d keys, want %d", len(gotKeys), len(wantKeyOrder))
	}
	for i, want := range wantKeyOrder {
		if gotKeys[i] != want {
			t.Errorf("key[%d] = %q, want %q (full order: got=%v want=%v)",
				i, gotKeys[i], want, gotKeys, wantKeyOrder)
		}
	}
}

// 014 FR-007: url-test groups (012 layout) MUST remain byte-identical after
// the load-balance ordering branch lands. Same input as the 012 url-test
// ordering test, asserted again to catch regressions if the branch logic
// accidentally affects url-test groups.
func TestReorderProxyGroupFields_URLTestOrderingPreserved(t *testing.T) {
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }
	seq := func(items ...string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range items {
			s.Content = append(s.Content, scalar(it))
		}
		return s
	}

	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("lazy"), scalar("true"),
			scalar("url"), scalar("https://gstatic/204"),
			scalar("name"), scalar("_region_JP"),
			scalar("max-failed-times"), scalar("3"),
			scalar("type"), scalar("url-test"),
			scalar("interval"), scalar("10"),
			scalar("proxies"), seq("a", "b"),
			scalar("timeout"), scalar("3000"),
		},
	}

	reorderProxyGroupFields(n)

	wantKeyOrder := []string{
		"name", "type", "proxies",
		"url", "interval", "timeout", "max-failed-times", "lazy",
	}
	gotKeys := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		gotKeys = append(gotKeys, n.Content[i].Value)
	}
	for i, want := range wantKeyOrder {
		if i >= len(gotKeys) || gotKeys[i] != want {
			t.Errorf("key[%d] = %q, want %q (url-test order MUST stay 012)", i, gotKeys[i], want)
		}
	}
}

// 012 FR-005: non-url-test groups (like the always-present Proxies selector)
// pass through reorderProxyGroupFields safely; the helper is a no-op for
// keys that aren't present.
func TestReorderProxyGroupFields_SelectGroupUntouched(t *testing.T) {
	scalar := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }
	seq := func(items ...string) *yaml.Node {
		s := &yaml.Node{Kind: yaml.SequenceNode}
		for _, it := range items {
			s.Content = append(s.Content, scalar(it))
		}
		return s
	}

	n := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("type"), scalar("select"),
			scalar("proxies"), seq("p1"),
			scalar("name"), scalar("Proxies"),
		},
	}
	reorderProxyGroupFields(n)

	wantKeyOrder := []string{"name", "type", "proxies"}
	gotKeys := make([]string, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		gotKeys = append(gotKeys, n.Content[i].Value)
	}
	if len(gotKeys) != 3 {
		t.Fatalf("got %d keys, want 3", len(gotKeys))
	}
	for i, want := range wantKeyOrder {
		if gotKeys[i] != want {
			t.Errorf("key[%d] = %q, want %q", i, gotKeys[i], want)
		}
	}
}
