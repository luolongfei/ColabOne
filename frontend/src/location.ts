import { colabOneLocaleCode, t, type ColabOneLocale } from "./i18n";
import type { InstanceLocation } from "./types";

const COUNTRY_OVERRIDES_BY_LOCALE: Record<
  ColabOneLocale,
  Record<string, string>
> = {
  "zh-CN": {
    US: "美国",
    CA: "加拿大",
    MX: "墨西哥",
    BR: "巴西",
    CL: "智利",
    AR: "阿根廷",
    CO: "哥伦比亚",
    GB: "英国",
    UK: "英国",
    DE: "德国",
    FR: "法国",
    NL: "荷兰",
    BE: "比利时",
    CH: "瑞士",
    IT: "意大利",
    ES: "西班牙",
    PT: "葡萄牙",
    PL: "波兰",
    FI: "芬兰",
    SE: "瑞典",
    NO: "挪威",
    DK: "丹麦",
    IE: "爱尔兰",
    AT: "奥地利",
    CZ: "捷克",
    RO: "罗马尼亚",
    HU: "匈牙利",
    GR: "希腊",
    UA: "乌克兰",
    RU: "俄罗斯",
    TR: "土耳其",
    CN: "中国",
    TW: "中国台湾",
    HK: "中国香港",
    MO: "中国澳门",
    JP: "日本",
    KR: "韩国",
    SG: "新加坡",
    ID: "印度尼西亚",
    IN: "印度",
    AU: "澳大利亚",
    NZ: "新西兰",
    MY: "马来西亚",
    TH: "泰国",
    VN: "越南",
    PH: "菲律宾",
    IL: "以色列",
    QA: "卡塔尔",
    SA: "沙特阿拉伯",
    AE: "阿联酋",
    ZA: "南非",
    EG: "埃及",
  },
  "en-US": {
    US: "United States",
    GB: "United Kingdom",
    UK: "United Kingdom",
    TW: "Taiwan",
    HK: "Hong Kong",
    MO: "Macau",
    KR: "South Korea",
    JP: "Japan",
    CN: "China",
    SG: "Singapore",
  },
  "zh-TW": {
    US: "美國",
    GB: "英國",
    UK: "英國",
    TW: "台灣",
    HK: "香港",
    MO: "澳門",
    JP: "日本",
    KR: "韓國",
    CN: "中國",
    SG: "新加坡",
    NL: "荷蘭",
    DE: "德國",
    FR: "法國",
    BE: "比利時",
    AU: "澳洲",
    NZ: "紐西蘭",
    IT: "義大利",
    AE: "阿聯酋",
  },
  "zh-HK": {
    US: "美國",
    GB: "英國",
    UK: "英國",
    TW: "台灣",
    HK: "香港",
    MO: "澳門",
    JP: "日本",
    KR: "韓國",
    CN: "中國",
    SG: "新加坡",
    NL: "荷蘭",
    DE: "德國",
    FR: "法國",
    BE: "比利時",
    AU: "澳洲",
    NZ: "新西蘭",
    IT: "意大利",
    AE: "阿聯酋",
  },
  "ja-JP": {
    US: "アメリカ",
    GB: "イギリス",
    UK: "イギリス",
    TW: "台湾",
    HK: "香港",
    MO: "マカオ",
    JP: "日本",
    KR: "韓国",
    CN: "中国",
    SG: "シンガポール",
    DE: "ドイツ",
    FR: "フランス",
    NL: "オランダ",
    BE: "ベルギー",
    AU: "オーストラリア",
  },
  "ko-KR": {
    US: "미국",
    GB: "영국",
    UK: "영국",
    TW: "대만",
    HK: "홍콩",
    MO: "마카오",
    JP: "일본",
    KR: "대한민국",
    CN: "중국",
    SG: "싱가포르",
    DE: "독일",
    FR: "프랑스",
    NL: "네덜란드",
    BE: "벨기에",
    AU: "호주",
  },
};

const SUBREGION_TRANSLATIONS: Record<
  string,
  Partial<Record<ColabOneLocale, string>>
