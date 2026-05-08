package merge

import (
	"math"
	"sort"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/dailyspend"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

// ServedTrafficHeader carries the values written into the public
// Subscription-Userinfo response header.
//
// 010 fields: DailyAllowanceBytes + ExpireUnix.
// 011 additions: UsedTodayBytes / TotalBytes / UploadBytes / DownloadBytes
// for the spend-tracking encoding, plus SnapshotLocalDate / RolloverFired
// for FR-011 structured-log emission.
//
// nil = no source contributed userinfo and the header is omitted entirely
// (010 FR-006). Non-nil with DailyAllowanceBytes == 0 is a real "every
// source is expired or fully consumed" state (010 FR-007).
type ServedTrafficHeader struct {
	DailyAllowanceBytes int64  // 010 FR-001 (also = TotalBytes when no spend tracking)
	ExpireUnix          int64  // 010 FR-002 (next 00:00 UTC) / 011 FR-002 (next 00:00 in budget timezone)
	UsedTodayBytes      int64  // 011 FR-001 — = UploadBytes + DownloadBytes
	TotalBytes          int64  // 011 FR-002 — = DailyAllowanceBytes + UsedTodayBytes (allowance + spend, stable through the day)
	UploadBytes         int64  // 011 FR-002 — usedToday × weighted-avg upload ratio
	DownloadBytes       int64  // 011 FR-002 — usedToday − UploadBytes
	SnapshotLocalDate   string // 011 FR-011 — date the snapshot reflects
	RolloverFired       bool   // 011 FR-011 — true when this request triggered the lazy rollover
}

// NextMidnightUTC returns the next 00:00 UTC strictly after now (010 FR-002).
// Strictly-after: a call exactly at 00:00:00 UTC returns the FOLLOWING day's
// midnight, never the current instant. Inputs in non-UTC timezones are
// normalized to UTC before computing the day boundary.
func NextMidnightUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
}

// ComposeServedTrafficHeader returns the value embedded in the public
// Subscription-Userinfo response header (010 FR-001/FR-002), or nil when no
// source contributed userinfo (010 FR-006). Backward-compatible thin
// wrapper around ComposeServedTrafficHeaderWithSpend that disables 011's
// spend tracking (no snapshot → UploadBytes/DownloadBytes/UsedTodayBytes
// stay zero, TotalBytes equals DailyAllowanceBytes, ExpireUnix uses UTC).
func ComposeServedTrafficHeader(
	perSource map[string]fetcher.SubscriptionUserinfo,
	clk clock.Clock,
) *ServedTrafficHeader {
	h, _ := ComposeServedTrafficHeaderWithSpend(perSource, clk, nil, time.UTC)
	return h
}

// ComposeServedTrafficHeaderWithSpend is the spend-aware variant per 011
// FR-001/FR-002/FR-003. Returns the served header value AND the (possibly
// new) snapshot. When the input snapshot is nil OR IsRolloverNeeded(...),
// captures a fresh snapshot via dailyspend.CaptureSnapshot and returns it
// as the second value (caller MUST persist via Snapshotter.Save). When the
// input snapshot is current, the second return is the same pointer.
//
// Returns (nil, nil) when no source contributed userinfo (010 FR-006).
//
// Pure: no I/O. Determinism per 011 FR-013.
func ComposeServedTrafficHeaderWithSpend(
	perSource map[string]fetcher.SubscriptionUserinfo,
	clk clock.Clock,
	snapshot *dailyspend.Snapshot,
	loc *time.Location,
) (*ServedTrafficHeader, *dailyspend.Snapshot) {
	if len(perSource) == 0 {
		return nil, snapshot
	}
	if loc == nil {
		loc = time.UTC
	}
	da := ComputeDailyAllowance(perSource, clk)
	allowance := da.PerDayRateBytes + da.NoExpiryRemainingBytes

	// Lazy rollover (011 FR-004): when the snapshot is stale or missing,
	// capture fresh baselines from current state and pin today's allowance.
	rolloverFired := false
	now := clk.Now()
	if dailyspend.IsRolloverNeeded(snapshot, now, loc) {
		snapshot = dailyspend.CaptureSnapshot(perSource, allowance, now, loc)
		rolloverFired = true
	}

	used := dailyspend.ComputeUsedToday(perSource, snapshot)
	upload, download := dailyspend.SplitUsedToday(used, perSource, snapshot)

	header := &ServedTrafficHeader{
		DailyAllowanceBytes: snapshot.AllowanceTodayBytes,
		ExpireUnix:          nextMidnightInLocation(now, loc).Unix(),
		UsedTodayBytes:      used,
		TotalBytes:          snapshot.AllowanceTodayBytes + used,
		UploadBytes:         upload,
		DownloadBytes:       download,
		SnapshotLocalDate:   snapshot.LocalDate,
		RolloverFired:       rolloverFired,
	}
	return header, snapshot
}

