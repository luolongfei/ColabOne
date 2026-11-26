<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import { colabOneLocaleCode, t, tb, tsrc } from "./i18n";
import {
  Button as AButton,
  Empty as AEmpty,
  Segmented as ASegmented,
  Space as ASpace,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhClock,
  PhCopy,
  PhSpeedometer,
  PhTerminalWindow,
  PhTrash,
  PhX,
} from "@phosphor-icons/vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import type { ColabOnePrivacyContext } from "./colabOnePrivacy";
import { formatTokenSpeed } from "./apiSpeed";
import type { ActivityEntry } from "./types";

const MIN_WIDTH = 320;
const MAX_WIDTH = 640;
const DEFAULT_WIDTH = 420;

const props = defineProps<{
  open: boolean;
  width?: number;
  standalone?: boolean;
  mobileOverlay?: boolean;
  loading?: boolean;
  logs: ActivityEntry[];
  currentActivity: string;
  running: boolean;
  privacyContext?: ColabOnePrivacyContext;
  activeSpeed?: {
    tokensPerSecond?: number | null;
    completionTokens?: number | null;
    estimated?: boolean;
  } | null;
  activeElapsed?: string;
}>();

const LOG_SKELETON_ROWS = [
  { srcW: 52, msgW: "86%" },
  { srcW: 44, msgW: "72%" },
  { srcW: 58, msgW: "92%" },
  { srcW: 48, msgW: "64%" },
  { srcW: 54, msgW: "78%" },
] as const;

const emit = defineEmits<{
  "update:width": [number];
  close: [];
  copy: [string];
  clear: [];
}>();

const logFollow = ref(true);
const logLevel = ref<"all" | "error">("all");
const logBody = ref<HTMLElement | null>(null);
const resizing = ref(false);

