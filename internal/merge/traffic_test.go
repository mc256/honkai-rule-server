package merge

import (
	"reflect"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/clock"
	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

const GB int64 = 1 << 30

func intPtr(n int) *int { return &n }

// TC-U-TRAFFIC-01: aggregate upload/download/total + earliest non-zero expire.
func TestTRAFFIC_01_AggregateSubscriptionUserinfo(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC).Unix()
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10 * GB, Download: 40 * GB, Total: 200 * GB, Expire: now + 30*86400},
		"b": {Upload: 5 * GB, Download: 15 * GB, Total: 100 * GB, Expire: now + 5*86400},
	}
	got := AggregateSubscriptionUserinfo(per)
	want := fetcher.SubscriptionUserinfo{
		Upload:   15 * GB,
		Download: 55 * GB,
		Total:    300 * GB,
		Expire:   now + 5*86400, // earliest non-zero
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TC-U-TRAFFIC-02: per-source weighted daily allowance.
// A: total=200GB, used=50GB, expire=now+30d → remaining=150GB / 30 = 5GB/day
// B: total=100GB, used=20GB, expire=now+5d  → remaining=80GB  /  5 = 16GB/day
// Sum: 21GB/day.
func TestTRAFFIC_02_DailyAllowancePerSourceWeighted(t *testing.T) {
	t0 := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10 * GB, Download: 40 * GB, Total: 200 * GB, Expire: t0.Add(30 * 24 * time.Hour).Unix()},
		"b": {Upload: 5 * GB, Download: 15 * GB, Total: 100 * GB, Expire: t0.Add(5 * 24 * time.Hour).Unix()},
	}
	got := ComputeDailyAllowance(per, clk)
	const wantPerDay = 21 * GB
	delta := got.PerDayRateBytes - wantPerDay
	// Allow 1MB rounding tolerance (ceil() may add a partial day for non-multiples).
	if delta > 1<<20 || delta < -(1<<20) {
		t.Errorf("PerDayRateBytes = %d (≈ %d GB/day), want %d (21 GB/day) ±1MB",
			got.PerDayRateBytes, got.PerDayRateBytes/GB, wantPerDay)
	}
	if got.NoExpiryRemainingBytes != 0 {
		t.Errorf("NoExpiryRemainingBytes = %d, want 0", got.NoExpiryRemainingBytes)
	}
	if len(got.ExpiredSourceFlags) != 0 {
		t.Errorf("ExpiredSourceFlags = %v, want []", got.ExpiredSourceFlags)
	}
}

// TC-U-TRAFFIC-03: source with Expire=0 contributes to no-expiry remaining
// (not the per-day rate); aggregated expire from other sources unchanged.
func TestTRAFFIC_03_NoExpirySourceSeparated(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC))
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10, Download: 20, Total: 100, Expire: 0},          // no-expiry
		"b": {Upload: 5, Download: 15, Total: 100, Expire: clk.Now().Add(10 * 24 * time.Hour).Unix()}, // 80 / 10 = 8 / day
	}
	da := ComputeDailyAllowance(per, clk)
	if da.NoExpiryRemainingBytes != 100-10-20 {
		t.Errorf("NoExpiryRemainingBytes = %d, want 70 (a's remaining)", da.NoExpiryRemainingBytes)
	}
	if da.PerDayRateBytes != 8 {
		t.Errorf("PerDayRateBytes = %d, want 8 (b only)", da.PerDayRateBytes)
	}

	ag := AggregateSubscriptionUserinfo(per)
	if ag.Expire == 0 {
		t.Errorf("aggregated expire = 0; want b's non-zero expire (since a has expire=0)")
	}
}

// TC-U-TRAFFIC-04: source with expire < now contributes 0 to the per-day
// rate and shows up in ExpiredSourceFlags.
func TestTRAFFIC_04_ExpiredSourceFlag(t *testing.T) {
	t0 := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"already-expired": {Upload: 10, Download: 20, Total: 100, Expire: t0.Add(-24 * time.Hour).Unix()},
		"healthy":         {Upload: 5, Download: 15, Total: 100, Expire: t0.Add(10 * 24 * time.Hour).Unix()}, // 80 / 10 = 8 / day
	}
	da := ComputeDailyAllowance(per, clk)
	if !reflect.DeepEqual(da.ExpiredSourceFlags, []string{"already-expired"}) {
		t.Errorf("ExpiredSourceFlags = %v, want [already-expired]", da.ExpiredSourceFlags)
	}
	if da.PerDayRateBytes != 8 {
		t.Errorf("PerDayRateBytes = %d, want 8 (expired excluded)", da.PerDayRateBytes)
	}
}

