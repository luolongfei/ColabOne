import { computed, nextTick, ref, watch } from "vue";
import { ColabOneDesktop } from "./colabOneBridge";
import { isColabOneDesktopRuntime } from "./colabOneWebConsole";
import { t } from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import {
  resolveThemeTransitionOrigin,
  runColabOneThemeTransition,
  type PointerLikeEvent,
  type ThemeTransitionPoint,
} from "./themeTransition";

export type ColabOneAppearance = "system" | "light" | "dark";
const media = window.matchMedia("(prefers-color-scheme: dark)");
const systemDark = ref(media.matches);
let syncingFromStorage = false;
let appearanceRequestSeq = 0;

function parseColabOneAppearance(value: string | null): ColabOneAppearance {
  if (value === "light" || value === "dark") return value;
  return "system";
}

function readSavedColabOneAppearance(): ColabOneAppearance {
  try {
    return parseColabOneAppearance(
      localStorage.getItem(COLABONE_STORAGE_KEYS.appearance),
    );
  } catch {
    /* Storage may be unavailable in a browser preview. */
  }
  return "system";
}

function currentViewport() {
  return {
    width: typeof window !== "undefined" ? window.innerWidth : 1280,
    height: typeof window !== "undefined" ? window.innerHeight : 800,
  };
}

async function queryNativeSystemDark(): Promise<boolean | null> {
  if (!isColabOneDesktopRuntime()) {
    return null;
  }
  try {
    const raw = await ColabOneDesktop.systemAppearance();
    if (raw === "dark") return true;
    if (raw === "light") return false;
  } catch {
    /* Web previews and mocked shells may not expose SystemAppearance. */
  }
  return null;
}

export const colabOneAppearance = ref<ColabOneAppearance>(
  readSavedColabOneAppearance(),
);

export const colabOneEffectiveAppearance = computed(() =>
  colabOneAppearance.value === "system"
    ? systemDark.value
      ? "dark"
      : "light"
    : colabOneAppearance.value,
);

export const COLABONE_APPEARANCE_OPTIONS = [
  {
    value: "system" as const,
    get label() {
      return t("appearance.system");
    },
  },
  {
    value: "light" as const,
    get label() {
      return t("appearance.light");
    },
  },
  {
    value: "dark" as const,
    get label() {
      return t("appearance.dark");
    },
  },
] as const;

// Serialize native updates so rapid choices cannot leave the title bar behind.
let nativeUpdate = Promise.resolve();

function persistAppearance(value: ColabOneAppearance, fromStorage?: boolean) {
  if (fromStorage) return;
  try {
    localStorage.setItem(COLABONE_STORAGE_KEYS.appearance, value);
  } catch {
    /* Keep session choice. */
  }
}

function syncNativeAppearance(value: ColabOneAppearance, fromStorage?: boolean) {
  if (fromStorage || !isColabOneDesktopRuntime()) return;
  nativeUpdate = nativeUpdate
    .then(() => ColabOneDesktop.setAppearance(value))
    .catch(() => {
      /* Web previews have no native title bar. */
    });
}

// Hydrate the real OS dark/light state on boot so WebView2's overridden
// PreferredColorScheme never masks the true system theme.
void queryNativeSystemDark().then((nativeDark) => {
  if (nativeDark !== null) {
    systemDark.value = nativeDark;
  }
});

media.addEventListener("change", (event) => {
  // When an explicit light/dark override is active, WebView2 echoes our own
  // PreferredColorScheme into prefers-color-scheme; ignore those echoes so
  // systemDark always tracks the real OS theme.
  if (colabOneAppearance.value !== "system") return;
  systemDark.value = event.matches;
});

export function resolveColabOneAppearance(
  mode: ColabOneAppearance,
): "light" | "dark" {
  return mode === "system" ? (systemDark.value ? "dark" : "light") : mode;
}

export function setColabOneAppearance(
  next: ColabOneAppearance,
  origin?: PointerLikeEvent | ThemeTransitionPoint | null,
  options?: { fromStorage?: boolean },
): void {
  const seq = ++appearanceRequestSeq;
  // Resolve pointer/button coordinates synchronously before any await since
  // DOM event.currentTarget becomes null after event dispatch finishes.
  const resolvedOrigin = origin
    ? resolveThemeTransitionOrigin(origin, currentViewport())
    : null;
  const fromStorage = Boolean(options?.fromStorage);

  const commitState = (nextSystemDark?: boolean) => {
    if (typeof nextSystemDark === "boolean") {
      systemDark.value = nextSystemDark;
    }
    if (fromStorage) {
      syncingFromStorage = true;
      try {
        colabOneAppearance.value = next;
      } finally {
        syncingFromStorage = false;
      }
    } else {
      colabOneAppearance.value = next;
    }
    persistAppearance(next, fromStorage);
  };

  const applyWithEffective = (nextSystemDark?: boolean) => {
    if (seq !== appearanceRequestSeq) return;
    const effectiveSystemDark =
      typeof nextSystemDark === "boolean" ? nextSystemDark : systemDark.value;
    const targetEffective: "light" | "dark" =
      next === "system" ? (effectiveSystemDark ? "dark" : "light") : next;

    if (targetEffective === colabOneEffectiveAppearance.value) {
      commitState(nextSystemDark);
      syncNativeAppearance(next, fromStorage);
      return;
    }

    runColabOneThemeTransition({
      origin: resolvedOrigin,
      commit: async () => {
        commitState(nextSystemDark);
        await nextTick();
        await nextTick();
      },
      onReady: () => {
        syncNativeAppearance(next, fromStorage);
      },
    });
  };

  if (next === "system") {
    void queryNativeSystemDark().then((nativeDark) => {
      applyWithEffective(nativeDark === null ? media.matches : nativeDark);
    });
    return;
  }

  applyWithEffective();
}

// Keep theme synchronized across the main window and standalone companion windows.
window.addEventListener("storage", (event) => {
  if (event.key !== COLABONE_STORAGE_KEYS.appearance) return;
  const next = parseColabOneAppearance(event.newValue);
  if (next === colabOneAppearance.value) return;
  setColabOneAppearance(next, { x: 0, y: 28 }, { fromStorage: true });
});

watch(
  colabOneEffectiveAppearance,
  (value) => {
    document.documentElement.dataset.theme = value;
    document.documentElement.style.colorScheme = value;
  },
  { immediate: true, flush: "sync" },
);

// Sync initial persisted choice to native window chrome once on startup.
if (!syncingFromStorage) {
  persistAppearance(colabOneAppearance.value, false);
  syncNativeAppearance(colabOneAppearance.value, false);
}
