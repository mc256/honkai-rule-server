package merge

import (
	"testing"
)

// TC-U-REGION-EMOJI-DECODE-01: decodeRegionalIndicatorPair("🇨🇳") returns ("CN", true)
func TestDecodeRegionalIndicator_CN(t *testing.T) {
	code, ok := decodeRegionalIndicatorPair("🇨🇳")
	if !ok {
		t.Fatalf("expected ok=true for 🇨🇳, got false")
	}
	if code != "CN" {
		t.Errorf("expected CN, got %q", code)
	}
}

// TC-U-REGION-EMOJI-DECODE-02: decodeRegionalIndicatorPair("🇿🇿") returns ("ZZ", true)
// Decoder validates Unicode-block range only, not ISO 3166-1 assignment.
func TestDecodeRegionalIndicator_ZZ(t *testing.T) {
	code, ok := decodeRegionalIndicatorPair("🇿🇿")
	if !ok {
		t.Fatalf("expected ok=true for 🇿🇿, got false")
	}
	if code != "ZZ" {
		t.Errorf("expected ZZ, got %q", code)
	}
}

// TC-U-REGION-EMOJI-DECODE-03: decodeRegionalIndicatorPair with single indicator returns false
func TestDecodeRegionalIndicator_SingleIndicator(t *testing.T) {
	// Single regional indicator character (🇨 = U+1F1E8)
	_, ok := decodeRegionalIndicatorPair("🇨")
	if ok {
		t.Errorf("expected ok=false for single indicator, got true")
	}
}

// TC-U-REGION-EMOJI-DECODE-04: decodeRegionalIndicatorPair finds pair in mixed string
func TestDecodeRegionalIndicator_MixedString(t *testing.T) {
	code, ok := decodeRegionalIndicatorPair("Foo 🇭🇰 Bar")
	if !ok {
		t.Fatalf("expected ok=true for string containing 🇭🇰, got false")
	}
	if code != "HK" {
		t.Errorf("expected HK, got %q", code)
	}
}

// TC-U-REGION-EMOJI-DECODE-05: no regional indicator in string returns false
func TestDecodeRegionalIndicator_NoneFound(t *testing.T) {
	_, ok := decodeRegionalIndicatorPair("Hong Kong Premium")
	if ok {
		t.Errorf("expected ok=false for string with no emoji flags, got true")
	}
}

// TC-U-REGION-EMOJI-01: display-name-substring match for 🇨🇳 returns CN
func TestRegionTable_Emoji_CN(t *testing.T) {
	code, ok := inferCountry("🇨🇳 上海 IPLC 01")
	if !ok {
		t.Fatalf("expected ok=true for 🇨🇳, got false")
	}
	if code != "CN" {
		t.Errorf("expected CN, got %q", code)
	}
}

// TC-U-REGION-EMOJI-02: display-name-substring match for 🇺🇸 returns US
func TestRegionTable_Emoji_US(t *testing.T) {
	code, ok := inferCountry("🇺🇸 Los Angeles Premium")
	if !ok {
		t.Fatalf("expected ok=true for 🇺🇸, got false")
	}
	if code != "US" {
		t.Errorf("expected US, got %q", code)
	}
}

// TC-U-REGION-EMOJI-03: display-name-substring match for 🇭🇰 returns HK
func TestRegionTable_Emoji_HK(t *testing.T) {
	code, ok := inferCountry("🇭🇰 香港 01")
	if !ok {
		t.Fatalf("expected ok=true for 🇭🇰, got false")
	}
	if code != "HK" {
		t.Errorf("expected HK, got %q", code)
	}
}

// TC-U-REGION-EMOJI-04: emoji takes precedence over Chinese name
func TestRegionTable_Emoji_Precedence(t *testing.T) {
	// 🇭🇰 should match HK even if 中国 is also present
	// (real-world unlikely, but precedence rule matters)
	code, ok := inferCountry("🇭🇰 中国专线")
	if !ok {
		t.Fatalf("expected ok=true, got false")
	}
	if code != "HK" {
		t.Errorf("expected HK (emoji precedence), got %q", code)
	}
}

// TC-U-REGION-CN-01: 中国 → CN
func TestRegionTable_CN_China(t *testing.T) {
	code, ok := inferCountry("中国 上海 01")
	if !ok {
		t.Fatalf("expected ok=true for 中国, got false")
	}
	if code != "CN" {
		t.Errorf("expected CN, got %q", code)
	}
}

// TC-U-REGION-CN-02: 美国 → US
func TestRegionTable_CN_US(t *testing.T) {
	code, ok := inferCountry("美国 Los Angeles")
	if !ok {
		t.Fatalf("expected ok=true for 美国, got false")
	}
	if code != "US" {
		t.Errorf("expected US, got %q", code)
	}
}

