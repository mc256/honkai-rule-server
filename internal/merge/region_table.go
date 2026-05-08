package merge

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// regionEntry maps a human-readable country indicator to an ISO 3166-1 alpha-2 code.
type regionEntry struct {
	Indicator string
	Code      string
	Lang      string
}

// countryContinentEntry maps an ISO 3166-1 alpha-2 country code to a continent code.
// Continent codes: AF (Africa), AS (Asia), EU (Europe), NA (North America),
// SA (South America), OC (Oceania), AN (Antarctica).
type countryContinentEntry struct {
	Country   string
	Continent string
}

// countryToContinent maps ISO 3166-1 alpha-2 country codes to continent codes.
// Ordered slice for deterministic iteration; init() validates uniqueness.
var countryToContinent = []countryContinentEntry{
	// Asia (AS)
	{"CN", "AS"}, {"JP", "AS"}, {"KR", "AS"}, {"HK", "AS"}, {"TW", "AS"},
	{"MO", "AS"}, // Macau
	{"SG", "AS"}, {"IN", "AS"}, {"VN", "AS"}, {"TH", "AS"}, {"MY", "AS"},
	{"PH", "AS"}, {"ID", "AS"}, {"AE", "AS"}, {"SA", "AS"}, {"IL", "AS"},
	{"TR", "AS"}, // Turkey is transcontinental but typically classified as Asia

	// Europe (EU)
	{"GB", "EU"}, {"DE", "EU"}, {"FR", "EU"}, {"NL", "EU"}, {"CH", "EU"},
	{"SE", "EU"}, {"NO", "EU"}, {"DK", "EU"}, {"FI", "EU"}, {"ES", "EU"},
	{"IT", "EU"}, {"IE", "EU"}, {"PL", "EU"}, {"UA", "EU"}, {"RU", "EU"},
	// Russia is transcontinental but EU portion larger for proxy targeting

	// North America (NA)
	{"US", "NA"}, {"CA", "NA"}, {"MX", "NA"}, // Mexico not in regionTable yet but add for completeness

	// South America (SA)
	{"BR", "SA"}, {"AR", "SA"}, {"CL", "SA"}, {"CO", "SA"}, {"PE", "SA"}, {"VE", "SA"},

	// Oceania (OC)
	{"AU", "OC"}, {"NZ", "OC"},

	// Africa (AF) - not currently in regionTable, but add for completeness
	{"ZA", "AF"}, {"EG", "AF"}, {"NG", "AF"}, {"KE", "AF"}, {"MA", "AF"}, {"TN", "AF"},

	// Antarctica (AN) - research stations, rarely used but include for completeness
}

// continentOf returns the continent code for a given country code.
// Returns the continent code and true if found; empty string and false otherwise.
func continentOf(cc string) (string, bool) {
	for _, e := range countryToContinent {
		if e.Country == cc {
			return e.Continent, true
		}
	}
	return "", false
}