// TC-U-TRAFFIC-05: minimum non-zero ProfileUpdateInterval wins.
func TestTRAFFIC_05_ProfileUpdateIntervalMin(t *testing.T) {
	per := map[string]*int{
		"a": intPtr(12),
		"b": intPtr(24),
	}
	if got := AggregateProfileUpdateInterval(per, 6); got != 12 {
		t.Errorf("got %d, want 12 (min of 12, 24)", got)
	}
}

// TC-U-TRAFFIC-06: every source omits the interval header → falls back to default.
func TestTRAFFIC_06_ProfileUpdateIntervalDefault(t *testing.T) {
	per := map[string]*int{
		"a": nil,
		"b": nil,
		"c": intPtr(0), // also treated as absent per Clash convention
	}
	if got := AggregateProfileUpdateInterval(per, 6); got != 6 {
		t.Errorf("got %d, want 6 (configured default)", got)
	}
}

// All sources have expire=0 → per-day rate is 0; no-expiry-remaining sums all.
// (Explicit edge from FR-011b text.)
func TestComputeDailyAllowance_AllNoExpiry(t *testing.T) {
	clk := clock.NewFakeClock(time.Now())
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10, Download: 20, Total: 100, Expire: 0},
		"b": {Upload: 5, Download: 15, Total: 100, Expire: 0},
	}
	da := ComputeDailyAllowance(per, clk)
	if da.PerDayRateBytes != 0 {
		t.Errorf("PerDayRateBytes = %d, want 0", da.PerDayRateBytes)
	}
	if da.NoExpiryRemainingBytes != (100-10-20)+(100-5-15) {
		t.Errorf("NoExpiryRemainingBytes = %d, want %d", da.NoExpiryRemainingBytes, (100-10-20)+(100-5-15))
	}
}

// remaining can't go negative — over-quota sources contribute 0.
func TestComputeDailyAllowance_RemainingNeverNegative(t *testing.T) {
	t0 := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"over-quota": {Upload: 200, Download: 100, Total: 100, Expire: t0.Add(10 * 24 * time.Hour).Unix()},
	}
	da := ComputeDailyAllowance(per, clk)
	if da.PerDayRateBytes != 0 {
		t.Errorf("over-quota source contributed %d to PerDayRateBytes; want 0",
			da.PerDayRateBytes)
	}
}

// 010 FR-002: NextMidnightUTC returns the next 00:00 UTC strictly after now.
func TestNextMidnightUTC_BeforeMidnight(t *testing.T) {
	in := time.Date(2026, 5, 1, 23, 59, 30, 0, time.UTC)
	want := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	if got := NextMidnightUTC(in); !got.Equal(want) {
		t.Errorf("NextMidnightUTC(%v) = %v, want %v", in, got, want)
	}
}

func TestNextMidnightUTC_ExactlyMidnight(t *testing.T) {
	in := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	want := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	if got := NextMidnightUTC(in); !got.Equal(want) {
		t.Errorf("NextMidnightUTC(%v) = %v, want %v (strictly after)", in, got, want)
	}
}

func TestNextMidnightUTC_MonthBoundary(t *testing.T) {
	in := time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)
	want := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if got := NextMidnightUTC(in); !got.Equal(want) {
		t.Errorf("NextMidnightUTC(%v) = %v, want %v", in, got, want)
	}
}

func TestNextMidnightUTC_YearBoundary(t *testing.T) {
	in := time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC)
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := NextMidnightUTC(in); !got.Equal(want) {
		t.Errorf("NextMidnightUTC(%v) = %v, want %v", in, got, want)
	}
}

func TestNextMidnightUTC_NonUTCInput(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("America/New_York timezone unavailable; skipping")
	}
	// 2026-05-01 20:00:00 EDT (UTC-4) = 2026-05-02 00:00:00 UTC.
	// Strictly-after rule → next midnight = 2026-05-03 00:00:00 UTC.
	in := time.Date(2026, 5, 1, 20, 0, 0, 0, loc)
	want := time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC)
	got := NextMidnightUTC(in)
	if !got.Equal(want) {
		t.Errorf("NextMidnightUTC(%v) = %v, want %v (input normalized via UTC)", in, got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("NextMidnightUTC returned location %v, want UTC", got.Location())
	}
}