// nextMidnightInLocation returns the next 00:00 strictly after now, in the
// given location. DST-aware: spring-forward day yields a 23-hour next
// midnight, fall-back yields 25 hours.
func nextMidnightInLocation(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
}

// DailyAllowance is the derived figure exposed by /health. It splits into
// three components per FR-011b so the operator can distinguish "spendable
// at a daily rate against an upcoming expiry" from "freely spendable from
// no-expiry plans" and from "stuck quota on an expired source".
type DailyAllowance struct {
	PerDayRateBytes        int64    `json:"perDayRateBytes"`
	NoExpiryRemainingBytes int64    `json:"noExpiryRemainingBytes"`
	ExpiredSourceFlags     []string `json:"expiredSourceFlags"`
}

// AggregateSubscriptionUserinfo sums upload, download, and total across
// every source whose userinfo is present, and picks the earliest non-zero
// expire (0 only when every source reports expire=0). Per FR-011.
//
// Sources whose userinfo is absent do not contribute (FR-012). For
// determinism, iteration over the input map is in sorted key order.
func AggregateSubscriptionUserinfo(perSource map[string]fetcher.SubscriptionUserinfo) fetcher.SubscriptionUserinfo {
	out := fetcher.SubscriptionUserinfo{}
	names := sortedKeys(perSource)
	for _, name := range names {
		ui := perSource[name]
		out.Upload += ui.Upload
		out.Download += ui.Download
		out.Total += ui.Total
		if ui.Expire == 0 {
			continue
		}
		if out.Expire == 0 || ui.Expire < out.Expire {
			out.Expire = ui.Expire
		}
	}
	return out
}

// AggregateProfileUpdateInterval returns the minimum non-zero interval
// (hours) across sources, falling back to defaultHours when no source
// supplies a positive value. Per FR-011a.
//
// The map value is *int so the absence of an upstream header (nil) and the
// upstream emitting `0` (which Clash treats as "unspecified") are
// distinguishable from a real value like 12.
func AggregateProfileUpdateInterval(perSource map[string]*int, defaultHours int) int {
	minHours := -1
	for _, name := range sortedStringKeys(perSource) {
		hours := perSource[name]
		if hours == nil || *hours <= 0 {
			continue
		}
		if minHours == -1 || *hours < minHours {
			minHours = *hours
		}
	}
	if minHours == -1 {
		return defaultHours
	}
	return minHours
}

// ComputeDailyAllowance computes the three-component daily allowance per
// FR-011b. The function is pure — no I/O, no time.Now(); the caller injects
// the clock.
//
// - PerDayRateBytes: sum over sources where expire_i > now of
//   max(0, remaining_i) / max(1, ceil((expire_i - now) / 86400 seconds)).
// - NoExpiryRemainingBytes: sum of remaining bytes for sources with
//   expire_i == 0 (no-expiry plans; not subject to a per-day rate).
// - ExpiredSourceFlags: alphabetically-sorted names of sources whose
//   expire_i > 0 is in the past — operator should renew or remove them.
func ComputeDailyAllowance(
	perSource map[string]fetcher.SubscriptionUserinfo,
	clk clock.Clock,
) DailyAllowance {
	out := DailyAllowance{
		ExpiredSourceFlags: []string{},
	}
	now := clk.Now().Unix()
	for _, name := range sortedKeys(perSource) {
		ui := perSource[name]
		remaining := ui.Total - ui.Upload - ui.Download
		if remaining < 0 {
			remaining = 0
		}
		switch {
		case ui.Expire == 0:
			out.NoExpiryRemainingBytes += remaining
		case ui.Expire <= now:
			out.ExpiredSourceFlags = append(out.ExpiredSourceFlags, name)
		default:
			secondsRemaining := ui.Expire - now
			daysRemaining := int64(math.Ceil(float64(secondsRemaining) / 86400.0))
			if daysRemaining < 1 {
				daysRemaining = 1
			}
			out.PerDayRateBytes += remaining / daysRemaining
		}
	}
	sort.Strings(out.ExpiredSourceFlags)
	return out
}

func sortedKeys(m map[string]fetcher.SubscriptionUserinfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStringKeys(m map[string]*int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

