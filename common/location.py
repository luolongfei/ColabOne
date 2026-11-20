"""Instance geographic region/country resolution from GCP zones, Colab endpoints, or VM metadata."""

from __future__ import annotations

import re

GCP_REGIONS: dict[str, tuple[str, str, str]] = {
    "us-central1": ("US", "美国", "艾奥瓦"),
    "us-central2": ("US", "美国", "俄克拉荷马"),
    "us-east1": ("US", "美国", "南卡罗来纳"),
    "us-east4": ("US", "美国", "弗吉尼亚"),
    "us-east5": ("US", "美国", "俄亥俄"),
    "us-south1": ("US", "美国", "得克萨斯"),
    "us-west1": ("US", "美国", "俄勒冈"),
    "us-west2": ("US", "美国", "加利福尼亚"),
    "us-west3": ("US", "美国", "犹他"),
    "us-west4": ("US", "美国", "内华达"),
    "northamerica-northeast1": ("CA", "加拿大", "蒙特利尔"),
    "northamerica-northeast2": ("CA", "加拿大", "多伦多"),
    "northamerica-south1": ("MX", "墨西哥", "克雷塔罗"),
    "southamerica-east1": ("BR", "巴西", "圣保罗"),
    "southamerica-west1": ("CL", "智利", "圣地亚哥"),
    "europe-west1": ("BE", "比利时", "圣吉兰"),
    "europe-west2": ("GB", "英国", "伦敦"),
    "europe-west3": ("DE", "德国", "法兰克福"),
    "europe-west4": ("NL", "荷兰", "埃姆斯哈文"),
    "europe-west6": ("CH", "瑞士", "苏黎世"),
    "europe-west8": ("IT", "意大利", "米兰"),
    "europe-west9": ("FR", "法国", "巴黎"),
    "europe-west10": ("DE", "德国", "柏林"),
    "europe-west11": ("NL", "荷兰", "米登梅尔"),
    "europe-west12": ("IT", "意大利", "都灵"),
    "europe-central2": ("PL", "波兰", "华沙"),
    "europe-north1": ("FI", "芬兰", "哈米纳"),
    "europe-north2": ("SE", "瑞典", "斯德哥尔摩"),
    "europe-southwest1": ("ES", "西班牙", "马德里"),
    "asia-east1": ("TW", "中国台湾", "彰化"),
    "asia-east2": ("HK", "中国香港", ""),
    "asia-northeast1": ("JP", "日本", "东京"),
    "asia-northeast2": ("JP", "日本", "大阪"),
    "asia-northeast3": ("KR", "韩国", "首尔"),
    "asia-southeast1": ("SG", "新加坡", ""),
    "asia-southeast2": ("ID", "印度尼西亚", "雅加达"),
    "asia-southeast3": ("TH", "泰国", "曼谷"),
    "asia-southeast4": ("MY", "马来西亚", "吉隆坡"),
    "asia-south1": ("IN", "印度", "孟买"),
    "asia-south2": ("IN", "印度", "德里"),
    "australia-southeast1": ("AU", "澳大利亚", "悉尼"),
    "australia-southeast2": ("AU", "澳大利亚", "墨尔本"),
    "me-west1": ("IL", "以色列", "特拉维夫"),
    "me-central1": ("QA", "卡塔尔", "多哈"),
    "me-central2": ("SA", "沙特阿拉伯", "达曼"),
    "africa-south1": ("ZA", "南非", "约翰内斯堡"),
}