const logLevelOptions = computed(() => [
  { label: t("logs.levelAll"), value: "all" },
  { label: t("logs.levelErrorOnly"), value: "error" },
]);

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
  new Date(time * 1000).toLocaleTimeString(colabOneLocaleCode.value, {
    hour12: false,
  });

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
        (entry) =>
          `[${logTime(entry.time)}] [${tsrc(entry.source)}] ${tb(entry.message)}`,
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
    :class="{
      'is-resizing': resizing,
      'is-standalone': !!standalone,
      'is-mobile-overlay': !standalone && !!mobileOverlay,
    }"
    role="complementary"
    :aria-label="t('logs.panelAria')"
    :style="
      standalone || mobileOverlay
        ? undefined
        : { '--co-log-drawer-width': `${width || DEFAULT_WIDTH}px` }
    "
  >
    <div
      v-if="!standalone && !mobileOverlay"
      class="co-log-resizer"
      role="separator"
      aria-orientation="vertical"
      :aria-label="t('logs.resizeAria')"
      :title="t('logs.resizeTooltip')"
      @pointerdown="startResize"
      @dblclick="resetWidth"
    ></div>

    <div
      v-if="!standalone && mobileOverlay"
      class="co-log-mobile-bar"
    >
      <div class="co-log-mobile-title">
        <PhTerminalWindow :size="16" aria-hidden="true" />
        <span>{{ t("nav.logs") }}</span>
      </div>
      <AButton
        size="small"
        type="text"
        class="co-log-mobile-close"
        data-testid="log-drawer-mobile-close"
        :aria-label="t('common.close')"
        @click="emit('close')"
      >
        <template #icon><PhX :size="16" /></template>
      </AButton>
    </div>

    <!-- Main Log Panel Content (no redundant in-page window header row) -->
    <div class="co-log-drawer-body">
      <div class="co-log-status">
        <div class="co-log-status-head">
          <span class="co-eyebrow">{{ t("logs.currentStatus") }}</span>
          <div
            v-if="liveSpeedLabel || activeElapsed"
            class="co-log-status-tags"
          >
            <ATag
              v-if="activeElapsed"
              :bordered="false"
              class="co-log-live-elapsed co-mono"
              data-testid="log-drawer-elapsed"
            >
              <PhClock :size="12" aria-hidden="true" />
              <span>{{ t("task.elapsedLive", { duration: activeElapsed }) }}</span>
            </ATag>
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
        </div>
        <p class="co-log-status-text">
          <template v-if="loading && !logs.length">
            <ColabOneSkeleton width="66%" :height="16" variant="text" />
            <span class="co-sr-only">{{ t("common.loading") }}</span>
          </template>
          <template v-else>
            <ASpin v-if="running" size="small" />
            <span>
              <ColabOnePrivacyText
                :text="tb(currentActivity)"
                :context="props.privacyContext"
              />
            </span>
          </template>
        </p>
      </div>

      <div class="co-log-toolbar">
        <ASegmented
          size="small"
          :value="logLevel"
          :options="logLevelOptions"
          :aria-label="t('logs.levelAria')"
          @change="onLogLevelChange"
        />
        <ASpace :size="6">
          <AButton
            size="small"
            :aria-label="t('logs.copyAria')"
            :disabled="!logEntries.length"
            @click="copyLogs"
          >
            <template #icon>
              <PhCopy :size="14" />
            </template>
            {{ t("common.copy") }}
          </AButton>
          <AButton
            size="small"
            danger
            :aria-label="t('logs.clearAria')"
            :disabled="!logs.length"
            @click="emit('clear')"
          >
            <template #icon>
              <PhTrash :size="14" />
            </template>
            {{ t("common.clear") }}
          </AButton>
        </ASpace>
      </div>

      <div
        ref="logBody"
        class="co-log-feed"
        role="log"
        :aria-label="t('logs.feedAria')"
        aria-live="off"
        tabindex="0"
        @scroll="logScroll"
      >
        <div
          v-if="loading && !logEntries.length"
          class="co-log-skeleton-list"
          role="status"
          aria-busy="true"
        >
          <span class="co-sr-only">{{ t("common.loading") }}</span>
          <div
            v-for="(row, idx) in LOG_SKELETON_ROWS"
            :key="idx"
            class="co-log-entry co-log-entry-skeleton"
          >
            <div class="co-log-entry-meta">
              <ColabOneSkeleton
                :width="56"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="idx * 60"
              />
              <ColabOneSkeleton
                :width="row.srcW"
                :height="18"
                variant="tag"
                tone="subtle"
                :delay="idx * 60 + 35"
              />
            </div>
            <ColabOneSkeleton
              :width="row.msgW"
              :height="14"
              variant="text"
              :delay="idx * 60 + 70"
            />
          </div>
        </div>
        <AEmpty
          v-else-if="!logEntries.length"
          :description="
            logLevel === 'error' ? t('logs.emptyError') : t('logs.emptyAll')
          "
          class="co-log-empty"
        />
        <div
          v-for="entry in logEntries"
          v-else
          :key="entry.id"
          class="co-log-entry co-sk-reveal"
          :class="entry.level"
        >
          <div class="co-log-entry-meta">
            <time class="co-mono">{{ logTime(entry.time) }}</time>
            <ATag
              :color="entry.source === 'API' ? 'blue' : undefined"
              :bordered="false"
              class="co-log-src"
            >
              {{ tsrc(entry.source) }}
            </ATag>
            <ATag
              v-if="entry.level === 'error'"
              color="error"
              :bordered="false"
              class="co-log-src"
            >
              {{ t("logs.tagError") }}
            </ATag>
            <ATag
              v-else-if="entry.level === 'warning'"
              color="warning"
              :bordered="false"
              class="co-log-src"
            >
              {{ t("logs.tagWarn") }}
            </ATag>
          </div>
          <p class="co-log-entry-msg co-mono">
            <ColabOnePrivacyText
              :text="tb(entry.message)"
              :context="props.privacyContext"
            />
          </p>
        </div>
      </div>
    </div>

    <!-- Pinned Footer Bar -->
    <footer class="co-log-drawer-footer">
      <span class="co-muted">{{ t("logs.footerLimit") }}</span>
      <AButton
        size="small"
        :type="logFollow ? 'primary' : 'default'"
        :ghost="logFollow"
        @click="followLogs"
      >
        {{ logFollow ? t("logs.following") : t("logs.jumpLatest") }}
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

.co-log-status-tags {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.co-log-live-speed,
.co-log-live-elapsed {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
  padding-inline: 8px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-variant-numeric: tabular-nums;
}

.co-log-live-elapsed {
  color: var(--co-secondary-text);
  background: var(--co-subtle);
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

.co-log-skeleton-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-log-entry-skeleton {
  background: var(--co-skeleton-card-bg);
  pointer-events: none;
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

.co-log-mobile-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 50px;
  padding: 0 12px 0 16px;
  border-bottom: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  flex-shrink: 0;
}

.co-log-mobile-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-log-mobile-close {
  width: 34px;
  height: 34px;
  border-radius: 9999px;
  color: var(--co-secondary-text);
}

.co-log-drawer.is-mobile-overlay {
  position: fixed;
  inset: 0;
  width: 100vw;
  min-width: 0;
  max-width: 100vw;
  height: 100%;
  height: 100dvh;
  border-left: none;
  z-index: 990;
  padding-top: var(--co-safe-top, 0px);
  background: var(--co-canvas);
}

.co-log-drawer.is-mobile-overlay .co-log-drawer-body {
  padding: 12px;
}

.co-log-drawer.is-mobile-overlay .co-log-drawer-footer {
  height: auto;
  min-height: 48px;
  padding: 8px 14px calc(8px + var(--co-safe-bottom, 0px));
}

@media (max-width: 768px) {
  .co-log-drawer:not(.is-standalone) {
    position: fixed;
    inset: 0;
    width: 100vw;
    min-width: 0;
    max-width: 100vw;
    height: 100%;
    height: 100dvh;
    border-left: none;
    z-index: 990;
  }

  .co-log-resizer {
    display: none;
  }
}
</style>
