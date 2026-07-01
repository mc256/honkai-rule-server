package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// integration testdata fixture (committed; the example/ dir is gitignored)
const fixturesSubscriptionsCSV = "../integration/testdata/fixtures/subscriptions.csv"

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "subs.csv")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TC-U-CSV-01: loads the subscriptions fixture (two rows, integer priorities, Enable).
func TestCSV_01_LoadsFixture(t *testing.T) {
	rows, err := LoadSubscriptions(fixturesSubscriptionsCSV)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	want := []SubscriptionRow{
		{Name: "alpha", Link: "http://upstream-alpha.test/sub?token=fake-alpha-token", Priority: 1000, Enable: true},
		{Name: "beta", Link: "http://upstream-berry.test/sub/fake-berry-token", Priority: 2000, Enable: true},
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

// TC-U-CSV-02: missing file → *ConfigLoadError naming the path.
func TestCSV_02_FileMissing(t *testing.T) {
	_, err := LoadSubscriptions("/no/such/path/subs.csv")
	if err == nil {
		t.Fatal("expected error")
	}
	var loadErr *ConfigLoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("error type = %T, want *ConfigLoadError", err)
	}
	if !strings.Contains(err.Error(), "/no/such/path/subs.csv") {
		t.Errorf("error %q does not name the missing path", err.Error())
	}
}

// TC-U-CSV-03: missing required column → *ConfigSchemaError listing it.
func TestCSV_03_MissingColumnHeader(t *testing.T) {
	p := writeCSV(t, "link,priority,enable\nx,http://x.test,1,Enable\n")
	_, err := LoadSubscriptions(p)
	var schemaErr *ConfigSchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("error type = %T, want *ConfigSchemaError", err)
	}
	found := false
	for _, m := range schemaErr.Missing {
		if m == "name" {
			found = true
		}
	}
	if !found {
		t.Errorf("Missing = %v, want includes \"name\"", schemaErr.Missing)
	}
}

// TC-U-CSV-04: duplicate name → *ConfigValidationError on the second row.
func TestCSV_04_DuplicateName(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nfoo,http://a.test,1,Enable\nfoo,http://b.test,2,Enable\n")
	_, err := LoadSubscriptions(p)
	var vErr *ConfigValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error type = %T, want *ConfigValidationError", err)
	}
	if vErr.Field != "name" {
		t.Errorf("Field = %q, want \"name\"", vErr.Field)
	}
	if vErr.Row != 2 {
		t.Errorf("Row = %d, want 2", vErr.Row)
	}
}

// TC-U-CSV-05: non-integer priority → *ConfigValidationError on the priority field.
func TestCSV_05_NonIntegerPriority(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nfoo,http://a.test,high,Enable\n")
	_, err := LoadSubscriptions(p)
	var vErr *ConfigValidationError
	if !errors.As(err, &vErr) || vErr.Field != "priority" {
		t.Fatalf("error = %v (Field=%q), want validation error on priority", err, vErr.Field)
	}
}

// TC-U-CSV-06: enable value outside Enable/Disable → error.
func TestCSV_06_InvalidEnableValue(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nfoo,http://a.test,1,yes\n")
	_, err := LoadSubscriptions(p)
	var vErr *ConfigValidationError
	if !errors.As(err, &vErr) || vErr.Field != "enable" {
		t.Fatalf("error = %v (Field=%q), want validation error on enable", err, vErr.Field)
	}
}

// TC-U-CSV-07: unknown column → *ConfigSchemaError listing it.
func TestCSV_07_UnknownColumn(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable,foo\nx,http://a.test,1,Enable,bar\n")
	_, err := LoadSubscriptions(p)
	var schemaErr *ConfigSchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("error type = %T, want *ConfigSchemaError", err)
	}
	found := false
	for _, u := range schemaErr.Unknown {
		if u == "foo" {
			found = true
		}
	}
	if !found {
		t.Errorf("Unknown = %v, want includes \"foo\"", schemaErr.Unknown)
	}
}

// TC-U-CSV-08: Disable row is loaded with Enable=false (case-insensitive).
func TestCSV_08_DisableRowLoaded(t *testing.T) {
	for _, val := range []string{"Disable", "disable", "DISABLE"} {
		p := writeCSV(t, "name,link,priority,enable\nfoo,http://a.test,1,"+val+"\n")
		rows, err := LoadSubscriptions(p)
		if err != nil {
			t.Fatalf("LoadSubscriptions(%q): %v", val, err)
		}
		if len(rows) != 1 || rows[0].Enable != false {
			t.Errorf("enable=%q got %+v, want one row with Enable=false", val, rows)
		}
	}
}

// TC-U-CSV-09: invalid link URL → error on the link field.
func TestCSV_09_InvalidLinkURL(t *testing.T) {
	cases := []string{
		"not a url",
		"ftp://wrong.scheme.test",
		"://missing-scheme",
		"http://",
	}
	for _, link := range cases {
		p := writeCSV(t, "name,link,priority,enable\nfoo,"+link+",1,Enable\n")
		_, err := LoadSubscriptions(p)
		if err == nil {
			t.Errorf("link %q did not error", link)
			continue
		}
		var vErr *ConfigValidationError
		if !errors.As(err, &vErr) || vErr.Field != "link" {
			t.Errorf("link %q error = %v (Field=%q), want validation on link", link, err, vErr.Field)
		}
	}
}