// 010 FR-001/FR-006: ComposeServedTrafficHeader builds the served pair from
// per-source userinfo + clock; nil when no source contributed.
func TestComposeServedTrafficHeader_TwoSourcesFR011b(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 0, Download: 50 * GB, Total: 200 * GB, Expire: t0.Add(30 * 24 * time.Hour).Unix()},
		"b": {Upload: 0, Download: 20 * GB, Total: 100 * GB, Expire: t0.Add(5 * 24 * time.Hour).Unix()},
	}
	got := ComposeServedTrafficHeader(per, clk)
	if got == nil {
		t.Fatalf("ComposeServedTrafficHeader returned nil; want non-nil for two-source fixture")
	}
	const wantBytes = 21 * GB
	if delta := got.DailyAllowanceBytes - wantBytes; delta > (1<<20) || delta < -(1<<20) {
		t.Errorf("DailyAllowanceBytes = %d (≈ %d GB), want %d (21 GB) ±1MB",
			got.DailyAllowanceBytes, got.DailyAllowanceBytes/GB, wantBytes)
	}
	wantExpire := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC).Unix()
	if got.ExpireUnix != wantExpire {
		t.Errorf("ExpireUnix = %d, want %d (next 00:00 UTC after %v)", got.ExpireUnix, wantExpire, t0)
	}
}

func TestComposeServedTrafficHeader_NoSources(t *testing.T) {
	clk := clock.NewFakeClock(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	if got := ComposeServedTrafficHeader(map[string]fetcher.SubscriptionUserinfo{}, clk); got != nil {
		t.Errorf("ComposeServedTrafficHeader(empty) = %+v, want nil (010 FR-006)", got)
	}
}

func TestComposeServedTrafficHeader_AllExpired(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10, Download: 20, Total: 100, Expire: t0.Add(-24 * time.Hour).Unix()},
		"b": {Upload: 5, Download: 15, Total: 100, Expire: t0.Add(-48 * time.Hour).Unix()},
	}
	got := ComposeServedTrafficHeader(per, clk)
	if got == nil {
		t.Fatalf("ComposeServedTrafficHeader returned nil; want non-nil with zero DailyAllowanceBytes")
	}
	if got.DailyAllowanceBytes != 0 {
		t.Errorf("DailyAllowanceBytes = %d, want 0 (010 FR-007: all expired → 0)", got.DailyAllowanceBytes)
	}
	wantExpire := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC).Unix()
	if got.ExpireUnix != wantExpire {
		t.Errorf("ExpireUnix = %d, want %d", got.ExpireUnix, wantExpire)
	}
}

func TestComposeServedTrafficHeader_AllNoExpiry(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 10, Download: 20, Total: 100, Expire: 0},
		"b": {Upload: 5, Download: 15, Total: 100, Expire: 0},
	}
	got := ComposeServedTrafficHeader(per, clk)
	if got == nil {
		t.Fatalf("ComposeServedTrafficHeader returned nil; want non-nil")
	}
	want := int64((100 - 10 - 20) + (100 - 5 - 15)) // 70 + 80 = 150
	if got.DailyAllowanceBytes != want {
		t.Errorf("DailyAllowanceBytes = %d, want %d (no-expiry remaining sum)", got.DailyAllowanceBytes, want)
	}
	wantExpire := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC).Unix()
	if got.ExpireUnix != wantExpire {
		t.Errorf("ExpireUnix = %d, want %d (010 FR-002: tomorrow regardless of no-expiry)", got.ExpireUnix, wantExpire)
	}
}

func TestComposeServedTrafficHeader_MixedExpiringAndNoExpiry(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"forever": {Upload: 0, Download: 30, Total: 100, Expire: 0}, // no-expiry remaining = 70
		"timed":   {Upload: 0, Download: 20, Total: 100, Expire: t0.Add(10 * 24 * time.Hour).Unix()}, // 80/10 = 8/day
	}
	got := ComposeServedTrafficHeader(per, clk)
	if got == nil {
		t.Fatalf("ComposeServedTrafficHeader returned nil; want non-nil")
	}
	const want = int64(70 + 8)
	if got.DailyAllowanceBytes != want {
		t.Errorf("DailyAllowanceBytes = %d, want %d (no-expiry-remaining + per-day-rate)", got.DailyAllowanceBytes, want)
	}
}

