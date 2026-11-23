// Package location resolves geographic country, region, and zone metadata
// from GCP zone identifiers, Colab endpoints/URLs, or geo-IP payloads.
package location

import (
	"sort"
	"strings"
)

// ColabOneLocation represents a resolved geographic location for a Colab runtime instance.
type ColabOneLocation struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	Region      string `json:"region"`
	Zone        string `json:"zone"`
}

// ColabOneGCPRegion holds geographic metadata for a Google Cloud region.
type ColabOneGCPRegion struct {
	Country     string
	CountryName string
	Region      string
}

// ToMap converts the location to a JSON-compatible map matching the wire schema.
// Returns nil if the receiver is nil.
func (l *ColabOneLocation) ToMap() map[string]any {
	if l == nil {
		return nil
	}
	return map[string]any{
		"country":      l.Country,
		"country_name": l.CountryName,
		"region":       l.Region,
		"zone":         l.Zone,
	}
}

// FormatShort formats the location as a concise human-readable label
// (e.g. "美国 · 俄勒冈" when country and region are both present and distinct).
// Returns an empty string if the receiver is nil.
func (l *ColabOneLocation) FormatShort() string {
	if l == nil {
		return ""
	}
	country := strings.TrimSpace(l.CountryName)
	if country == "" {
		country = strings.TrimSpace(l.Country)
	}
	region := strings.TrimSpace(l.Region)
	if country != "" && region != "" && region != country {
		return country + " · " + region
	}
	if country != "" {
		return country
	}
	return region
}

// GCPRegions maps canonical GCP region names to ISO2 country code, Chinese country name,
// and Chinese subregion name.
var GCPRegions = map[string]ColabOneGCPRegion{
	"us-central1":             {Country: "US", CountryName: "美国", Region: "艾奥瓦"},
	"us-central2":             {Country: "US", CountryName: "美国", Region: "俄克拉荷马"},
	"us-east1":                {Country: "US", CountryName: "美国", Region: "南卡罗来纳"},
	"us-east4":                {Country: "US", CountryName: "美国", Region: "弗吉尼亚"},
	"us-east5":                {Country: "US", CountryName: "美国", Region: "俄亥俄"},
	"us-south1":               {Country: "US", CountryName: "美国", Region: "得克萨斯"},
	"us-west1":                {Country: "US", CountryName: "美国", Region: "俄勒冈"},
	"us-west2":                {Country: "US", CountryName: "美国", Region: "加利福尼亚"},
	"us-west3":                {Country: "US", CountryName: "美国", Region: "犹他"},
	"us-west4":                {Country: "US", CountryName: "美国", Region: "内华达"},
	"northamerica-northeast1": {Country: "CA", CountryName: "加拿大", Region: "蒙特利尔"},
	"northamerica-northeast2": {Country: "CA", CountryName: "加拿大", Region: "多伦多"},
	"northamerica-south1":     {Country: "MX", CountryName: "墨西哥", Region: "克雷塔罗"},
	"southamerica-east1":      {Country: "BR", CountryName: "巴西", Region: "圣保罗"},
	"southamerica-west1":      {Country: "CL", CountryName: "智利", Region: "圣地亚哥"},
	"europe-west1":            {Country: "BE", CountryName: "比利时", Region: "圣吉兰"},
	"europe-west2":            {Country: "GB", CountryName: "英国", Region: "伦敦"},
	"europe-west3":            {Country: "DE", CountryName: "德国", Region: "法兰克福"},
	"europe-west4":            {Country: "NL", CountryName: "荷兰", Region: "埃姆斯哈文"},
	"europe-west6":            {Country: "CH", CountryName: "瑞士", Region: "苏黎世"},
	"europe-west8":            {Country: "IT", CountryName: "意大利", Region: "米兰"},
	"europe-west9":            {Country: "FR", CountryName: "法国", Region: "巴黎"},
	"europe-west10":           {Country: "DE", CountryName: "德国", Region: "柏林"},
	"europe-west11":           {Country: "NL", CountryName: "荷兰", Region: "米登梅尔"},
	"europe-west12":           {Country: "IT", CountryName: "意大利", Region: "都灵"},
	"europe-central2":         {Country: "PL", CountryName: "波兰", Region: "华沙"},
	"europe-north1":           {Country: "FI", CountryName: "芬兰", Region: "哈米纳"},
	"europe-north2":           {Country: "SE", CountryName: "瑞典", Region: "斯德哥尔摩"},
	"europe-southwest1":       {Country: "ES", CountryName: "西班牙", Region: "马德里"},
	"asia-east1":              {Country: "TW", CountryName: "中国台湾", Region: "彰化"},
	"asia-east2":              {Country: "HK", CountryName: "中国香港", Region: ""},
	"asia-northeast1":         {Country: "JP", CountryName: "日本", Region: "东京"},
	"asia-northeast2":         {Country: "JP", CountryName: "日本", Region: "大阪"},
	"asia-northeast3":         {Country: "KR", CountryName: "韩国", Region: "首尔"},
	"asia-southeast1":         {Country: "SG", CountryName: "新加坡", Region: ""},
	"asia-southeast2":         {Country: "ID", CountryName: "印度尼西亚", Region: "雅加达"},
	"asia-southeast3":         {Country: "TH", CountryName: "泰国", Region: "曼谷"},
	"asia-southeast4":         {Country: "MY", CountryName: "马来西亚", Region: "吉隆坡"},
	"asia-south1":             {Country: "IN", CountryName: "印度", Region: "孟买"},
	"asia-south2":             {Country: "IN", CountryName: "印度", Region: "德里"},
	"australia-southeast1":    {Country: "AU", CountryName: "澳大利亚", Region: "悉尼"},
	"australia-southeast2":    {Country: "AU", CountryName: "澳大利亚", Region: "墨尔本"},
	"me-west1":                {Country: "IL", CountryName: "以色列", Region: "特拉维夫"},
	"me-central1":             {Country: "QA", CountryName: "卡塔尔", Region: "多哈"},
	"me-central2":             {Country: "SA", CountryName: "沙特阿拉伯", Region: "达曼"},
	"africa-south1":           {Country: "ZA", CountryName: "南非", Region: "约翰内斯堡"},
}

