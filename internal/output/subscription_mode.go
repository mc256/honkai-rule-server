// Package output renders the merge layer's MergedConfig into the response
// body and headers for the subscription endpoint. This is the only mode-aware
// component in the project (Constitution Principle I); a future override-mode
// adapter will plug in at the same interface.
package output

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/mc256/honkai-rule-server/internal/merge"
)

// Rendered is the output of SubscriptionMode.Render — body bytes plus the
// response headers the HTTP handler must set.
type Rendered struct {
	Body    []byte
	Headers http.Header
}

// SubscriptionMode is the v1 output adapter. It loads the served-config
// template once at construction and re-uses it for every render.
type SubscriptionMode struct {
	templateBytes []byte
}

// NewSubscriptionMode reads the served-config template from disk and returns
// an adapter. The template MUST parse as a Clash config; a parse error here
// surfaces at startup, not at request time.
func NewSubscriptionMode(templatePath string) (*SubscriptionMode, error) {
	b, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("output: read template %s: %w", templatePath, err)
	}
	// Validate it parses; we re-parse on every render to get a fresh tree.
	var probe yaml.Node
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("output: template %s does not parse as YAML: %w", templatePath, err)
	}
	return &SubscriptionMode{templateBytes: b}, nil
}

// NewSubscriptionModeFromBytes is a test-friendly constructor that skips
// the disk read.
func NewSubscriptionModeFromBytes(template []byte) (*SubscriptionMode, error) {
	var probe yaml.Node
	if err := yaml.Unmarshal(template, &probe); err != nil {
		return nil, fmt.Errorf("output: template does not parse as YAML: %w", err)
	}
	return &SubscriptionMode{templateBytes: template}, nil
}

// Render produces the served body + response headers from a MergedConfig.
// Replaces the template's `proxies`, `proxy-groups`, and `rules` keys with
// the merged content (regardless of what the template originally had as a
// placeholder value); other top-level keys (mixed-port, mode, dns, etc.)
// pass through unchanged.
//
// Per FR-005c: globals come from the template, not from upstreams. Per
// FR-005: the body never echoes upstream URLs, tokens, or the cache.
//
// The returned Headers always set Content-Type. Subscription-Userinfo and
// Profile-Update-Interval are emitted in US4 once US3 has populated the
// aggregated values; for now they remain unset.
func (s *SubscriptionMode) Render(merged *merge.MergedConfig) (*Rendered, error) {
	if merged == nil {
		return nil, fmt.Errorf("output: nil MergedConfig")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(s.templateBytes, &doc); err != nil {
		return nil, fmt.Errorf("output: re-parse template: %w", err)
	}
	root := docRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("output: template root must be a YAML mapping")
	}

	setMappingValue(root, "proxies", sequenceOfNodes(merged.Proxies))
	setMappingValue(root, "proxy-groups", sequenceOfNodes(merged.ProxyGroups))
	setMappingValue(root, "rules", sequenceOfStrings(merged.Rules))

	// 016 FR-005/FR-006: emit the merged `rule-providers:` mapping when any
	// surviving RULE-SET rule referenced a provider; omit the key entirely
	// otherwise (nil node). Appended at end of the document mapping.
	if merged.RuleProviders != nil {
		setMappingValue(root, "rule-providers", merged.RuleProviders)
	}

	// Strip ALL comments from the document. The template carries docstrings
	// for operators (explaining the __MERGED_*__ placeholders); those are
	// useful in the file but should not appear in every served response.
	stripComments(&doc)

	// Normalize proxy rendering: force flow style (folded single-line) and
	// ensure "name" field appears first for consistent, readable output.
	normalizeProxyStyle(root)

	// Normalize proxy-groups: block style with consistent field ordering
	// (name, type, proxies first, then remaining fields).
	normalizeProxyGroupStyle(root)

	// Feature 005: emit one head comment per priority bucket naming all
	// contributors that supplied rules at that priority.
	normalizeRulesPriorityComments(root, merged.RulePriorities, merged.RuleContributors)

	// Reset scalar styles so the emitter picks the most natural style for
	// each value rather than carrying over upstream styles. Without this,
	// yaml.v3 sometimes inherits DoubleQuotedStyle on scalars containing
	// emoji / supplementary-plane Unicode and escapes them as \U0001Fxxx,
	// which is semantically valid but less readable than raw UTF-8.
	resetScalarStyles(&doc)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("output: encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("output: close encoder: %w", err)
	}

	// Feature 006: yaml.v3's emitter unconditionally escapes Unicode code points
	// above U+FFFF as \Uxxxxxxxx regardless of node Style. Rewrite those escapes
	// back to literal UTF-8 so emoji proxy names render readably.
	body := unescapeSupplementaryPlane(buf.Bytes())

	headers := http.Header{}
	headers.Set("Content-Type", "application/yaml; charset=utf-8")
	headers.Set("Cache-Control", "no-store, no-cache, must-revalidate")

	// 011 FR-001/FR-002 (supersedes 010 FR-003's recommended encoding):
	// emit Subscription-Userinfo carrying the spend-tracking encoding —
	// upload+download = used_today, total = allowance + used_today,
	// expire = next 00:00 in budget timezone (America/Toronto in prod).
	// When no snapshotter is configured (test fixtures without spend
	// tracking, override mode), the helper leaves TotalBytes = 0 and
	// Used/Upload/Download = 0 — emit as upload=0; download=0;
	// total=DailyAllowanceBytes per 010's encoding. Wire format
	// unchanged so stock Mihomo / Clash UIs render correctly. Header
	// omitted when no source contributed userinfo (010 FR-006).
	if h := merged.ServedTrafficHeader; h != nil {
		total := h.TotalBytes
		if total == 0 {
			// 010 backward-compat path (no snapshotter configured).
			total = h.DailyAllowanceBytes
		}
		headers.Set("Subscription-Userinfo",
			fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
				h.UploadBytes, h.DownloadBytes, total, h.ExpireUnix))
	}

	// FR-011a: emit aggregated Profile-Update-Interval (integer hours)
	// so the client respects the operator's refresh cadence.
	if h := merged.AggregatedProfileUpdateIntervalHours; h > 0 {
		headers.Set("Profile-Update-Interval", fmt.Sprintf("%d", h))
	}

	return &Rendered{
		Body:    body,
		Headers: headers,
	}, nil
}