COLAB_SHORT_ZONES: dict[str, str] = {
    "usc1": "us-central1",
    "usc2": "us-central2",
    "use1": "us-east1",
    "use4": "us-east4",
    "use5": "us-east5",
    "uss1": "us-south1",
    "usw1": "us-west1",
    "usw2": "us-west2",
    "usw3": "us-west3",
    "usw4": "us-west4",
    "nane1": "northamerica-northeast1",
    "nane2": "northamerica-northeast2",
    "nas1": "northamerica-south1",
    "sae1": "southamerica-east1",
    "saw1": "southamerica-west1",
    "euw1": "europe-west1",
    "euw2": "europe-west2",
    "euw3": "europe-west3",
    "euw4": "europe-west4",
    "euw6": "europe-west6",
    "euw8": "europe-west8",
    "euw9": "europe-west9",
    "euw10": "europe-west10",
    "euw11": "europe-west11",
    "euw12": "europe-west12",
    "euc2": "europe-central2",
    "eun1": "europe-north1",
    "eun2": "europe-north2",
    "eusw1": "europe-southwest1",
    "ase1": "asia-east1",
    "ase2": "asia-east2",
    "asne1": "asia-northeast1",
    "ane1": "asia-northeast1",
    "asne2": "asia-northeast2",
    "ane2": "asia-northeast2",
    "asne3": "asia-northeast3",
    "ane3": "asia-northeast3",
    "asse1": "asia-southeast1",
    "asea1": "asia-southeast1",
    "asse2": "asia-southeast2",
    "asea2": "asia-southeast2",
    "asse3": "asia-southeast3",
    "asea3": "asia-southeast3",
    "asse4": "asia-southeast4",
    "asea4": "asia-southeast4",
    "ass1": "asia-south1",
    "aso1": "asia-south1",
    "ass2": "asia-south2",
    "aso2": "asia-south2",
    "ause1": "australia-southeast1",
    "ause2": "australia-southeast2",
    "mew1": "me-west1",
    "mec1": "me-central1",
    "mec2": "me-central2",
    "afs1": "africa-south1",
}

