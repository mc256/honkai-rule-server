# Specification Quality Checklist: Subscription Aggregator

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-04-29
**Last Updated**: 2026-04-30
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain — FR-018 (delivery mode), FR-019 (auth), FR-020 (own-proxy routing role) all resolved 2026-04-30
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded (CSV rules, override mode, chained-exit topology explicitly out of scope; Assumptions enumerates deferrals)
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows (4 user stories: aggregate, own-proxies, traffic+daily-allowance, stock-Clash-server behavior)
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Resolution Log

- **2026-04-30 — FR-018**: subscription mode only for v1; override deferred. Transform/output-adapter split required to honor Principle I.
- **2026-04-30 — FR-019**: per-client tokens in URL query parameter; revocable per device; tokens hashed in logs.
- **2026-04-30 — FR-020**: own-proxies merged as standalone selectable proxies (no chaining). Chained-exit topology deferred.
- **2026-04-30 — Added FR-005a**: per-source upstream-rule priority (CSS `z-index`-style merge order).
- **2026-04-30 — Added FR-005b**: capture upstream `Subscription-Userinfo` and `Profile-Update-Interval` headers alongside cached payload.
- **2026-04-30 — Added FR-011a + FR-011b**: emit aggregated `Profile-Update-Interval` and compute derived daily-allowance figure.
- **2026-04-30 — Added FR-019a/b**: token issuance/revocation semantics; drop-in Clash-subscription-server behavior.
- **2026-04-30 — Added US4**: explicit "behave like a stock Clash subscription server" story to make the response-shape contract testable on its own.
- **2026-04-30 — Concrete header formats baked in (FR-005b, FR-010, FR-011, FR-011a)**: real upstream sample (upstream.example.com) supplied. `Subscription-Userinfo` wire format is `upload=<bytes>; download=<bytes>; total=<bytes>; expire=<unix_seconds>` (semicolon-space separated). `Profile-Update-Interval` unit is **integer hours**. `expire=0` is the "no expiry" sentinel and MUST NOT be misinterpreted as "expired in 1970".
- **2026-04-30 — FR-011b daily-allowance branches**: split into three cases (`expire>0` future / `expire>0` past / `expire==0`) so the derived metric never reports negative or `Infinity` and never silently drops the no-expiry case.
- **2026-04-30 — Other Clash headers** (`Profile-Web-Page-Url`, `Content-Disposition`, `Cache-Control`, `Content-Type`): explicitly NOT in MVP scope; deferred to a follow-up. Documented in Assumptions to keep planning focused.
- **2026-04-30 — Subscriptions CSV is the v1 config surface (FR-001 / FR-001a / FR-001b)**: required columns `name`, `url`, `rule_priority`; optional columns `enabled` (default `true`), `ttl_seconds`, `stale_on_error_seconds`. CSV is secret-bearing per updated FR-016 (distinct from the rules CSV in Constitution Principle III, which is reviewable/publishable).
- **2026-04-30 — Gap-fill from "anything missing?" review**: 8 items raised, 6 baked into the spec, 2 deferred:
  - (1) HTTPS at K8s Ingress, server runs plain HTTP behind it → **FR-019c**.
  - (2) Proxy-group merging by name + customizable geo merging deferred → **FR-008a**, **FR-009a** (always-present `Proxies` select group).
  - (3) Server-side served-config template for Clash globals → **FR-005c** + draft template at `templates/served-config.template.yaml` in this spec dir.
  - (4) Independent per-source background fetch schedule + cold-start fail-closed → **FR-003a**, **FR-003b**, edge case for bootstrap-window failure.
  - (5) Own-proxies YAML format with `proxies` and `proxy-groups` keys → **FR-006** rewritten.
  - (6) Subscriptions CSV `enabled` column → folded into FR-001a / FR-001b.
  - (7) Daily allowance per-source weighted sum + separate no-expiry-remaining + expired-source flags → **FR-011b** rewritten, US3 acceptance scenarios 4/5/6 rewritten, entity rewrite, **SC-015**.
  - (8) Rate limiting → out of MVP scope, handled by nginx cache layer in K8s; documented in Assumptions.
- **2026-04-30 — Success criteria additions**: SC-012 (cache absorbs traffic / no upstream amplification), SC-013 (`enabled=false` row excluded), SC-014 (proxy-group same-name union merge), SC-015 (per-source weighted daily allowance).
- **2026-04-30 (rev 2 of plan/research/data-model/quickstart) — Implementation language switched from TypeScript/Node to Go 1.23** at user request. **No spec FR / SC changed** (all language-agnostic). Updated artifacts: plan.md (Technical Context, Project Structure, Test Plan tooling), research.md (R1–R18 redone for Go: stdlib `net/http` + `log/slog`, `gopkg.in/yaml.v3` with `yaml.Node`, `encoding/csv`, `cupaloy/v2` snapshots, `fsnotify`, `golang.org/x/sync/singleflight`; added R17 Clock interface, R18 build/CI tooling), data-model.md (TypeScript types → Go structs across all entities; added `Clock` interface package), quickstart.md (npm → go commands, multi-stage `FROM scratch` Dockerfile). Unchanged: spec.md, contracts/*.openapi.yaml, templates/served-config.template.yaml, this checklist's Content/Requirements/Readiness sections. Module path placeholder: `github.com/<owner>/honkai-rule-server` — change at repo init via `go mod edit -module`.

## Notes

- Spec is ready for `/speckit-tasks`. `/speckit-clarify` is optional at this point — the resolved decisions cover the scope-shaping questions; the language switch did not introduce any new ambiguity in the spec.
