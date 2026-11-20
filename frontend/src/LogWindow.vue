<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ConfigProvider as AConfigProvider } from "ant-design-vue";
import { PhCheck } from "@phosphor-icons/vue";
import { colabOneLocale, buildColabOneTheme } from "./colabOneTheme";
import { colabOneEffectiveAppearance } from "./appearance";
import LogDrawer from "./LogDrawer.vue";
import { colabOneCommand, colabOneSetLogDrawer, colabOneSnapshot, colabOneWriteClipboard } from "./api";
import { createColabOneSnapshot } from "./types";

const colabOneTheme = computed(() => buildColabOneTheme(colabOneEffectiveAppearance.value));
const state = ref(createColabOneSnapshot());
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
const currentActivity = computed(
  () =>
    state.value.operation?.title ||
    (task.value
      ? {
          download: "正在下载模型到远端",
          load: "正在装载模型到显存",
          public_chat: "正在处理对外 API 对话请求",
          public_responses: "正在处理对外 API Responses 请求",
          public_compact: "正在压缩对外 API 上下文",
          comfyui_start: "正在准备并启动远端 ComfyUI",
          comfyui_run: "正在远端运行 ComfyUI 工作流",
        }[task.value.kind] || "远端任务进行中"
      : ready.value
        ? "GPU 已就绪，等待操作"
        : "等待启动远端 GPU"),
);

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
    notify("已复制");
  } catch {
    try {
      await navigator.clipboard.writeText(text);
      notify("已复制");
    } catch {
      notify("复制失败，请手动复制");
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

onMounted(() => {
  document.title = "运行日志 · ColabOne";
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
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneLocale">
    <main class="co-log-window-root">
      <LogDrawer
        :open="true"
        :standalone="true"
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
