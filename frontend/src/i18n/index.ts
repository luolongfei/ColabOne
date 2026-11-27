import { computed, ref } from "vue";
import type { Locale as AntdLocale } from "ant-design-vue/es/locale";
import enUSAntd from "ant-design-vue/es/locale/en_US";
import jaJPAntd from "ant-design-vue/es/locale/ja_JP";
import koKRAntd from "ant-design-vue/es/locale/ko_KR";
import zhCNAntd from "ant-design-vue/es/locale/zh_CN";
import zhHKAntd from "ant-design-vue/es/locale/zh_HK";
import zhTWAntd from "ant-design-vue/es/locale/zh_TW";
import {
  colabOneCommand,
  colabOneSetLocale,
  isColabOneDesktopRuntime,
  isColabOneOutputShareActive,
  isColabOneWorkflowShareActive,
} from "../api";
import { COLABONE_STORAGE_KEYS } from "../storage";
import {
  translateColabOneActivitySource,
  translateColabOneBackendText,
} from "./backendMessages";
import {
  COLABONE_LOCALE_LIST,
  COLABONE_LOCALE_MAP,
  type ColabOneLocale,
  type ColabOneLocaleMeta,
} from "./flags";
import { enUSMessages } from "./locales/en-US";
import { jaJPMessages } from "./locales/ja-JP";
import { koKRMessages } from "./locales/ko-KR";
import {
  zhCNMessages,
  type ColabOneMessageKey,
  type ColabOneMessages,
} from "./locales/zh-CN";
import { zhHKMessages } from "./locales/zh-HK";
import { zhTWMessages } from "./locales/zh-TW";

export {
  COLABONE_LOCALE_LIST,
  COLABONE_LOCALE_MAP,
  translateColabOneActivitySource,
  translateColabOneBackendText,
  type ColabOneLocale,
  type ColabOneLocaleMeta,
  type ColabOneMessageKey,
  type ColabOneMessages,
};

export const COLABONE_MESSAGES: Record<ColabOneLocale, ColabOneMessages> = {
  "zh-CN": zhCNMessages,
  "en-US": enUSMessages,
  "zh-TW": zhTWMessages,
  "zh-HK": zhHKMessages,
  "ja-JP": jaJPMessages,
  "ko-KR": koKRMessages,
};

function withColabOneEmptyLocale(
  base: AntdLocale,
  emptyText: string,
): AntdLocale {
  return {
    ...base,
    Empty: {
      ...(base.Empty ?? {}),
      description: emptyText,
    },
    Table: base.Table
      ? {
          ...base.Table,
          emptyText,
        }
      : base.Table,
  };
}

export const COLABONE_ANTD_LOCALES: Record<ColabOneLocale, AntdLocale> = {
  "zh-CN": withColabOneEmptyLocale(zhCNAntd, zhCNMessages["common.emptyData"]),
  "en-US": withColabOneEmptyLocale(enUSAntd, enUSMessages["common.emptyData"]),
  "zh-TW": withColabOneEmptyLocale(zhTWAntd, zhTWMessages["common.emptyData"]),
  "zh-HK": withColabOneEmptyLocale(zhHKAntd, zhHKMessages["common.emptyData"]),
  "ja-JP": withColabOneEmptyLocale(jaJPAntd, jaJPMessages["common.emptyData"]),
  "ko-KR": withColabOneEmptyLocale(koKRAntd, koKRMessages["common.emptyData"]),
};

export function normalizeColabOneLocale(
  raw: string | null | undefined,
): ColabOneLocale | null {
  const s = String(raw || "")
    .trim()
    .replace(/_/g, "-")
    .toLowerCase();
  if (!s) return null;
  if (
    s === "zh-hk" ||
    s === "zh-mo" ||
    s.startsWith("zh-hant-hk") ||
    s.startsWith("zh-hant-mo") ||
    s === "yue" ||
    s.startsWith("yue-")
  ) {
    return "zh-HK";
  }
  if (s === "zh-tw" || s.startsWith("zh-hant")) {
    return "zh-TW";
  }
  if (s === "zh" || s === "zh-cn" || s === "zh-sg" || s.startsWith("zh-hans")) {
    return "zh-CN";
  }
  if (s === "ja" || s.startsWith("ja-")) {
    return "ja-JP";
  }
  if (s === "ko" || s.startsWith("ko-")) {
    return "ko-KR";
  }
  if (s === "en" || s.startsWith("en-")) {
    return "en-US";
  }
  return null;
}

export function detectColabOneSystemLocale(
  languages?: readonly string[],
): ColabOneLocale {
  const candidates: string[] = [];
  if (languages && languages.length) {
    candidates.push(...languages);
  } else if (typeof navigator !== "undefined") {
    if (Array.isArray(navigator.languages) && navigator.languages.length) {
      candidates.push(...navigator.languages);
    }
    if (navigator.language) {
      candidates.push(navigator.language);
    }
  }
  for (const raw of candidates) {
    const matched = normalizeColabOneLocale(raw);
    if (matched) return matched;
  }
  return "en-US";
}

function resolveLocaleStorageKey(): string {
  if (isColabOneOutputShareActive()) {
    return COLABONE_STORAGE_KEYS.outputShareLocale;
  }
  if (isColabOneWorkflowShareActive()) {
    return COLABONE_STORAGE_KEYS.workflowShareLocale;
  }
  return COLABONE_STORAGE_KEYS.locale;
}

export function readStoredColabOneLocale(): ColabOneLocale | null {
  if (typeof window === "undefined") return null;
  try {
    return normalizeColabOneLocale(
      window.localStorage.getItem(resolveLocaleStorageKey()),
    );
  } catch {
    return null;
  }
}

