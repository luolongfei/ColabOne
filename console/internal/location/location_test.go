package location

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestLookupMapCountsAndIntegrity(t *testing.T) {
	if got := len(GCPRegions); got != 46 {
		t.Fatalf("len(GCPRegions) = %d, want 46", got)
	}
	if got := len(ColabShortZones); got != 55 {
		t.Fatalf("len(ColabShortZones) = %d, want 55", got)
	}
	if got := len(CountryNamesZH); got != 51 {
		t.Fatalf("len(CountryNamesZH) = %d, want 51", got)
	}
	if got := len(RegionNamesZH); got != 77 {
		t.Fatalf("len(RegionNamesZH) = %d, want 77", got)
	}

	for region, meta := range GCPRegions {
		if len(meta.Country) != 2 || !isASCIIUpper(meta.Country[0]) || !isASCIIUpper(meta.Country[1]) {
			t.Errorf("GCPRegions[%q].Country = %q, want 2-letter ASCII uppercase ISO code", region, meta.Country)
		}
		zh, ok := CountryNamesZH[meta.Country]
		if !ok {
			t.Errorf("GCPRegions[%q].Country = %q missing from CountryNamesZH", region, meta.Country)
		} else if zh != meta.CountryName {
			t.Errorf("GCPRegions[%q].CountryName = %q, CountryNamesZH[%q] = %q", region, meta.CountryName, meta.Country, zh)
		}
	}

	for short, region := range ColabShortZones {
		if _, ok := GCPRegions[region]; !ok {
			t.Errorf("ColabShortZones[%q] = %q missing from GCPRegions", short, region)
		}
	}
}

func TestLocationFromZoneAllGCPRegions(t *testing.T) {
	for region, meta := range GCPRegions {
		// Bare region
		got := LocationFromZone(region)
		if got == nil {
			t.Fatalf("LocationFromZone(%q) = nil, want non-nil", region)
		}
		want := ColabOneLocation{
			Country:     meta.Country,
			CountryName: meta.CountryName,
			Region:      meta.Region,
			Zone:        region,
		}
		if *got != want {
			t.Errorf("LocationFromZone(%q) = %+v, want %+v", region, *got, want)
		}

		// All valid zone letters a..f
		for _, letter := range []string{"a", "b", "c", "d", "e", "f"} {
			zone := region + "-" + letter
			raw := "  projects/123456/zones/" + strings.ToUpper(zone) + "  "
			gotZone := LocationFromZone(raw)
			if gotZone == nil {
				t.Fatalf("LocationFromZone(%q) = nil, want non-nil", raw)
			}
			wantZone := ColabOneLocation{
				Country:     meta.Country,
				CountryName: meta.CountryName,
				Region:      meta.Region,
				Zone:        zone,
			}
			if *gotZone != wantZone {
				t.Errorf("LocationFromZone(%q) = %+v, want %+v", raw, *gotZone, wantZone)
			}
		}
	}
}

func TestLocationFromZoneEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want *ColabOneLocation
	}{
		{
			name: "empty",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace only",
			raw:  "   \t\n ",
			want: nil,
		},
		{
			name: "metadata path europe-west4-a",
			raw:  "projects/12345/zones/europe-west4-a",
			want: &ColabOneLocation{Country: "NL", CountryName: "荷兰", Region: "埃姆斯哈文", Zone: "europe-west4-a"},
		},
		{
			name: "longest prefix europe-west10-a over europe-west1",
			raw:  "europe-west10-a",
			want: &ColabOneLocation{Country: "DE", CountryName: "德国", Region: "柏林", Zone: "europe-west10-a"},
		},
		{
			name: "longest prefix europe-west11-b",
			raw:  "europe-west11-b",
			want: &ColabOneLocation{Country: "NL", CountryName: "荷兰", Region: "米登梅尔", Zone: "europe-west11-b"},
		},
		{
			name: "longest prefix europe-west12-c",
			raw:  "europe-west12-c",
			want: &ColabOneLocation{Country: "IT", CountryName: "意大利", Region: "都灵", Zone: "europe-west12-c"},
		},
		{
			name: "backtrack when zone suffix followed by alpha",
			raw:  "europe-west1-ab",
			want: &ColabOneLocation{Country: "BE", CountryName: "比利时", Region: "圣吉兰", Zone: "europe-west1"},
		},
		{
			name: "backtrack when zone suffix followed by digit",
			raw:  "europe-west1-a1",
			want: &ColabOneLocation{Country: "BE", CountryName: "比利时", Region: "圣吉兰", Zone: "europe-west1"},
		},
		{
			name: "backtrack when zone suffix out of range a-f",
			raw:  "europe-west1-g",
			want: &ColabOneLocation{Country: "BE", CountryName: "比利时", Region: "圣吉兰", Zone: "europe-west1"},
		},
		{
			name: "backtrack europe-west10-ab",
			raw:  "europe-west10-ab",
			want: &ColabOneLocation{Country: "DE", CountryName: "德国", Region: "柏林", Zone: "europe-west10"},
		},
		{
			name: "reject trailing alnum immediately after region",
			raw:  "europe-west1a",
			want: nil,
		},
		{
			name: "reject trailing digit making unknown region",
			raw:  "us-west10",
			want: nil,
		},
		{
			name: "reject leading alpha",
			raw:  "xus-west1-a",
			want: nil,
		},
		{
			name: "reject leading digit",
			raw:  "1us-west1-a",
			want: nil,
		},
		{
			name: "leftmost match wins over longer later key",
			raw:  "foo-us-west1-a-europe-west10-b",
			want: &ColabOneLocation{Country: "US", CountryName: "美国", Region: "俄勒冈", Zone: "us-west1-a"},
		},
		{
			name: "dash delimiter after zone suffix",
			raw:  "us-central1-f-extra",
			want: &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-f"},
		},
		{
			name: "slash delimiter after zone suffix",
			raw:  "us-central1-f/extra",
			want: &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-f"},
		},
		{
			name: "unicode prefix is non-ascii-alnum boundary",
			raw:  "区us-west1-a",
			want: &ColabOneLocation{Country: "US", CountryName: "美国", Region: "俄勒冈", Zone: "us-west1-a"},
		},
		{
			name: "uppercase input",
			raw:  "ASIA-EAST1-C",
			want: &ColabOneLocation{Country: "TW", CountryName: "中国台湾", Region: "彰化", Zone: "asia-east1-c"},
		},
		{
			name: "singapore empty region",
			raw:  "asia-southeast1-b",
			want: &ColabOneLocation{Country: "SG", CountryName: "新加坡", Region: "", Zone: "asia-southeast1-b"},
		},
		{
			name: "hong kong empty region",
			raw:  "asia-east2-a",
			want: &ColabOneLocation{Country: "HK", CountryName: "中国香港", Region: "", Zone: "asia-east2-a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LocationFromZone(tc.raw)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("LocationFromZone(%q) = %+v, want nil", tc.raw, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("LocationFromZone(%q) = nil, want %+v", tc.raw, *tc.want)
			}
			if *got != *tc.want {
				t.Errorf("LocationFromZone(%q) = %+v, want %+v", tc.raw, *got, *tc.want)
			}
		})
	}
}

func TestInferLocationFromEndpointAllShortZones(t *testing.T) {
	for short, gcpRegion := range ColabShortZones {
		meta := GCPRegions[gcpRegion]

		// Typical Colab endpoint with zone letter + trailing digit
		ep := "gpu-t4-s-kkb-" + short + "b1-abc123"
		got := InferLocationFromEndpoint(ep, "")
		want := ColabOneLocation{
			Country:     meta.Country,
			CountryName: meta.CountryName,
			Region:      meta.Region,
			Zone:        gcpRegion + "-b",
		}
		if got == nil || *got != want {
			t.Errorf("InferLocationFromEndpoint(%q, \"\") = %+v, want %+v", ep, got, want)
		}

		// Bare uppercase short zone without letter
		upper := strings.ToUpper(short)
		gotBare := InferLocationFromEndpoint(upper, "")
		wantBare := ColabOneLocation{
			Country:     meta.Country,
			CountryName: meta.CountryName,
			Region:      meta.Region,
			Zone:        gcpRegion,
		}
		if gotBare == nil || *gotBare != wantBare {
			t.Errorf("InferLocationFromEndpoint(%q, \"\") = %+v, want %+v", upper, gotBare, wantBare)
		}
	}
}

func TestInferLocationFromEndpointEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		url      string
		want     *ColabOneLocation
	}{
		{
			name:     "python test_provider usw1b1",
			endpoint: "gpu-t4-s-kkb-usw1b1-abc123",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "俄勒冈", Zone: "us-west1-b"},
		},
		{
			name:     "python test_provider ase1c1",
			endpoint: "m-s-kkb-ase1c1-999",
			want:     &ColabOneLocation{Country: "TW", CountryName: "中国台湾", Region: "彰化", Zone: "asia-east1-c"},
		},
		{
			name:     "python test_provider asse1a1",
			endpoint: "m-s-kkb-asse1a1-000",
			want:     &ColabOneLocation{Country: "SG", CountryName: "新加坡", Region: "", Zone: "asia-southeast1-a"},
		},
		{
			name:     "python test_provider usw4b1",
			endpoint: "gpu-t4-s-kkb-usw4b1-demo",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "内华达", Zone: "us-west4-b"},
		},
		{
			name:     "python test_telemetry ass1a1",
			endpoint: "gpu-t4-s-kkb-ass1a1-boot",
			want:     &ColabOneLocation{Country: "IN", CountryName: "印度", Region: "孟买", Zone: "asia-south1-a"},
		},
		{
			name:     "unrecognized endpoint",
			endpoint: "m-s-1",
			want:     nil,
		},
		{
			name:     "both empty",
			endpoint: "",
			url:      "",
			want:     nil,
		},
		{
			name:     "short prefix euw10a1 over euw1",
			endpoint: "m-s-euw10a1-xyz",
			want:     &ColabOneLocation{Country: "DE", CountryName: "德国", Region: "柏林", Zone: "europe-west10-a"},
		},
		{
			name:     "short prefix euw11 over euw1",
			endpoint: "m-s-euw11-xyz",
			want:     &ColabOneLocation{Country: "NL", CountryName: "荷兰", Region: "米登梅尔", Zone: "europe-west11"},
		},
		{
			name:     "short prefix euw12f99",
			endpoint: "m-s-euw12f99-xyz",
			want:     &ColabOneLocation{Country: "IT", CountryName: "意大利", Region: "都灵", Zone: "europe-west12-f"},
		},
		{
			name:     "short zone letter out of a-f rejected",
			endpoint: "m-s-euw1g1-xyz",
			want:     nil,
		},
		{
			name:     "fallback to url short zone",
			endpoint: "m-s-1",
			url:      "https://8080-gpu-t4-s-usc1b1-abc.prod.colab.google.com",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-b"},
		},
		{
			name:     "fallback to url full zone",
			endpoint: "m-s-1",
			url:      "https://8080-us-central1-a.prod.colab.google.com",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-a"},
		},
		{
			name:     "endpoint wins over url",
			endpoint: "m-s-usc1a1",
			url:      "https://us-east1-b.example.com",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-a"},
		},
		{
			name:     "full zone in endpoint wins over short zone in same endpoint",
			endpoint: "usc1a1-us-west2-b",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "加利福尼亚", Zone: "us-west2-b"},
		},
		{
			name:     "underscore delimiters",
			endpoint: "foo_ane2d1_bar",
			want:     &ColabOneLocation{Country: "JP", CountryName: "日本", Region: "大阪", Zone: "asia-northeast2-d"},
		},
		{
			name:     "slash delimiters",
			endpoint: "foo/asne3e2/bar",
			want:     &ColabOneLocation{Country: "KR", CountryName: "韩国", Region: "首尔", Zone: "asia-northeast3-e"},
		},
		{
			name:     "dot delimiters",
			endpoint: "host.euw4c3.colab.google",
			want:     &ColabOneLocation{Country: "NL", CountryName: "荷兰", Region: "埃姆斯哈文", Zone: "europe-west4-c"},
		},
		{
			name:     "uppercase mixed case",
			endpoint: "m-s-USW2C3-UPPER",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "加利福尼亚", Zone: "us-west2-c"},
		},
		{
			name:     "reject leading alnum",
			endpoint: "xusc1a1",
			want:     nil,
		},
		{
			name:     "reject trailing alnum",
			endpoint: "usc1a1x",
			want:     nil,
		},
		{
			name:     "reject trailing letter after digit",
			endpoint: "usc1a1b",
			want:     nil,
		},
		{
			name:     "reject digits without zone letter",
			endpoint: "usc11",
			want:     nil,
		},
		{
			name:     "reject surrounding spaces without delimiter",
			endpoint: " usc1a1 ",
			want:     nil,
		},
		{
			name:     "accept trailing newline like python regex dollar",
			endpoint: "usc1a1\n",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "艾奥瓦", Zone: "us-central1-a"},
		},
		{
			name:     "skip invalid short token and match later valid token",
			endpoint: "m-s-usw1x-usw2b1-abc",
			want:     &ColabOneLocation{Country: "US", CountryName: "美国", Region: "加利福尼亚", Zone: "us-west2-b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InferLocationFromEndpoint(tc.endpoint, tc.url)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("InferLocationFromEndpoint(%q, %q) = %+v, want nil", tc.endpoint, tc.url, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("InferLocationFromEndpoint(%q, %q) = nil, want %+v", tc.endpoint, tc.url, *tc.want)
			}
			if *got != *tc.want {
				t.Errorf("InferLocationFromEndpoint(%q, %q) = %+v, want %+v", tc.endpoint, tc.url, *got, *tc.want)
			}
		})
	}
}

