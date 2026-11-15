import { computed, ref, watch } from "vue";
import { Desktop } from "./mygo";

export type Appearance = "system" | "light" | "dark";
const key = "colabone.appearance";
const media = window.matchMedia("(prefers-color-scheme: dark)");
const systemDark = ref(media.matches);
function savedAppearance(): Appearance {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "dark") return value;
  } catch {
    /* Storage may be unavailable in a browser preview. */
  }
  return "system";
}
export const appearance = ref<Appearance>(savedAppearance());
export const effectiveAppearance = computed(() =>
  appearance.value === "system"
    ? systemDark.value
      ? "dark"
      : "light"
    : appearance.value,
);
export const appearanceOptions = [
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
] as const;

media.addEventListener("change", (event) => {
  systemDark.value = event.matches;
});
watch(
  effectiveAppearance,
  (value) => {
    document.documentElement.dataset.theme = value;
    document.documentElement.style.colorScheme = value;
  },
  { immediate: true, flush: "sync" },
);

// Serialize native updates so rapid choices cannot leave the title bar behind.
let nativeUpdate = Promise.resolve();
watch(
  appearance,
  (value) => {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* Keep session choice. */
    }
    nativeUpdate = nativeUpdate
      .then(() => Desktop.setAppearance(value))
      .catch(() => {
        /* Web previews have no native title bar. */
      });
  },
  { immediate: true, flush: "sync" },
);
