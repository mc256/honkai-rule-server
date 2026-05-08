package dailyspend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

// helpers --------------------------------------------------------------

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func sampleSnapshot() *Snapshot {
	return &Snapshot{
		LocalDate:           "2026-05-02",
		SnapshotUnix:        1746158400,
		AllowanceTodayBytes: 22548578304,
		Baselines: map[string]int64{
			"alpha":     242977323837,
			"beta": 15025246189,
		},
		BaselineUploadRatios: map[string]float64{
			"alpha":     0.10,
			"beta": 0.12,
		},
	}
}

// T002: Snapshot file ops --------------------------------------------------

func TestSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "today-zero.json")
	fs := NewFileSnapshotter(path)
	in := sampleSnapshot()
	if err := fs.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := fs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in: %+v\nout: %+v", in, out)
	}
}

func TestSnapshot_AtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "today-zero.json")
	fs := NewFileSnapshotter(path)
	original := sampleSnapshot()
	if err := fs.Save(original); err != nil {
		t.Fatalf("Save (initial): %v", err)
	}
	// Simulate a partial write: write the .tmp file but never rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("PARTIAL_GARBAGE"), 0o644); err != nil {
		t.Fatalf("write .tmp: %v", err)
	}
	// Original file must still parse correctly.
	out, err := fs.Load()
	if err != nil {
		t.Fatalf("Load after partial: %v", err)
	}
	if out == nil || out.LocalDate != original.LocalDate {
		t.Errorf("Load returned wrong content; got %+v want %+v", out, original)
	}
}

func TestSnapshot_LoadMissing(t *testing.T) {
	fs := NewFileSnapshotter(filepath.Join(t.TempDir(), "absent.json"))
	out, err := fs.Load()
	if err != nil {
		t.Fatalf("Load (missing) returned err: %v; want (nil, nil)", err)
	}
	if out != nil {
		t.Errorf("Load (missing) returned non-nil snapshot: %+v", out)
	}
}

func TestSnapshot_LoadCorrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "today-zero.json")
	if err := os.WriteFile(path, []byte("{ NOT JSON"), 0o644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}
	fs := NewFileSnapshotter(path)
	out, err := fs.Load()
	if err != nil {
		t.Errorf("Load (corrupted) returned err %v; want (nil, nil) per FR-007", err)
	}
	if out != nil {
		t.Errorf("Load (corrupted) returned non-nil snapshot: %+v", out)
	}
}

func TestSnapshot_LoadInvalidLocalDate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "today-zero.json")
	bad := *sampleSnapshot()
	bad.LocalDate = "not-a-date"
	b, _ := json.Marshal(&bad)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	fs := NewFileSnapshotter(path)
	out, err := fs.Load()
	if err != nil {
		t.Errorf("Load (invalid date) returned err %v; want (nil, nil) per FR-007", err)
	}
	if out != nil {
		t.Errorf("Load (invalid date) returned non-nil snapshot: %+v", out)
	}
}

// T003: rollover predicate -----------------------------------------------

func TestIsRolloverNeeded_NilSnapshot(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	if !IsRolloverNeeded(nil, time.Now(), loc) {
		t.Error("IsRolloverNeeded(nil, ...) = false; want true")
	}
}

func TestIsRolloverNeeded_SameDay(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	now := time.Date(2026, 5, 2, 14, 30, 0, 0, loc)
	s := &Snapshot{LocalDate: "2026-05-02"}
	if IsRolloverNeeded(s, now, loc) {
		t.Error("IsRolloverNeeded(same day) = true; want false")
	}
}

func TestIsRolloverNeeded_NextDay(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	s := &Snapshot{LocalDate: "2026-05-02"}
	now := time.Date(2026, 5, 3, 0, 0, 1, 0, loc)
	if !IsRolloverNeeded(s, now, loc) {
		t.Error("IsRolloverNeeded(next day) = false; want true")
	}
}