// regionTable is the ordered translation table for country inference.
// Order matters: for the same Lang, earlier entries match first.
// Specific forms (中国香港, 中国台湾, 中国澳门) precede general forms.
var regionTable = []regionEntry{
	// --- Chinese (zh): specific compound forms first ---
	{Indicator: "中国香港", Code: "HK", Lang: "zh"},
	{Indicator: "中国台湾", Code: "TW", Lang: "zh"},
	{Indicator: "中国澳门", Code: "MO", Lang: "zh"},
	// --- Chinese (zh): general country names ---
	{Indicator: "中国", Code: "CN", Lang: "zh"},
	{Indicator: "美国", Code: "US", Lang: "zh"},
	{Indicator: "日本", Code: "JP", Lang: "zh"},
	{Indicator: "韩国", Code: "KR", Lang: "zh"},
	{Indicator: "香港", Code: "HK", Lang: "zh"},
	{Indicator: "台湾", Code: "TW", Lang: "zh"},
	{Indicator: "臺灣", Code: "TW", Lang: "zh"},
	{Indicator: "新加坡", Code: "SG", Lang: "zh"},
	{Indicator: "英国", Code: "GB", Lang: "zh"},
	{Indicator: "德国", Code: "DE", Lang: "zh"},
	{Indicator: "法国", Code: "FR", Lang: "zh"},
	{Indicator: "加拿大", Code: "CA", Lang: "zh"},
	{Indicator: "澳大利亚", Code: "AU", Lang: "zh"},
	{Indicator: "俄罗斯", Code: "RU", Lang: "zh"},
	{Indicator: "印度", Code: "IN", Lang: "zh"},
	{Indicator: "越南", Code: "VN", Lang: "zh"},
	{Indicator: "泰国", Code: "TH", Lang: "zh"},
	{Indicator: "马来西亚", Code: "MY", Lang: "zh"},
	{Indicator: "菲律宾", Code: "PH", Lang: "zh"},
	{Indicator: "印度尼西亚", Code: "ID", Lang: "zh"},
	{Indicator: "巴西", Code: "BR", Lang: "zh"},
	{Indicator: "阿根廷", Code: "AR", Lang: "zh"},
	{Indicator: "土耳其", Code: "TR", Lang: "zh"},
	{Indicator: "沙特阿拉伯", Code: "SA", Lang: "zh"},
	{Indicator: "阿联酋", Code: "AE", Lang: "zh"},
	{Indicator: "以色列", Code: "IL", Lang: "zh"},
	{Indicator: "乌克兰", Code: "UA", Lang: "zh"},
	{Indicator: "波兰", Code: "PL", Lang: "zh"},
	{Indicator: "荷兰", Code: "NL", Lang: "zh"},
	{Indicator: "瑞士", Code: "CH", Lang: "zh"},
	{Indicator: "瑞典", Code: "SE", Lang: "zh"},
	{Indicator: "挪威", Code: "NO", Lang: "zh"},
	{Indicator: "丹麦", Code: "DK", Lang: "zh"},
	{Indicator: "芬兰", Code: "FI", Lang: "zh"},
	{Indicator: "西班牙", Code: "ES", Lang: "zh"},
	{Indicator: "意大利", Code: "IT", Lang: "zh"},
	{Indicator: "爱尔兰", Code: "IE", Lang: "zh"},
	// --- English (en): lowercase substring matched ---
	{Indicator: "hong kong", Code: "HK", Lang: "en"},
	{Indicator: "singapore", Code: "SG", Lang: "en"},
	{Indicator: "united states", Code: "US", Lang: "en"},
	{Indicator: "united kingdom", Code: "GB", Lang: "en"},
	{Indicator: "japan", Code: "JP", Lang: "en"},
	{Indicator: "germany", Code: "DE", Lang: "en"},
	{Indicator: "france", Code: "FR", Lang: "en"},
	{Indicator: "taiwan", Code: "TW", Lang: "en"},
	{Indicator: "south korea", Code: "KR", Lang: "en"},
	{Indicator: "canada", Code: "CA", Lang: "en"},
	{Indicator: "australia", Code: "AU", Lang: "en"},
	{Indicator: "russia", Code: "RU", Lang: "en"},
	{Indicator: "india", Code: "IN", Lang: "en"},
	{Indicator: "vietnam", Code: "VN", Lang: "en"},
	{Indicator: "thailand", Code: "TH", Lang: "en"},
	{Indicator: "malaysia", Code: "MY", Lang: "en"},
	{Indicator: "philippines", Code: "PH", Lang: "en"},
	{Indicator: "indonesia", Code: "ID", Lang: "en"},
	{Indicator: "brazil", Code: "BR", Lang: "en"},
	{Indicator: "argentina", Code: "AR", Lang: "en"},
	{Indicator: "turkey", Code: "TR", Lang: "en"},
	{Indicator: "netherlands", Code: "NL", Lang: "en"},
	{Indicator: "switzerland", Code: "CH", Lang: "en"},
	{Indicator: "sweden", Code: "SE", Lang: "en"},
	{Indicator: "norway", Code: "NO", Lang: "en"},
	{Indicator: "denmark", Code: "DK", Lang: "en"},
	{Indicator: "finland", Code: "FI", Lang: "en"},
	{Indicator: "spain", Code: "ES", Lang: "en"},
	{Indicator: "italy", Code: "IT", Lang: "en"},
	{Indicator: "ireland", Code: "IE", Lang: "en"},
	{Indicator: "ukraine", Code: "UA", Lang: "en"},
	{Indicator: "poland", Code: "PL", Lang: "en"},
	{Indicator: "israel", Code: "IL", Lang: "en"},
}

