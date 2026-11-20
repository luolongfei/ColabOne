<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import {
  Button as AButton,
  Empty as AEmpty,
  Segmented as ASegmented,
  Space as ASpace,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import { PhCopy, PhSpeedometer, PhTrash } from "@phosphor-icons/vue";
import { formatTokenSpeed } from "./apiSpeed";
import type { ActivityEntry } from "./types";

const MIN_WIDTH = 320;
const MAX_WIDTH = 640;
const DEFAULT_WIDTH = 420;

const props = defineProps<{
  open: boolean;
  width?: number;
  standalone?: boolean;
  logs: ActivityEntry[];
  currentActivity: string;
  running: boolean;
  activeSpeed?: {
    tokensPerSecond?: number | null;
    completionTokens?: number | null;
    estimated?: boolean;
  } | null;
}>();

const emit = defineEmits<{
  "update:width": [number];
  copy: [string];
  clear: [];
}>();

const logFollow = ref(true);
const logLevel = ref<"all" | "error">("all");
const logBody = ref<HTMLElement | null>(null);
const resizing = ref(false);

const logLevelOptions = [
  { label: "全部", value: "all" },
  { label: "仅错误", value: "error" },
];

const logEntries = computed(() =>
  (props.logs || []).filter(
    (entry) => logLevel.value === "all" || entry.level === "error",
  ),
);

const liveSpeedLabel = computed(() => {
  const tps = props.activeSpeed?.tokensPerSecond;
  if (typeof tps !== "number" || tps <= 0) return "";
  return formatTokenSpeed(tps, !!props.activeSpeed?.estimated);
});

const logTime = (time: number) =>
  new Date(time * 1000).toLocaleTimeString("zh-CN", { hour12: false });

function onLogLevelChange(raw: unknown) {
  logLevel.value = raw === "error" ? "error" : "all";
}

watch(
  () => [props.logs?.at(-1)?.id, logLevel.value, props.open],
  async () => {
    if (!props.open || !logFollow.value) return;
    await nextTick();
    logBody.value?.scrollTo({ top: logBody.value.scrollHeight });
  },
);

function logScroll() {
  const el = logBody.value;
  if (el) {
    logFollow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 45;
  }
}

async function followLogs() {
  logFollow.value = true;
  await nextTick();
  logBody.value?.scrollTo({ top: logBody.value.scrollHeight });
}

function copyLogs() {
  emit(
    "copy",
    logEntries.value
      .map(
        (entry) => `[${logTime(entry.time)}] [${entry.source}] ${entry.message}`,
      )
      .join("\n"),
  );
}

let dragStartX = 0;
let dragStartWidth = DEFAULT_WIDTH;

function onResizeMove(e: PointerEvent) {
  const delta = dragStartX - e.clientX;
  const maxAllowed = Math.min(
    MAX_WIDTH,
    Math.max(MIN_WIDTH, Math.floor(window.innerWidth * 0.55)),
  );
  const next = Math.max(
    MIN_WIDTH,
    Math.min(maxAllowed, Math.round(dragStartWidth + delta)),
  );
  emit("update:width", next);
}

function stopResize() {
  if (!resizing.value) return;
  resizing.value = false;
  window.removeEventListener("pointermove", onResizeMove);
  window.removeEventListener("pointerup", stopResize);
  window.removeEventListener("pointercancel", stopResize);
  document.body.style.removeProperty("cursor");
  document.body.style.removeProperty("user-select");
}

function startResize(e: PointerEvent) {
  if (e.button !== 0 || props.standalone) return;
  e.preventDefault();
  dragStartX = e.clientX;
  dragStartWidth = props.width || DEFAULT_WIDTH;
  resizing.value = true;
  document.body.style.cursor = "col-resize";
  document.body.style.userSelect = "none";
  window.addEventListener("pointermove", onResizeMove);
  window.addEventListener("pointerup", stopResize);
  window.addEventListener("pointercancel", stopResize);
}

function resetWidth() {
  if (!props.standalone) {
    emit("update:width", DEFAULT_WIDTH);
  }
}

onUnmounted(() => {
  stopResize();
});
</script>

<template>
  <aside
    v-if="open"
    id="activity-panel"
    class="co-log-drawer co-activity-panel"
    :class="{ 'is-resizing': resizing, 'is-standalone': !!standalone }"
    role="complementary"
    aria-label="运行日志"
    :style="
      standalone ? undefined : { '--co-log-drawer-width': `${width || DEFAULT_WIDTH}px` }
    "
  >
    <div
      v-if="!standalone"
      class="co-log-resizer"
      role="separator"
      aria-orientation="vertical"
      aria-label="调整运行日志宽度"
      title="拖动调整宽度，双击恢复默认"
      @pointerdown="startResize"
      @dblclick="resetWidth"
    ></div>

    <!-- Main Log Panel Content (no redundant in-page window header row) -->
    <div class="co-log-drawer-body">
      <div class="co-log-status">
        <div class="co-log-status-head">
          <span class="co-eyebrow">当前状态</span>
          <ATag
            v-if="liveSpeedLabel"
            color="blue"
            :bordered="false"
            class="co-log-live-speed co-mono"
          >
            <PhSpeedometer :size="12" />
            <span>{{ liveSpeedLabel }}</span>
          </ATag>
        </div>
        <p class="co-log-status-text">
          <ASpin v-if="running" size="small" />
          <span>{{ currentActivity }}</span>
        </p>
      </div>

      <div class="co-log-toolbar">
        <ASegmented
          size="small"
          :value="logLevel"
          :options="logLevelOptions"
          aria-label="日志级别"
          @change="onLogLevelChange"
        />
        <ASpace :size="6">
          <AButton
            size="small"
            aria-label="复制日志"
            :disabled="!logEntries.length"
            @click="copyLogs"
          >
            <template #icon>
              <PhCopy :size="14" />
            </template>
            复制
          </AButton>
          <AButton
            size="small"
            danger
            aria-label="清空日志"
            :disabled="!logs.length"
            @click="emit('clear')"
          >
            <template #icon>
              <PhTrash :size="14" />
            </template>
            清空
          </AButton>
        </ASpace>
      </div>

      <div
        ref="logBody"
        class="co-log-feed"
        role="log"
        aria-label="实时运行记录"
        aria-live="off"
        tabindex="0"
        @scroll="logScroll"
      >
        <AEmpty
          v-if="!logEntries.length"
          :description="
            logLevel === 'error'
              ? '暂无错误日志'
              : '暂无运行日志，开始操作后将在此实时显示。'
          "
          class="co-log-empty"
        />
        <div
          v-for="entry in logEntries"
          :key="entry.id"
          class="co-log-entry"
          :class="entry.level"
        >
          <div class="co-log-entry-meta">
            <time class="co-mono">{{ logTime(entry.time) }}</time>
            <ATag
              :color="entry.source === 'API' ? 'blue' : undefined"
              :bordered="false"
              class="co-log-src"
            >
              {{ entry.source }}
            </ATag>
            <ATag
              v-if="entry.level === 'error'"
              color="error"
              :bordered="false"
              class="co-log-src"
            >
              错误
            </ATag>
            <ATag
              v-else-if="entry.level === 'warning'"
              color="warning"
              :bordered="false"
              class="co-log-src"
            >
              提醒
            </ATag>
          </div>
          <p class="co-log-entry-msg co-mono">{{ entry.message }}</p>
        </div>
      </div>
    </div>

    <!-- Pinned Footer Bar -->
    <footer class="co-log-drawer-footer">
      <span class="co-muted">最近 300 条 · 本次运行</span>
      <AButton
        size="small"
        :type="logFollow ? 'primary' : 'default'"
        :ghost="logFollow"
        @click="followLogs"
      >
        {{ logFollow ? "跟随最新" : "回到最新" }}
      </AButton>
    </footer>
  </aside>
</template>

<style scoped>
.co-log-drawer {
  position: relative;
  width: min(var(--co-log-drawer-width, 420px), 46vw);
  min-width: 300px;
  max-width: 640px;
  height: 100vh;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  background: var(--co-elevated);
  border-left: 1px solid var(--co-border);
  overflow: hidden;
  z-index: 2;
}

.co-log-drawer.is-standalone {
  width: 100vw;
  min-width: 0;
  max-width: none;
  height: 100vh;
  flex: 1 1 auto;
  border-left: none;
  background: var(--co-canvas);
}

.co-log-drawer-body,
.co-log-drawer-footer {
  animation: co-log-content-fade 0.11s ease-out;
}

@keyframes co-log-content-fade {
  from {
    opacity: 0.72;
  }
  to {
    opacity: 1;
  }
}

.co-log-resizer {
  position: absolute;
  top: 0;
  bottom: 0;
  left: -3px;
  width: 7px;
  cursor: col-resize;
  z-index: 5;
}

.co-log-resizer::after {
  content: "";
  position: absolute;
  top: 0;
  bottom: 0;
  left: 3px;
  width: 2px;
  background: transparent;
  transition: background-color 0.14s ease;
}

.co-log-resizer:hover::after,
.co-log-drawer.is-resizing .co-log-resizer::after {
  background: var(--co-accent);
}

.co-log-drawer-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  overflow: hidden;
}