> = {
  艾奥瓦: {
    "en-US": "Iowa",
    "zh-TW": "愛荷華",
    "zh-HK": "愛荷華",
    "ja-JP": "アイオワ",
    "ko-KR": "아이오와",
  },
  俄克拉荷马: {
    "en-US": "Oklahoma",
    "zh-TW": "奧克拉荷馬",
    "zh-HK": "奧克拉荷馬",
    "ja-JP": "オクラホマ",
    "ko-KR": "오클라호마",
  },
  南卡罗来纳: {
    "en-US": "South Carolina",
    "zh-TW": "南卡羅來納",
    "zh-HK": "南卡羅來納",
    "ja-JP": "サウスカロライナ",
    "ko-KR": "사우스캐롤라이나",
  },
  弗吉尼亚: {
    "en-US": "Virginia",
    "zh-TW": "維吉尼亞",
    "zh-HK": "維珍尼亞",
    "ja-JP": "バージニア",
    "ko-KR": "버지니아",
  },
  俄亥俄: {
    "en-US": "Ohio",
    "zh-TW": "俄亥俄",
    "zh-HK": "俄亥俄",
    "ja-JP": "オハイオ",
    "ko-KR": "오하이오",
  },
  得克萨斯: {
    "en-US": "Texas",
    "zh-TW": "德克薩斯",
    "zh-HK": "德克薩斯",
    "ja-JP": "テキサス",
    "ko-KR": "텍사스",
  },
  俄勒冈: {
    "en-US": "Oregon",
    "zh-TW": "奧勒岡",
    "zh-HK": "俄勒岡",
    "ja-JP": "オレゴン",
    "ko-KR": "오리건",
  },
  加利福尼亚: {
    "en-US": "California",
    "zh-TW": "加州",
    "zh-HK": "加州",
    "ja-JP": "カリフォルニア",
    "ko-KR": "캘리포니아",
  },
  犹他: {
    "en-US": "Utah",
    "zh-TW": "猶他",
    "zh-HK": "猶他",
    "ja-JP": "ユタ",
    "ko-KR": "유타",
  },
  内华达: {
    "en-US": "Nevada",
    "zh-TW": "內華達",
    "zh-HK": "內華達",
    "ja-JP": "ネバダ",
    "ko-KR": "네바다",
  },
  彰化: {
    "en-US": "Changhua",
    "zh-TW": "彰化",
    "zh-HK": "彰化",
    "ja-JP": "彰化",
    "ko-KR": "창화",
  },
  东京: {
    "en-US": "Tokyo",
    "zh-TW": "東京",
    "zh-HK": "東京",
    "ja-JP": "東京",
    "ko-KR": "도쿄",
  },
  大阪: {
    "en-US": "Osaka",
    "zh-TW": "大阪",
    "zh-HK": "大阪",
    "ja-JP": "大阪",
    "ko-KR": "오사카",
  },
  首尔: {
    "en-US": "Seoul",
    "zh-TW": "首爾",
    "zh-HK": "首爾",
    "ja-JP": "ソウル",
    "ko-KR": "서울",
  },
  新加坡: {
    "en-US": "Singapore",
    "zh-TW": "新加坡",
    "zh-HK": "新加坡",
    "ja-JP": "シンガポール",
    "ko-KR": "싱가포르",
  },
  法兰克福: {
    "en-US": "Frankfurt",
    "zh-TW": "法蘭克福",
    "zh-HK": "法蘭克福",
    "ja-JP": "フランクフルト",
    "ko-KR": "프랑크푸르트",
  },
  伦敦: {
    "en-US": "London",
    "zh-TW": "倫敦",
    "zh-HK": "倫敦",
    "ja-JP": "ロンドン",
    "ko-KR": "런던",
  },
  圣吉斯兰: {
    "en-US": "St. Ghislain",
    "zh-TW": "聖吉斯蘭",
    "zh-HK": "聖吉斯蘭",
    "ja-JP": "サン＝ギスラン",
    "ko-KR": "생기슬랭",
  },
  荷兰: {
    "en-US": "Netherlands",
    "zh-TW": "荷蘭",
    "zh-HK": "荷蘭",
    "ja-JP": "オランダ",
    "ko-KR": "네덜란드",
  },
  巴黎: {
    "en-US": "Paris",
    "zh-TW": "巴黎",
    "zh-HK": "巴黎",
    "ja-JP": "パリ",
    "ko-KR": "파리",
  },
  悉尼: {
    "en-US": "Sydney",
    "zh-TW": "雪梨",
    "zh-HK": "悉尼",
    "ja-JP": "シドニー",
    "ko-KR": "시드니",
  },
  墨尔本: {
    "en-US": "Melbourne",
    "zh-TW": "墨爾本",
    "zh-HK": "墨爾本",
    "ja-JP": "メルボルン",
    "ko-KR": "멜버른",
  },
  蒙特利尔: {
    "en-US": "Montreal",
    "zh-TW": "蒙特婁",
    "zh-HK": "滿地可",
    "ja-JP": "モントリオール",
    "ko-KR": "몬트리올",
  },
  多伦多: {
    "en-US": "Toronto",
    "zh-TW": "多倫多",
    "zh-HK": "多倫多",
    "ja-JP": "トロント",
    "ko-KR": "토론토",
  },
  孟买: {
    "en-US": "Mumbai",
    "zh-TW": "孟買",
    "zh-HK": "孟買",
    "ja-JP": "ムンバイ",
    "ko-KR": "뭄바이",
  },
  德里: {
    "en-US": "Delhi",
    "zh-TW": "德里",
    "zh-HK": "德里",
    "ja-JP": "デリー",
    "ko-KR": "델리",
  },
  圣保罗: {
    "en-US": "São Paulo",
    "zh-TW": "聖保羅",
    "zh-HK": "聖保羅",
    "ja-JP": "サンパウロ",
    "ko-KR": "상파울루",
  },
};

