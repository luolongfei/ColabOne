import type {
  ColabOneMigrationPreview,
  ColabOneMigrationUIPrefs,
} from "./api";
import {
  colabOnePrivacyDefault,
  setColabOnePrivacyActive,
  setColabOnePrivacyDefault,
} from "./colabOnePrivacy";
import {
  colabOneLocaleCode,
  normalizeColabOneLocale,
  setColabOneLocale,
  type ColabOneLocale,
} from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";

export type ColabOneMigrationAppearance = "system" | "light" | "dark";

export const COLABONE_MIGRATION_MODULES = [
  "account",
  "credentials",
  "settings",
  "library_workflows",
  "cu_usage",
  "workflow_drafts",
] as const;

export type ColabOneMigrationModuleId =
  (typeof COLABONE_MIGRATION_MODULES)[number];

export const COLABONE_MIGRATION_MIN_PASSWORD_LEN = 6;
export const COLABONE_MIGRATION_MAX_PASSWORD_BYTES = 512;

export type ColabOneExportPasswordError =
  | "tooShort"
  | "tooLong"
  | "mismatch"
  | null;

export function utf8ByteLength(value: string): number {
  if (typeof TextEncoder !== "undefined") {
    return new TextEncoder().encode(value).length;
  }
  return value.length;
}

export function validateColabOneExportPassword(
  password: string,
  confirmPassword: string,
): ColabOneExportPasswordError {
  if (
    Array.from(password).length < COLABONE_MIGRATION_MIN_PASSWORD_LEN ||
    password.trim().length === 0
  ) {
    return "tooShort";
  }
  if (utf8ByteLength(password) > COLABONE_MIGRATION_MAX_PASSWORD_BYTES) {
    return "tooLong";
  }
  if (password !== confirmPassword) {
    return "mismatch";
  }
  return null;
}

export function normalizeColabOneAppearancePref(
  value: string | null | undefined,
): ColabOneMigrationAppearance | null {
  const norm = (value || "").trim().toLowerCase();
  if (norm === "system" || norm === "light" || norm === "dark") {
    return norm;
  }
  return null;
}

export function collectColabOneMigrationUIPrefs(
  storage: Pick<Storage, "getItem"> | null | undefined = typeof window !==
  "undefined"
    ? window.localStorage
    : null,
  currentAppearance?: ColabOneMigrationAppearance,
): ColabOneMigrationUIPrefs {
  let appearance: ColabOneMigrationAppearance = currentAppearance || "system";
  if (!currentAppearance && storage) {
    try {
      const savedApp = normalizeColabOneAppearancePref(
        storage.getItem(COLABONE_STORAGE_KEYS.appearance),
      );
      if (savedApp) appearance = savedApp;
    } catch {
      // Ignore storage read failures.
    }
  }
  const prefs: ColabOneMigrationUIPrefs = {
    appearance,
    locale: colabOneLocaleCode.value,
    privacy_mask_default: Boolean(colabOnePrivacyDefault.value),
  };

  if (!storage) {
    return prefs;
  }

  try {
    const rawLayout = storage.getItem(COLABONE_STORAGE_KEYS.monitorLayout);
    if (rawLayout === "grid" || rawLayout === "rows" || rawLayout === "split") {
      prefs.monitor_layout = rawLayout === "split" ? "rows" : rawLayout;
    } else {
      prefs.monitor_layout = "grid";
    }
  } catch {
    // Ignore storage read failures.
  }

  try {
    const rawMetric = storage.getItem(COLABONE_STORAGE_KEYS.cuUsageMetric);
    if (
      rawMetric === "consumed" ||
      rawMetric === "balance" ||
      rawMetric === "rate"
    ) {
      prefs.cu_usage_metric = rawMetric;
    } else {
      prefs.cu_usage_metric = "consumed";
    }
  } catch {
    // Ignore storage read failures.
  }

  try {
    const rawWidth = storage.getItem(COLABONE_STORAGE_KEYS.logsWidth);
    if (rawWidth !== null && rawWidth !== "") {
      const parsed = Math.round(Number(rawWidth));
      if (Number.isFinite(parsed) && parsed >= 240 && parsed <= 1200) {
        prefs.logs_width = parsed;
      }
    }
  } catch {
    // Ignore storage read failures.
  }

  return prefs;
}