func TestIsRolloverNeeded_DSTSpringForward(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	// 2026 spring-forward: 2026-03-08 02:00 EST → 03:00 EDT (one 23-hour day)
	s := &Snapshot{LocalDate: "2026-03-07"}
	beforeMidnight := time.Date(2026, 3, 7, 23, 0, 0, 0, loc)
	if IsRolloverNeeded(s, beforeMidnight, loc) {
		t.Errorf("rollover at 2026-03-07 23:00 EST = true; want false (still same local day)")
	}
	afterMidnight := time.Date(2026, 3, 8, 0, 30, 0, 0, loc)
	if !IsRolloverNeeded(s, afterMidnight, loc) {
		t.Errorf("rollover at 2026-03-08 00:30 EST = false; want true (new local day)")
	}
}

func TestIsRolloverNeeded_DSTFallBack(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	// 2026 fall-back: 2026-11-01 02:00 EDT → 01:00 EST (one 25-hour day)
	s := &Snapshot{LocalDate: "2026-11-01"}
	beforeMidnight := time.Date(2026, 11, 1, 23, 0, 0, 0, loc)
	if IsRolloverNeeded(s, beforeMidnight, loc) {
		t.Errorf("rollover at 2026-11-01 23:00 = true; want false")
	}
	afterMidnight := time.Date(2026, 11, 2, 0, 30, 0, 0, loc)
	if !IsRolloverNeeded(s, afterMidnight, loc) {
		t.Errorf("rollover at 2026-11-02 00:30 = false; want true")
	}
}

func TestIsRolloverNeeded_YearBoundary(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	s := &Snapshot{LocalDate: "2026-12-31"}
	now := time.Date(2027, 1, 1, 0, 0, 1, 0, loc)
	if !IsRolloverNeeded(s, now, loc) {
		t.Error("rollover at 2027-01-01 00:00 = false; want true")
	}
}

// T004: ComputeUsedToday -------------------------------------------------

func TestComputeUsedToday_TwoSources(t *testing.T) {
	s := &Snapshot{
		Baselines: map[string]int64{
			"a": 100,
			"b": 200,
		},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 60, Download: 70}, // 130 - 100 = 30
		"b": {Upload: 50, Download: 200}, // 250 - 200 = 50
	}
	got := ComputeUsedToday(per, s)
	if got != 80 {
		t.Errorf("ComputeUsedToday = %d; want 80 (30 + 50)", got)
	}
}

func TestComputeUsedToday_NewSourceNoBaseline(t *testing.T) {
	s := &Snapshot{
		Baselines: map[string]int64{"a": 100},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a":   {Upload: 60, Download: 50}, // 110 - 100 = 10
		"new": {Upload: 50, Download: 50}, // no baseline → contributes 0
	}
	got := ComputeUsedToday(per, s)
	if got != 10 {
		t.Errorf("ComputeUsedToday = %d; want 10 (only `a` contributes)", got)
	}
}

func TestComputeUsedToday_ProviderReset(t *testing.T) {
	s := &Snapshot{
		Baselines: map[string]int64{"a": 1000},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 5, Download: 10}, // 15 < 1000 → clamped to 0
	}
	got := ComputeUsedToday(per, s)
	if got != 0 {
		t.Errorf("ComputeUsedToday = %d; want 0 (provider reset, clamped)", got)
	}
}

// T005: SplitUsedToday ---------------------------------------------------

func TestSplitUsedToday_AllUpload(t *testing.T) {
	s := &Snapshot{
		Baselines:            map[string]int64{"a": 0},
		BaselineUploadRatios: map[string]float64{"a": 1.0},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 100, Download: 0},
	}
	up, down := SplitUsedToday(100, per, s)
	if up != 100 || down != 0 {
		t.Errorf("SplitUsedToday(ratio=1.0) = (%d,%d); want (100,0)", up, down)
	}
}

func TestSplitUsedToday_AllDownload(t *testing.T) {
	s := &Snapshot{
		Baselines:            map[string]int64{"a": 0},
		BaselineUploadRatios: map[string]float64{"a": 0.0},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 0, Download: 100},
	}
	up, down := SplitUsedToday(100, per, s)
	if up != 0 || down != 100 {
		t.Errorf("SplitUsedToday(ratio=0.0) = (%d,%d); want (0,100)", up, down)
	}
}