func TestComposeServedTrafficHeader_NegativeRemainingClamped(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"over": {Upload: 200, Download: 100, Total: 100, Expire: t0.Add(10 * 24 * time.Hour).Unix()},
	}
	got := ComposeServedTrafficHeader(per, clk)
	if got == nil {
		t.Fatalf("ComposeServedTrafficHeader returned nil; want non-nil")
	}
	if got.DailyAllowanceBytes != 0 {
		t.Errorf("DailyAllowanceBytes = %d, want 0 (over-quota source clamped to 0)", got.DailyAllowanceBytes)
	}
}

func TestComposeServedTrafficHeader_DeterministicWithinDay(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 0, Download: 50 * GB, Total: 200 * GB, Expire: t0.Add(30 * 24 * time.Hour).Unix()},
	}

	clk1 := clock.NewFakeClock(t0)
	got1 := ComposeServedTrafficHeader(per, clk1)

	clk2 := clock.NewFakeClock(t0.Add(30 * time.Minute)) // 30 minutes later, same UTC day
	got2 := ComposeServedTrafficHeader(per, clk2)

	if got1 == nil || got2 == nil {
		t.Fatalf("nil result: got1=%v got2=%v", got1, got2)
	}
	if got1.DailyAllowanceBytes != got2.DailyAllowanceBytes {
		t.Errorf("DailyAllowanceBytes drifted within UTC day: %d vs %d",
			got1.DailyAllowanceBytes, got2.DailyAllowanceBytes)
	}
	if got1.ExpireUnix != got2.ExpireUnix {
		t.Errorf("ExpireUnix drifted within UTC day: %d vs %d (010 SC-006)",
			got1.ExpireUnix, got2.ExpireUnix)
	}
}

// 011 FR-001/FR-002/FR-003: ComposeServedTrafficHeaderWithSpend covers
// the eight acceptance scenarios from 011 spec.md.

const TB = int64(1) << 30 // alias to avoid clashing with file-local GB constant

func mustToronto(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

// Scenario 1: midnight first-of-day request → used ≈ 0; total ≈ allowance.
func TestComposeWithSpend_AcceptanceScenario1_FirstRequestOfDay(t *testing.T) {
	loc := mustToronto(t)
	t0 := time.Date(2026, 5, 2, 0, 0, 1, 0, loc) // first second after local midnight
	clk := clock.NewFakeClock(t0)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 0, Download: 50 * GB, Total: 200 * GB, Expire: t0.Add(30 * 24 * time.Hour).Unix()},
	}
	h, snap := ComposeServedTrafficHeaderWithSpend(per, clk, nil, loc)
	if h == nil || snap == nil {
		t.Fatalf("nil result: header=%v snap=%v", h, snap)
	}
	if h.UsedTodayBytes != 0 {
		t.Errorf("UsedTodayBytes = %d; want 0 (first request, just captured baselines)", h.UsedTodayBytes)
	}
	if h.TotalBytes != h.DailyAllowanceBytes {
		t.Errorf("TotalBytes (%d) != DailyAllowanceBytes (%d) at first request", h.TotalBytes, h.DailyAllowanceBytes)
	}
	if !h.RolloverFired {
		t.Error("RolloverFired = false; want true (first request captures snapshot)")
	}
	if h.SnapshotLocalDate != "2026-05-02" {
		t.Errorf("SnapshotLocalDate = %q; want 2026-05-02", h.SnapshotLocalDate)
	}
}

// Scenario 2: mid-day after N bytes consumed → used == ΣN, total == allowance + ΣN.
func TestComposeWithSpend_AcceptanceScenario2_MidDaySpend(t *testing.T) {
	loc := mustToronto(t)
	day := time.Date(2026, 5, 2, 0, 0, 0, 0, loc)
	noon := day.Add(12 * time.Hour)

	// First request: capture baselines.
	clk := clock.NewFakeClock(day)
	perInitial := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 200 * GB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	_, snap := ComposeServedTrafficHeaderWithSpend(perInitial, clk, nil, loc)

	// Mid-day: cumulative grew by 5_000 bytes.
	clk2 := clock.NewFakeClock(noon)
	perAfter := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900 + 5000, Total: 200 * GB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	h, snap2 := ComposeServedTrafficHeaderWithSpend(perAfter, clk2, snap, loc)
	if h == nil {
		t.Fatal("nil header")
	}
	if h.UsedTodayBytes != 5000 {
		t.Errorf("UsedTodayBytes = %d; want 5000", h.UsedTodayBytes)
	}
	if h.TotalBytes != h.DailyAllowanceBytes+5000 {
		t.Errorf("TotalBytes = %d; want DailyAllowanceBytes (%d) + 5000",
			h.TotalBytes, h.DailyAllowanceBytes)
	}
	if h.RolloverFired {
		t.Error("RolloverFired = true; want false (same local day)")
	}
	if snap2 != snap {
		t.Error("snapshot pointer changed mid-day; want same pointer (no rollover)")
	}
}