COUNTRY_NAMES_ZH: dict[str, str] = {
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

REGION_NAMES_ZH: dict[str, str] = {
    "iowa": "艾奥瓦",
    "oregon": "俄勒冈",
    "south carolina": "南卡罗来纳",
    "north carolina": "北卡罗来纳",
    "virginia": "弗吉尼亚",
    "ohio": "俄亥俄",
    "nevada": "内华达",
    "texas": "得克萨斯",
    "california": "加利福尼亚",
    "utah": "犹他",
    "washington": "华盛顿",
    "new york": "纽约",
    "new jersey": "新泽西",
    "illinois": "伊利诺伊",
    "georgia": "佐治亚",
    "florida": "佛罗里达",
    "arizona": "亚利桑那",
    "colorado": "科罗拉多",
    "quebec": "魁北克",
    "montreal": "蒙特利尔",
    "ontario": "安大略",
    "toronto": "多伦多",
    "changhua": "彰化",
    "changhua county": "彰化",
    "taipei": "台北",
    "new taipei": "新北",
    "taoyuan": "桃园",
    "kaohsiung": "高雄",
    "tokyo": "东京",
    "osaka": "大阪",
    "seoul": "首尔",
    "jakarta": "雅加达",
    "mumbai": "孟买",
    "maharashtra": "马哈拉施特拉",
    "delhi": "德里",
    "national capital territory of delhi": "德里",
    "sydney": "悉尼",
    "new south wales": "新南威尔士",
    "melbourne": "墨尔本",
    "victoria": "维多利亚",
    "groningen": "格罗宁根",
    "eemshaven": "埃姆斯哈文",
    "north holland": "北荷兰",
    "amsterdam": "阿姆斯特丹",
    "hainaut": "圣吉兰",
    "wallonia": "瓦隆",
    "saint-ghislain": "圣吉兰",
    "st. ghislain": "圣吉兰",
    "brussels": "布鲁塞尔",
    "hesse": "法兰克福",
    "frankfurt": "法兰克福",
    "berlin": "柏林",
    "ile-de-france": "巴黎",
    "île-de-france": "巴黎",
    "paris": "巴黎",
    "england": "伦敦",
    "london": "伦敦",
    "zurich": "苏黎世",
    "lombardy": "米兰",
    "milan": "米兰",
    "piedmont": "都灵",
    "turin": "都灵",
    "madrid": "马德里",
    "mazovia": "华沙",
    "warsaw": "华沙",
    "kymenlaakso": "哈米纳",
    "hamina": "哈米纳",
    "stockholm": "斯德哥尔摩",
    "sao paulo": "圣保罗",
    "são paulo": "圣保罗",
    "santiago": "圣地亚哥",
    "santiago metropolitan": "圣地亚哥",
    "tel aviv": "特拉维夫",
    "doha": "多哈",
    "dammam": "达曼",
    "johannesburg": "约翰内斯堡",
    "gauteng": "约翰内斯堡",
}

_FULL_ZONE_RE = re.compile(
    r"(?<![a-z0-9])("
    + "|".join(sorted(map(re.escape, GCP_REGIONS.keys()), key=len, reverse=True))
    + r")(?:-[a-f])?(?![a-z0-9])",
    re.IGNORECASE,
)

_SHORT_ZONE_RE = re.compile(
    r"(?:^|[-._/])("
    + "|".join(sorted(map(re.escape, COLAB_SHORT_ZONES.keys()), key=len, reverse=True))
    + r")(?:([a-f])\d*)?(?:$|[-._/])",
    re.IGNORECASE,
)


def location_from_zone(raw_zone: str) -> dict | None:
    if not raw_zone:
        return None
    match = _FULL_ZONE_RE.search(raw_zone.strip().lower())
    if not match:
        return None
    gcp_region = match.group(1).lower()
    full_zone = match.group(0).lower()
    country, country_name, region_name = GCP_REGIONS[gcp_region]
    return {
        "country": country,
        "country_name": country_name,
        "region": region_name,
        "zone": full_zone,
    }


def infer_location_from_endpoint(endpoint: str = "", url: str = "") -> dict | None:
    for candidate in (endpoint, url):
        if not candidate:
            continue
        direct = location_from_zone(candidate)
        if direct:
            return direct
        short = _SHORT_ZONE_RE.search(candidate)
        if short:
            gcp_region = COLAB_SHORT_ZONES[short.group(1).lower()]
            zone_letter = (short.group(2) or "").lower()
            zone = f"{gcp_region}-{zone_letter}" if zone_letter else gcp_region
            country, country_name, region_name = GCP_REGIONS[gcp_region]
            return {
                "country": country,
                "country_name": country_name,
                "region": region_name,
                "zone": zone,
            }
    return None


def _clean_subregion(raw: str, country_code: str, country_name: str) -> str:
    text = (raw or "").strip()
    if not text:
        return ""
    lowered = text.lower()
    if lowered in (
        country_code.lower(),
        country_name.lower(),
        "taiwan",
        "singapore",
        "hong kong",
        "macau",
        "macao",
    ):
        return ""
    translated = REGION_NAMES_ZH.get(lowered, text)
    if translated == country_name:
        return ""
    return translated[:32]


def normalize_geo_payload(
    country: str, region: str = "", city: str = "", zone: str = ""
) -> dict | None:
    code = (country or "").strip().upper()
    if code == "UK":
        code = "GB"
    if len(code) != 2 or not code.isalpha() or code in ("XX", "ZZ", "T1"):
        return None
    country_name = COUNTRY_NAMES_ZH.get(code, code)
    sub = _clean_subregion(region, code, country_name) or _clean_subregion(
        city, code, country_name
    )
    return {
        "country": code,
        "country_name": country_name,
        "region": sub,
        "zone": (zone or "").strip().lower()[:40],
    }


def probe_vm_location(http) -> dict | None:
    """Detect VM location using internal GCP metadata first, then lightweight HTTPS geo endpoints."""
    try:
        resp = http.get(
            "http://metadata.google.internal/computeMetadata/v1/instance/zone",
            headers={"Metadata-Flavor": "Google"},
            timeout=1.5,
        )
        if getattr(resp, "status_code", 0) == 200:
            loc = location_from_zone(str(resp.text))
            if loc:
                return loc
    except Exception:
        pass

    try:
        resp = http.get("https://ipinfo.io/json", timeout=3)
        if getattr(resp, "status_code", 0) == 200:
            data = resp.json()
            if isinstance(data, dict):
                loc = normalize_geo_payload(
                    str(data.get("country") or ""),
                    str(data.get("region") or ""),
                    str(data.get("city") or ""),
                )
                if loc:
                    return loc
    except Exception:
        pass

    try:
        resp = http.get("https://api.country.is/", timeout=3)
        if getattr(resp, "status_code", 0) == 200:
            data = resp.json()
            if isinstance(data, dict):
                loc = normalize_geo_payload(str(data.get("country") or ""))
                if loc:
                    return loc
    except Exception:
        pass

    try:
        resp = http.get("https://www.cloudflare.com/cdn-cgi/trace", timeout=3)
        if getattr(resp, "status_code", 0) == 200:
            for line in str(resp.text).splitlines():
                if line.startswith("loc="):
                    loc = normalize_geo_payload(line.partition("=")[2])
                    if loc:
                        return loc
    except Exception:
        pass

    return None