const displayNamesCache = new Map<ColabOneLocale, Intl.DisplayNames | null>();

function getRegionDisplayNames(locale: ColabOneLocale): Intl.DisplayNames | null {
  if (displayNamesCache.has(locale)) {
    return displayNamesCache.get(locale) || null;
  }
  const instance =
    typeof Intl !== "undefined" && typeof Intl.DisplayNames === "function"
      ? new Intl.DisplayNames([locale], { type: "region" })
      : null;
  displayNamesCache.set(locale, instance);
  return instance;
}

export function countryLabel(loc?: InstanceLocation | null): string {
  if (!loc?.country) return "";
  const code = loc.country.trim().toUpperCase();
  const locale = colabOneLocaleCode.value;
  const overrides = COUNTRY_OVERRIDES_BY_LOCALE[locale];
  if (overrides && overrides[code]) return overrides[code];
  if (
    locale === "zh-CN" &&
    loc.country_name &&
    loc.country_name.toUpperCase() !== code
  ) {
    return loc.country_name;
  }
  const intlCode = code === "UK" ? "GB" : code;
  try {
    const formatted = getRegionDisplayNames(locale)?.of(intlCode);
    if (formatted && formatted.toUpperCase() !== intlCode) return formatted;
  } catch {
    // Fallback below
  }
  if (loc.country_name && loc.country_name.toUpperCase() !== code)
    return loc.country_name;
  return loc.country_name || code;
}

export function locationShort(loc?: InstanceLocation | null): string {
  return countryLabel(loc);
}

export function locationFull(loc?: InstanceLocation | null): string {
  const country = countryLabel(loc);
  if (!country) return "";
  const rawSub = (loc?.region || "").trim();
  const locale = colabOneLocaleCode.value;
  const sub =
    locale === "zh-CN"
      ? rawSub
      : SUBREGION_TRANSLATIONS[rawSub]?.[locale] || rawSub;
  if (!sub || sub === country) return country;
  return `${country} · ${sub}`;
}

export function locationTooltip(loc?: InstanceLocation | null): string {
  const full = locationFull(loc);
  if (!full) return "";
  return loc?.zone
    ? t("location.tooltipWithZone", { full, zone: loc.zone })
    : t("location.tooltipNoZone", { full });
}