// normalizeProxyStyle forces all proxy entries to render as flow style
// (folded single-line {key: val, ...}) with the "name" field appearing first.
// This produces consistent, readable output regardless of how upstream
// providers formatted their YAML.
func normalizeProxyStyle(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxies" {
			seq := root.Content[i+1]
			if seq.Kind != yaml.SequenceNode {
				return
			}
			for _, proxy := range seq.Content {
				setFlowStyleRecursive(proxy)
				reorderNameFirst(proxy)
			}
			return
		}
	}
}

func setFlowStyleRecursive(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style = yaml.FlowStyle
	}
	for _, c := range n.Content {
		setFlowStyleRecursive(c)
	}
}

func reorderNameFirst(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "name" && i > 0 {
			key, val := n.Content[i], n.Content[i+1]
			copy(n.Content[2:], n.Content[:i])
			n.Content[0] = key
			n.Content[1] = val
			break
		}
	}
}

// normalizeProxyGroupStyle ensures all proxy-groups render in block format
// with fields ordered: name, type, proxies first (then remaining fields).
func normalizeProxyGroupStyle(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "proxy-groups" {
			seq := root.Content[i+1]
			if seq.Kind != yaml.SequenceNode {
				return
			}
			for _, group := range seq.Content {
				if group.Kind != yaml.MappingNode {
					continue
				}
				// US1: Force block style (remove flow style)
				group.Style = 0
				// US2: Reorder fields: name, type, proxies first
				reorderProxyGroupFields(group)
			}
			return
		}
	}
}

