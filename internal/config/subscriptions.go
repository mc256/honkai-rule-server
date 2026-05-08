package config

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// nameFormatRe validates the FR-001 ^[a-z]+$ rule on CSV name values.
// Compiled once at package init for performance.
var nameFormatRe = regexp.MustCompile(`^[a-z]+$`)

// SubscriptionRow is one parsed and validated row of the subscriptions CSV.
// See spec FR-001a / FR-001b.
type SubscriptionRow struct {
	Name                string
	Link                string
	Priority            int
	Enable              bool
	TTLSeconds          int // 0 means "use ServerConfig.DefaultTTLSeconds"
	StaleOnErrorSeconds int // 0 means "use ServerConfig.DefaultStaleOnErrorSeconds"
}

// requiredCols / optionalCols define the subscriptions CSV schema.
// Unknown columns are rejected so a typo (e.g., "enabld") cannot become a
// missing override (Constitution Principle III: loud failure).
var (
	requiredCols = []string{"name", "link", "priority", "enable"}
	optionalCols = []string{"ttl_seconds", "stale_on_error_seconds"}
)

// ConfigLoadError wraps an OS-level error opening the file.
type ConfigLoadError struct {
	Path string
	Err  error
}

func (e *ConfigLoadError) Error() string {
	return fmt.Sprintf("config: load %s: %v", e.Path, e.Err)
}
func (e *ConfigLoadError) Unwrap() error { return e.Err }

// ConfigSchemaError reports header-row problems: missing required columns or
// unknown columns the parser will not silently skip.
type ConfigSchemaError struct {
	Missing []string
	Unknown []string
}

func (e *ConfigSchemaError) Error() string {
	parts := []string{"config: subscriptions CSV schema error"}
	if len(e.Missing) > 0 {
		parts = append(parts, "missing required columns: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Unknown) > 0 {
		parts = append(parts, "unknown columns: "+strings.Join(e.Unknown, ", "))
	}
	return strings.Join(parts, "; ")
}

// ConfigValidationError reports a per-row, per-field validation failure.
type ConfigValidationError struct {
	Row    int // 1-indexed; the header row is row 0
	Field  string
	Reason string
}

func (e *ConfigValidationError) Error() string {
	return fmt.Sprintf("config: subscriptions CSV row %d field %q: %s", e.Row, e.Field, e.Reason)
}

// LoadSubscriptions opens path, parses it as CSV with the schema in FR-001a,
// and returns the validated rows. All schema and validation errors are loud.
func LoadSubscriptions(path string) ([]SubscriptionRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &ConfigLoadError{Path: path, Err: err}
	}
	defer f.Close()
	return parseSubscriptions(f)
}

func parseSubscriptions(r io.Reader) ([]SubscriptionRow, error) {
	rdr := csv.NewReader(r)
	rdr.TrimLeadingSpace = true
	// Allow rows with fewer fields than the header so an absent optional
	// column doesn't trip the parser; we'll read by column index.
	rdr.FieldsPerRecord = -1

	header, err := rdr.Read()
	if err != nil {
		return nil, fmt.Errorf("config: read CSV header: %w", err)
	}

	colIdx := make(map[string]int, len(header))
	validSet := make(map[string]bool, len(requiredCols)+len(optionalCols))
	for _, c := range requiredCols {
		validSet[c] = true
	}
	for _, c := range optionalCols {
		validSet[c] = true
	}

	var unknown []string
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		if !validSet[key] {
			unknown = append(unknown, key)
			continue
		}
		colIdx[key] = i
	}

	var missing []string
	for _, req := range requiredCols {
		if _, ok := colIdx[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 || len(unknown) > 0 {
		return nil, &ConfigSchemaError{Missing: missing, Unknown: unknown}
	}

	var rows []SubscriptionRow
	seen := make(map[string]int) // name -> row number where it first appeared
	rowNum := 0

	for {
		rec, err := rdr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("config: read CSV row %d: %w", rowNum+1, err)
		}
		rowNum++

		// FR-001: name-format validation BEFORE duplicate detection.
		// Violations are soft-skip (warn + exclude row, continue).
		nameVal := ""
		if idx, ok := colIdx["name"]; ok && idx < len(rec) {
			nameVal = strings.TrimSpace(rec[idx])
		}
		if nameVal == "" || !nameFormatRe.MatchString(nameVal) {
			slog.Warn("name-format-violation",
				"event", "name-format-violation",
				"name", nameVal,
				"row", rowNum,
			)
			continue // skip this row, continue with others
		}

		row, err := parseRow(rec, colIdx, rowNum)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[row.Name]; dup {
			return nil, &ConfigValidationError{
				Row: rowNum, Field: "name",
				Reason: fmt.Sprintf("duplicate of row %d", prev),
			}
		}
		seen[row.Name] = rowNum
		rows = append(rows, row)
	}

	return rows, nil
}

func parseRow(rec []string, colIdx map[string]int, rowNum int) (SubscriptionRow, error) {
	var row SubscriptionRow

	get := func(col string) (string, bool) {
		idx, ok := colIdx[col]
		if !ok || idx >= len(rec) {
			return "", false
		}
		return strings.TrimSpace(rec[idx]), true
	}

	if v, _ := get("name"); v != "" {
		row.Name = v
	} else {
		return row, &ConfigValidationError{Row: rowNum, Field: "name", Reason: "empty"}
	}

	link, _ := get("link")
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return row, &ConfigValidationError{
			Row: rowNum, Field: "link",
			Reason: fmt.Sprintf("invalid URL %q (must be http:// or https:// with a host)", link),
		}
	}
	row.Link = link

	priorityStr, _ := get("priority")
	p, err := strconv.Atoi(priorityStr)
	if err != nil {
		return row, &ConfigValidationError{
			Row: rowNum, Field: "priority",
			Reason: fmt.Sprintf("not an integer: %q", priorityStr),
		}
	}
	row.Priority = p

	enableStr, _ := get("enable")
	switch {
	case strings.EqualFold(enableStr, "Enable"):
		row.Enable = true
	case strings.EqualFold(enableStr, "Disable"):
		row.Enable = false
	default:
		return row, &ConfigValidationError{
			Row: rowNum, Field: "enable",
			Reason: fmt.Sprintf("must be Enable or Disable (case-insensitive), got %q", enableStr),
		}
	}

	if v, ok := get("ttl_seconds"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return row, &ConfigValidationError{
				Row: rowNum, Field: "ttl_seconds",
				Reason: fmt.Sprintf("not a positive integer: %q", v),
			}
		}
		row.TTLSeconds = n
	}

	if v, ok := get("stale_on_error_seconds"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return row, &ConfigValidationError{
				Row: rowNum, Field: "stale_on_error_seconds",
				Reason: fmt.Sprintf("not a positive integer: %q", v),
			}
		}
		row.StaleOnErrorSeconds = n
	}

	return row, nil
}
