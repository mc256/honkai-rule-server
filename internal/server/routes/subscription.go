// Package routes contains the HTTP handlers for the served endpoints.
package routes

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/mc256/honkai-rule-server/internal/auth"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
	"github.com/mc256/honkai-rule-server/internal/output"
)

// servedTrafficLogFields returns the (allowance, expire) values for log
// emission, encoded as -1 when ServedTrafficHeader is nil. The sentinel
// distinguishes "header omitted" (-1) from "header carried zero" (0) per
// 010 FR-008.
func servedTrafficLogFields(h *merge.ServedTrafficHeader) (int64, int64) {
	if h == nil {
		return -1, -1
	}
	return h.DailyAllowanceBytes, h.ExpireUnix
}

// servedSpendLogFields returns the 011 FR-011 spend-tracking values for
// log emission. -1 sentinels for nil header (matches the 010 pattern).
// snapDate is empty string when no header / no snapshot yet.
func servedSpendLogFields(h *merge.ServedTrafficHeader) (used, total int64, snapDate string, rollover bool) {
	if h == nil {
		return -1, -1, "", false
	}
	return h.UsedTodayBytes, h.TotalBytes, h.SnapshotLocalDate, h.RolloverFired
}

// SubscriptionDeps bundles everything Subscription needs.
type SubscriptionDeps struct {
	Coordinator *fetcher.Coordinator
	Pipeline    *merge.Pipeline
	Adapter     *output.SubscriptionMode
	Logger      *slog.Logger
}

// Subscription returns an http.HandlerFunc for GET /. Per
// contracts/served-subscription.openapi.yaml:
//   - 200 + Clash YAML body when bootstrap is complete and ALL enabled
//     sources succeeded their first fetch (or recovered);
//   - 503 + JSON `{"error":"warming_up"}` while bootstrap is in progress;
//   - 503 + JSON `{"error":"bootstrap_failed", "failingSources":[...]}`
//     when bootstrap completed but at least one enabled source had no
//     usable cache to serve.
//
// The auth middleware (server.RequireToken) gates this handler — by the
// time it runs, the request has been authorized.
func Subscription(deps SubscriptionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Bootstrap gate.
		select {
		case <-deps.Coordinator.Ready():
			// fall through
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"warming_up"}`)
			return
		}
		if !deps.Coordinator.AllSucceeded() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"bootstrap_failed"}`)
			return
		}

		merged, err := deps.Pipeline.Build()
		if err != nil {
			deps.Logger.Error("pipeline build failed", "err", err.Error())
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		rendered, err := deps.Adapter.Render(merged)
		if err != nil {
			deps.Logger.Error("output render failed", "err", err.Error())
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Copy adapter's headers, then write status + body.
		for k, vs := range rendered.Headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(rendered.Body); err != nil {
			deps.Logger.Warn("write body failed", "err", err.Error())
			return
		}

		// Per FR-013: log every served-subscription request with the inputs
		// that contributed to the response. Token hash comes from auth context.
		// 010 FR-008: include the served daily-allowance figure and expire
		// value so operators can correlate header bytes with logs.
		// 011 FR-011: also include the spend-tracking fields so operators
		// can debug today's-spend display + see when rollover fires.
		allowance, expireUnix := servedTrafficLogFields(merged.ServedTrafficHeader)
		usedToday, totalBytes, snapDate, rolloverFired := servedSpendLogFields(merged.ServedTrafficHeader)
		fields := []any{
			"path", r.URL.Path,
			"contributingSources", merged.ContributingSources,
			"proxies", len(merged.Proxies),
			"groups", len(merged.ProxyGroups),
			"rules", len(merged.Rules),
			"bytes", len(rendered.Body),
			"served_daily_allowance_bytes", allowance,
			"served_expire_unix", expireUnix,
			"served_used_today_bytes", usedToday,
			"served_total_bytes", totalBytes,
			"snapshot_local_date", snapDate,
			"rollover_fired", rolloverFired,
		}
		if rec, ok := auth.TokenFromContext(r.Context()); ok {
			fields = append(fields, "token_label", rec.Label)
		}
		deps.Logger.Info("served subscription", fields...)

		// 010 FR-008: at debug verbosity, emit the per-component breakdown
		// of the served daily allowance so operators can debug a header
		// value that looks wrong without inspecting upstream payloads.
		if deps.Logger.Enabled(r.Context(), slog.LevelDebug) {
			da := deps.Pipeline.ComputeDailyAllowance()
			deps.Logger.LogAttrs(context.Background(), slog.LevelDebug,
				"served daily allowance breakdown",
				slog.Int64("per_day_rate_bytes", da.PerDayRateBytes),
				slog.Int64("no_expiry_remaining_bytes", da.NoExpiryRemainingBytes),
				slog.Any("expired_source_flags", da.ExpiredSourceFlags),
			)
		}
	}
}