export interface ColabOneApplyUIPrefsCallbacks {
  storage?: Pick<Storage, "setItem"> | null;
  setAppearance?: (mode: ColabOneMigrationAppearance) => void;
  setLocale?: (locale: ColabOneLocale) => void;
  setPrivacyDefault?: (enabled: boolean) => void;
  setPrivacyActive?: (enabled: boolean) => void;
  setLogsWidth?: (width: number) => void;
}

export function applyColabOneMigrationUIPrefs(
  prefs: ColabOneMigrationUIPrefs | null | undefined,
  callbacks: ColabOneApplyUIPrefsCallbacks = {},
): void {
  if (!prefs) return;

  const storage =
    callbacks.storage !== undefined
      ? callbacks.storage
      : typeof window !== "undefined"
        ? window.localStorage
        : null;

  const nextAppearance = normalizeColabOneAppearancePref(prefs.appearance);
  if (nextAppearance) {
    if (callbacks.setAppearance) {
      callbacks.setAppearance(nextAppearance);
    } else {
      try {
        storage?.setItem(COLABONE_STORAGE_KEYS.appearance, nextAppearance);
      } catch {
        // Ignore storage write failures.
      }
    }
  }

  const nextLocale = normalizeColabOneLocale(prefs.locale);
  if (nextLocale) {
    if (callbacks.setLocale) {
      callbacks.setLocale(nextLocale);
    } else {
      // Go already updated native tray/window locale & settings.json.
      setColabOneLocale(nextLocale, { persist: true, syncNative: false });
    }
  }

  if (typeof prefs.privacy_mask_default === "boolean") {
    if (callbacks.setPrivacyDefault) {
      callbacks.setPrivacyDefault(prefs.privacy_mask_default);
    } else {
      setColabOnePrivacyDefault(prefs.privacy_mask_default);
    }
    if (callbacks.setPrivacyActive) {
      callbacks.setPrivacyActive(prefs.privacy_mask_default);
    } else {
      setColabOnePrivacyActive(prefs.privacy_mask_default);
    }
  }

  if (
    prefs.monitor_layout === "grid" ||
    prefs.monitor_layout === "rows" ||
    prefs.monitor_layout === "split"
  ) {
    const cleanLayout =
      prefs.monitor_layout === "split" ? "rows" : prefs.monitor_layout;
    try {
      storage?.setItem(COLABONE_STORAGE_KEYS.monitorLayout, cleanLayout);
    } catch {
      // Ignore storage write failures.
    }
  }

  if (
    prefs.cu_usage_metric === "consumed" ||
    prefs.cu_usage_metric === "balance" ||
    prefs.cu_usage_metric === "rate"
  ) {
    try {
      storage?.setItem(
        COLABONE_STORAGE_KEYS.cuUsageMetric,
        prefs.cu_usage_metric,
      );
    } catch {
      // Ignore storage write failures.
    }
  }

  if (
    typeof prefs.logs_width === "number" &&
    Number.isFinite(prefs.logs_width) &&
    prefs.logs_width >= 240 &&
    prefs.logs_width <= 1200
  ) {
    const cleanWidth = Math.round(prefs.logs_width);
    try {
      storage?.setItem(COLABONE_STORAGE_KEYS.logsWidth, String(cleanWidth));
    } catch {
      // Ignore storage write failures.
    }
    callbacks.setLogsWidth?.(cleanWidth);
  }
}

export function defaultImportModuleSelection(
  preview: ColabOneMigrationPreview | null | undefined,
): ColabOneMigrationModuleId[] {
  if (!preview?.modules?.length) return [];
  const selected: ColabOneMigrationModuleId[] = [];
  for (const mod of preview.modules) {
    if (!mod.included) continue;
    if (
      !COLABONE_MIGRATION_MODULES.includes(
        mod.id as ColabOneMigrationModuleId,
      )
    ) {
      continue;
    }
    if (mod.id === "account" && preview.has_running_instances) {
      continue;
    }
    selected.push(mod.id as ColabOneMigrationModuleId);
  }
  return selected;
}

export function formatMigrationFileSize(bytes?: number | null): string {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

export function formatMigrationSourceOS(os?: string | null): string {
  switch ((os || "").trim().toLowerCase()) {
    case "windows":
      return "Windows";
    case "darwin":
      return "macOS";
    case "linux":
      return "Linux";
    default:
      return os?.trim() || "Unknown";
  }
}