func TestSplitUsedToday_Mixed(t *testing.T) {
	// Two sources, equal contribution; ratios 0.0 and 0.5.
	// Weighted-average ratio = (50*0.0 + 50*0.5) / 100 = 0.25.
	// upload = round(100 * 0.25) = 25; download = 75.
	s := &Snapshot{
		Baselines:            map[string]int64{"a": 0, "b": 0},
		BaselineUploadRatios: map[string]float64{"a": 0.0, "b": 0.5},
	}
	per := map[string]fetcher.SubscriptionUserinfo{
		"a": {Upload: 0, Download: 50},
		"b": {Upload: 25, Download: 25},
	}
	up, down := SplitUsedToday(100, per, s)
	if up+down != 100 {
		t.Errorf("up+down = %d; want 100", up+down)
	}
	if up < 24 || up > 26 {
		t.Errorf("up = %d; want ~25 (±1)", up)
	}
}

// T006: CaptureSnapshot --------------------------------------------------

func TestCaptureSnapshot_TwoSourcesFR011b(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, loc)
	per := map[string]fetcher.SubscriptionUserinfo{
		"alpha":     {Upload: 10, Download: 90, Total: 1000, Expire: now.Add(30 * 24 * time.Hour).Unix()},
		"beta": {Upload: 12, Download: 88, Total: 500, Expire: now.Add(5 * 24 * time.Hour).Unix()},
	}
	s := CaptureSnapshot(per, 21*(1<<30), now, loc)
	if s.LocalDate != "2026-05-02" {
		t.Errorf("LocalDate = %q; want 2026-05-02", s.LocalDate)
	}
	if s.AllowanceTodayBytes != 21*(1<<30) {
		t.Errorf("AllowanceTodayBytes = %d; want %d", s.AllowanceTodayBytes, int64(21*(1<<30)))
	}
	if s.Baselines["alpha"] != 100 {
		t.Errorf("Baselines[alpha] = %d; want 100 (10+90)", s.Baselines["alpha"])
	}
	if s.Baselines["beta"] != 100 {
		t.Errorf("Baselines[beta] = %d; want 100 (12+88)", s.Baselines["beta"])
	}
	if got, want := s.BaselineUploadRatios["alpha"], 0.10; got != want {
		t.Errorf("BaselineUploadRatios[alpha] = %v; want %v", got, want)
	}
	if got, want := s.BaselineUploadRatios["beta"], 0.12; got != want {
		t.Errorf("BaselineUploadRatios[beta] = %v; want %v", got, want)
	}
}

func TestCaptureSnapshot_ZeroUploadRatio(t *testing.T) {
	loc := mustLoadLocation(t, "America/Toronto")
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, loc)
	per := map[string]fetcher.SubscriptionUserinfo{
		"new": {Upload: 0, Download: 0, Total: 100, Expire: 0}, // brand-new, no consumption
	}
	s := CaptureSnapshot(per, 100, now, loc)
	if got, want := s.BaselineUploadRatios["new"], 0.0; got != want {
		t.Errorf("BaselineUploadRatios[new] = %v; want %v (R3: divide-by-zero → 0.0)", got, want)
	}
}

// T007: MapSnapshotter ---------------------------------------------------

func TestMapSnapshotter_SaveLoad(t *testing.T) {
	m := NewMapSnapshotter(nil)
	out, err := m.Load()
	if err != nil {
		t.Fatalf("Load (initial): %v", err)
	}
	if out != nil {
		t.Errorf("Load (initial) = %+v; want nil", out)
	}
	in := sampleSnapshot()
	if err := m.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err = m.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in: %+v\nout: %+v", in, out)
	}
	// Mutate the input; Load result MUST be unaffected (deep copy).
	in.LocalDate = "1999-01-01"
	out2, _ := m.Load()
	if out2.LocalDate == "1999-01-01" {
		t.Errorf("Save did not deep-copy; mutating input changed stored value")
	}
}

// silence unused-import warnings if individual tests are skipped
var _ = sync.Mutex{}