// TC-U-CSV-10: optional refresh and stale_on_error_seconds parse; absent → 0.
// refresh is tri-state: 0/absent → default interval, >0 → interval seconds,
// <0 → never refresh.
func TestCSV_10_OptionalColumns(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable,refresh,stale_on_error_seconds\nfoo,http://a.test,1,Enable,300,7200\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if rows[0].RefreshSeconds != 300 || rows[0].StaleOnErrorSeconds != 7200 {
		t.Errorf("got refresh=%d, stale=%d, want 300/7200", rows[0].RefreshSeconds, rows[0].StaleOnErrorSeconds)
	}

	p2 := writeCSV(t, "name,link,priority,enable\nfoo,http://a.test,1,Enable\n")
	rows2, err := LoadSubscriptions(p2)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if rows2[0].RefreshSeconds != 0 || rows2[0].StaleOnErrorSeconds != 0 {
		t.Errorf("got refresh=%d, stale=%d, want 0/0", rows2[0].RefreshSeconds, rows2[0].StaleOnErrorSeconds)
	}

	// refresh=0 → use default interval (accepted, stored as 0).
	p3 := writeCSV(t, "name,link,priority,enable,refresh\nfoo,http://a.test,1,Enable,0\n")
	rows3, err := LoadSubscriptions(p3)
	if err != nil {
		t.Fatalf("refresh=0 should be accepted (means default): %v", err)
	}
	if rows3[0].RefreshSeconds != 0 {
		t.Errorf("refresh=0 → RefreshSeconds=%d, want 0", rows3[0].RefreshSeconds)
	}

	// refresh<0 → never refresh (accepted, stored as the negative value).
	p4 := writeCSV(t, "name,link,priority,enable,refresh\nfoo,http://a.test,1,Enable,-1\n")
	rows4, err := LoadSubscriptions(p4)
	if err != nil {
		t.Fatalf("refresh=-1 should be accepted (means never refresh): %v", err)
	}
	if rows4[0].RefreshSeconds != -1 {
		t.Errorf("refresh=-1 → RefreshSeconds=%d, want -1", rows4[0].RefreshSeconds)
	}

	// Non-integer refresh → loud validation error.
	p5 := writeCSV(t, "name,link,priority,enable,refresh\nfoo,http://a.test,1,Enable,abc\n")
	_, err = LoadSubscriptions(p5)
	var vErr *ConfigValidationError
	if !errors.As(err, &vErr) || vErr.Field != "refresh" {
		t.Errorf("refresh=abc error = %v (Field=%q), want validation on refresh", err, vErr.Field)
	}
}

// TC-U-CSV-NAME-01: lowercase-only name passes FR-001 ^[a-z]+$ validation.
func TestCSV_NAME_01_LowercasePasses(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nalpha,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "alpha" {
		t.Errorf("got %+v, want one row with name=alpha", rows)
	}
}

// TC-U-CSV-NAME-02: name with underscore is warn-skipped (FR-002 soft skip).
func TestCSV_NAME_02_UnderscoreWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nbad_name,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}

// TC-U-CSV-NAME-03: name with uppercase + digit is warn-skipped.
func TestCSV_NAME_03_UppercaseDigitWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nAlpha2024,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}

// TC-U-CSV-NAME-04: empty name is warn-skipped.
func TestCSV_NAME_04_EmptyNameWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\n,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}

// TC-U-CSV-NAME-05: mixed CSV with one valid + one violating row → only valid returned.
func TestCSV_NAME_05_MixedValidity(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nalpha,http://a.test,1,Enable\nBad_Name,http://b.test,2,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error)", err)
	}
	if len(rows) != 1 || rows[0].Name != "alpha" {
		t.Errorf("got %+v, want one row with name=alpha", rows)
	}
}

// TC-U-CSV-NAME-06: violation does NOT raise *ConfigValidationError (soft skip path).
func TestCSV_NAME_06_NoValidationError(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nbad_name,http://a.test,1,Enable\n")
	_, err := LoadSubscriptions(p)
	if err != nil {
		t.Errorf("got error %v (%T), want nil (soft skip, not loud fail)", err, err)
	}
}

// TC-U-CSV-NAME-07: duplicate name still raises loud failure AFTER name-format soft-skip.
func TestCSV_NAME_07_DuplicateAfterSoftSkip(t *testing.T) {
	// Two rows that pass FR-001 with the same name → duplicate loud-fail.
	p := writeCSV(t, "name,link,priority,enable\nalpha,http://a.test,1,Enable\nalpha,http://b.test,2,Enable\n")
	_, err := LoadSubscriptions(p)
	var vErr *ConfigValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error type = %T, want *ConfigValidationError", err)
	}
	if vErr.Field != "name" {
		t.Errorf("Field = %q, want \"name\"", vErr.Field)
	}
}

// TC-U-CSV-NAME-08: name with digit is warn-skipped.
func TestCSV_NAME_08_DigitWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nprovider1,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}

// TC-U-CSV-NAME-09: name with hyphen is warn-skipped.
func TestCSV_NAME_09_HyphenWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\nmy-provider,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}

// TC-U-CSV-NAME-10: non-ASCII name is warn-skipped.
func TestCSV_NAME_10_NonASCIIWarnSkipped(t *testing.T) {
	p := writeCSV(t, "name,link,priority,enable\n测试源,http://a.test,1,Enable\n")
	rows, err := LoadSubscriptions(p)
	if err != nil {
		t.Fatalf("LoadSubscriptions: %v (want no error, just skip)", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (violating row skipped)", len(rows))
	}
}