func TestNormalizeGeoPayloadCountriesAndRegions(t *testing.T) {
	// Every country in CountryNamesZH (case-insensitive & trimmed)
	for code, name := range CountryNamesZH {
		got := NormalizeGeoPayload(" "+strings.ToLower(code)+" ", "", "", "")
		if got == nil {
			t.Fatalf("NormalizeGeoPayload(%q) = nil", code)
		}
		if got.Country != code || got.CountryName != name || got.Region != "" || got.Zone != "" {
			t.Errorf("NormalizeGeoPayload(%q) = %+v, want country=%q name=%q", code, *got, code, name)
		}
	}

	// Every region in RegionNamesZH
	for key, zh := range RegionNamesZH {
		got := NormalizeGeoPayload("US", " "+strings.ToUpper(key)+" ", "", "")
		if got == nil {
			t.Fatalf("NormalizeGeoPayload(US, %q) = nil", key)
		}
		if got.Region != zh {
			t.Errorf("NormalizeGeoPayload(US, %q).Region = %q, want %q", key, got.Region, zh)
		}
	}
}

func TestNormalizeGeoPayloadEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		country string
		region  string
		city    string
		zone    string
		want    *ColabOneLocation
	}{
		{
			name:    "UK normalized to GB",
			country: "UK",
			region:  "England",
			city:    "London",
			want:    &ColabOneLocation{Country: "GB", CountryName: "英国", Region: "伦敦", Zone: ""},
		},
		{
			name:    "lowercase uk with whitespace normalized to GB",
			country: "  uk ",
			city:    "London",
			want:    &ColabOneLocation{Country: "GB", CountryName: "英国", Region: "伦敦", Zone: ""},
		},
		{
			name:    "invalid XX",
			country: "XX",
			want:    nil,
		},
		{
			name:    "invalid xx lowercase",
			country: "xx",
			want:    nil,
		},
		{
			name:    "invalid ZZ",
			country: "ZZ",
			want:    nil,
		},
		{
			name:    "invalid T1",
			country: "T1",
			want:    nil,
		},
		{
			name:    "invalid t1 lowercase",
			country: "t1",
			want:    nil,
		},
		{
			name:    "invalid empty",
			country: "",
			want:    nil,
		},
		{
			name:    "invalid single letter",
			country: "U",
			want:    nil,
		},
		{
			name:    "invalid three letters",
			country: "USA",
			want:    nil,
		},
		{
			name:    "invalid numeric",
			country: "12",
			want:    nil,
		},
		{
			name:    "invalid alphanumeric",
			country: "U1",
			want:    nil,
		},
		{
			name:    "invalid non-ASCII two runes",
			country: "美国",
			want:    nil,
		},
		{
			name:    "deduplicate singapore in region and city",
			country: "SG",
			region:  "Singapore",
			city:    "SINGAPORE",
			want:    &ColabOneLocation{Country: "SG", CountryName: "新加坡", Region: "", Zone: ""},
		},
		{
			name:    "deduplicate taiwan region and fall back to taipei city",
			country: "TW",
			region:  "Taiwan",
			city:    "Taipei",
			want:    &ColabOneLocation{Country: "TW", CountryName: "中国台湾", Region: "台北", Zone: ""},
		},
		{
			name:    "deduplicate hong kong",
			country: "HK",
			region:  "Hong Kong",
			city:    "hong kong",
			want:    &ColabOneLocation{Country: "HK", CountryName: "中国香港", Region: "", Zone: ""},
		},
		{
			name:    "deduplicate macau and macao",
			country: "MO",
			region:  "Macau",
			city:    "Macao",
			want:    &ColabOneLocation{Country: "MO", CountryName: "中国澳门", Region: "", Zone: ""},
		},
		{
			name:    "deduplicate region matching country code and fall back to city",
			country: "JP",
			region:  "jp",
			city:    "Tokyo",
			want:    &ColabOneLocation{Country: "JP", CountryName: "日本", Region: "东京", Zone: ""},
		},
		{
			name:    "deduplicate region matching chinese country name and fall back to city",
			country: "JP",
			region:  "日本",
			city:    "Osaka",
			want:    &ColabOneLocation{Country: "JP", CountryName: "日本", Region: "大阪", Zone: ""},
		},
		{
			name:    "unicode region ile-de-france with circumflex",
			country: "FR",
			region:  "Île-de-France",
			want:    &ColabOneLocation{Country: "FR", CountryName: "法国", Region: "巴黎", Zone: ""},
		},
		{
			name:    "unicode region sao paulo with tilde",
			country: "BR",
			region:  "São Paulo",
			want:    &ColabOneLocation{Country: "BR", CountryName: "巴西", Region: "圣保罗", Zone: ""},
		},
		{
			name:    "unknown valid ISO2 country falls back to code as name",
			country: "is",
			region:  "Reykjavik",
			want:    &ColabOneLocation{Country: "IS", CountryName: "IS", Region: "Reykjavik", Zone: ""},
		},
		{
			name:    "unknown region preserved verbatim trimmed",
			country: "US",
			region:  "  Maine  ",
			city:    "Portland",
			zone:    "  US-EAST1-B  ",
			want:    &ColabOneLocation{Country: "US", CountryName: "美国", Region: "Maine", Zone: "us-east1-b"},
		},
		{
			name:    "region truncated to 32 ASCII runes",
			country: "US",
			region:  strings.Repeat("a", 50),
			want:    &ColabOneLocation{Country: "US", CountryName: "美国", Region: strings.Repeat("a", 32), Zone: ""},
		},
		{
			name:    "region truncated to 32 multibyte unicode runes without splitting UTF-8",
			country: "CN",
			region:  strings.Repeat("测", 40),
			want:    &ColabOneLocation{Country: "CN", CountryName: "中国", Region: strings.Repeat("测", 32), Zone: ""},
		},
		{
			name:    "zone trimmed lowercased and truncated to 40 runes",
			country: "US",
			zone:    "  " + strings.Repeat("Z", 55) + "  ",
			want:    &ColabOneLocation{Country: "US", CountryName: "美国", Region: "", Zone: strings.Repeat("z", 40)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeGeoPayload(tc.country, tc.region, tc.city, tc.zone)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("NormalizeGeoPayload(%q, %q, %q, %q) = %+v, want nil", tc.country, tc.region, tc.city, tc.zone, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("NormalizeGeoPayload(%q, %q, %q, %q) = nil, want %+v", tc.country, tc.region, tc.city, tc.zone, *tc.want)
			}
			if *got != *tc.want {
				t.Errorf("NormalizeGeoPayload(%q, %q, %q, %q) = %+v, want %+v", tc.country, tc.region, tc.city, tc.zone, *got, *tc.want)
			}
			if !utf8.ValidString(got.Region) || !utf8.ValidString(got.Zone) {
				t.Errorf("invalid UTF-8 in result: %+v", *got)
			}
		})
	}
}

