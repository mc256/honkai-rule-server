package integration

import (
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TC-I-16: with the own-proxies fixture loaded, the served body contains
// both own-proxies (by name with underscore prefix) AND the always-present
// `Proxies` group. After 002 FR-007a, own-proxies are prefixed with `_`.
// After 008 FR-007/-008, own-proxies are NOT direct members of the global
// Proxies selector — they remain reachable through their own-groups (002
// FR-007b's `_<group>` form) and through fan-out copies (008 FR-001..-006).
func TestI_16_OwnProxiesAppearInServedBody(t *testing.T) {
	tc := newTestCluster(t)

	resp := tc.Get(t, "/", validToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, string(bodyOf(t, resp)))
	}

	body := bodyOf(t, resp)

	type proxy struct {
		Name string `yaml:"name"`
	}
	type group struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	}
	type doc struct {
		Proxies     []proxy `yaml:"proxies"`
		ProxyGroups []group `yaml:"proxy-groups"`
	}
	var d doc
	if err := yaml.Unmarshal(body, &d); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Own-proxies present by name with underscore prefix (FR-007a).
	wantOwnProxies := map[string]bool{"_my-home-trojan": true, "_my-vps-vless": true}
	for _, p := range d.Proxies {
		delete(wantOwnProxies, p.Name)
	}
	for n := range wantOwnProxies {
		t.Errorf("own-proxy %q missing from served body", n)
	}

	// Own group present with underscore prefix (FR-007b).
	hasMyOwn := false
	var proxiesGroup *group
	for i, g := range d.ProxyGroups {
		if g.Name == "_My-Own" {
			hasMyOwn = true
		}
		if g.Name == "Proxies" {
			proxiesGroup = &d.ProxyGroups[i]
		}
	}
	if !hasMyOwn {
		t.Errorf("own group '_My-Own' missing from served body (got %d groups)", len(d.ProxyGroups))
	}

	// FR-009a: always-present Proxies group exists, contains both own-proxies.
	if proxiesGroup == nil {
		names := make([]string, 0, len(d.ProxyGroups))
		for _, g := range d.ProxyGroups {
			names = append(names, g.Name)
		}
		t.Fatalf("always-present 'Proxies' group not found (got groups: %s)", strings.Join(names, ", "))
	}
	if proxiesGroup.Type != "select" {
		t.Errorf("Proxies group type = %q, want select", proxiesGroup.Type)
	}
	// 008 FR-007/-008: own-proxies and via_* fan-out copies are NOT direct
	// members of the always-present Proxies selector group.
	forbidden := map[string]bool{"_my-home-trojan": true, "_my-vps-vless": true}
	for _, m := range proxiesGroup.Proxies {
		if forbidden[m] {
			t.Errorf("Proxies group must NOT contain own-proxy %q (008 FR-007)", m)
		}
		if strings.HasPrefix(m, "via_") {
			t.Errorf("Proxies group must NOT contain fan-out copy %q (008 FR-008)", m)
		}
	}
	// Sanity: Proxies group should still include upstream proxies and
	// `_region_*`/`_continent_*` group names.
	if len(proxiesGroup.Proxies) < 100 {
		t.Errorf("Proxies group has %d members; want >=100 (every upstream proxy + region/continent groups)", len(proxiesGroup.Proxies))
	}
}