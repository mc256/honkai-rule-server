// Package dailyspend tracks today's spend across upstream subscription
// providers so the served Subscription-Userinfo header's usage bar fills
// as the user consumes traffic during the local day (011 FR-001..FR-011).
//
// State persists across pod restarts in one JSON file on the existing PVC
// (default /data/today-zero.json). The midnight rollover is lazy
// (request-driven): the first served request after the local-day boundary
// captures fresh baselines + a fresh allowance, persists the snapshot,
// and continues serving.
//
// This package is deliberately self-contained — no imports from other
// internal/ packages except internal/fetcher (for the SubscriptionUserinfo
// shape). Keeps the dependency graph clean and the merge package free of
// file I/O.
package dailyspend

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sync"
	"time"

	"github.com/mc256/honkai-rule-server/internal/fetcher"
)

// Snapshot is the persistent record of "where the upstream counters were
// at today's local midnight" plus "what today's pinned allowance is" plus
// "the upload/download ratio per source at midnight" per 011 FR-005/FR-006.
type Snapshot struct {
	LocalDate            string             `json:"snapshot_local_date"`
	SnapshotUnix         int64              `json:"snapshot_unix"`
	AllowanceTodayBytes  int64              `json:"allowance_today_bytes"`
	Baselines            map[string]int64   `json:"baselines"`
	BaselineUploadRatios map[string]float64 `json:"baseline_upload_ratios"`
}

// FileSnapshotter persists Snapshot JSON to a file via atomic rename.
type FileSnapshotter struct {
	Path string
}

// NewFileSnapshotter constructs a FileSnapshotter targeting path.
func NewFileSnapshotter(path string) *FileSnapshotter {
	return &FileSnapshotter{Path: path}
}

// Load reads the snapshot file. Returns (nil, nil) on missing-or-corrupt
// (corruption logged INFO per 011 FR-007 — recoverable degradation, not
// loud-fail). Returns (nil, err) only on real I/O failure.
func (f *FileSnapshotter) Load() (*Snapshot, error) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("dailyspend: read %s: %w", f.Path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		slog.Info("dailyspend snapshot file corrupted — reinitializing",
			"path", f.Path, "err", err.Error())
		return nil, nil
	}
	if !validateSnapshot(&s) {
		slog.Info("dailyspend snapshot file failed validation — reinitializing",
			"path", f.Path)
		return nil, nil
	}
	return &s, nil
}

// Save atomically writes the snapshot via .tmp + fsync + rename. POSIX
// guarantees the rename is atomic on the same filesystem.
func (f *FileSnapshotter) Save(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("dailyspend: Save: nil snapshot")
	}
	tmp := f.Path + ".tmp"
	tmpFile, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("dailyspend: open %s: %w", tmp, err)
	}
	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("dailyspend: encode: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("dailyspend: fsync %s: %w", tmp, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("dailyspend: close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("dailyspend: rename %s -> %s: %w", tmp, f.Path, err)
	}
	return nil
}

func validateSnapshot(s *Snapshot) bool {
	if s == nil {
		return false
	}
	if _, err := time.Parse("2006-01-02", s.LocalDate); err != nil {
		return false
	}
	if s.AllowanceTodayBytes < 0 {
		return false
	}
	for _, r := range s.BaselineUploadRatios {
		if r < 0.0 || r > 1.0 || math.IsNaN(r) {
			return false
		}
	}
	return true
}

// MapSnapshotter is the test-friendly in-memory implementation. Save and
// Load deep-copy to avoid aliasing through the interface.
type MapSnapshotter struct {
	mu      sync.Mutex
	current *Snapshot
}

// NewMapSnapshotter constructs a MapSnapshotter with optional initial state.
func NewMapSnapshotter(initial *Snapshot) *MapSnapshotter {
	m := &MapSnapshotter{}
	if initial != nil {
		_ = m.Save(initial)
	}
	return m
}

// Load returns a deep copy of the held snapshot, or (nil, nil) when none.
func (m *MapSnapshotter) Load() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil, nil
	}
	return deepCopySnapshot(m.current), nil
}