// Scenario 3: overspend → total - upload - download may go negative.
func TestComposeWithSpend_AcceptanceScenario3_Overspend(t *testing.T) {
	loc := mustToronto(t)
	day := time.Date(2026, 5, 2, 0, 0, 0, 0, loc)

	// Capture baselines.
	clk := clock.NewFakeClock(day)
	perInitial := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 1100, Expire: day.Add(2 * 24 * time.Hour).Unix()},
	}
	_, snap := ComposeServedTrafficHeaderWithSpend(perInitial, clk, nil, loc)
	allowance := snap.AllowanceTodayBytes

	// Overspend by 2x the allowance.
	clk2 := clock.NewFakeClock(day.Add(8 * time.Hour))
	perAfter := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900 + 2*allowance, Total: 1100, Expire: day.Add(2 * 24 * time.Hour).Unix()},
	}
	h, _ := ComposeServedTrafficHeaderWithSpend(perAfter, clk2, snap, loc)
	if h == nil {
		t.Fatal("nil header")
	}
	if h.UsedTodayBytes != 2*allowance {
		t.Errorf("UsedTodayBytes = %d; want %d", h.UsedTodayBytes, 2*allowance)
	}
	// total - upload - download = allowance + used - used = allowance (well-formed)
	// Operator-accepted: clients render the bar over-full if used > allowance; spec SC-005.
	remaining := h.TotalBytes - h.UploadBytes - h.DownloadBytes
	if remaining != allowance {
		t.Errorf("remaining (T-U-D) = %d; want allowance %d", remaining, allowance)
	}
}

// Scenario 4: crossing local midnight regenerates snapshot.
func TestComposeWithSpend_AcceptanceScenario4_CrossMidnight(t *testing.T) {
	loc := mustToronto(t)
	day1 := time.Date(2026, 5, 2, 23, 30, 0, 0, loc)
	day2 := time.Date(2026, 5, 3, 0, 30, 0, 0, loc)

	clk1 := clock.NewFakeClock(day1)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 100 * GB, Expire: day1.Add(30 * 24 * time.Hour).Unix()},
	}
	_, snap1 := ComposeServedTrafficHeaderWithSpend(per, clk1, nil, loc)
	if snap1.LocalDate != "2026-05-02" {
		t.Fatalf("snap1.LocalDate = %q; want 2026-05-02", snap1.LocalDate)
	}

	clk2 := clock.NewFakeClock(day2)
	h, snap2 := ComposeServedTrafficHeaderWithSpend(per, clk2, snap1, loc)
	if !h.RolloverFired {
		t.Error("RolloverFired = false; want true (crossed local midnight)")
	}
	if snap2.LocalDate != "2026-05-03" {
		t.Errorf("snap2.LocalDate = %q; want 2026-05-03", snap2.LocalDate)
	}
	if snap2 == snap1 {
		t.Error("snapshot pointer unchanged after rollover; want different")
	}
	if h.UsedTodayBytes != 0 {
		t.Errorf("UsedTodayBytes = %d; want 0 (rollover just captured baselines from current state)", h.UsedTodayBytes)
	}
}

// Scenario 5: pod restart preserves used through snapshot reload.
func TestComposeWithSpend_AcceptanceScenario5_PodRestart(t *testing.T) {
	loc := mustToronto(t)
	day := time.Date(2026, 5, 2, 0, 0, 0, 0, loc)
	noon := day.Add(12 * time.Hour)

	// Pre-restart: capture + spend.
	clk := clock.NewFakeClock(day)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 1 * TB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	_, snap := ComposeServedTrafficHeaderWithSpend(per, clk, nil, loc)

	// Pod restarts: simulate by re-loading the snapshot (same content) at noon
	// with an updated cumulative_used reflecting traffic during the restart window.
	clk2 := clock.NewFakeClock(noon)
	perAfter := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900 + 7000, Total: 1 * TB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	h, _ := ComposeServedTrafficHeaderWithSpend(perAfter, clk2, snap, loc)
	if h.UsedTodayBytes != 7000 {
		t.Errorf("UsedTodayBytes = %d; want 7000 (restart preserved baseline via snapshot)", h.UsedTodayBytes)
	}
	if h.RolloverFired {
		t.Error("RolloverFired = true; want false (same local day, not a fresh capture)")
	}
}