// ColabShortZones maps Colab endpoint short zone codes to canonical GCP region names.
var ColabShortZones = map[string]string{
	"usc1":  "us-central1",
	"usc2":  "us-central2",
	"use1":  "us-east1",
	"use4":  "us-east4",
	"use5":  "us-east5",
	"uss1":  "us-south1",
	"usw1":  "us-west1",
	"usw2":  "us-west2",
	"usw3":  "us-west3",
	"usw4":  "us-west4",
	"nane1": "northamerica-northeast1",
	"nane2": "northamerica-northeast2",
	"nas1":  "northamerica-south1",
	"sae1":  "southamerica-east1",
	"saw1":  "southamerica-west1",
	"euw1":  "europe-west1",
	"euw2":  "europe-west2",
	"euw3":  "europe-west3",
	"euw4":  "europe-west4",
	"euw6":  "europe-west6",
	"euw8":  "europe-west8",
	"euw9":  "europe-west9",
	"euw10": "europe-west10",
	"euw11": "europe-west11",
	"euw12": "europe-west12",
	"euc2":  "europe-central2",
	"eun1":  "europe-north1",
	"eun2":  "europe-north2",
	"eusw1": "europe-southwest1",
	"ase1":  "asia-east1",
	"ase2":  "asia-east2",
	"asne1": "asia-northeast1",
	"ane1":  "asia-northeast1",
	"asne2": "asia-northeast2",
	"ane2":  "asia-northeast2",
	"asne3": "asia-northeast3",
	"ane3":  "asia-northeast3",
	"asse1": "asia-southeast1",
	"asea1": "asia-southeast1",
	"asse2": "asia-southeast2",
	"asea2": "asia-southeast2",
	"asse3": "asia-southeast3",
	"asea3": "asia-southeast3",
	"asse4": "asia-southeast4",
	"asea4": "asia-southeast4",
	"ass1":  "asia-south1",
	"aso1":  "asia-south1",
	"ass2":  "asia-south2",
	"aso2":  "asia-south2",
	"ause1": "australia-southeast1",
	"ause2": "australia-southeast2",
	"mew1":  "me-west1",
	"mec1":  "me-central1",
	"mec2":  "me-central2",
	"afs1":  "africa-south1",
}

