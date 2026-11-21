<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ConfigProvider as AConfigProvider } from "ant-design-vue";
import { PhCheck } from "@phosphor-icons/vue";
import { buildColabOneTheme } from "./colabOneTheme";
import { colabOneEffectiveAppearance } from "./appearance";
import {
  colabOneAntdLocale,
  colabOneLocaleCode,
  hydrateColabOneLocaleFromSettings,
  t,
  tb,
} from "./i18n";
import LogDrawer from "./LogDrawer.vue";
import { colabOneCommand, colabOneSetLogDrawer, colabOneSnapshot, colabOneWriteClipboard } from "./api";
import { createColabOneSnapshot } from "./types";

const colabOneTheme = computed(() =>
  buildColabOneTheme(colabOneEffectiveAppearance.value, colabOneLocaleCode.value),
);
const state = ref(createColabOneSnapshot());
const snapshotLoaded = ref(false);
const toast = ref("");

let stopped = false;
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let toastTimer: ReturnType<typeof setTimeout> | undefined;

const ready = computed(() => state.value.runtime.state === "ready");
const task = computed(() =>
  state.value.runtime.tasks?.find((t) => t.state === "running"),
);
const activeApiRequest = computed(
  () =>
    state.value.runtime.public_api?.recent_requests?.find(
      (req) => req.state === "running",
    ) || null,
);
const activeApiSpeed = computed(() =>
  activeApiRequest.value
    ? {
        tokensPerSecond: activeApiRequest.value.tokens_per_second,
        completionTokens: activeApiRequest.value.completion_tokens,
        estimated: activeApiRequest.value.estimated,
      }
    : null,
);
const currentActivity = computed(() => {
  if (state.value.operation?.title) return tb(state.value.operation.title);
  if (task.value) {
    const taskLabels: Record<string, string> = {
      download: t("task.download"),
      load: t("task.load"),
      public_chat: t("task.publicChat"),
      public_responses: t("task.publicResponses"),
      public_compact: t("task.publicCompact"),
      comfyui_start: t("task.comfyStart"),
      comfyui_run: t("task.comfyRun"),
    };
    return taskLabels[task.value.kind] || t("task.runningGeneric");
  }
  return ready.value ? t("task.gpuReadyIdle") : t("task.gpuWaitingStart");
});

function notify(message: string) {
  toast.value = message;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toast.value = "";
  }, 2400);
}

async function syncSnapshot() {
  try {
    state.value = await colabOneSnapshot();
    snapshotLoaded.value = true;
    hydrateColabOneLocaleFromSettings(state.value.settings?.locale);
  } catch {
    // Keep last known snapshot on transient poll error.
  }
}

async function pollLoop() {
  if (stopped) return;
  await syncSnapshot();
  if (!stopped) {
    pollTimer = setTimeout(pollLoop, 1000);
  }
}

async function copy(text: string) {
  try {
    await colabOneWriteClipboard(text);
    await navigator.clipboard?.writeText(text).catch(() => {});
    notify(t("common.copied"));
  } catch {
    try {
      await navigator.clipboard.writeText(text);
      notify(t("common.copied"));
    } catch {
      notify(t("common.copyFailedShort"));
    }
  }
}

async function clearLogs() {
  try {
    await colabOneCommand("logs.clear");
    await syncSnapshot();
  } catch {
    // Ignore transient clear error.
  }
}

function handleKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "j") {
    e.preventDefault();
    void colabOneSetLogDrawer(false, 420);
  }
}

watch(
  colabOneLocaleCode,
  () => {
    document.title = t("logs.windowTitle");
  },
  { immediate: true },
);

onMounted(() => {
  document.title = t("logs.windowTitle");
  void pollLoop();
  window.addEventListener("keydown", handleKeydown);
});

onUnmounted(() => {
  stopped = true;
  clearTimeout(pollTimer);
  clearTimeout(toastTimer);
  window.removeEventListener("keydown", handleKeydown);
});
</script>

<template>
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneAntdLocale">
    <main class="co-log-window-root">
      <LogDrawer
        :open="true"
        :standalone="true"
        :loading="!snapshotLoaded"
        :logs="state.logs || []"
        :current-activity="currentActivity"
        :running="!!(state.operation || task)"
        :active-speed="activeApiSpeed"
        @copy="copy"
        @clear="clearLogs"
      />

      <Transition name="co-toast">
        <div v-if="toast" class="co-toast" role="status">
          <PhCheck :size="16" weight="bold" />
          <span>{{ toast }}</span>
        </div>
      </Transition>
    </main>
  </AConfigProvider>
</template>

<style scoped>
.co-log-window-root {
  width: 100vw;
  height: 100vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: var(--co-canvas);
}

.co-toast {
  position: fixed;
  left: 50%;
  bottom: 62px;
  transform: translateX(-50%);
  z-index: 1200;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 9px 16px;
  border-radius: 12px;
  background: var(--co-elevated);
  color: var(--co-text);
  border: 1px solid var(--co-success-border);
  box-shadow: 0 10px 28px rgba(15, 23, 42, 0.14);
  font-size: 13px;
  font-weight: 550;
}

.co-toast svg {
  color: var(--co-success);
}

.co-toast-enter-active,
.co-toast-leave-active {
  transition:
    opacity 0.2s ease,
    transform 0.2s ease;
}

.co-toast-enter-from,
.co-toast-leave-to {
  opacity: 0;
  transform: translate(-50%, 8px);
}
</style>
