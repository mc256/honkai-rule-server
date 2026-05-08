package integration

import (
	"fmt"
	"testing"
	"time"
)

// TC-I-13: per-source weighted daily allowance.
// alpha:      total=200GB / used=50GB / expire=now+30d → remaining=150GB / 30 = 5GB/day
// beta: total=100GB / used=20GB / expire=now+5d  → remaining=80GB  /  5 = 16GB/day
// /health.dailyAllowance.perDayRateBytes ≈ 21GB (within rounding).
func TestI_13_DailyAllowancePerSourceWeighted(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		"alpha":      fmt.Sprintf("upload=10737418240; download=42949672960; total=214748364800; expire=%d", now.Add(30*24*time.Hour).Unix()),
		"beta": fmt.Sprintf("upload=5368709120; download=16106127360; total=107374182400; expire=%d", now.Add(5*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	_, got := getHealth(t, tc)
	const wantPerDay = 21 * (1 << 30) // 21 GB
	delta := got.DailyAllowance.PerDayRateBytes - wantPerDay
	if delta > 1<<20 || delta < -(1<<20) {
		t.Errorf("PerDayRateBytes = %d (≈ %d GB), want ~21GB ±1MB",
			got.DailyAllowance.PerDayRateBytes,
			got.DailyAllowance.PerDayRateBytes/(1<<30))
	}
	if got.DailyAllowance.NoExpiryRemainingBytes != 0 {
		t.Errorf("NoExpiryRemainingBytes = %d, want 0", got.DailyAllowance.NoExpiryRemainingBytes)
	}
	if len(got.DailyAllowance.ExpiredSourceFlags) != 0 {
		t.Errorf("ExpiredSourceFlags = %v, want []", got.DailyAllowance.ExpiredSourceFlags)
	}
}

// TC-I-14: every upstream reports expire=0 → per-day rate 0; no-expiry sum.
func TestI_14_AllNoExpiry(t *testing.T) {
	opts := defaultOpts()
	opts.perSourceUserinfo = map[string]string{
		"alpha":      "upload=10; download=20; total=200; expire=0",
		"beta": "upload=5; download=15; total=100; expire=0",
	}
	tc := newTestClusterWithOpts(t, opts)

	_, got := getHealth(t, tc)
	if got.DailyAllowance.PerDayRateBytes != 0 {
		t.Errorf("PerDayRateBytes = %d, want 0", got.DailyAllowance.PerDayRateBytes)
	}
	want := int64((200-10-20) + (100-5-15))
	if got.DailyAllowance.NoExpiryRemainingBytes != want {
		t.Errorf("NoExpiryRemainingBytes = %d, want %d",
			got.DailyAllowance.NoExpiryRemainingBytes, want)
	}
}

// TC-I-15: source with expire = now - 1d shows in ExpiredSourceFlags;
// contributes 0 to the per-day rate.
func TestI_15_ExpiredSourceFlagged(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	opts := defaultOpts()
	opts.clockNow = now
	opts.perSourceUserinfo = map[string]string{
		"alpha":      fmt.Sprintf("upload=10; download=20; total=200; expire=%d", now.Add(-24*time.Hour).Unix()),
		"beta": fmt.Sprintf("upload=5; download=15; total=100; expire=%d", now.Add(10*24*time.Hour).Unix()),
	}
	tc := newTestClusterWithOpts(t, opts)

	_, got := getHealth(t, tc)
	if len(got.DailyAllowance.ExpiredSourceFlags) != 1 || got.DailyAllowance.ExpiredSourceFlags[0] != "alpha" {
		t.Errorf("ExpiredSourceFlags = %v, want [alpha]", got.DailyAllowance.ExpiredSourceFlags)
	}
	// beta remaining: 80; days: 10 → 8 bytes/day. alpha contributes 0.
	if got.DailyAllowance.PerDayRateBytes != 8 {
		t.Errorf("PerDayRateBytes = %d, want 8 (only beta contributes)",
			got.DailyAllowance.PerDayRateBytes)
	}
}