// CountryNamesZH maps ISO 3166-1 alpha-2 country codes to Simplified Chinese names.
var CountryNamesZH = map[string]string{
	"US": "美国",
	"CA": "加拿大",
	"MX": "墨西哥",
	"BR": "巴西",
	"CL": "智利",
	"AR": "阿根廷",
	"CO": "哥伦比亚",
	"GB": "英国",
	"DE": "德国",
	"FR": "法国",
	"NL": "荷兰",
	"BE": "比利时",
	"CH": "瑞士",
	"IT": "意大利",
	"ES": "西班牙",
	"PT": "葡萄牙",
	"PL": "波兰",
	"FI": "芬兰",
	"SE": "瑞典",
	"NO": "挪威",
	"DK": "丹麦",
	"IE": "爱尔兰",
	"AT": "奥地利",
	"CZ": "捷克",
	"RO": "罗马尼亚",
	"HU": "匈牙利",
	"GR": "希腊",
	"UA": "乌克兰",
	"RU": "俄罗斯",
	"TR": "土耳其",
	"CN": "中国",
	"TW": "中国台湾",
	"HK": "中国香港",
	"MO": "中国澳门",
	"JP": "日本",
	"KR": "韩国",
	"SG": "新加坡",
	"ID": "印度尼西亚",
	"IN": "印度",
	"AU": "澳大利亚",
	"NZ": "新西兰",
	"MY": "马来西亚",
	"TH": "泰国",
	"VN": "越南",
	"PH": "菲律宾",
	"IL": "以色列",
	"QA": "卡塔尔",
	"SA": "沙特阿拉伯",
	"AE": "阿联酋",
	"ZA": "南非",
	"EG": "埃及",
}

// RegionNamesZH maps lowercase region/city names to Simplified Chinese names.
var RegionNamesZH = map[string]string{
	"iowa":                                "艾奥瓦",
	"oregon":                              "俄勒冈",
	"south carolina":                      "南卡罗来纳",
	"north carolina":                      "北卡罗来纳",
	"virginia":                            "弗吉尼亚",
	"ohio":                                "俄亥俄",
	"nevada":                              "内华达",
	"texas":                               "得克萨斯",
	"california":                          "加利福尼亚",
	"utah":                                "犹他",
	"washington":                          "华盛顿",
	"new york":                            "纽约",
	"new jersey":                          "新泽西",
	"illinois":                            "伊利诺伊",
	"georgia":                             "佐治亚",
	"florida":                             "佛罗里达",
	"arizona":                             "亚利桑那",
	"colorado":                            "科罗拉多",
	"quebec":                              "魁北克",
	"montreal":                            "蒙特利尔",
	"ontario":                             "安大略",
	"toronto":                             "多伦多",
	"changhua":                            "彰化",
	"changhua county":                     "彰化",
	"taipei":                              "台北",
	"new taipei":                          "新北",
	"taoyuan":                             "桃园",
	"kaohsiung":                           "高雄",
	"tokyo":                               "东京",
	"osaka":                               "大阪",
	"seoul":                               "首尔",
	"jakarta":                             "雅加达",
	"mumbai":                              "孟买",
	"maharashtra":                         "马哈拉施特拉",
	"delhi":                               "德里",
	"national capital territory of delhi": "德里",
	"sydney":                              "悉尼",
	"new south wales":                     "新南威尔士",
	"melbourne":                           "墨尔本",
	"victoria":                            "维多利亚",
	"groningen":                           "格罗宁根",
	"eemshaven":                           "埃姆斯哈文",
	"north holland":                       "北荷兰",
	"amsterdam":                           "阿姆斯特丹",
	"hainaut":                             "圣吉兰",
	"wallonia":                            "瓦隆",
	"saint-ghislain":                      "圣吉兰",
	"st. ghislain":                        "圣吉兰",
	"brussels":                            "布鲁塞尔",
	"hesse":                               "法兰克福",
	"frankfurt":                           "法兰克福",
	"berlin":                              "柏林",
	"ile-de-france":                       "巴黎",
	"île-de-france":                       "巴黎",
	"paris":                               "巴黎",
	"england":                             "伦敦",
	"london":                              "伦敦",
	"zurich":                              "苏黎世",
	"lombardy":                            "米兰",
	"milan":                               "米兰",
	"piedmont":                            "都灵",
	"turin":                               "都灵",
	"madrid":                              "马德里",
	"mazovia":                             "华沙",
	"warsaw":                              "华沙",
	"kymenlaakso":                         "哈米纳",
	"hamina":                              "哈米纳",
	"stockholm":                           "斯德哥尔摩",
	"sao paulo":                           "圣保罗",
	"são paulo":                           "圣保罗",
	"santiago":                            "圣地亚哥",
	"santiago metropolitan":               "圣地亚哥",
	"tel aviv":                            "特拉维夫",
	"doha":                                "多哈",
	"dammam":                              "达曼",
	"johannesburg":                        "约翰内斯堡",
	"gauteng":                             "约翰内斯堡",
}