// TC-U-REGION-CN-03: 香港 → HK
func TestRegionTable_CN_HK(t *testing.T) {
	code, ok := inferCountry("香港 01")
	if !ok {
		t.Fatalf("expected ok=true for 香港, got false")
	}
	if code != "HK" {
		t.Errorf("expected HK, got %q", code)
	}
}

// TC-U-REGION-CN-04: 台湾 / 臺灣 → TW
func TestRegionTable_CN_TW(t *testing.T) {
	for _, name := range []string{"台湾 01", "臺灣 02"} {
		code, ok := inferCountry(name)
		if !ok {
			t.Fatalf("expected ok=true for %q, got false", name)
		}
		if code != "TW" {
			t.Errorf("for %q: expected TW, got %q", name, code)
		}
	}
}

// TC-U-REGION-EN-01: Hong Kong case-insensitive → HK
func TestRegionTable_EN_HK(t *testing.T) {
	for _, name := range []string{"Hong Kong Premium", "HONG KONG", "hong kong 01"} {
		code, ok := inferCountry(name)
		if !ok {
			t.Fatalf("expected ok=true for %q, got false", name)
		}
		if code != "HK" {
			t.Errorf("for %q: expected HK, got %q", name, code)
		}
	}
}

// TC-U-REGION-EN-02: United States → US
func TestRegionTable_EN_US(t *testing.T) {
	code, ok := inferCountry("United States West")
	if !ok {
		t.Fatalf("expected ok=true for 'United States', got false")
	}
	if code != "US" {
		t.Errorf("expected US, got %q", code)
	}
}

// TC-U-REGION-TABLE-INVARIANT-01: every entry's Code is two uppercase ASCII letters
func TestRegionTable_Invariant_CodeFormat(t *testing.T) {
	for _, e := range regionTable {
		if len(e.Code) != 2 {
			t.Errorf("Code %q length != 2", e.Code)
			continue
		}
		for i, c := range e.Code {
			if c < 'A' || c > 'Z' {
				t.Errorf("Code %q char[%d] = %c, not uppercase ASCII letter", e.Code, i, c)
			}
		}
	}
}

// TC-U-REGION-TABLE-INVARIANT-02: every entry's Lang ∈ {"zh", "en"}
func TestRegionTable_Invariant_Lang(t *testing.T) {
	validLangs := map[string]bool{"zh": true, "en": true}
	for _, e := range regionTable {
		if !validLangs[e.Lang] {
			t.Errorf("Indicator %q has Lang %q, want zh or en", e.Indicator, e.Lang)
		}
	}
}

// TC-U-REGION-TABLE-INVARIANT-03: no duplicate Indicator value
func TestRegionTable_Invariant_NoDuplicates(t *testing.T) {
	seen := make(map[string]string) // indicator → code
	for _, e := range regionTable {
		if prev, exists := seen[e.Indicator]; exists {
			t.Errorf("duplicate Indicator %q: maps to both %q and %q", e.Indicator, prev, e.Code)
		}
		seen[e.Indicator] = e.Code
	}
}

// TC-U-CONTINENT-MAP-01: every entry in countryToContinent has valid continent code
func TestContinentMap_ValidCodes(t *testing.T) {
	validContinents := map[string]bool{
		"AF": true, // Africa
		"AS": true, // Asia
		"EU": true, // Europe
		"NA": true, // North America
		"SA": true, // South America
		"OC": true, // Oceania
		"AN": true, // Antarctica
	}

	for i, e := range countryToContinent {
		if !validContinents[e.Continent] {
			t.Errorf("countryToContinent[%d]: invalid continent code %q for country %q", i, e.Continent, e.Country)
		}
	}
}

// TC-U-CONTINENT-MAP-02: all country codes from regionTable have continent mapping
func TestContinentMap_CompleteCoverage(t *testing.T) {
	// Build set of mapped countries
	mappedCountries := make(map[string]bool)
	for _, e := range countryToContinent {
		mappedCountries[e.Country] = true
	}

	// Extract unique country codes from regionTable
	regionCountries := make(map[string]bool)
	for _, e := range regionTable {
		regionCountries[e.Code] = true
	}

	// Every regionTable country must have a continent mapping
	for cc := range regionCountries {
		if !mappedCountries[cc] {
			t.Errorf("country %q from regionTable has no continent mapping", cc)
		}
	}
}

// TC-U-CONTINENT-MAP-03: country codes are unique in countryToContinent
func TestContinentMap_UniqueCountries(t *testing.T) {
	seen := make(map[string]int)
	for i, e := range countryToContinent {
		if prev, exists := seen[e.Country]; exists {
			t.Errorf("countryToContinent[%d]: duplicate country %q (first at [%d])", i, e.Country, prev)
		}
		seen[e.Country] = i
	}
}

// TC-U-CONTINENT-MAP-04: init validation panics on invalid entries
// This test is implicitly covered by the init() function - if there's an invalid
// entry, the init() will panic at package load time. We verify the table is valid
// by the fact that other tests run successfully.
