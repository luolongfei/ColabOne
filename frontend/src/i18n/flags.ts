// Ready-made 4:3 SVG country/region flags from `flag-icons` (MIT).
import cnFlagUrl from "flag-icons/flags/4x3/cn.svg";
import hkFlagUrl from "flag-icons/flags/4x3/hk.svg";
import jpFlagUrl from "flag-icons/flags/4x3/jp.svg";
import krFlagUrl from "flag-icons/flags/4x3/kr.svg";
import twFlagUrl from "flag-icons/flags/4x3/tw.svg";
import usFlagUrl from "flag-icons/flags/4x3/us.svg";

export type ColabOneLocale =
  | "zh-CN"
  | "en-US"
  | "zh-TW"
  | "zh-HK"
  | "ja-JP"
  | "ko-KR";

export interface ColabOneLocaleMeta {
  code: ColabOneLocale;
  nativeLabel: string;
  shortLabel: string;
  countryCode: string;
  flagUrl: string;
}

export const COLABONE_LOCALE_LIST: readonly ColabOneLocaleMeta[] = [
  {
    code: "zh-CN",
    nativeLabel: "简体中文",
    shortLabel: "简中",
    countryCode: "CN",
    flagUrl: cnFlagUrl,
  },
  {
    code: "en-US",
    nativeLabel: "English",
    shortLabel: "EN",
    countryCode: "US",
    flagUrl: usFlagUrl,
  },
  {
    code: "zh-TW",
    nativeLabel: "繁體中文（台灣）",
    shortLabel: "台灣",
    countryCode: "TW",
    flagUrl: twFlagUrl,
  },
  {
    code: "zh-HK",
    nativeLabel: "繁體中文（香港）",
    shortLabel: "香港",
    countryCode: "HK",
    flagUrl: hkFlagUrl,
  },
  {
    code: "ja-JP",
    nativeLabel: "日本語",
    shortLabel: "日本語",
    countryCode: "JP",
    flagUrl: jpFlagUrl,
  },
  {
    code: "ko-KR",
    nativeLabel: "한국어",
    shortLabel: "한국어",
    countryCode: "KR",
    flagUrl: krFlagUrl,
  },
] as const;

export const COLABONE_LOCALE_MAP: Record<ColabOneLocale, ColabOneLocaleMeta> = {
  "zh-CN": COLABONE_LOCALE_LIST[0],
  "en-US": COLABONE_LOCALE_LIST[1],
  "zh-TW": COLABONE_LOCALE_LIST[2],
  "zh-HK": COLABONE_LOCALE_LIST[3],
  "ja-JP": COLABONE_LOCALE_LIST[4],
  "ko-KR": COLABONE_LOCALE_LIST[5],
};