var (
	gcpRegionKeysDesc      []string
	colabShortZoneKeysDesc []string
)

func init() {
	gcpRegionKeysDesc = make([]string, 0, len(GCPRegions))
	for k := range GCPRegions {
		gcpRegionKeysDesc = append(gcpRegionKeysDesc, k)
	}
	sort.Slice(gcpRegionKeysDesc, func(i, j int) bool {
		if len(gcpRegionKeysDesc[i]) != len(gcpRegionKeysDesc[j]) {
			return len(gcpRegionKeysDesc[i]) > len(gcpRegionKeysDesc[j])
		}
		return gcpRegionKeysDesc[i] < gcpRegionKeysDesc[j]
	})

	colabShortZoneKeysDesc = make([]string, 0, len(ColabShortZones))
	for k := range ColabShortZones {
		colabShortZoneKeysDesc = append(colabShortZoneKeysDesc, k)
	}
	sort.Slice(colabShortZoneKeysDesc, func(i, j int) bool {
		if len(colabShortZoneKeysDesc[i]) != len(colabShortZoneKeysDesc[j]) {
			return len(colabShortZoneKeysDesc[i]) > len(colabShortZoneKeysDesc[j])
		}
		return colabShortZoneKeysDesc[i] < colabShortZoneKeysDesc[j]
	})
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func isASCIIUpper(b byte) bool {
	return b >= 'A' && b <= 'Z'
}

func isShortZoneDelim(b byte) bool {
	return b == '-' || b == '.' || b == '_' || b == '/'
}

// matchFullZone finds the leftmost GCP region (with optional "-[a-f]" zone suffix)
// bounded on both sides by non-[a-z0-9] characters, matching longest key first at each position.
// Expects s to already be lowercased.
func matchFullZone(s string) (gcpRegion, fullZone string, ok bool) {
	for i := 0; i < len(s); i++ {
		if i > 0 && isASCIIAlnum(s[i-1]) {
			continue
		}
		rem := s[i:]
		for _, key := range gcpRegionKeysDesc {
			if !strings.HasPrefix(rem, key) {
				continue
			}
			end := i + len(key)
			// Greedy optional "-[a-f]" followed by (?![a-z0-9]).
			if end+2 <= len(s) && s[end] == '-' && s[end+1] >= 'a' && s[end+1] <= 'f' {
				if end+2 == len(s) || !isASCIIAlnum(s[end+2]) {
					return key, s[i : end+2], true
				}
			}
			// Fallback to bare region followed by (?![a-z0-9]).
			if end == len(s) || !isASCIIAlnum(s[end]) {
				return key, key, true
			}
		}
	}
	return "", "", false
}

// matchShortZone finds the leftmost Colab short zone token bounded by start/end or [-._/],
// matching longest short key first at each position.
func matchShortZone(candidate string) (gcpRegion, zoneLetter string, ok bool) {
	s := strings.ToLower(candidate)
	for i := 0; i < len(s); i++ {
		if i > 0 && !isShortZoneDelim(s[i-1]) {
			continue
		}
		rem := s[i:]
		for _, shortKey := range colabShortZoneKeysDesc {
			if !strings.HasPrefix(rem, shortKey) {
				continue
			}
			pos := i + len(shortKey)
			letter := ""
			if pos < len(s) && s[pos] >= 'a' && s[pos] <= 'f' {
				letter = s[pos : pos+1]
				pos++
				for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
					pos++
				}
			}
			if pos == len(s) || isShortZoneDelim(s[pos]) || (pos == len(s)-1 && s[pos] == '\n') {
				return ColabShortZones[shortKey], letter, true
			}
		}
	}
	return "", "", false
}