// Scenario 6: provider counter reset → clamped to 0.
func TestComposeWithSpend_AcceptanceScenario6_ProviderReset(t *testing.T) {
	loc := mustToronto(t)
	day := time.Date(2026, 5, 2, 0, 0, 0, 0, loc)
	clk := clock.NewFakeClock(day)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 1000, Download: 9000, Total: 100 * GB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	_, snap := ComposeServedTrafficHeaderWithSpend(per, clk, nil, loc)
	if snap.Baselines["a"] != 10000 {
		t.Fatalf("setup: baselines[a] = %d; want 10000", snap.Baselines["a"])
	}

	// Provider's monthly reset: cumulative drops to 100 (well below baseline 10000).
	clk2 := clock.NewFakeClock(day.Add(8 * time.Hour))
	perAfter := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 50, Download: 50, Total: 100 * GB, Expire: day.Add(30 * 24 * time.Hour).Unix()},
	}
	h, _ := ComposeServedTrafficHeaderWithSpend(perAfter, clk2, snap, loc)
	if h.UsedTodayBytes != 0 {
		t.Errorf("UsedTodayBytes = %d; want 0 (provider reset → clamped per FR-008)", h.UsedTodayBytes)
	}
}

// Scenario 7: DST spring-forward → exactly one rollover; expire is correct.
func TestComposeWithSpend_AcceptanceScenario7_DSTSpringForward(t *testing.T) {
	loc := mustToronto(t)
	// Day before spring-forward (2026-03-07 in EST, UTC-5).
	beforeDay := time.Date(2026, 3, 7, 12, 0, 0, 0, loc)
	clk1 := clock.NewFakeClock(beforeDay)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 100 * GB, Expire: beforeDay.Add(30 * 24 * time.Hour).Unix()},
	}
	_, snap := ComposeServedTrafficHeaderWithSpend(per, clk1, nil, loc)
	expire1 := time.Unix(snap.SnapshotUnix, 0)
	_ = expire1

	// Day after spring-forward (2026-03-08, the 23-hour day in EDT, UTC-4).
	afterDay := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	clk2 := clock.NewFakeClock(afterDay)
	h, snap2 := ComposeServedTrafficHeaderWithSpend(per, clk2, snap, loc)
	if !h.RolloverFired {
		t.Error("RolloverFired = false on first request after DST spring-forward day; want true")
	}
	if snap2.LocalDate != "2026-03-08" {
		t.Errorf("LocalDate after DST = %q; want 2026-03-08", snap2.LocalDate)
	}

	// expire should be unix(2026-03-09 00:00 EDT). DST math tested elsewhere
	// (dailyspend); here we just check non-zero and within the next 24h-ish.
	wantExpire := time.Date(2026, 3, 9, 0, 0, 0, 0, loc).Unix()
	if h.ExpireUnix != wantExpire {
		t.Errorf("ExpireUnix = %d; want %d (next 00:00 EDT after 2026-03-08 12:00)", h.ExpireUnix, wantExpire)
	}
}

// Scenario 8: first boot (no snapshot) initializes from current; used = 0.
func TestComposeWithSpend_AcceptanceScenario8_FirstBoot(t *testing.T) {
	loc := mustToronto(t)
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, loc)
	clk := clock.NewFakeClock(now)
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 900, Total: 100 * GB, Expire: now.Add(30 * 24 * time.Hour).Unix()},
	}
	h, snap := ComposeServedTrafficHeaderWithSpend(per, clk, nil, loc)
	if !h.RolloverFired {
		t.Error("first boot: RolloverFired = false; want true")
	}
	if h.UsedTodayBytes != 0 {
		t.Errorf("first boot: UsedTodayBytes = %d; want 0", h.UsedTodayBytes)
	}
	if snap.Baselines["a"] != 1000 {
		t.Errorf("first boot: baselines[a] = %d; want 1000 (current upload+download)", snap.Baselines["a"])
	}
}
