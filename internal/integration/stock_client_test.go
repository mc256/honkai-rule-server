package integration

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// US4 acceptance gate (formalizes T084's manual stock-client check):
// reproduce what a stock Mihomo / Sparkle client does when given the URL,
// and verify each promise the contract makes:
//
// 1. GET responds with 200 + Content-Type: application/yaml.
// 2. Body parses as Clash YAML and contains a non-empty proxy list.
// 3. Subscription-Userinfo header is present with all 4 fields, parses
//    as integers, and is internally consistent (upload+download <= total
//    when total > 0).
// 4. Profile-Update-Interval is a positive integer (hours).
// 5. Every proxy in the body is reachable from at least one select-type
//    proxy-group: upstream proxies via the always-present FR-009a `Proxies`
//    group; own-proxies via the operator's own-groups (002 FR-007b);
//    fan-out copies (`via_*`) via custom rules (003) or operator-declared
//    own-groups — for stock-client compatibility we require every
//    own-proxy to be reachable from at least one own-group.
//
// Together these are what makes the URL "drop-in" for stock clients —
// i.e., adds without configuration changes and the client's usage bar
// renders correctly.
func TestStockClientCompatibility(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		"alpha":      fmt.Sprintf("upload=10737418240; download=42949672960; total=214748364800; expire=%d", now.Add(30*24*time.Hour).Unix()),
		"beta": fmt.Sprintf("upload=5368709120; download=16106127360; total=107374182400; expire=%d", now.Add(5*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	// Step 1: HTTP shape.
	resp, err := http.Get(tc.URL() + "/?token=" + validToken)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml...", ct)
	}

	// Step 2: body parses + has proxies.
	body := bodyOf(t, resp)
	type proxy struct {
		Name string `yaml:"name"`
	}
	type group struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Proxies []string `yaml:"proxies"`
	}
	type clashCfg struct {
		Proxies     []proxy `yaml:"proxies"`
		ProxyGroups []group `yaml:"proxy-groups"`
	}
	var cfg clashCfg
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("body does not parse as Clash YAML: %v", err)
	}
	if len(cfg.Proxies) == 0 {
		t.Fatalf("served body has no proxies; client would show empty list")
	}

	// Step 3: Subscription-Userinfo well-formed.
	ui := resp.Header.Get("Subscription-Userinfo")
	if ui == "" {
		t.Fatalf("missing Subscription-Userinfo — client usage bar would not render")
	}
	uinfo := parseSubscriptionUserinfo(t, ui)
	if uinfo["upload"] < 0 || uinfo["download"] < 0 || uinfo["total"] <= 0 {
		t.Errorf("Subscription-Userinfo values look bogus: %v", uinfo)
	}
	if uinfo["upload"]+uinfo["download"] > uinfo["total"]+1 {
		t.Errorf("Subscription-Userinfo internally inconsistent: used > total: %v", uinfo)
	}

	// Step 4: Profile-Update-Interval is a positive integer.
	pui := resp.Header.Get("Profile-Update-Interval")
	if pui == "" {
		t.Fatal("missing Profile-Update-Interval — client would never auto-refresh")
	}
	hours, err := strconv.Atoi(pui)
	if err != nil || hours <= 0 {
		t.Errorf("Profile-Update-Interval = %q, want positive integer", pui)
	}

	// Step 5: every proxy is reachable from at least one select-type group.
	// After 008, own-proxies (`_<own>`) and fan-out copies (`via_*`) are
	// excluded from the always-present `Proxies` selector — they're reached
	// via own-groups (002 FR-007b) or custom rules. The check thus splits
	// per proxy class:
	//   - upstream-prefixed proxies → must appear in the `Proxies` group
	//   - own-proxies (`_<name>` not matching `_region_*`/`_continent_*`)
	//     → must appear in at least one own-group's member list
	//   - fan-out copies (`via_*`) → not asserted (operator opt-in via
	//     custom rules or explicit own-group declaration)
	selectGroups := make(map[string][]string)
	for _, g := range cfg.ProxyGroups {
		if g.Type == "select" {
			selectGroups[g.Name] = g.Proxies
		}
	}
	proxiesGroup, ok := selectGroups["Proxies"]
	if !ok {
		t.Fatalf("always-present 'Proxies' selector group is missing; client UI would have nothing to select")
	}
	proxiesGroupSet := make(map[string]bool, len(proxiesGroup))
	for _, m := range proxiesGroup {
		proxiesGroupSet[m] = true
	}

	// Build a reverse map: proxy name → list of own-groups that contain it.
	memberOfGroups := make(map[string][]string)
	for groupName, members := range selectGroups {
		// own-groups have leading `_` and are NOT region/continent groups.
		if !strings.HasPrefix(groupName, "_") ||
			strings.HasPrefix(groupName, "_region_") ||
			strings.HasPrefix(groupName, "_continent_") {
			continue
		}
		for _, m := range members {
			memberOfGroups[m] = append(memberOfGroups[m], groupName)
		}
	}

	for _, p := range cfg.Proxies {
		switch {
		case strings.HasPrefix(p.Name, "via_"):
			// Fan-out copy — operator routes via custom rules; not asserted.
		case strings.HasPrefix(p.Name, "_"):
			// Own-proxy → must be in at least one own-group.
			if len(memberOfGroups[p.Name]) == 0 {
				t.Errorf("own-proxy %q is not a member of any own-group; client UI cannot reach it", p.Name)
			}
		default:
			// Upstream-prefixed proxy → must be in the `Proxies` selector.
			if !proxiesGroupSet[p.Name] {
				t.Errorf("upstream proxy %q is missing from the `Proxies` selector; client UI cannot reach it (FR-009a)", p.Name)
			}
		}
	}
}

// parseSubscriptionUserinfo decodes the `upload=N; download=N; total=N;
// expire=N` wire format into a small map for assertions.
func parseSubscriptionUserinfo(t *testing.T, s string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, field := range strings.Split(s, ";") {
		parts := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(parts) != 2 {
			continue
		}
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			t.Errorf("Subscription-Userinfo field %q: %v", field, err)
		}
		out[parts[0]] = n
	}
	return out
}