export function resolveInitialColabOneLocale(): {
  locale: ColabOneLocale;
  isManual: boolean;
} {
  if (typeof window === "undefined") {
    return { locale: "zh-CN", isManual: false };
  }
  const stored = readStoredColabOneLocale();
  if (stored) {
    return { locale: stored, isManual: true };
  }
  return { locale: detectColabOneSystemLocale(), isManual: false };
}

const initialState = resolveInitialColabOneLocale();

export const colabOneLocaleCode = ref<ColabOneLocale>(initialState.locale);
export const colabOneLocaleIsManual = ref<boolean>(initialState.isManual);

export const colabOneLocaleMeta = computed<ColabOneLocaleMeta>(
  () => COLABONE_LOCALE_MAP[colabOneLocaleCode.value],
);

export const colabOneAntdLocale = computed<AntdLocale>(
  () => COLABONE_ANTD_LOCALES[colabOneLocaleCode.value],
);

function applyDocumentLang(locale: ColabOneLocale) {
  if (typeof document !== "undefined") {
    document.documentElement.lang = locale;
  }
}

applyDocumentLang(colabOneLocaleCode.value);

export function syncColabOneLocaleToNative(
  locale: ColabOneLocale,
  persist: boolean,
) {
  if (typeof window === "undefined") return;
  if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) {
    return;
  }
  try {
    void colabOneSetLocale(locale).catch(() => {});
  } catch {
    // Offline/browser preview without native bridge.
  }
  try {
    void colabOneCommand("locale.set", { locale, persist }).catch(() => {});
  } catch {
    // Offline/browser preview without backend bridge.
  }
}

if (typeof window !== "undefined") {
  // Sync initial active locale to Go tray/windows & Python OAuth callback on
  // desktop launch without firing unguarded HTTP POSTs on Web Console import.
  if (isColabOneDesktopRuntime()) {
    syncColabOneLocaleToNative(colabOneLocaleCode.value, false);
  }

  if (typeof window.addEventListener === "function") {
    window.addEventListener("storage", (event) => {
      if (event.key !== resolveLocaleStorageKey()) return;
      const synced = normalizeColabOneLocale(event.newValue);
      if (!synced) return;
      colabOneLocaleCode.value = synced;
      colabOneLocaleIsManual.value = true;
      applyDocumentLang(synced);
    });
  }
}

/**
 * Sets the active UI language. By default (`persist: true`), treats the change
 * as an explicit user preference stored in localStorage & desktop settings so
 * future launches never overwrite it with the OS language.
 */
export function setColabOneLocale(
  next: ColabOneLocale,
  options: { persist?: boolean; syncNative?: boolean } = {},
) {
  const persist = options.persist !== false;
  const syncNative = options.syncNative !== false;
  colabOneLocaleCode.value = next;
  if (persist) {
    colabOneLocaleIsManual.value = true;
    if (typeof window !== "undefined") {
      try {
        window.localStorage.setItem(resolveLocaleStorageKey(), next);
      } catch {
        // Ignore restricted storage contexts.
      }
    }
  }
  applyDocumentLang(next);
  if (syncNative) {
    syncColabOneLocaleToNative(next, persist);
  }
}

let nativeSyncedAfterSnapshot = false;

/**
 * Hydrates a previously saved manual locale from desktop settings.json when
 * localStorage has not yet recorded a manual preference (e.g. fresh WebView cache).
 * Never overwrites an explicit manual preference already present in localStorage.
 * On the first snapshot sync, also guarantees the active locale reaches Go/Python
 * once the desktop bridge is ready (without persisting OS auto-detected values).
 */
export function hydrateColabOneLocaleFromSettings(
  savedLocale: string | null | undefined,
) {
  if (isColabOneWorkflowShareActive() || isColabOneOutputShareActive()) return;
  const normalized = normalizeColabOneLocale(savedLocale);
  const hasManual = colabOneLocaleIsManual.value || Boolean(readStoredColabOneLocale());
  if (normalized && !hasManual) {
    nativeSyncedAfterSnapshot = true;
    setColabOneLocale(normalized, { persist: true, syncNative: true });
    return;
  }
  if (!nativeSyncedAfterSnapshot) {
    nativeSyncedAfterSnapshot = true;
    const needsPersist = hasManual && normalized !== colabOneLocaleCode.value;
    syncColabOneLocaleToNative(colabOneLocaleCode.value, needsPersist);
  }
}

export function t(
  key: ColabOneMessageKey,
  params?: Record<string, string | number>,
  localeOverride?: ColabOneLocale,
): string {
  const loc = localeOverride ?? colabOneLocaleCode.value;
  const template =
    COLABONE_MESSAGES[loc]?.[key] ?? COLABONE_MESSAGES["zh-CN"][key] ?? key;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name) =>
    Object.prototype.hasOwnProperty.call(params, name)
      ? String(params[name])
      : match,
  );
}

export function tb(text: string | null | undefined): string {
  return translateColabOneBackendText(text, colabOneLocaleCode.value);
}

export function tsrc(source: string | null | undefined): string {
  return translateColabOneActivitySource(source, colabOneLocaleCode.value);
}

export function useColabOneI18n() {
  return {
    locale: colabOneLocaleCode,
    localeMeta: colabOneLocaleMeta,
    antdLocale: colabOneAntdLocale,
    locales: COLABONE_LOCALE_LIST,
    setLocale: setColabOneLocale,
    t,
    tb,
    tsrc,
  };
}