// decodeRegionalIndicatorPair walks the string by rune looking for two
// consecutive regional-indicator symbols (U+1F1E6..U+1F1FF). If found,
// returns the 2-letter uppercase code and true. Otherwise returns "", false.
func decodeRegionalIndicatorPair(s string) (string, bool) {
	for i := 0; i < len(s); {
		r1, sz1 := utf8.DecodeRuneInString(s[i:])
		if r1 >= 0x1F1E6 && r1 <= 0x1F1FF {
			// Found first regional indicator, check for second
			nextIdx := i + sz1
			if nextIdx < len(s) {
				r2, _ := utf8.DecodeRuneInString(s[nextIdx:])
				if r2 >= 0x1F1E6 && r2 <= 0x1F1FF {
					c1 := byte(r1 - 0x1F1E6 + 'A')
					c2 := byte(r2 - 0x1F1E6 + 'A')
					return string([]byte{c1, c2}), true
				}
			}
		}
		i += sz1
	}
	return "", false
}

func init() {
	for i, e := range regionTable {
		if len(e.Code) != 2 || e.Code[0] < 'A' || e.Code[0] > 'Z' || e.Code[1] < 'A' || e.Code[1] > 'Z' {
			panic(fmt.Sprintf("regionTable[%d]: Code %q is not two uppercase ASCII letters", i, e.Code))
		}
		if e.Lang != "zh" && e.Lang != "en" {
			panic(fmt.Sprintf("regionTable[%d]: Lang %q is not 'zh' or 'en'", i, e.Lang))
		}
		for j := 0; j < i; j++ {
			if regionTable[j].Indicator == e.Indicator {
				panic(fmt.Sprintf("regionTable[%d]: duplicate Indicator %q (also at [%d])", i, e.Indicator, j))
			}
		}
	}

	// Validate countryToContinent table
	validContinents := map[string]bool{
		"AF": true, "AS": true, "EU": true, "NA": true, "SA": true, "OC": true, "AN": true,
	}
	seen := make(map[string]int)
	for i, e := range countryToContinent {
		if len(e.Country) != 2 || e.Country[0] < 'A' || e.Country[0] > 'Z' || e.Country[1] < 'A' || e.Country[1] > 'Z' {
			panic(fmt.Sprintf("countryToContinent[%d]: Country %q is not two uppercase ASCII letters", i, e.Country))
		}
		if !validContinents[e.Continent] {
			panic(fmt.Sprintf("countryToContinent[%d]: invalid continent code %q for country %q", i, e.Continent, e.Country))
		}
		if prev, exists := seen[e.Country]; exists {
			panic(fmt.Sprintf("countryToContinent[%d]: duplicate country %q (first at [%d])", i, e.Country, prev))
		}
		seen[e.Country] = i
	}
}

// inferCountry attempts to infer an ISO 3166-1 alpha-2 country code from a
// proxy display name. Precedence: (1) emoji regional-indicator pair;
// (2) Chinese table entry; (3) English table entry.
func inferCountry(name string) (string, bool) {
	// 1. Emoji flag — highest precedence
	if code, ok := decodeRegionalIndicatorPair(name); ok {
		return code, true
	}

	// 2. Chinese table (zh entries)
	for _, e := range regionTable {
		if e.Lang != "zh" {
			continue
		}
		if strings.Contains(name, e.Indicator) {
			return e.Code, true
		}
	}

	// 3. English table (en entries) — case-insensitive
	lower := strings.ToLower(name)
	for _, e := range regionTable {
		if e.Lang != "en" {
			continue
		}
		if strings.Contains(lower, e.Indicator) {
			return e.Code, true
		}
	}

	return "", false
}
