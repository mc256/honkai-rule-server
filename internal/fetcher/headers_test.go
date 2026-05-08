package fetcher

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

// TC-U-HDR-01: real alpha-example header value parses to all four integer fields.
func TestHDR_01_RealAlphaExample(t *testing.T) {
	const raw = "upload=23398198706; download=203036431271; total=654791671808; expire=1804180937"
	want := &SubscriptionUserinfo{
		Upload:   23398198706,
		Download: 203036431271,
		Total:    654791671808,
		Expire:   1804180937,
	}
	got, missing, err := ParseSubscriptionUserinfo(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want []", missing)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TC-U-HDR-02: missing field returns parsed fields + missing flag, no error.
func TestHDR_02_MissingExpireField(t *testing.T) {
	const raw = "upload=100; download=200; total=300"
	got, missing, err := ParseSubscriptionUserinfo(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Expire != 0 {
		t.Errorf("got.Expire = %d, want 0 (default for missing field)", got.Expire)
	}
	sort.Strings(missing)
	if !reflect.DeepEqual(missing, []string{"expire"}) {
		t.Errorf("missing = %v, want [expire]", missing)
	}
}

// TC-U-HDR-03: expire=0 parses to 0 and is preserved as the no-expiry sentinel.
func TestHDR_03_ExpireZeroIsNoExpiry(t *testing.T) {
	const raw = "upload=10; download=20; total=100; expire=0"
	got, missing, err := ParseSubscriptionUserinfo(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want [] (expire=0 is present, just sentinel)", missing)
	}
	if got.Expire != 0 {
		t.Errorf("got.Expire = %d, want 0", got.Expire)
	}
}

// TC-U-HDR-04: unparseable string returns nil + error.
func TestHDR_04_Unparseable(t *testing.T) {
	cases := []string{
		"",
		"upload=notanumber; download=1; total=2; expire=3",
		"random text",
	}
	for _, raw := range cases {
		got, _, err := ParseSubscriptionUserinfo(raw)
		if err == nil {
			t.Errorf("ParseSubscriptionUserinfo(%q) = %+v, nil; want error", raw, got)
			continue
		}
		if !errors.Is(err, ErrSubscriptionUserinfoUnparseable) {
			t.Errorf("ParseSubscriptionUserinfo(%q) error = %v, want errors.Is(ErrSubscriptionUserinfoUnparseable)", raw, err)
		}
		if got != nil {
			t.Errorf("ParseSubscriptionUserinfo(%q) = %+v, want nil on error", raw, got)
		}
	}
}

// TC-U-HDR-05: Profile-Update-Interval parses integer hours.
func TestHDR_05_ProfileUpdateIntervalInteger(t *testing.T) {
	got, ok := ParseProfileUpdateInterval("12")
	if !ok || got != 12 {
		t.Errorf("ParseProfileUpdateInterval(\"12\") = (%d, %v), want (12, true)", got, ok)
	}
}

// TC-U-HDR-06: missing/empty Profile-Update-Interval returns ok=false.
func TestHDR_06_ProfileUpdateIntervalMissing(t *testing.T) {
	for _, in := range []string{"", "   ", "not-a-number", "-5"} {
		if got, ok := ParseProfileUpdateInterval(in); ok {
			t.Errorf("ParseProfileUpdateInterval(%q) = (%d, true), want ok=false", in, got)
		}
	}
}

// Bonus: case-insensitive field names + unknown fields tolerated.
func TestParseSubscriptionUserinfo_CaseInsensitiveAndUnknownFields(t *testing.T) {
	const raw = "UPLOAD=10; Download=20; total=100; expire=0; reset=86400"
	got, missing, err := ParseSubscriptionUserinfo(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want []", missing)
	}
	if got.Upload != 10 || got.Download != 20 || got.Total != 100 || got.Expire != 0 {
		t.Errorf("got %+v, want {10, 20, 100, 0}", got)
	}
}
