import { computed, nextTick, ref, watch } from "vue";
import { ColabOneDesktop } from "./colabOneBridge";
import { COLABONE_STORAGE_KEYS } from "./storage";
import {
  runColabOneThemeTransition,
  type PointerLikeEvent,
  type ThemeTransitionPoint,
} from "./themeTransition";

export type ColabOneAppearance = "system" | "light" | "dark";
const media = window.matchMedia("(prefers-color-scheme: dark)");
const systemDark = ref(media.matches);
let syncingFromStorage = false;

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
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
] as const;

media.addEventListener("change", (event) => {
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
  const commitSync = () => {
    if (options?.fromStorage) {
      syncingFromStorage = true;
      try {
        colabOneAppearance.value = next;
      } finally {
        syncingFromStorage = false;
      }
    } else {
      colabOneAppearance.value = next;
    }
  };

  if (resolveColabOneAppearance(next) === colabOneEffectiveAppearance.value) {
    commitSync();
    return;
  }

  runColabOneThemeTransition({
    origin,
    commit: async () => {
      commitSync();
      await nextTick();
      await nextTick();
    },
  });
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

// Serialize native updates so rapid choices cannot leave the title bar behind.
let nativeUpdate = Promise.resolve();
watch(
  colabOneAppearance,
  (value) => {
    if (syncingFromStorage) return;
    try {
      localStorage.setItem(COLABONE_STORAGE_KEYS.appearance, value);
    } catch {
      /* Keep session choice. */
    }
    nativeUpdate = nativeUpdate
      .then(() => ColabOneDesktop.setAppearance(value))
      .catch(() => {
        /* Web previews have no native title bar. */
      });
  },
  { immediate: true, flush: "sync" },
);