// Save deep-copies and stores s.
func (m *MapSnapshotter) Save(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("dailyspend: MapSnapshotter.Save: nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = deepCopySnapshot(s)
	return nil
}

// Current returns the held snapshot (deep copy) for test assertions.
func (m *MapSnapshotter) Current() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	return deepCopySnapshot(m.current)
}

func deepCopySnapshot(s *Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	out := &Snapshot{
		LocalDate:            s.LocalDate,
		SnapshotUnix:         s.SnapshotUnix,
		AllowanceTodayBytes:  s.AllowanceTodayBytes,
		Baselines:            make(map[string]int64, len(s.Baselines)),
		BaselineUploadRatios: make(map[string]float64, len(s.BaselineUploadRatios)),
	}
	for k, v := range s.Baselines {
		out.Baselines[k] = v
	}
	for k, v := range s.BaselineUploadRatios {
		out.BaselineUploadRatios[k] = v
	}
	return out
}

// IsRolloverNeeded reports whether Pipeline.Build should capture a fresh
// snapshot. True when the snapshot is nil (first run / corruption recovery)
// or when its LocalDate doesn't match today in the budget timezone.
func IsRolloverNeeded(s *Snapshot, now time.Time, loc *time.Location) bool {
	if s == nil {
		return true
	}
	if loc == nil {
		loc = time.UTC
	}
	return s.LocalDate != now.In(loc).Format("2006-01-02")
}

// ComputeUsedToday sums per-source clamped deltas:
//
//	used_today = Σ_i max(0, perSource[i].(upload+download) - snapshot.Baselines[i])
//
// Sources missing from snapshot.Baselines (newly appeared since midnight)
// contribute 0. Per 011 FR-001 / FR-008.
func ComputeUsedToday(perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) int64 {
	if s == nil {
		return 0
	}
	var total int64
	for name, ui := range perSource {
		baseline, ok := s.Baselines[name]
		if !ok {
			continue
		}
		current := ui.Upload + ui.Download
		if delta := current - baseline; delta > 0 {
			total += delta
		}
	}
	return total
}

// SplitUsedToday computes the weighted-average upload split per 011 FR-002.
func SplitUsedToday(usedToday int64, perSource map[string]fetcher.SubscriptionUserinfo, s *Snapshot) (upload, download int64) {
	if usedToday <= 0 || s == nil {
		return 0, 0
	}
	var weightedSum float64
	var totalDelta int64
	for name, ui := range perSource {
		baseline, ok := s.Baselines[name]
		if !ok {
			continue
		}
		current := ui.Upload + ui.Download
		delta := current - baseline
		if delta <= 0 {
			continue
		}
		ratio := s.BaselineUploadRatios[name]
		weightedSum += float64(delta) * ratio
		totalDelta += delta
	}
	if totalDelta == 0 {
		return 0, usedToday
	}
	avgRatio := weightedSum / float64(totalDelta)
	upload = int64(math.Round(float64(usedToday) * avgRatio))
	if upload < 0 {
		upload = 0
	}
	if upload > usedToday {
		upload = usedToday
	}
	download = usedToday - upload
	return upload, download
}

// CaptureSnapshot builds a fresh Snapshot from current state at the
// rollover instant. BaselineUploadRatios per source: ratio = upload /
// (upload+download) when denominator > 0; 0.0 when 0 (R3: avoids
// divide-by-zero, attributes brand-new sources entirely to download).
func CaptureSnapshot(perSource map[string]fetcher.SubscriptionUserinfo, allowance int64, now time.Time, loc *time.Location) *Snapshot {
	if loc == nil {
		loc = time.UTC
	}
	s := &Snapshot{
		LocalDate:            now.In(loc).Format("2006-01-02"),
		SnapshotUnix:         now.Unix(),
		AllowanceTodayBytes:  allowance,
		Baselines:            make(map[string]int64, len(perSource)),
		BaselineUploadRatios: make(map[string]float64, len(perSource)),
	}
	for name, ui := range perSource {
		s.Baselines[name] = ui.Upload + ui.Download
		denom := ui.Upload + ui.Download
		if denom > 0 {
			s.BaselineUploadRatios[name] = float64(ui.Upload) / float64(denom)
		} else {
			s.BaselineUploadRatios[name] = 0.0
		}
	}
	return s
}