// LocationFromZone resolves a geographic location from a raw GCP zone string
// (such as "projects/12345/zones/europe-west4-a" or "us-west1-b").
func LocationFromZone(rawZone string) *ColabOneLocation {
	if rawZone == "" {
		return nil
	}
	cleaned := strings.ToLower(strings.TrimSpace(rawZone))
	if cleaned == "" {
		return nil
	}
	gcpRegion, fullZone, ok := matchFullZone(cleaned)
	if !ok {
		return nil
	}
	meta := GCPRegions[gcpRegion]
	return &ColabOneLocation{
		Country:     meta.Country,
		CountryName: meta.CountryName,
		Region:      meta.Region,
		Zone:        fullZone,
	}
}

// InferLocationFromEndpoint infers the runtime geographic location from a Colab endpoint
// identifier and/or proxy URL, preferring full GCP zone names before Colab short zone codes.
func InferLocationFromEndpoint(endpoint, url string) *ColabOneLocation {
	for _, candidate := range [2]string{endpoint, url} {
		if candidate == "" {
			continue
		}
		if direct := LocationFromZone(candidate); direct != nil {
			return direct
		}
		if gcpRegion, zoneLetter, ok := matchShortZone(candidate); ok {
			zone := gcpRegion
			if zoneLetter != "" {
				zone = gcpRegion + "-" + zoneLetter
			}
			meta := GCPRegions[gcpRegion]
			return &ColabOneLocation{
				Country:     meta.Country,
				CountryName: meta.CountryName,
				Region:      meta.Region,
				Zone:        zone,
			}
		}
	}
	return nil
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if len(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

func cleanSubregion(raw, countryCode, countryName string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	lowered := strings.ToLower(text)
	switch lowered {
	case strings.ToLower(countryCode),
		strings.ToLower(countryName),
		"taiwan",
		"singapore",
		"hong kong",
		"macau",
		"macao":
		return ""
	}
	translated, ok := RegionNamesZH[lowered]
	if !ok {
		translated = text
	}
	if translated == countryName {
		return ""
	}
	return truncateRunes(translated, 32)
}

// NormalizeGeoPayload normalizes a geo-IP payload (country code, optional region, city, and zone)
// into a ColabOneLocation, mapping UK to GB, rejecting invalid/reserved codes, translating known
// country/region names to Chinese, and deduplicating redundant subregions.
func NormalizeGeoPayload(country, region, city, zone string) *ColabOneLocation {
	code := strings.ToUpper(strings.TrimSpace(country))
	if code == "UK" {
		code = "GB"
	}
	if len(code) != 2 || !isASCIIUpper(code[0]) || !isASCIIUpper(code[1]) || code == "XX" || code == "ZZ" || code == "T1" {
		return nil
	}
	countryName, ok := CountryNamesZH[code]
	if !ok {
		countryName = code
	}
	sub := cleanSubregion(region, code, countryName)
	if sub == "" {
		sub = cleanSubregion(city, code, countryName)
	}
	return &ColabOneLocation{
		Country:     code,
		CountryName: countryName,
		Region:      sub,
		Zone:        truncateRunes(strings.ToLower(strings.TrimSpace(zone)), 40),
	}
}