func TestColabOneLocationHelpersAndJSON(t *testing.T) {
	// Nil receiver safety
	var nilLoc *ColabOneLocation
	if got := nilLoc.ToMap(); got != nil {
		t.Errorf("nil.ToMap() = %v, want nil", got)
	}
	if got := nilLoc.FormatShort(); got != "" {
		t.Errorf("nil.FormatShort() = %q, want empty", got)
	}

	// Non-nil ToMap & JSON wire format (all 4 keys always emitted, even when empty)
	sg := InferLocationFromEndpoint("m-s-kkb-asse1a1-000", "")
	if sg == nil {
		t.Fatal("expected non-nil Singapore location")
	}
	m := sg.ToMap()
	wantMap := map[string]any{
		"country":      "SG",
		"country_name": "新加坡",
		"region":       "",
		"zone":         "asia-southeast1-a",
	}
	if len(m) != len(wantMap) {
		t.Fatalf("len(ToMap()) = %d, want %d", len(m), len(wantMap))
	}
	for k, wv := range wantMap {
		if m[k] != wv {
			t.Errorf("ToMap()[%q] = %v, want %v", k, m[k], wv)
		}
	}

	rawJSON, err := json.Marshal(sg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(rawJSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	for _, key := range []string{"country", "country_name", "region", "zone"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("JSON output %s missing key %q", string(rawJSON), key)
		}
	}

	// FormatShort cases
	formatCases := []struct {
		loc  ColabOneLocation
		want string
	}{
		{ColabOneLocation{Country: "US", CountryName: "美国", Region: "俄勒冈"}, "美国 · 俄勒冈"},
		{ColabOneLocation{Country: "SG", CountryName: "新加坡", Region: ""}, "新加坡"},
		{ColabOneLocation{Country: "SG", CountryName: "新加坡", Region: "新加坡"}, "新加坡"},
		{ColabOneLocation{Country: "US", CountryName: "", Region: "California"}, "US · California"},
		{ColabOneLocation{Country: "US", CountryName: "", Region: ""}, "US"},
		{ColabOneLocation{Country: "", CountryName: "", Region: "俄勒冈"}, "俄勒冈"},
		{ColabOneLocation{Country: "  ", CountryName: "  ", Region: "  "}, ""},
	}
	for _, tc := range formatCases {
		if got := tc.loc.FormatShort(); got != tc.want {
			t.Errorf("%+v.FormatShort() = %q, want %q", tc.loc, got, tc.want)
		}
	}
}

func TestLocationConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if loc := LocationFromZone("projects/1/zones/europe-west4-a"); loc == nil || loc.Country != "NL" {
					t.Errorf("unexpected LocationFromZone result: %+v", loc)
				}
				if loc := InferLocationFromEndpoint("gpu-t4-s-kkb-usw1b1-abc123", ""); loc == nil || loc.Zone != "us-west1-b" {
					t.Errorf("unexpected InferLocationFromEndpoint result: %+v", loc)
				}
				if loc := NormalizeGeoPayload("FR", "Île-de-France", "Paris", "europe-west9-a"); loc == nil || loc.Region != "巴黎" {
					t.Errorf("unexpected NormalizeGeoPayload result: %+v", loc)
				}
			}
		}()
	}
	wg.Wait()
}
