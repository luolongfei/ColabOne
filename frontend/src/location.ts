import type { InstanceLocation } from "./types";

const COUNTRY_OVERRIDES_ZH: Record<string, string> = {
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
};

const displayNames =
  typeof Intl !== "undefined" && typeof Intl.DisplayNames === "function"
    ? new Intl.DisplayNames(["zh-CN"], { type: "region" })
    : null;

export function countryLabel(loc?: InstanceLocation | null): string {
  if (!loc?.country) return "";
  const code = loc.country.trim().toUpperCase();
  if (COUNTRY_OVERRIDES_ZH[code]) return COUNTRY_OVERRIDES_ZH[code];
  if (loc.country_name && loc.country_name.toUpperCase() !== code)
    return loc.country_name;
  try {
    return displayNames?.of(code) || loc.country_name || code;
  } catch {
    return loc.country_name || code;
  }
}

export function locationShort(loc?: InstanceLocation | null): string {
  return countryLabel(loc);
}

export function locationFull(loc?: InstanceLocation | null): string {
  const country = countryLabel(loc);
  if (!country) return "";
  const sub = (loc?.region || "").trim();
  if (!sub || sub === country) return country;
  return `${country} · ${sub}`;
}

export function locationTooltip(loc?: InstanceLocation | null): string {
  const full = locationFull(loc);
  if (!full) return "";
  return loc?.zone ? `实例位置：${full} (${loc.zone})` : `实例位置：${full}`;
}