// reorderProxyGroupFields reorders mapping Content to put name, type, proxies first.
// Content alternates key, value nodes. We swap pairs to desired positions.
//
// Per 012 FR-007: url-test groups carry five additional fields after
// proxies in the order url, interval, timeout, max-failed-times, lazy.
// Per 014 FR-006: load-balance groups carry six fields after proxies in
// the order url, interval, lazy, strategy, timeout, max-failed-times — note
// `lazy` and `strategy` come earlier than the url-test order. The two layouts
// share the leading triple (name, type, proxies) but diverge after, so we
// branch on the value of the `type` field. moveFieldToPosition is a no-op
// when the key is absent, so non-url-test / non-load-balance groups (e.g.,
// the always-present Proxies selector) only get the leading triple ordered.
func reorderProxyGroupFields(n *yaml.Node) {
	if n.Kind != yaml.MappingNode || len(n.Content) < 2 {
		return
	}
	moveFieldToPosition(n, "name", 0)
	moveFieldToPosition(n, "type", 2)
	moveFieldToPosition(n, "proxies", 4)

	// Branch on the type value to apply the right tail ordering. The type
	// scalar lives at Content[3] after the moves above (key at index 2,
	// value at index 3). For groups without these tail keys, both branches
	// are no-ops on missing keys.
	typeValue := ""
	if len(n.Content) > 3 && n.Content[3].Kind == yaml.ScalarNode {
		typeValue = n.Content[3].Value
	}
	switch typeValue {
	case "load-balance":
		// 014 FR-006: name, type, proxies, url, interval, lazy, strategy,
		// timeout, max-failed-times.
		moveFieldToPosition(n, "url", 6)
		moveFieldToPosition(n, "interval", 8)
		moveFieldToPosition(n, "lazy", 10)
		moveFieldToPosition(n, "strategy", 12)
		moveFieldToPosition(n, "timeout", 14)
		moveFieldToPosition(n, "max-failed-times", 16)
	default:
		// 012 FR-007: name, type, proxies, url, interval, timeout,
		// max-failed-times, lazy. Default branch covers url-test and any
		// custom group with these keys; the no-op-on-missing semantic keeps
		// it safe for the Proxies selector.
		moveFieldToPosition(n, "url", 6)
		moveFieldToPosition(n, "interval", 8)
		moveFieldToPosition(n, "timeout", 10)
		moveFieldToPosition(n, "max-failed-times", 12)
		moveFieldToPosition(n, "lazy", 14)
	}
}

// moveFieldToPosition finds a key and swaps its key-value pair to target position.
func moveFieldToPosition(n *yaml.Node, key string, targetPos int) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if i == targetPos {
				return // already in place
			}
			// Swap pair (key at i, value at i+1) with (key at targetPos, value at targetPos+1)
			// But targetPos+1 might not exist if target is beyond current length
			if targetPos+1 >= len(n.Content) {
				return // can't swap to position beyond array
			}
			// Save target pair
			targetKey := n.Content[targetPos]
			targetVal := n.Content[targetPos+1]
			// Place key-value at target
			n.Content[targetPos] = n.Content[i]
			n.Content[targetPos+1] = n.Content[i+1]
			// Place saved target pair at original position
			n.Content[i] = targetKey
			n.Content[i+1] = targetVal
			return
		}
	}
}

// --- yaml.Node helpers (private to this package) ---

func docRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

func setMappingValue(n *yaml.Node, key string, value *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}

func sequenceOfNodes(items []*yaml.Node) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	seq.Content = append(seq.Content, items...)
	return seq
}

func sequenceOfStrings(items []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, s := range items {
		seq.Content = append(seq.Content, &yaml.Node{
			Kind: yaml.ScalarNode, Value: s, Tag: "!!str",
		})
	}
	return seq
}

// resetScalarStyles walks the tree and clears the Style field on every
// scalar node, letting the emitter pick the most natural style for the
// content. Other Kind values (mapping, sequence) keep their Style so block
// vs. flow rendering stays consistent with the template.
func resetScalarStyles(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode {
		n.Style = 0
	}
	for _, c := range n.Content {
		resetScalarStyles(c)
	}
}