.co-log-drawer.is-standalone .co-log-drawer-body {
  padding: 16px 18px 14px;
}

.co-log-status {
  padding: 12px 14px;
  border-radius: 14px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  box-shadow: var(--co-card-shadow);
  flex-shrink: 0;
}

.co-log-status-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.co-eyebrow {
  display: block;
  font-size: 12px;
  font-weight: 600;
  color: var(--co-muted);
}

.co-log-live-speed {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
  padding-inline: 8px;
}

.co-log-status-text {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
}

.co-log-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.co-log-feed {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px;
  border-radius: 14px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  box-shadow: var(--co-card-shadow);
}

.co-log-empty {
  margin: auto 0;
}

.co-log-entry {
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-log-entry.error {
  background: var(--co-danger-bg);
  border-color: var(--co-danger-border);
}

.co-log-entry-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--co-muted);
}

.co-log-src {
  margin-inline-end: 0;
  font-size: 12px;
  line-height: 18px;
  padding-inline: 8px;
}

.co-log-entry-msg {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--co-text);
  word-break: break-word;
  white-space: pre-wrap;
}

.co-log-drawer-footer {
  height: 48px;
  padding: 0 16px;
  border-top: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  flex-shrink: 0;
}

.co-log-drawer.is-standalone .co-log-drawer-footer {
  padding: 0 18px;
}
</style>