// stripComments recursively clears every Head/Line/Foot comment from a node
// tree. The merged proxies/groups inherit comments from upstream YAML
// payloads (some providers embed sales pitches as comments in their config);
// served output should not echo those.
func stripComments(n *yaml.Node) {
	if n == nil {
		return
	}
	n.HeadComment = ""
	n.LineComment = ""
	n.FootComment = ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

// normalizeRulesPriorityComments attaches one head comment per priority bucket
// to the rules sequence in the served YAML. Comment format:
//
//	# --- priority N (contributor-a, contributor-b, ...) ---
//
// Boundary detection: priorities[i] != priorities[i-1] (or i == 0). The
// MATCH fallback (priority 0, contributor "") gets no head comment.
//
// priorities and contributors are parallel slices, both equal in length to
// the rules sequence. The function tolerates length mismatch by walking up
// to min(len(priorities), len(seq.Content)).
func normalizeRulesPriorityComments(root *yaml.Node, priorities []int, contributors []string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "rules" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind != yaml.SequenceNode || len(seq.Content) == 0 {
			return
		}
		n := len(seq.Content)
		if len(priorities) < n {
			n = len(priorities)
		}
		if len(contributors) < n {
			n = len(contributors)
		}

		// Walk forward, detecting bucket boundaries. At each boundary, peek
		// ahead to collect every contributor name belonging to this bucket.
		idx := 0
		for idx < n {
			if priorities[idx] == 0 {
				// MATCH fallback (or any priority-0 entry): no header.
				idx++
				continue
			}
			bucketEnd := idx + 1
			for bucketEnd < n && priorities[bucketEnd] == priorities[idx] {
				bucketEnd++
			}
			// Collect unique contributor names in this bucket, alphabetical.
			seen := make(map[string]bool, bucketEnd-idx)
			names := make([]string, 0, bucketEnd-idx)
			for k := idx; k < bucketEnd; k++ {
				if c := contributors[k]; c != "" && !seen[c] {
					seen[c] = true
					names = append(names, c)
				}
			}
			sort.Strings(names)
			seq.Content[idx].HeadComment = fmt.Sprintf("# --- priority %d (%s) ---",
				priorities[idx], strings.Join(names, ", "))
			idx = bucketEnd
		}
		return
	}
}

// unescapeSupplementaryPlane walks the encoded YAML bytes and replaces
// "\Uxxxxxxxx" escape sequences (inside double-quoted strings only) with
// the literal UTF-8 bytes of the corresponding code point. yaml.v3's
// emitter generates these escapes for any code point above U+FFFF
// regardless of node Style; this helper makes the served bytes readable
// while preserving valid YAML.
//
// Outside double-quoted strings: every byte passes through unchanged.
// Inside double-quoted strings: backslash escape sequences are recognized.
// Only `\Uxxxxxxxx` (capital U + exactly 8 hex digits) is rewritten;
// every other escape (`\xhh`, `\n`, `\\`, `\"`, etc.) passes through.
//
// The helper assumes its input is well-formed YAML produced by yaml.v3;
// malformed input (e.g., a `\U` inside double-quoted with fewer than 8 hex
// digits) is left unchanged and not rewritten.
func unescapeSupplementaryPlane(body []byte) []byte {
	out := make([]byte, 0, len(body))
	inDQ := false
	for i := 0; i < len(body); i++ {
		b := body[i]
		if !inDQ {
			out = append(out, b)
			if b == '"' {
				inDQ = true
			}
			continue
		}
		// inDQ == true
		if b == '\\' && i+1 < len(body) {
			next := body[i+1]
			if next == 'U' && i+9 < len(body) && allHex(body[i+2:i+10]) {
				codepoint, err := strconv.ParseInt(string(body[i+2:i+10]), 16, 32)
				if err == nil {
					var buf [4]byte
					n := utf8.EncodeRune(buf[:], rune(codepoint))
					out = append(out, buf[:n]...)
					i += 9 // consumed: \, U, 8 hex digits
					continue
				}
			}
			// Any other escape (\\, \", \n, \t, \xhh, \uXXXX, ...) — copy
			// the backslash and the next byte verbatim. The next byte is
			// not eligible to start its own escape (we've already consumed
			// the pair).
			out = append(out, b, next)
			i++
			continue
		}
		out = append(out, b)
		if b == '"' {
			inDQ = false
		}
	}
	return out
}

// allHex reports whether every byte in p is an ASCII hex digit (0-9, a-f, A-F).
func allHex(p []byte) bool {
	for _, b := range p {
		switch {
		case b >= '0' && b <= '9':
		case b >= 'a' && b <= 'f':
		case b >= 'A' && b <= 'F':
		default:
			return false
		}
	}
	return true
}
