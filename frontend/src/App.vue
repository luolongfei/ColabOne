<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Checkbox as ACheckbox,
  Collapse as ACollapse,
  CollapsePanel as ACollapsePanel,
  ConfigProvider as AConfigProvider,
  Divider as ADivider,
  Empty as AEmpty,
  InputPassword as AInputPassword,
  Layout as ALayout,
  LayoutContent as ALayoutContent,
  Modal as AModal,
  Progress as AProgress,
  Segmented as ASegmented,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Spin as ASpin,
  Switch as ASwitch,
  Tag as ATag,
  Textarea as ATextarea,
  Tooltip as ATooltip,
  TypographyText as ATypographyText,
  TypographyTitle as ATypographyTitle,
} from "ant-design-vue";
import {
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhCaretRight,
  PhCheck,
  PhCloud,
  PhCopy,
  PhDesktop,
  PhFlowArrow,
  PhGearSix,
  PhGlobe,
  PhHardDrives,
  PhKey,
  PhMoon,
  PhPlay,
  PhPlus,
  PhSlidersHorizontal,
  PhStop,
  PhSun,
  PhTerminalWindow,
  PhUserCircle,
} from "@phosphor-icons/vue";
import rawAppIcon from "../../console/resources/icon.svg?raw";
import { colabOneLocale, buildColabOneTheme } from "./colabOneTheme";
import { COLABONE_STORAGE_KEYS } from "./storage";
import {
  colabOneAppearance,
  COLABONE_APPEARANCE_OPTIONS,
  colabOneEffectiveAppearance,
  setColabOneAppearance,
  type ColabOneAppearance,
} from "./appearance";
import ApiPanel from "./ApiPanel.vue";
import ComfyPanel from "./ComfyPanel.vue";
import BillingBar from "./BillingBar.vue";
import BillingCard from "./BillingCard.vue";
import LogDrawer from "./LogDrawer.vue";
import ResourceMonitor from "./ResourceMonitor.vue";
import FlagIcon from "./FlagIcon.vue";
import { locationFull, locationShort, locationTooltip } from "./location";
import { formatTokenCount, formatTokenSpeed } from "./apiSpeed";
import {
  COLABONE_IDLE_RELEASE_OPTIONS,
  describeColabOneBilling,
  formatCountdownClock,
  formatIdlePolicyLabel,
  friendlyErrorMessage,
  isTransientIpcError,
} from "./billing";
import {
  colabOneCommand,
  colabOneGetLogDrawerState,
  colabOneOpenURL,
  colabOneReadClipboard,
  colabOneSetLogDrawer,
  colabOneSnapshot,
  colabOneWriteClipboard,
} from "./api";
import { createColabOneSnapshot, COLABONE_STATUS_LABELS, type AccountInstance } from "./types";

// Trim the outer desktop-icon gutter so the white rounded tile fills inline UI icon slots edge-to-edge.
const appIcon = rawAppIcon.replace(
  'viewBox="0 0 1254 1254"',
  'viewBox="120 120 1014 1014"',
);

const colabOneTheme = computed(() => buildColabOneTheme(colabOneEffectiveAppearance.value));

const state = ref(createColabOneSnapshot()),
  page = ref("device"),
  ipcError = ref(""),
  ipcDisconnected = ref(false),
  actionError = ref(""),
  dismissedTaskIds = ref<Record<string, boolean>>({}),
  toast = ref(""),
  busy = ref(false),
  savingIdle = ref(false);
let ipcFailures = 0;

function formatError(
  raw: unknown,
  source: "ipc" | "action" | "state" = "action",
): string {
  const cleaned = String(raw ?? "")
    .trim()
    .replace(/^(?:Error|CallError|RuntimeError|TypeError|ValueError):\s*/i, "");
  if (!cleaned) return "";
  if (isTransientIpcError(cleaned)) {
    const isLocalTransport =
      source === "ipc" ||
      ipcDisconnected.value ||
      /failed to fetch|networkerror|ipc.*closed/i.test(cleaned);
    return isLocalTransport
      ? friendlyErrorMessage(cleaned)
      : "与远端实例的控制连接短暂中断，请稍候重试或刷新状态";
  }
  if (/mygo:\s*runtime not found/i.test(cleaned)) {
    return "桌面运行环境正在初始化，请稍候…";
  }
  if (/timed?\s*out/i.test(cleaned)) {
    return "请求响应超时，请检查网络或远端实例状态后重试";
  }
  return friendlyErrorMessage(cleaned);
}

const bannerError = computed(() => {
  if (ipcDisconnected.value && ipcError.value) return ipcError.value;
  if (actionError.value) return formatError(actionError.value, "action");
  if (state.value.error) return formatError(state.value.error, "state");
  if (ipcError.value) return ipcError.value;
  return "";
});

function showError(message: string) {
  actionError.value = formatError(message, "action");
}

function dismissError() {
  ipcError.value = "";
  actionError.value = "";
  if (state.value.error) {
    state.value.error = "";
    colabOneCommand("error.clear").catch(() => {});
  }
}

function dismissTaskError(taskId: string) {
  dismissedTaskIds.value = { ...dismissedTaskIds.value, [taskId]: true };
}

const visibleFailedTasks = computed(() =>
  (state.value.runtime.tasks || [])
    .filter((t) => t.state === "failed" && !dismissedTaskIds.value[t.id])
    .slice(-1),
);

const nav = [
  { id: "device", label: "远端设备", icon: PhHardDrives },
  { id: "api", label: "对外 API", icon: PhGlobe },
  { id: "comfyui", label: "ComfyUI", icon: PhFlowArrow },
  { id: "settings", label: "设置", icon: PhGearSix },
];

const modelsDrawerOpen = ref(false);
watch(page, (next) => {
  if (next !== "api") modelsDrawerOpen.value = false;
});

function toggleAppearance(event?: MouseEvent) {
  const next: ColabOneAppearance =
    colabOneEffectiveAppearance.value === "dark" ? "light" : "dark";
  setColabOneAppearance(next, event);
}

const logsOpen = ref(false);
const logWindowStandalone = ref(false);
const savedLogWidth = Number(
  localStorage.getItem(COLABONE_STORAGE_KEYS.logsWidth),
);
const logDrawerWidth = ref(
  Number.isFinite(savedLogWidth) && savedLogWidth >= 320 && savedLogWidth <= 640
    ? Math.round(savedLogWidth)
    : 420,
);
let logTransitionRunning = false;
let pendingLogTarget: boolean | null = null;

async function syncLogWindowState() {
  const res = await colabOneGetLogDrawerState();
  if (res.standalone) {
    logWindowStandalone.value = true;
    logsOpen.value = res.open;
  }
}

function handleLogWindowEvent(event: Event) {
  const detail = (event as CustomEvent<{ open?: boolean }>).detail;
  if (typeof detail?.open === "boolean") {
    logWindowStandalone.value = true;
    logsOpen.value = detail.open;
  }
}

async function applyLogWindowToggle(nextOpen: boolean) {
  const res = await colabOneSetLogDrawer(nextOpen, logDrawerWidth.value);
  logWindowStandalone.value = Boolean(res.standalone);
  logsOpen.value = res.standalone ? res.open : nextOpen;
}

async function setLogsOpen(nextOpen: boolean) {
  pendingLogTarget = nextOpen;
  if (logTransitionRunning) return;
  logTransitionRunning = true;
  try {
    while (pendingLogTarget !== null) {
      const target = pendingLogTarget;
      pendingLogTarget = null;
      await applyLogWindowToggle(target);
    }
  } finally {
    logTransitionRunning = false;
  }
}

function onLogDrawerWidthChange(width: number) {
  logDrawerWidth.value = width;
  localStorage.setItem(COLABONE_STORAGE_KEYS.logsWidth, String(width));
}

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

const instanceCount = computed(
  () => state.value.account.assignments ?? (state.value.runtime.id ? 1 : 0),
);
const billing = computed(() => (state.value.account.rate ?? 0) > 0);
const accountStale = computed(
  () =>
    !state.value.account.checked_at ||
    Date.now() / 1000 - state.value.account.checked_at > 60,
);
const releasePlan = ref<{
  id: string;
  instances: AccountInstance[];
} | null>(null);
const selectedReleaseIds = ref<string[]>([]);

const ready = computed(() => state.value.runtime.state === "ready");
const task = computed(() =>
  state.value.runtime.tasks?.find((t) => t.state === "running"),
);

const runtimeBadgeStatus = computed<"success" | "processing" | "default">(
  () => {
    if (
      ipcDisconnected.value ||
      ["starting", "releasing", "disconnected"].includes(
        state.value.runtime.state,
      )
    ) {
      return "processing";
    }
    return ready.value ? "success" : "default";
  },
);
const runtimeTagColor = computed<"success" | "processing" | "default">(() => {
  if (
    ipcDisconnected.value ||
    ["starting", "releasing", "disconnected"].includes(
      state.value.runtime.state,
    )
  ) {
    return "processing";
  }
  return ready.value ? "success" : "default";
});

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

const showRelease = ref(false),
  force = ref(false);

const activeVramModel = computed(() =>
  state.value.runtime.state === "stopped"
    ? undefined
    : state.value.models.find(
        (m) => m.loaded || m.id === state.value.runtime.loaded_model,
      ),
);

function isNavLive(id: string): boolean {
  if (id === "device") return ready.value;
  if (id === "api") return state.value.runtime.public_api?.state === "ready";
  if (id === "comfyui") return !!state.value.runtime.comfyui?.running;
  return false;
}
function navDotColor(id: string): string {
  if (id === "comfyui") return "#3b82f6";
  return "#10b981";
}

const downloadThreadOptions = [
  { label: "16 线程", value: 16 },
  { label: "32 线程", value: 32 },
];
const appearanceSegmentedOptions = COLABONE_APPEARANCE_OPTIONS.map((option) => ({
  value: option.value,
  title: option.label,
  payload: { label: option.label },
}));

const accountInstances = computed<AccountInstance[]>(() => {
  const raw = state.value.account.instances || [];
  if (raw.length) return raw;
  if (state.value.runtime.id && state.value.runtime.state !== "stopped") {
    return [
      {
        id: state.value.runtime.id,
        managed: true,
        active: true,
        accelerator: state.value.settings.gpu || "GPU",
        state: state.value.runtime.state,
        gpu_name: state.value.runtime.gpu?.name,
        vram_used_mib: state.value.runtime.gpu?.used_mib,
        vram_total_mib: state.value.runtime.gpu?.total_mib,
        disk_free: state.value.runtime.disk_free,
        loaded_model: activeVramModel.value?.name,
        active_task: task.value?.kind,
        drive_connected: state.value.setup.drive,
        location: state.value.runtime.location,
      },
    ];
  }
  return [];
});
const activeLocation = computed(() => {
  if (state.value.runtime.state === "stopped") return null;
  return (
    state.value.runtime.location ||
    accountInstances.value.find((i) => i.active && i.managed)?.location ||
    null
  );
});
const managedInstances = computed(() =>
  accountInstances.value.filter((i) => i.managed),
);
const externalInstances = computed(() =>
  accountInstances.value.filter((i) => !i.managed),
);
const shapeLabel = (shape?: string) =>
  shape === "HIGH_RAM" ? "高内存" : shape === "STANDARD" ? "标准内存" : "";
const taskKindLabel = (kind?: string) =>
  (kind &&
    {
      download: "模型下载中",
      load: "模型装载中",
      public_chat: "对外 API 推理中",
      public_responses: "对外 API Responses 推理中",
      public_compact: "对外 API 上下文压缩中",
      comfyui_start: "准备 ComfyUI 中",
      comfyui_run: "运行 ComfyUI 工作流中",
    }[kind]) ||
  "";
const taskBarTitle = (kind: string) =>
  ({
    download: "正在下载模型到远端",
    load: "正在装载模型到远端显存",
    public_chat: "正在处理对外 API 请求",
    public_responses: "正在处理对外 API Responses 请求",
    public_compact: "正在压缩对外 API 上下文",
    comfyui_start: "正在准备并启动远端 ComfyUI",
    comfyui_run: "正在远端运行 ComfyUI 工作流",
  })[kind] || "正在执行远端任务";

const GPU_SPECS = [
  { id: "T4", label: "T4 · 16 GB 显存（高性价比）" },
  { id: "L4", label: "L4 · 24 GB 显存（均衡进阶）" },
  { id: "A100", label: "A100 · 40/80 GB 显存（大模型 / ComfyUI）" },
  { id: "H100", label: "H100 · 80 GB 显存（旗舰算力）" },
  { id: "G4", label: "G4 · 96 GB 显存（RTX PRO 6000）" },
] as const;

const form = ref({
  gpu: "T4",
  idle_minutes: 30,
  idle_countdown_seconds: 60,
  auto_drive: true,
  download_threads: 16,
  hf_token: "",
});
const isPresetIdleMinutes = (m: number) =>
  COLABONE_IDLE_RELEASE_OPTIONS.some((opt) => opt.value === m);

const clockNow = ref(Date.now());
const billingOverview = computed(() =>
  describeColabOneBilling(
    state.value.account,
    state.value.runtime.state === "stopped"
      ? undefined
      : state.value.runtime.id,
    clockNow.value,
  ),
);

const code = ref(""),
  showAuthPaste = ref(false);

let timer: ReturnType<typeof setTimeout>,
  toastTimer: ReturnType<typeof setTimeout>,
  stopped = false;
let clockTimer: ReturnType<typeof setInterval>;
let lastInteraction = 0;

const idle = computed(() => state.value.runtime.idle);
const snapshotAt = ref(Date.now());
const idleRemaining = computed(() =>
  Math.max(
    0,
    Math.ceil(
      (idle.value?.remaining_seconds ?? 0) -
        (clockNow.value - snapshotAt.value) / 1000,
    ),
  ),
);
const countdown = computed(
  () =>
    idle.value &&
    idle.value.phase !== "active" &&
    idleRemaining.value <= idle.value.countdown_seconds,
);
const duration = (seconds: number | null) => {
  if (seconds === null) return "计算中";
  const whole = Math.ceil(Math.max(0, seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
};

function interaction(event?: Event) {
  if (event && !event.isTrusted) return;
  if (!ready.value || Date.now() - lastInteraction < 5000) return;
  lastInteraction = Date.now();
  colabOneCommand("runtime.activity").catch(() => {});
}

function handleGlobalKeydown(e: KeyboardEvent) {
  if (e.key === "Escape") {
    if (showRelease.value && !busy.value) {
      showRelease.value = false;
    } else if (
      !modelsDrawerOpen.value &&
      !logWindowStandalone.value &&
      logsOpen.value
    ) {
      void setLogsOpen(false);
    }
    return;
  }
  if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "j") {
    e.preventDefault();
    void setLogsOpen(!logsOpen.value);
    return;
  }
  if (
    e.altKey &&
    !e.ctrlKey &&
    !e.metaKey &&
    !e.shiftKey &&
    !modelsDrawerOpen.value &&
    !showRelease.value
  ) {
    const map: Record<string, string> = {
      "1": "device",
      "2": "api",
      "3": "comfyui",
      "4": "settings",
    };
    const targetPage = map[e.key];
    if (targetPage) {
      e.preventDefault();
      page.value = targetPage;
      return;
    }
  }
}

let initialized = false,
  openedAuth = "";
let lastSyncedSettings: Partial<typeof state.value.settings> = {};
function syncFormFromSettings(next: typeof state.value.settings | undefined) {
  if (!next) return;
  const keys = [
    "gpu",
    "idle_minutes",
    "idle_countdown_seconds",
    "auto_drive",
    "download_threads",
  ] as const;
  if (!initialized) {
    for (const key of keys) {
      if (next[key] !== undefined) {
        (form.value as Record<string, unknown>)[key] = next[key];
      }
    }
    lastSyncedSettings = { ...next };
    initialized = true;
    return;
  }
  for (const key of keys) {
    if (next[key] !== undefined && next[key] !== lastSyncedSettings[key]) {
      (form.value as Record<string, unknown>)[key] = next[key];
      (lastSyncedSettings as Record<string, unknown>)[key] = next[key];
    }
  }
}

const bytes = (n = 0) =>
  n >= 1024 ** 3
    ? (n / 1024 ** 3).toFixed(1) + " GB"
    : (n / 1024 ** 2).toFixed(0) + " MB";

function notify(text: string) {
  toast.value = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 3500);
}

async function syncSnapshot() {
  const value = await colabOneSnapshot();
  const wasDisconnected = ipcDisconnected.value;
  ipcFailures = 0;
  ipcDisconnected.value = false;
  if (ipcError.value) ipcError.value = "";
  if (
    actionError.value &&
    (isTransientIpcError(actionError.value) ||
      actionError.value === "与本地后台服务的连接短暂中断，正在自动重连…")
  ) {
    actionError.value = "";
  }
  if (wasDisconnected) clockNow.value = Date.now();
  if (
    value.error &&
    isTransientIpcError(value.error) &&
    (value.runtime.state === "ready" ||
      (value.runtime.state === "stopped" &&
        !value.operation &&
        (value.account.assignments ?? 0) === 0))
  ) {
    value.error = "";
    colabOneCommand("error.clear").catch(() => {});
  }
  if (value.runtime.idle?.server_time !== state.value.runtime.idle?.server_time)
    snapshotAt.value = Date.now();
  state.value = value;
  syncFormFromSettings(value.settings);
}

async function refresh() {
  try {
    await syncSnapshot();
  } catch (e) {
    ipcFailures += 1;
    if (ipcFailures >= 2) {
      ipcDisconnected.value = true;
      ipcError.value = formatError(e, "ipc");
    }
  }
  if (!stopped) timer = setTimeout(refresh, 600);
}

async function act(action: string, payload: Record<string, unknown> = {}) {
  busy.value = true;
  actionError.value = "";
  try {
    const res = await colabOneCommand(action, payload);
    await syncSnapshot().catch(() => {});
    return res;
  } catch (e) {
    actionError.value = formatError(e, "action");
    return null;
  } finally {
    busy.value = false;
  }
}

async function refreshAccount() {
  if (await act("account.refresh")) {
    notify("账户计费与实例状态已刷新");
  }
}

async function changeIdleMinutes(rawMinutes: unknown) {
  const minutes = Number(rawMinutes);
  if (!Number.isFinite(minutes) || minutes < 1) return;
  const previous = form.value.idle_minutes;
  form.value.idle_minutes = minutes;
  lastSyncedSettings.idle_minutes = minutes;
  actionError.value = "";
  savingIdle.value = true;
  try {
    await colabOneCommand("settings.save", { idle_minutes: minutes });
    await syncSnapshot().catch(() => {});
    notify(`已将空闲自动释放设为 ${formatIdlePolicyLabel(minutes)}`);
  } catch (e) {
    form.value.idle_minutes = previous;
    lastSyncedSettings.idle_minutes = previous;
    actionError.value = formatError(e, "action");
  } finally {
    savingIdle.value = false;
  }
}

async function changeDownloadThreads(rawThreads: unknown) {
  const threads = Number(rawThreads);
  if (threads !== 16 && threads !== 32) return;
  const previous = form.value.download_threads;
  form.value.download_threads = threads;
  lastSyncedSettings.download_threads = threads;
  actionError.value = "";
  try {
    await colabOneCommand("settings.save", { download_threads: threads });
    await syncSnapshot().catch(() => {});
    notify(`已将远端下载并发设为 ${threads} 线程`);
  } catch (e) {
    form.value.download_threads = previous;
    lastSyncedSettings.download_threads = previous;
    actionError.value = formatError(e, "action");
  }
}

function onGpuSelect(raw: unknown) {
  if (raw !== undefined && raw !== null) {
    form.value.gpu = String(raw);
  }
}

async function changeDefaultGpu(raw: unknown) {
  if (raw === undefined || raw === null) return;
  const gpu = String(raw);
  const previous = form.value.gpu;
  form.value.gpu = gpu;
  lastSyncedSettings.gpu = gpu;
  actionError.value = "";
  try {
    await colabOneCommand("settings.save", { gpu });
    await syncSnapshot().catch(() => {});
    notify(`已将默认 GPU 设为 ${gpu}`);
  } catch (e) {
    form.value.gpu = previous;
    lastSyncedSettings.gpu = previous;
    actionError.value = formatError(e, "action");
  }
}

function onAutoDriveChange(raw: unknown) {
  form.value.auto_drive = Boolean(raw);
  void save();
}

let lastAppearancePointer: { x: number; y: number; at: number } | null = null;

function rememberAppearancePointer(event: PointerEvent | MouseEvent) {
  if (event.clientX > 0 || event.clientY > 0) {
    lastAppearancePointer = {
      x: event.clientX,
      y: event.clientY,
      at: Date.now(),
    };
  }
}

function onAppearanceChange(raw: unknown) {
  const next = String(raw);
  if (next === "system" || next === "light" || next === "dark") {
    const origin =
      lastAppearancePointer && Date.now() - lastAppearancePointer.at < 600
        ? { x: lastAppearancePointer.x, y: lastAppearancePointer.y }
        : null;
    lastAppearancePointer = null;
    setColabOneAppearance(next as ColabOneAppearance, origin);
  }
}

function onAuthPasteToggle(keys: unknown) {
  showAuthPaste.value = Array.isArray(keys)
    ? keys.includes("paste")
    : keys === "paste";
}

async function beginLogin() {
  await act("auth.colab.auto");
}
async function manualLogin() {
  const result = await act("auth.colab.begin");
  if (result) {
    showAuthPaste.value = true;
    openedAuth = result.url;
    await colabOneOpenURL(result.url);
  }
}
async function finishLogin() {
  const reply = code.value.trim();
  if (!reply) return;
  if (await act("auth.colab.finish", { code: reply })) {
    code.value = "";
    notify("正在验证授权，请稍候");
  }
}
async function pasteLogin() {
  try {
    code.value = await colabOneReadClipboard();
    await finishLogin();
  } catch {
    actionError.value = "未能读取剪贴板，请直接粘贴到下方输入框";
  }
}
watch(
  () => state.value.auth_url,
  async (url) => {
    if (url && url !== openedAuth) {
      openedAuth = url;
      try {
        await colabOneOpenURL(url);
      } catch (e) {
        actionError.value = formatError(e, "action");
      }
    }
  },
);

async function save() {
  const payload: Record<string, unknown> = {
    gpu: form.value.gpu,
    idle_minutes: form.value.idle_minutes,
    auto_drive: form.value.auto_drive,
    download_threads: form.value.download_threads,
  };
  if (form.value.hf_token.trim()) {
    payload.hf_token = form.value.hf_token.trim();
  }
  if (await act("settings.save", payload)) {
    lastSyncedSettings = {
      ...lastSyncedSettings,
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
      download_threads: form.value.download_threads,
    };
    form.value.hf_token = "";
    notify("设置已保存");
  }
}

async function start() {
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
    })
  )
    await act("runtime.start");
}

async function startNew() {
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
    })
  )
    await act("runtime.start", { new: true, gpu: form.value.gpu });
}

async function switchInstance(endpoint: string) {
  if (!endpoint || endpoint === state.value.runtime.id) return;
  await act("runtime.select", { endpoint });
}

async function openRelease(onlyEndpoint?: string) {
  showRelease.value = true;
  force.value = false;
  releasePlan.value = null;
  selectedReleaseIds.value = [];
  const plan = await act("runtime.release.preview");
  releasePlan.value = plan;
  if (plan?.instances?.length) {
    if (
      onlyEndpoint &&
      plan.instances.some((item: AccountInstance) => item.id === onlyEndpoint)
    ) {
      selectedReleaseIds.value = [onlyEndpoint];
    } else {
      selectedReleaseIds.value = plan.instances.map(
        (item: AccountInstance) => item.id,
      );
    }
  }
}

const isAllReleaseSelected = computed(
  () =>
    !!releasePlan.value?.instances.length &&
    selectedReleaseIds.value.length === releasePlan.value.instances.length,
);

function toggleReleaseSelection(id: string, checked: boolean) {
  if (checked) {
    if (!selectedReleaseIds.value.includes(id)) {
      selectedReleaseIds.value = [...selectedReleaseIds.value, id];
    }
  } else {
    selectedReleaseIds.value = selectedReleaseIds.value.filter(
      (item) => item !== id,
    );
  }
}

function selectManagedReleaseOnly() {
  if (!releasePlan.value) return;
  selectedReleaseIds.value = releasePlan.value.instances
    .filter((item) => item.managed)
    .map((item) => item.id);
}

function selectAllRelease() {
  if (!releasePlan.value) return;
  selectedReleaseIds.value = releasePlan.value.instances.map((item) => item.id);
}

async function release() {
  if (!releasePlan.value || !selectedReleaseIds.value.length) return;
  const payload: Record<string, unknown> = {
    force: force.value,
    plan_id: releasePlan.value.id,
  };
  if (!isAllReleaseSelected.value) {
    payload.endpoints = [...selectedReleaseIds.value];
  }
  const result = await act("runtime.release.all", payload);
  if (result) showRelease.value = false;
}

function openModels() {
  page.value = "api";
  modelsDrawerOpen.value = true;
}

function openDevice() {
  modelsDrawerOpen.value = false;
  page.value = "device";
}

async function updateShortcut(
  target: "start_menu" | "desktop" | "both",
  remove = false,
) {
  const res = await act("desktop.shortcuts", { target, remove });
  if (res) {
    notify(
      remove
        ? "已移除快捷方式"
        : target === "desktop"
          ? "已创建桌面快捷方式"
          : target === "both"
            ? "已添加到开始菜单与桌面"
            : "已添加到开始菜单，打开后可在底部任务栏右键固定",
    );
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
      actionError.value = "复制失败，请手动选择并复制";
    }
  }
}

onMounted(() => {
  refresh();
  void syncLogWindowState();
  clockTimer = setInterval(() => {
    if (!ipcDisconnected.value) clockNow.value = Date.now();
  }, 1000);
  for (const name of ["pointerdown", "keydown", "wheel"])
    window.addEventListener(name, interaction, { passive: true });
  window.addEventListener("keydown", handleGlobalKeydown);
  window.addEventListener("colabone:log-window-state", handleLogWindowEvent);
});
onUnmounted(() => {
  stopped = true;
  clearInterval(clockTimer);
  for (const name of ["pointerdown", "keydown", "wheel"])
    window.removeEventListener(name, interaction);
  window.removeEventListener("keydown", handleGlobalKeydown);
  window.removeEventListener("colabone:log-window-state", handleLogWindowEvent);
  clearTimeout(timer);
  clearTimeout(toastTimer);
});
</script>

<template>
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneLocale">
    <ALayout
      class="co-shell co-app-shell"
      :class="{ 'co-logs-open': logsOpen }"
    >
      <!-- Main Workspace -->
      <ALayout class="co-workspace">
        <!-- ColabOne Top Floating Capsule Navbar -->
        <header class="co-navbar">
          <div class="co-navbar-inner">
            <div class="co-nav-left">
              <button
                type="button"
                class="co-brand"
                aria-label="返回远端设备首页"
                @click="page = 'device'"
              >
                <span
                  class="co-brand-icon"
                  aria-hidden="true"
                  v-html="appIcon"
                ></span>
                <span class="co-brand-text">ColabOne</span>
              </button>
            </div>

            <div class="co-nav-center">
              <nav
                class="co-nav-pill-track"
                aria-label="主导航"
                role="menubar"
              >
                <button
                  v-for="(item, idx) in nav"
                  :key="item.id"
                  type="button"
                  role="menuitem"
                  class="co-nav-pill-item"
                  :class="{ 'is-active': page === item.id }"
                  :aria-current="page === item.id ? 'page' : undefined"
                  :aria-label="item.label"
                  :title="`${item.label} (Alt+${idx + 1})`"
                  @click="page = item.id"
                >
                  <component
                    :is="item.icon"
                    :size="16"
                    class="co-nav-pill-icon"
                    aria-hidden="true"
                  />
                  <span class="co-nav-pill-label">{{ item.label }}</span>
                  <span
                    v-if="isNavLive(item.id)"
                    class="co-nav-live-dot"
                    :style="{ backgroundColor: navDotColor(item.id) }"
                    aria-hidden="true"
                  ></span>
                </button>
              </nav>
            </div>

            <div class="co-nav-right">
              <ATooltip
                :title="
                  accountStale
                    ? '状态超过 60 秒未同步，点击刷新账户实例与计费'
                    : '刷新账户实例与计费状态'
                "
              >
                <button
                  type="button"
                  class="co-nav-circle-btn"
                  aria-label="刷新账户实例"
                  :disabled="!state.setup.colab || busy || !!state.operation"
                  @click="refreshAccount"
                >
                  <PhArrowsClockwise :size="17" />
                </button>
              </ATooltip>

              <ATooltip
                :title="
                  colabOneEffectiveAppearance === 'dark'
                    ? '切换到浅色模式'
                    : '切换到深色模式'
                "
              >
                <button
                  type="button"
                  class="co-nav-circle-btn"
                  :aria-label="
                    colabOneEffectiveAppearance === 'dark'
                      ? '切换到浅色模式'
                      : '切换到深色模式'
                  "
                  @click="toggleAppearance($event)"
                >
                  <PhSun v-if="colabOneEffectiveAppearance === 'dark'" :size="17" />
                  <PhMoon v-else :size="17" />
                </button>
              </ATooltip>

              <ATooltip
                :title="
                  logsOpen ? '关闭运行日志 (Ctrl+J)' : '打开运行日志 (Ctrl+J)'
                "
              >
                <button
                  type="button"
                  class="co-logs-trigger"
                  :class="{ 'is-active': logsOpen }"
                  :aria-label="logsOpen ? '收起运行日志' : '展开运行日志'"
                  :aria-expanded="logsOpen"
                  aria-controls="activity-panel"
                  @click="setLogsOpen(!logsOpen)"
                >
                  <PhTerminalWindow :size="16" aria-hidden="true" />
                  <span class="co-logs-trigger-label">运行日志</span>
                </button>
              </ATooltip>
            </div>
          </div>
        </header>

        <!-- Slim Runtime & Billing Status Sub-bar -->
        <div class="co-status-bar">
          <div class="co-status-bar-inner">
            <div class="co-topbar-left">
              <ATag :color="runtimeTagColor" class="co-runtime-tag">
                <ABadge :status="runtimeBadgeStatus" />
                <span>{{
                  ipcDisconnected
                    ? "本地服务重连中"
                    : COLABONE_STATUS_LABELS[state.runtime.state] || state.runtime.state
                }}</span>
              </ATag>

              <span class="co-gpu-name co-connection-gpu">
                {{
                  state.runtime.state === "stopped"
                    ? `预选 ${form.gpu}`
                    : state.runtime.gpu?.name || form.gpu
                }}
              </span>

              <ATooltip
                v-if="
                  activeLocation?.country && state.runtime.state !== 'stopped'
                "
                :title="locationTooltip(activeLocation)"
              >
                <ATag class="co-location-tag co-location-chip">
                  <FlagIcon :code="activeLocation.country" :size="14" />
                  <span>{{ locationShort(activeLocation) }}</span>
                </ATag>
              </ATooltip>

              <ATooltip
                v-if="activeVramModel && page !== 'api'"
                :title="`已装载 ${activeVramModel.name}，点击前往对外 API`"
              >
                <AButton
                  size="small"
                  class="co-model-chip co-topbar-model-chip"
                  @click="page = 'api'"
                >
                  <ABadge status="success" />
                  <span class="co-model-chip-text">{{
                    activeVramModel.name
                  }}</span>
                </AButton>
              </ATooltip>

              <ASelect
                v-if="managedInstances.length > 1"
                size="small"
                class="co-instance-select co-topbar-instance-select"
                aria-label="当前工作实例"
                :value="state.runtime.id || state.active_endpoint"
                :disabled="busy || !!state.operation"
                :dropdown-match-select-width="false"
                @change="(val) => switchInstance(String(val ?? ''))"
              >
                <ASelectOption
                  v-for="inst in managedInstances"
                  :key="inst.id"
                  :value="inst.id"
                >
                  {{ inst.accelerator || "GPU" }} ·
                  {{
                    inst.location?.country
                      ? `${locationShort(inst.location)} · `
                      : ""
                  }}{{ inst.id }}
                  {{ inst.loaded_model ? `(${inst.loaded_model})` : "" }}
                </ASelectOption>
              </ASelect>
            </div>

            <div class="co-topbar-right co-header-right">
              <BillingBar
                :account="state.account"
                :runtime-state="state.runtime.state"
                :runtime-id="state.runtime.id"
                :idle="idle"
                :idle-remaining="idleRemaining"
                :idle-minutes="form.idle_minutes"
                :now-ms="clockNow"
                @touch="interaction()"
              />
            </div>
          </div>
        </div>

        <!-- Global Status & Error Banners -->
        <div
          v-if="
            (ready && (state.runtime.protocol || 0) < 7) ||
            bannerError ||
            state.operation ||
            visibleFailedTasks.length
          "
          class="co-banners"
        >
          <div class="co-banners-inner">
            <AAlert
              v-if="ready && (state.runtime.protocol || 0) < 7"
              type="info"
              show-icon
              message="当前实例需要更新远端服务，以启用实时硬件监控、对外 API 与 ComfyUI 工作流引擎；请先结束任务并卸载模型，模型缓存会保留。"
            >
              <template #action>
                <AButton
                  type="primary"
                  size="small"
                  :disabled="
                    !!task || !!state.runtime.loaded_model || !!state.operation
                  "
                  @click="act('runtime.upgrade')"
                >
                  更新远端服务
                </AButton>
              </template>
            </AAlert>

            <AAlert
              v-if="bannerError"
              type="error"
              show-icon
              closable
              :message="bannerError"
              @close="dismissError"
            />

            <AAlert
              v-if="state.operation"
              type="info"
              :show-icon="false"
              class="co-op-banner"
            >
              <template #message>
                <span class="co-op-message" role="status">
                  <ASpin size="small" />
                  <span>{{ state.operation.title }}</span>
                </span>
              </template>
            </AAlert>

            <AAlert
              v-for="failed in visibleFailedTasks"
              :key="failed.id"
              type="error"
              show-icon
              closable
              :message="formatError(failed.error, 'state')"
              @close="dismissTaskError(failed.id)"
            />
          </div>
        </div>

        <!-- Scrollable Workspace Content -->
        <ALayoutContent class="co-content">
          <!-- STAGE 1: 远端设备 -->
          <main
            v-if="page === 'device'"
            key="device"
            class="co-page-shell co-page-motion co-device-page"
          >
            <div class="co-page-inner">
              <div class="co-page-header">
                <div>
                  <h1 class="co-page-title">远端设备</h1>
                  <p class="co-page-subtitle">
                    按需启动远端 GPU 实例，实时掌握余额消耗与硬件负载。
                  </p>
                </div>
                <ATag color="blue" :bordered="false">Google Colab</ATag>
              </div>

              <!-- Unauthenticated Google Callout -->
              <ACard
                v-if="!state.setup.colab"
                class="co-setup-callout co-setup-callout"
              >
                <div class="co-callout-row">
                  <div class="co-callout-icon" aria-hidden="true">
                    <PhUserCircle :size="26" weight="duotone" />
                  </div>
                  <div class="co-callout-copy">
                    <h3>连接你的 Google 账户</h3>
                    <p>
                      登录授权一次即可按需启动远端 GPU，凭据安全保存在本机系统凭据库。
                    </p>
                  </div>
                  <ASpace :size="8" wrap>
                    <AButton
                      type="primary"
                      :disabled="busy || !!state.operation"
                      @click="beginLogin"
                    >
                      <template #icon>
                        <PhArrowSquareOut :size="15" />
                      </template>
                      登录 Google
                    </AButton>
                    <AButton @click="page = 'settings'">
                      <span>更多设置</span>
                      <PhCaretRight :size="14" />
                    </AButton>
                  </ASpace>
                </div>
              </ACard>

              <!-- Hero Grid: GPU Control + Billing Overview (side-by-side on wide fullscreen) -->
              <div class="co-device-hero-grid">
                <!-- Primary GPU Control Card -->
                <ACard class="co-device-card co-device-panel">
                <div class="co-device-head co-device-title">
                  <div class="co-device-icon" aria-hidden="true">
                    <PhHardDrives :size="26" weight="duotone" />
                  </div>
                  <div class="co-device-title-main">
                    <h2>{{ state.runtime.gpu?.name || "Colab 远端 GPU" }}</h2>
                    <div class="co-device-subtitle">
                      <template v-if="ready && activeVramModel">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>已装载 {{ activeVramModel.name }}</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="page = 'api'"
                        >
                          前往对外 API →
                        </AButton>
                      </template>
                      <template v-else-if="ready && state.runtime.comfyui?.running">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>ComfyUI 工作流引擎运行中</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="page = 'comfyui'"
                        >
                          前往 ComfyUI →
                        </AButton>
                      </template>
                      <template v-else-if="ready">
                        <code v-if="state.runtime.id" class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                        <span>GPU 已就绪，尚未装载模型</span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="openModels"
                        >
                          去选择模型 →
                        </AButton>
                      </template>
                      <template v-else-if="state.runtime.state === 'starting'">
                        <span>正在分配并初始化 {{ form.gpu }} 远端运行环境…</span>
                      </template>
                      <template v-else-if="state.runtime.id">
                        <code class="co-runtime-id">{{
                          state.runtime.id
                        }}</code>
                      </template>
                      <template v-else>
                        <span>尚未分配运行实例 · 启动后开始计费，停止后立即释放</span>
                      </template>
                    </div>
                  </div>
                  <div class="co-device-badges co-device-badges">
                    <ATooltip
                      v-if="
                        activeLocation?.country &&
                        state.runtime.state !== 'stopped'
                      "
                      :title="locationTooltip(activeLocation)"
                    >
                      <ATag class="co-location-tag">
                        <FlagIcon :code="activeLocation.country" :size="14" />
                        <span>{{ locationFull(activeLocation) }}</span>
                      </ATag>
                    </ATooltip>
                    <ATag
                      v-if="ready && state.setup.drive"
                      color="success"
                      :bordered="false"
                    >
                      <PhCloud :size="13" />
                      <span>云盘已挂载</span>
                    </ATag>
                    <ATag :color="ready ? 'success' : 'default'">
                      {{ COLABONE_STATUS_LABELS[state.runtime.state] || state.runtime.state }}
                    </ATag>
                  </div>
                </div>

                <ADivider class="co-card-divider" />

                <div class="co-device-controls co-device-controls">
                  <div class="co-device-fields">
                    <div class="co-control-field">
                      <span class="co-field-label">GPU 型号</span>
                      <ASelect
                        :value="form.gpu"
                        aria-label="GPU 型号"
                        class="co-select-gpu"
                        :disabled="busy || !!state.operation"
                        @change="onGpuSelect"
                      >
                        <ASelectOption
                          v-for="g in GPU_SPECS"
                          :key="g.id"
                          :value="g.id"
                        >
                          {{ g.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>

                    <div class="co-control-field">
                      <span class="co-field-label">空闲自动释放</span>
                      <ASelect
                        :value="form.idle_minutes"
                        aria-label="空闲自动释放时长"
                        class="co-select-idle"
                        :disabled="savingIdle"
                        @change="changeIdleMinutes"
                      >
                        <ASelectOption
                          v-if="!isPresetIdleMinutes(form.idle_minutes)"
                          :value="form.idle_minutes"
                        >
                          {{ formatIdlePolicyLabel(form.idle_minutes) }}
                        </ASelectOption>
                        <ASelectOption
                          v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                          :key="opt.value"
                          :value="opt.value"
                        >
                          {{ opt.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>
                  </div>

                  <div class="co-device-actions co-device-action-group">
                    <ATooltip
                      v-if="state.runtime.state === 'stopped'"
                      :title="
                        !state.setup.colab
                          ? '请先登录 Google 账户'
                          : state.operation?.title || ''
                      "
                    >
                      <AButton
                        type="primary"
                        :disabled="
                          busy || !!state.operation || !state.setup.colab
                        "
                        @click="start"
                      >
                        <template #icon>
                          <PhPlay :size="14" weight="fill" />
                        </template>
                        启动 GPU
                      </AButton>
                    </ATooltip>
                    <AButton
                      v-else
                      type="primary"
                      :disabled="
                        busy || !!state.operation || !state.setup.colab
                      "
                      @click="startNew"
                    >
                      <template #icon>
                        <PhPlus :size="14" weight="bold" />
                      </template>
                      新增实例
                    </AButton>

                    <AButton
                      v-if="
                        state.runtime.state !== 'stopped' &&
                        state.runtime.id &&
                        instanceCount > 1
                      "
                      danger
                      :disabled="busy || !!state.operation"
                      @click="openRelease(state.runtime.id)"
                    >
                      <template #icon>
                        <PhStop :size="14" weight="fill" />
                      </template>
                      仅停止当前实例
                    </AButton>

                    <AButton
                      v-if="
                        instanceCount > 0 ||
                        state.runtime.state !== 'stopped' ||
                        billing
                      "
                      type="primary"
                      danger
                      class="co-stop-all"
                      :disabled="busy || !!state.operation"
                      @click="openRelease()"
                    >
                      <template #icon>
                        <PhStop :size="14" weight="fill" />
                      </template>
                      {{
                        instanceCount > 1
                          ? `停止全部（${instanceCount} 个实例）`
                          : instanceCount === 1
                            ? "停止实例"
                            : "停止远端实例"
                      }}
                    </AButton>
                  </div>
                </div>

                <!-- Idle Policy & Drive Status Strips -->
                <div class="co-device-meta-strips">
                  <div class="co-device-policy co-device-policy">
                    <template v-if="ready && idle">
                      <div
                        v-if="idle.phase === 'active'"
                        class="co-policy-live is-active"
                      >
                        <ABadge status="processing" />
                        <span>任务运行中 · 自动释放已暂停</span>
                      </div>
                      <div
                        v-else
                        class="co-policy-live"
                        :class="{ 'is-urgent': countdown }"
                      >
                        <span>
                          剩余
                          <b class="co-tabular">{{
                            formatCountdownClock(idleRemaining)
                          }}</b>
                          自动释放
                        </span>
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link"
                          @click="interaction()"
                        >
                          重置
                        </AButton>
                      </div>
                      <span class="co-policy-hint">
                        下载、装载与推理期间自动暂停倒计时
                      </span>
                    </template>
                    <template v-else>
                      <div class="co-policy-live">
                        空闲满
                        {{ formatIdlePolicyLabel(form.idle_minutes) }} 自动释放
                      </div>
                      <span class="co-policy-hint">
                        无操作和任务时倒计时释放，运行任务期间自动暂停
                      </span>
                    </template>
                  </div>

                  <!-- Drive Sync Bar inside Device Card when Ready -->
                  <div
                    v-if="ready && (state.setup.drive || state.setup.drive_authorized)"
                    class="co-drive-strip co-sync-note"
                  >
                    <PhCloud :size="16" class="co-drive-icon" />
                    <span class="co-drive-text">
                      <template v-if="state.setup.drive">
                        {{
                          state.runtime.drive?.error ||
                          `已自动挂载 Google 云盘（My Drive / ${state.runtime.drive?.remote_folder || "ColabOne_Drive"}${state.runtime.drive?.free_bytes ? ` · 可用 ${bytes(state.runtime.drive.free_bytes)}` : ""}）· ${state.runtime.drive?.pending ? `${state.runtime.drive.pending} 项待同步` : "模型清单、ComfyUI 工作流与输出作品自动持久化"}`
                        }}
                      </template>
                      <template v-else>
                        已授权 Google 云盘（My Drive / ColabOne_Drive），可随时挂载以自动同步模型清单与 ComfyUI 作品。
                      </template>
                    </span>
                    <AButton
                      size="small"
                      :disabled="busy || !!state.operation"
                      @click="act(state.setup.drive ? 'drive.sync' : 'drive.connect')"
                    >
                      {{ state.setup.drive ? "立即同步" : "立即挂载云盘" }}
                    </AButton>
                  </div>
                </div>
              </ACard>

              <!-- Billing Card -->
              <section class="co-billing-card-wrap" aria-label="扣费与余额概览">
                <BillingCard
                  :account="state.account"
                  :setup-colab="state.setup.colab"
                  :runtime-state="state.runtime.state"
                  :runtime-id="state.runtime.id"
                  :idle="idle"
                  :idle-remaining="idleRemaining"
                  :idle-minutes="form.idle_minutes"
                  :busy="busy || !!state.operation || savingIdle"
                  :now-ms="clockNow"
                  :show-idle-footer="false"
                  @refresh-account="refreshAccount"
                  @touch="interaction()"
                  @change-idle-minutes="changeIdleMinutes"
                  @open-settings="
                    !busy && !state.operation
                      ? beginLogin()
                      : (page = 'settings')
                  "
                />
              </section>
              </div>

              <!-- Real-time Hardware Resource Monitor -->
              <section
                class="co-resource-monitor-wrap"
                aria-label="远端设备资源监控"
              >
                <ResourceMonitor
                  class="co-monitor"
                  :runtime="state.runtime"
                  :drive-connected="state.setup.drive"
                  :drive-free-bytes="state.runtime.drive?.free_bytes"
                  :loaded-model-name="activeVramModel?.name"
                  :selected-gpu="form.gpu"
                  :operation-title="state.operation?.title"
                  :ipc-disconnected="ipcDisconnected"
                />
              </section>

              <!-- Multi-instance Management Card -->
              <section
                v-if="accountInstances.length"
                class="co-instances-section"
                aria-label="账户运行实例"
              >
                <ACard class="co-instances-card">
                  <template #title>
                    <div class="co-card-title-block">
                      <span>账户运行实例（{{ accountInstances.length }}）</span>
                      <span class="co-card-title-sub">
                        本客户端管理 {{ managedInstances.length }} 台 · 其他来源
                        {{ externalInstances.length }} 台
                      </span>
                    </div>
                  </template>
                  <template #extra>
                    <AButton
                      size="small"
                      :disabled="
                        busy || !!state.operation || !state.setup.colab
                      "
                      @click="refreshAccount"
                    >
                      <template #icon>
                        <PhArrowsClockwise :size="14" />
                      </template>
                      刷新列表
                    </AButton>
                  </template>

                  <div class="co-instance-list">
                    <article
                      v-for="inst in accountInstances"
                      :key="inst.id"
                      class="co-instance-row co-instance-card"
                      :class="{ active: inst.active && inst.managed }"
                    >
                      <div class="co-instance-main">
                        <div class="co-instance-tags">
                          <ATag
                            v-if="inst.active && inst.managed"
                            color="success"
                          >
                            当前使用
                          </ATag>
                          <ATag v-else-if="inst.managed" color="blue">
                            本客户端管理
                          </ATag>
                          <ATag v-else>其他来源</ATag>
                          <ATag v-if="inst.accelerator">{{
                            inst.accelerator
                          }}</ATag>
                          <ATag v-if="shapeLabel(inst.shape)">{{
                            shapeLabel(inst.shape)
                          }}</ATag>
                          <ATooltip
                            v-if="inst.location?.country"
                            :title="locationTooltip(inst.location)"
                          >
                            <ATag class="co-location-tag">
                              <FlagIcon
                                :code="inst.location.country"
                                :size="13"
                              />
                              <span>{{ locationFull(inst.location) }}</span>
                            </ATag>
                          </ATooltip>
                          <ATag
                            v-if="inst.drive_connected"
                            color="success"
                            :bordered="false"
                          >
                            云盘已挂载
                          </ATag>
                        </div>

                        <code class="co-instance-endpoint">{{ inst.id }}</code>

                        <p v-if="inst.managed" class="co-instance-detail">
                          <span>
                            {{ COLABONE_STATUS_LABELS[inst.state || "ready"] || inst.state }}
                            <template v-if="inst.gpu_name">
                              · {{ inst.gpu_name }}
                            </template>
                          </span>
                          <span v-if="inst.vram_total_mib">
                            · 显存
                            {{ ((inst.vram_used_mib || 0) / 1024).toFixed(1) }}
                            / {{ (inst.vram_total_mib / 1024).toFixed(0) }} GB
                          </span>
                          <span v-if="inst.disk_free">
                            · 可用磁盘 {{ bytes(inst.disk_free) }}
                          </span>
                          <span>
                            ·
                            {{
                              inst.loaded_model
                                ? `已装载 ${inst.loaded_model}`
                                : "未装载模型"
                            }}
                          </span>
                          <span v-if="taskKindLabel(inst.active_task)">
                            · {{ taskKindLabel(inst.active_task) }}
                          </span>
                        </p>
                        <p v-else class="co-instance-detail">
                          由其他客户端或 Colab 网页启动，不受本客户端空闲自动释放管理
                        </p>
                      </div>

                      <div class="co-instance-actions">
                        <AButton
                          v-if="inst.managed && !inst.active"
                          size="small"
                          :disabled="busy || !!state.operation"
                          @click="switchInstance(inst.id)"
                        >
                          切换使用
                        </AButton>
                        <AButton
                          danger
                          size="small"
                          :disabled="busy || !!state.operation"
                          @click="openRelease(inst.id)"
                        >
                          单独停止
                        </AButton>
                      </div>
                    </article>
                  </div>
                </ACard>
              </section>

              <p class="co-footnote">
                模型权重、对外 API 推理与 ComfyUI 工作流均在远端 GPU 执行。{{
                  state.setup.drive
                    ? "模型清单、ComfyUI 工作流与输出作品已自动持久化到 Google 云盘 (ColabOne_Drive)，释放实例前会自动同步。"
                    : form.auto_drive && state.setup.drive_authorized
                      ? "已开启 Google 云盘自动挂载：启动 GPU 后将自动连接 ColabOne_Drive，持久保存模型清单、ComfyUI 工作流与输出作品。"
                      : "未挂载云盘时，释放实例将清空未导出的文件；如需保留，请先导出或在「设置」中开启云盘自动挂载。"
                }}
              </p>
            </div>
          </main>

          <!-- STAGE 2: 对外 API -->
          <main
            v-else-if="page === 'api'"
            key="api"
            class="co-panel-wrap co-page-motion co-panel-host"
          >
            <ApiPanel
              v-model:models-open="modelsDrawerOpen"
              :state="state"
              :busy="busy"
              @device="openDevice"
              @sync="syncSnapshot"
              @notify="notify"
              @error="showError"
            />
          </main>

          <!-- STAGE 3: ComfyUI -->
          <main
            v-else-if="page === 'comfyui'"
            key="comfyui"
            class="co-panel-wrap co-page-motion co-panel-host"
          >
            <ComfyPanel
              :state="state"
              @device="openDevice"
              @error="showError"
            />
          </main>

          <!-- STAGE 4: 设置 -->
          <main
            v-else
            key="settings"
            class="co-page-shell co-page-motion co-settings-page"
          >
            <div class="co-page-inner co-settings-inner">
              <div class="co-page-header">
                <div>
                  <h1 class="co-page-title">设置</h1>
                  <p class="co-page-subtitle">
                    管理 Google 账户、外观主题、默认算力与空闲释放策略。
                  </p>
                </div>
                <AButton
                  type="primary"
                  :disabled="busy"
                  @click="save"
                >
                  保存设置
                </AButton>
              </div>

              <!-- Settings Bento Row 1: Account/Drive + Compute/Idle Defaults -->
              <div class="co-settings-row-primary">
                <!-- Settings Card 1: Google 账号与云盘授权 -->
                <ACard class="co-settings-card co-settings-section">
                  <template #title>
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-blue" aria-hidden="true">
                        <PhUserCircle :size="16" weight="duotone" />
                      </span>
                      <h2>Google 账户与云盘授权</h2>
                    </div>
                  </template>
                  <template #extra>
                    <ATag v-if="state.setup.colab" color="success">已连接</ATag>
                    <ATag v-else>未连接</ATag>
                  </template>

                  <p class="co-settings-desc">
                    {{
                      state.account.email ||
                      "在浏览器选择 Google 账户并授权，完成后会自动返回这里。"
                    }}
                  </p>

                  <ASpace :size="8" wrap>
                    <AButton
                      :type="state.setup.colab ? 'default' : 'primary'"
                      :disabled="
                        busy ||
                        !!state.operation ||
                        state.runtime.state !== 'stopped'
                      "
                      @click="beginLogin"
                    >
                      <template #icon>
                        <PhArrowSquareOut :size="15" />
                      </template>
                      {{ state.setup.colab ? "重新登录" : "登录 Google" }}
                    </AButton>

                    <AButton
                      v-if="state.auth_url && state.operation"
                      type="link"
                      @click="colabOneOpenURL(state.auth_url)"
                    >
                      重新打开登录页
                    </AButton>
                  </ASpace>

                  <p v-if="state.setup.colab" class="co-saved-login">
                    <PhCheck :size="15" />
                    <span>{{
                      state.setup.persistent_login
                        ? "登录已安全保存在本机，重启自动恢复、到期自动续期。"
                        : "登录已保存到本机。若 Google 要求重新授权，会在这里提示。"
                    }}</span>
                  </p>

                  <ACollapse
                    :active-key="showAuthPaste ? ['paste'] : []"
                    ghost
                    class="co-inline-collapse"
                    @update:active-key="onAuthPasteToggle"
                  >
                    <ACollapsePanel
                      key="paste"
                      header="授权页返回了一段内容？点此手动粘贴"
                    >
                      <div class="co-collapse-body">
                        <p class="co-muted">
                          整段页面文字、完整链接或授权码均可，客户端会自动提取。请使用本次登录页面返回的内容。
                        </p>
                        <ASpace :size="8" wrap>
                          <AButton
                            :disabled="busy || state.runtime.state !== 'stopped'"
                            @click="pasteLogin"
                          >
                            <template #icon>
                              <PhCopy :size="15" />
                            </template>
                            粘贴并完成登录
                          </AButton>
                          <AButton
                            type="link"
                            :disabled="
                              busy ||
                              !!state.operation ||
                              state.runtime.state !== 'stopped'
                            "
                            @click="manualLogin"
                          >
                            <template #icon>
                              <PhArrowSquareOut :size="14" />
                            </template>
                            改用手动授权登录
                          </AButton>
                        </ASpace>
                        <ATextarea
                          v-model:value="code"
                          :rows="3"
                          autocomplete="off"
                          autocapitalize="off"
                          spellcheck="false"
                          placeholder="粘贴浏览器授权页显示的全部内容或授权码…"
                          aria-label="Google 授权内容"
                        />
                        <div>
                          <AButton
                            type="primary"
                            :disabled="
                              !code.trim() ||
                              busy ||
                              state.runtime.state !== 'stopped'
                            "
                            @click="finishLogin"
                          >
                            自动识别并登录
                          </AButton>
                        </div>
                      </div>
                    </ACollapsePanel>
                  </ACollapse>

                  <ADivider class="co-section-divider" />

                  <div class="co-drive-subhead">
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-emerald" aria-hidden="true">
                        <PhCloud :size="16" weight="duotone" />
                      </span>
                      <h3>Google 云盘</h3>
                    </div>
                    <ATag
                      :color="
                        state.setup.drive || state.setup.drive_authorized
                          ? 'success'
                          : 'default'
                      "
                    >
                      {{
                        state.setup.drive
                          ? "已挂载 · ColabOne_Drive"
                          : state.setup.drive_authorized
                            ? "已授权 · 启动自动挂载"
                            : state.setup.colab
                              ? "需重新登录一次授权"
                              : "未连接"
                      }}
                    </ATag>
                  </div>

                  <p class="co-settings-desc">
                    复用本机 Google OAuth 凭据自动挂载专属目录
                    <code>My Drive / ColabOne_Drive</code>
                    ，一次登录长期自动续期，新建或切换多台 GPU
                    实例均无需浏览器二次确认。
                  </p>

                  <AAlert
                    v-if="state.setup.colab && !state.setup.drive_authorized"
                    type="warning"
                    show-icon
                    message="当前授权尚未包含云盘权限；点击上方「重新登录」补充授权后，后续实例将自动挂载云盘。"
                    class="co-sub-alert"
                  />

                  <ASpace :size="8" wrap>
                    <AButton
                      :disabled="
                        busy ||
                        !ready ||
                        !!state.operation ||
                        !state.setup.drive_authorized
                      "
                      @click="
                        act(state.setup.drive ? 'drive.sync' : 'drive.connect')
                      "
                    >
                      <template #icon>
                        <PhCloud :size="15" />
                      </template>
                      {{ state.setup.drive ? "立即同步云盘" : "立即挂载云盘" }}
                    </AButton>
                  </ASpace>
                </ACard>

                <!-- Settings Card 3: 默认算力与空闲释放 -->
                <ACard class="co-settings-card">
                  <template #title>
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-blue" aria-hidden="true">
                        <PhSlidersHorizontal :size="16" weight="duotone" />
                      </span>
                      <h2>默认算力与空闲释放</h2>
                    </div>
                  </template>
                  <p class="co-settings-desc">
                    配置新建实例的默认 GPU、空闲自动释放时长与远端下载并发。
                  </p>

                  <div class="co-settings-grid">
                    <div class="co-setting-field">
                      <span class="co-field-label">默认 GPU 型号</span>
                      <ASelect
                        :value="form.gpu"
                        aria-label="默认 GPU 型号"
                        class="co-full-width"
                        :disabled="busy || !!state.operation"
                        @change="changeDefaultGpu"
                      >
                        <ASelectOption
                          v-for="g in GPU_SPECS"
                          :key="g.id"
                          :value="g.id"
                        >
                          {{ g.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>

                    <div class="co-setting-field">
                      <span class="co-field-label">空闲自动释放时长</span>
                      <ASelect
                        :value="form.idle_minutes"
                        aria-label="空闲自动释放时长"
                        class="co-full-width"
                        :disabled="savingIdle"
                        @change="changeIdleMinutes"
                      >
                        <ASelectOption
                          v-if="!isPresetIdleMinutes(form.idle_minutes)"
                          :value="form.idle_minutes"
                        >
                          {{ formatIdlePolicyLabel(form.idle_minutes) }}
                        </ASelectOption>
                        <ASelectOption
                          v-for="opt in COLABONE_IDLE_RELEASE_OPTIONS"
                          :key="opt.value"
                          :value="opt.value"
                        >
                          {{ opt.label }}
                        </ASelectOption>
                      </ASelect>
                    </div>

                    <div class="co-setting-field">
                      <span class="co-field-label">远端下载并发线程</span>
                      <div>
                        <ASegmented
                          :value="form.download_threads"
                          :options="downloadThreadOptions"
                          aria-label="下载并发线程"
                          @change="changeDownloadThreads"
                        />
                      </div>
                    </div>

                    <div class="co-setting-field">
                      <span class="co-field-label">云盘自动挂载</span>
                      <div class="co-switch-row">
                        <ASwitch
                          :checked="form.auto_drive"
                          @update:checked="onAutoDriveChange"
                        />
                        <span class="co-switch-label">
                          启动 GPU 时自动挂载并同步 Google 云盘 (ColabOne_Drive)
                        </span>
                      </div>
                    </div>
                  </div>

                  <div class="co-idle-policy-box">
                    <strong>空闲自动释放保护</strong>
                    <p>
                      超过
                      {{ formatIdlePolicyLabel(form.idle_minutes) }}
                      无操作或任务时自动释放远端资源；继续操作会重置倒计时，模型下载与装载、对外
                      API 推理及 ComfyUI 运行期间自动暂停倒计时。
                    </p>
                  </div>

                  <p class="co-footnote">
                    默认 GPU 与空闲释放时长与「远端设备」实时同步，修改后立即生效。
                  </p>
                </ACard>
              </div>

              <!-- Settings Bento Row 2: Appearance + HF Token + Desktop Shortcuts -->
              <div class="co-settings-row-secondary">
                <!-- Settings Card 2: 外观主题 -->
                <ACard
                  class="co-settings-card co-appearance-section"
                  aria-labelledby="appearance-heading"
                >
                  <template #title>
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-amber" aria-hidden="true">
                        <PhSun :size="16" weight="duotone" />
                      </span>
                      <h2 id="appearance-heading">外观主题</h2>
                    </div>
                  </template>
                  <p class="co-settings-desc">
                    仅对当前设备生效；选择「跟随系统」时将自动同步系统明暗外观。
                  </p>
                  <div @pointerdown.capture="rememberAppearancePointer">
                    <ASegmented
                      :value="colabOneAppearance"
                      :options="appearanceSegmentedOptions"
                      aria-label="外观模式"
                      @change="onAppearanceChange"
                    >
                      <template #label="{ value, title, payload }">
                        <span class="co-seg-item">
                          <PhDesktop v-if="value === 'system'" :size="15" />
                          <PhSun v-else-if="value === 'light'" :size="15" />
                          <PhMoon v-else :size="15" />
                          <span>{{ payload?.label ?? title ?? value }}</span>
                        </span>
                      </template>
                    </ASegmented>
                  </div>
                </ACard>

                <!-- Settings Card 4: Hugging Face Token -->
                <ACard class="co-settings-card">
                  <template #title>
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-violet" aria-hidden="true">
                        <PhKey :size="16" weight="duotone" />
                      </span>
                      <h2>Hugging Face Token</h2>
                    </div>
                  </template>
                  <template #extra>
                    <ATag v-if="state.setup.hf" color="success">已保存 Token</ATag>
                    <ATag v-else>可选</ATag>
                  </template>
                  <p class="co-settings-desc">
                    下载公开 GGUF 模型无需填写；访问受限或需协议授权的社区仓库时才需配置 Token。
                  </p>
                  <div class="co-hf-row">
                    <AInputPassword
                      v-model:value="form.hf_token"
                      autocomplete="new-password"
                      :placeholder="
                        state.setup.hf
                          ? '已保存，输入新值可覆盖更新'
                          : 'hf_...（访问受限模型仓库时填写）'
                      "
                    />
                    <AButton
                      type="primary"
                      :disabled="!form.hf_token.trim() || busy"
                      @click="save"
                    >
                      保存 Token
                    </AButton>
                  </div>
                </ACard>

                <!-- Settings Card 5: 桌面快捷方式 -->
                <ACard
                  class="co-settings-card"
                  aria-labelledby="shortcuts-heading"
                >
                  <template #title>
                    <div class="co-card-head-title">
                      <span class="co-head-icon-badge is-slate" aria-hidden="true">
                        <PhDesktop :size="16" weight="duotone" />
                      </span>
                      <h2 id="shortcuts-heading">桌面与开始菜单快捷方式</h2>
                    </div>
                  </template>
                  <template #extra>
                    <ATag v-if="state.setup.start_menu_shortcut" color="success">
                      已加入开始菜单
                    </ATag>
                  </template>
                  <p class="co-settings-desc">
                    当前为便携版，无需管理员权限即可创建开始菜单或桌面快捷方式；如需常驻任务栏，右键底部运行图标选择「固定到任务栏」。
                  </p>
                  <ASpace :size="8" wrap>
                    <AButton
                      :type="
                        state.setup.start_menu_shortcut ? 'default' : 'primary'
                      "
                      :disabled="busy"
                      @click="updateShortcut('start_menu')"
                    >
                      {{
                        state.setup.start_menu_shortcut
                          ? "更新开始菜单快捷方式"
                          : "添加到开始菜单"
                      }}
                    </AButton>
                    <AButton
                      :disabled="busy"
                      @click="updateShortcut('desktop')"
                    >
                      {{
                        state.setup.desktop_shortcut
                          ? "更新桌面快捷方式"
                          : "创建桌面快捷方式"
                      }}
                    </AButton>
                    <AButton
                      v-if="
                        state.setup.start_menu_shortcut ||
                        state.setup.desktop_shortcut
                      "
                      type="link"
                      danger
                      :disabled="busy"
                      @click="updateShortcut('both', true)"
                    >
                      移除快捷方式
                    </AButton>
                  </ASpace>
                </ACard>
              </div>

              <!-- Settings Card 6: 关于与开源许可 -->
              <ACard class="co-settings-card co-about-card">
                <div class="co-about-row">
                  <div class="co-about-copy">
                    <div class="co-about-head">
                      <span
                        class="co-about-icon"
                        aria-hidden="true"
                        v-html="appIcon"
                      ></span>
                      <ATypographyTitle :level="5" class="co-about-title">
                        关于与开源许可
                      </ATypographyTitle>
                    </div>
                    <ATypographyText type="secondary">
                      ColabOne · 远端 GGUF 对外 API 与 ComfyUI 工作流 · 凭据安全保存在本机系统凭据库
                    </ATypographyText>
                  </div>
                  <div class="co-about-pills">
                    <ATag color="blue" :bordered="false">GPL-3.0</ATag>
                    <ATag>Apache-2.0</ATag>
                    <ATag>MIT</ATag>
                  </div>
                </div>
              </ACard>
            </div>
          </main>
        </ALayoutContent>

        <!-- Pinned Active Remote Task Progress Bar -->
        <div v-if="task" class="co-task-dock">
          <ACard size="small" class="co-task-card co-task-bar">
            <div class="co-task-row">
              <ASpin size="small" />
              <div class="co-task-main">
                <div class="co-task-head">
                  <strong>{{ taskBarTitle(task.kind) }}</strong>
                  <span class="co-muted">{{ task.message }}</span>
                  <ATag
                    v-if="
                      (task.kind === 'public_chat' ||
                        task.kind === 'public_responses' ||
                        task.kind === 'public_compact') &&
                      activeApiSpeed?.tokensPerSecond
                    "
                    color="blue"
                    :bordered="false"
                    class="co-mono"
                  >
                    {{
                      formatTokenSpeed(
                        activeApiSpeed.tokensPerSecond,
                        !!activeApiSpeed.estimated,
                      )
                    }}
                    <template v-if="activeApiSpeed.completionTokens">
                      · {{ formatTokenCount(activeApiSpeed.completionTokens) }} tokens
                    </template>
                  </ATag>
                </div>
                <div v-if="task.download" class="co-download-stats">
                  <b class="co-tabular">
                    {{ (task.download.speed_bps / 1024 ** 2).toFixed(1) }} MiB/s
                  </b>
                  <span class="co-tabular">
                    {{ bytes(task.download.downloaded_bytes) }} /
                    {{ bytes(task.download.total_bytes) }}
                  </span>
                  <span>
                    {{
                      task.download.phase === "verifying"
                        ? "正在校验 SHA-256"
                        : `预计剩余 ${duration(task.download.eta_seconds)}`
                    }}
                  </span>
                  <span class="co-tabular">
                    {{ task.download.connections }}/{{ task.download.threads }}
                    个连接 · 文件 {{ task.download.file_index }}/{{
                      task.download.file_count
                    }}
                  </span>
                </div>
                <AProgress
                  v-if="
                    task.kind === 'download' ||
                    task.kind === 'comfyui_start' ||
                    task.kind === 'comfyui_run'
                  "
                  :percent="Math.round((task.progress || 0) * 100)"
                  size="small"
                  status="active"
                />
              </div>
              <AButton
                danger
                size="small"
                @click="act('task.cancel', { id: task.id })"
              >
                取消
              </AButton>
            </div>
          </ACard>
        </div>
      </ALayout>

      <!-- Inline Fallback Logs Column (only in non-desktop web previews when standalone OS window is unavailable) -->
      <LogDrawer
        v-if="!logWindowStandalone"
        :open="logsOpen"
        :width="logDrawerWidth"
        :logs="state.logs || []"
        :current-activity="currentActivity"
        :running="!!(state.operation || task)"
        :active-speed="activeApiSpeed"
        @update:width="onLogDrawerWidthChange"
        @copy="copy"
        @clear="act('logs.clear')"
      />

      <!-- Floating Pill Toast -->
      <Transition name="co-toast">
        <div v-if="toast" class="co-toast" role="status">
          <PhCheck :size="16" weight="bold" />
          <span>{{ toast }}</span>
        </div>
      </Transition>

      <!-- Modal: Selective Instance Release Review -->
      <AModal
        v-model:open="showRelease"
        title="选择要停止的远端实例"
        :width="580"
        :mask-closable="!busy"
        :closable="!busy"
        :footer="null"
        centered
      >
        <div class="co-modal-stack">
          <p class="co-muted co-modal-lead">
            可勾选需要停止的实例，保留其他客户端或页面正在运行的实例。已选实例上的模型文件和未导出的内容会被清空。
          </p>

          <div v-if="!releasePlan" class="co-modal-loading">
            <ASpin v-if="busy" size="small" />
            <span>{{
              busy ? "正在查询运行实例…" : "清单读取失败，请取消后重试。"
            }}</span>
          </div>

          <template v-else>
            <div
              v-if="releasePlan.instances.length > 1"
              class="co-release-toolbar"
            >
              <AButton
                type="link"
                size="small"
                @click="selectAllRelease"
              >
                全选（{{ releasePlan.instances.length }}）
              </AButton>
              <AButton
                v-if="
                  releasePlan.instances.some((i) => i.managed) &&
                  releasePlan.instances.some((i) => !i.managed)
                "
                type="link"
                size="small"
                @click="selectManagedReleaseOnly"
              >
                仅选本客户端（{{
                  releasePlan.instances.filter((i) => i.managed).length
                }}）
              </AButton>
            </div>

            <div class="co-release-list">
              <div
                v-for="instance in releasePlan.instances"
                :key="instance.id"
                class="co-release-item"
                :class="{
                  selected: selectedReleaseIds.includes(instance.id),
                }"
              >
                <ACheckbox
                  :checked="selectedReleaseIds.includes(instance.id)"
                  @update:checked="
                    (checked) =>
                      toggleReleaseSelection(instance.id, Boolean(checked))
                  "
                >
                  <div class="co-release-item-info">
                    <div class="co-release-item-top">
                      <code class="co-mono">{{ instance.id }}</code>
                      <ATag v-if="instance.accelerator">{{
                        instance.accelerator
                      }}</ATag>
                      <ATag v-if="shapeLabel(instance.shape)">{{
                        shapeLabel(instance.shape)
                      }}</ATag>
                      <ATooltip
                        v-if="instance.location?.country"
                        :title="locationTooltip(instance.location)"
                      >
                        <ATag class="co-location-tag">
                          <FlagIcon
                            :code="instance.location.country"
                            :size="13"
                          />
                          <span>{{ locationShort(instance.location) }}</span>
                        </ATag>
                      </ATooltip>
                    </div>
                    <span class="co-release-item-desc">{{
                      instance.managed
                        ? instance.active
                          ? `本客户端当前使用实例${instance.loaded_model ? ` · 已装载 ${instance.loaded_model}` : ""}`
                          : `本客户端管理的后台实例${instance.loaded_model ? ` · 已装载 ${instance.loaded_model}` : ""}`
                        : "其他来源的 Colab 实例，停止前请确认已保存数据"
                    }}</span>
                  </div>
                </ACheckbox>
              </div>

              <AEmpty
                v-if="!releasePlan.instances.length"
                description="未发现运行实例，Google 费率可能仍在更新。"
              />
            </div>
          </template>

          <AAlert
            v-if="state.operation"
            type="info"
            show-icon
            message="正在结束当前操作，完成后即可停止。"
          />

          <ACheckbox v-model:checked="force">
            我已导出或不需要保留所选实例上的远端模型与未导出作品（强制释放）
          </ACheckbox>

          <div class="co-modal-footer">
            <AButton
              :disabled="busy"
              @click="showRelease = false"
            >
              取消
            </AButton>
            <AButton
              type="primary"
              danger
              :loading="busy"
              :disabled="
                busy ||
                !!state.operation ||
                !releasePlan?.instances.length ||
                !selectedReleaseIds.length
              "
              @click="release"
            >
              {{
                isAllReleaseSelected
                  ? `确认停止全部（${releasePlan?.instances.length || 0}）`
                  : `确认停止所选（${selectedReleaseIds.length}）`
              }}
            </AButton>
          </div>
        </div>
      </AModal>
    </ALayout>
  </AConfigProvider>
</template>

<style scoped>
/* Root Shell — horizontal row so LogDrawer expands strictly to the right */
.co-shell {
  height: 100vh;
  width: 100vw;
  overflow: hidden;
  display: flex;
  flex-direction: row !important;
  background: var(--co-canvas);
}

/* Main Workspace */
.co-workspace {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--co-canvas);
}

/* ColabOne Top Floating Capsule Navbar */
.co-navbar {
  flex-shrink: 0;
  background: var(--co-canvas);
  user-select: none;
  z-index: 20;
}

.co-navbar-inner {
  max-width: var(--co-container-max);
  width: 100%;
  height: 72px;
  margin: 0 auto;
  padding: 0 var(--co-container-pad);
  box-sizing: border-box;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 16px;
}

.co-nav-left {
  display: flex;
  align-items: center;
  min-width: 0;
}

.co-brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  padding: 4px 6px 4px 0;
  border: none;
  background: transparent;
  color: var(--co-text);
  font-size: 26px;
  font-weight: 700;
  line-height: 1.1;
  letter-spacing: -0.028em;
  min-width: 0;
  cursor: pointer;
  transition: opacity 0.16s ease;
}

.co-brand:hover {
  opacity: 0.86;
}

.co-brand:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
  border-radius: 12px;
}

.co-brand-icon {
  width: 48px;
  height: 48px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: transform 0.14s ease;
}

.co-brand:active .co-brand-icon {
  transform: scale(0.94);
}

.co-brand-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-brand-text {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ColabOne Centered Floating Capsule Pill Navigation */
.co-nav-center {
  display: flex;
  justify-content: center;
  min-width: 0;
}

.co-nav-pill-track {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px;
  border-radius: 9999px;
  background: var(--co-track);
}

:global(:root[data-theme="dark"]) .co-nav-pill-track {
  border: 1px solid var(--co-border-secondary);
}

.co-nav-pill-item {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 38px;
  padding: 0 22px;
  border: none;
  border-radius: 9999px;
  background: transparent;
  color: var(--co-secondary-text);
  font-family: inherit;
  font-size: 14px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
  cursor: pointer;
  transition: transform 0.12s ease;
}

.co-nav-pill-item:hover:not(.is-active) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-nav-pill-item:active {
  transform: scale(0.97);
}

.co-nav-pill-item:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-nav-pill-item.is-active {
  background: var(--co-pill-active-bg);
  color: var(--co-pill-active-text);
  font-weight: 600;
  box-shadow: var(--co-pill-active-shadow);
}

/* ColabOne NavMenu: clean text-only pills on desktop, icon-only pills below 800px */
.co-nav-pill-icon {
  display: none;
  flex-shrink: 0;
  opacity: 0.85;
}

.co-nav-pill-item.is-active .co-nav-pill-icon {
  opacity: 1;
}

.co-nav-live-dot {
  width: 5px;
  height: 5px;
  border-radius: 9999px;
  flex-shrink: 0;
  box-shadow: 0 0 0 2px rgba(16, 185, 129, 0.24);
}

/* Right Circular Utility Buttons & Logs Pill */
.co-nav-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
}

.co-nav-circle-btn {
  width: 40px;
  height: 40px;
  padding: 0;
  border: none;
  border-radius: 9999px;
  background: var(--co-track);
  color: var(--co-secondary-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

:global(:root[data-theme="dark"]) .co-nav-circle-btn,
:global(:root[data-theme="dark"]) .co-logs-trigger {
  border: 1px solid var(--co-border-secondary);
}

.co-nav-circle-btn:hover:not(:disabled) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-nav-circle-btn:active:not(:disabled) {
  transform: scale(0.95);
}

.co-nav-circle-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-nav-circle-btn:focus-visible,
.co-logs-trigger:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-logs-trigger {
  height: 40px;
  padding: 0 15px;
  border: none;
  border-radius: 9999px;
  background: var(--co-track);
  color: var(--co-secondary-text);
  font-family: inherit;
  font-size: 13.5px;
  font-weight: 500;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  cursor: pointer;
  flex-shrink: 0;
  transition: transform 0.12s ease;
}

.co-logs-trigger:hover:not(.is-active) {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-logs-trigger:active {
  transform: scale(0.97);
}

.co-logs-trigger.is-active {
  background: var(--co-pill-active-bg);
  border-color: var(--co-pill-active-bg);
  color: var(--co-pill-active-text);
  font-weight: 600;
  box-shadow: var(--co-pill-active-shadow);
}

/* ColabOne Breathable Seamless Status & Billing Strip */
.co-status-bar {
  flex-shrink: 0;
  background: var(--co-canvas);
}

.co-status-bar-inner {
  max-width: var(--co-container-max);
  width: 100%;
  min-height: 36px;
  margin: 0 auto;
  padding: 0 var(--co-container-pad) 6px;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.co-topbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
  overflow: hidden;
}

.co-runtime-tag {
  margin-inline-end: 0;
  font-weight: 500;
  flex-shrink: 0;
}

.co-gpu-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-secondary-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.co-location-tag {
  margin-inline-end: 0;
  flex-shrink: 0;
}

.co-model-chip {
  height: 28px;
  max-width: 220px;
  border-radius: 9999px !important;
  flex-shrink: 1;
}

.co-model-chip-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-instance-select {
  min-width: 170px;
  max-width: 240px;
  flex-shrink: 1;
}

.co-topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* Global Banners */
.co-banners {
  flex-shrink: 0;
  background: var(--co-canvas);
}

.co-banners-inner {
  max-width: var(--co-container-max);
  width: 100%;
  margin: 0 auto;
  padding: 4px var(--co-container-pad) 6px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-op-message {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}

/* Main Scrollable Content */
.co-content {
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow-y: auto;
  overflow-x: hidden;
  background: var(--co-canvas);
  display: flex;
  flex-direction: column;
}

.co-page-shell {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  flex: 1 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
}

.co-page-inner {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-settings-inner {
  width: 100%;
}

.co-panel-wrap {
  width: 100%;
  flex: 1 0 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

@media (max-width: 980px) {
  .co-navbar-inner {
    height: 64px;
  }

  .co-brand {
    gap: 10px;
    font-size: 21px;
  }

  .co-brand-icon {
    width: 38px;
    height: 38px;
  }

  .co-logs-trigger {
    width: 40px;
    padding: 0;
    justify-content: center;
  }

  .co-logs-trigger-label {
    display: none;
  }
}

@media (max-width: 780px) {
  .co-nav-pill-item {
    width: 38px;
    padding: 0;
  }

  .co-nav-pill-icon {
    display: inline-block;
  }

  .co-nav-pill-label {
    display: none;
  }

  .co-nav-live-dot {
    position: absolute;
    top: 6px;
    right: 6px;
  }
}

@media (max-width: 600px) {
  .co-brand-text {
    display: none;
  }
}

/* Setup Callout */
.co-setup-callout {
  border-color: var(--co-accent);
  background: var(--co-surface);
}

.co-callout-row {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-callout-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border: 1px solid rgba(59, 130, 246, 0.18);
}

.co-callout-copy {
  flex: 1;
  min-width: 220px;
}

.co-callout-copy h3 {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-callout-copy p {
  margin: 0;
  font-size: 13px;
  color: var(--co-secondary-text);
}

/* Primary Device Hero Grid (GPU Control + Billing Overview side-by-side on wide fullscreen) */
.co-device-hero-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 16px;
}

@media (min-width: 1380px) {
  .co-device-hero-grid {
    grid-template-columns: minmax(0, 1.15fr) minmax(0, 0.85fr);
    align-items: stretch;
  }

  .co-device-hero-grid > .co-device-card,
  .co-device-hero-grid > .co-billing-card-wrap {
    min-width: 0;
    display: flex;
    flex-direction: column;
  }

  .co-device-hero-grid > .co-device-card :deep(.ant-card-body),
  .co-device-hero-grid > .co-billing-card-wrap :deep(.co-billing-card) {
    flex: 1;
    display: flex;
    flex-direction: column;
  }

  .co-device-hero-grid > .co-billing-card-wrap :deep(.co-billing-card .ant-card-body) {
    flex: 1;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .co-device-hero-grid > .co-device-card .co-device-meta-strips {
    margin-top: auto;
    padding-top: 14px;
  }
}

/* Primary Device Control Card */
.co-device-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-device-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--co-selected);
  border: 1px solid rgba(59, 130, 246, 0.18);
  color: var(--co-accent);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-device-title-main {
  flex: 1;
  min-width: 220px;
}

.co-device-title-main h2 {
  margin: 0 0 4px;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--co-text);
}

.co-device-subtitle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-runtime-id {
  font-family: var(--co-font-mono);
  font-size: 12px;
  padding: 1px 7px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-secondary-text);
}

.co-device-badges {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-card-divider {
  margin: 14px 0 !important;
}

.co-device-controls {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px 16px;
  flex-wrap: wrap;
}

.co-device-fields {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  flex-wrap: wrap;
}

.co-control-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-field-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-secondary-text);
  line-height: 1.3;
}

.co-select-gpu {
  width: 272px;
}

.co-select-idle {
  width: 152px;
}

.co-device-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-device-meta-strips {
  margin-top: 16px;
  display: flex;
  flex-direction: column;
  gap: 0;
}

.co-device-policy {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  flex-wrap: wrap;
  padding: 12px 0 0;
  border-top: 1px solid var(--co-border-secondary);
  background: transparent;
}

.co-policy-live {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--co-text);
}

.co-policy-live.is-active {
  color: var(--co-accent);
}

.co-policy-live.is-urgent {
  color: var(--co-danger-text);
}

.co-policy-hint {
  font-size: 12px;
  color: var(--co-muted);
}

.co-drive-strip {
  margin-top: 12px;
  padding: 12px 0 0;
  border-top: 1px solid var(--co-border-secondary);
  background: transparent;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.co-drive-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-drive-text {
  flex: 1;
  min-width: 200px;
  font-size: 13px;
  color: var(--co-secondary-text);
}

/* Multi-Instance Card */
.co-card-title-block {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.co-card-title-sub {
  font-size: 13px;
  font-weight: 400;
  color: var(--co-muted);
}

.co-instance-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(520px, 100%), 1fr));
  gap: 12px;
}

.co-instance-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  flex-wrap: wrap;
  transition: border-color 0.16s ease, background-color 0.16s ease;
}

.co-instance-row.active {
  border-color: rgba(59, 130, 246, 0.45);
  background: var(--co-selected);
}

.co-instance-main {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.co-instance-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-instance-endpoint {
  font-family: var(--co-font-mono);
  font-size: 13px;
  color: var(--co-text);
}

.co-instance-detail {
  margin: 0;
  font-size: 13px;
  color: var(--co-secondary-text);
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.co-instance-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-footnote {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-muted);
}

/* Settings Stage */
.co-settings-row-primary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  align-items: stretch;
}

.co-settings-row-secondary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  align-items: stretch;
}

.co-settings-row-primary > .co-settings-card,
.co-settings-row-secondary > .co-settings-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.co-settings-row-primary > .co-settings-card :deep(.ant-card-body),
.co-settings-row-secondary > .co-settings-card :deep(.ant-card-body) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

@media (max-width: 1239px) {
  .co-settings-row-secondary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .co-settings-row-secondary > .co-settings-card:last-child {
    grid-column: 1 / -1;
  }
}

@media (max-width: 1024px) {
  .co-settings-row-primary,
  .co-settings-row-secondary {
    grid-template-columns: 1fr;
  }

  .co-settings-row-secondary > .co-settings-card:last-child {
    grid-column: auto;
  }
}

.co-card-head-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: var(--co-text);
}

.co-head-icon-badge {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-head-icon-badge.is-blue {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-head-icon-badge.is-emerald {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

.co-head-icon-badge.is-amber {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-head-icon-badge.is-violet {
  background: rgba(139, 92, 246, 0.12);
  color: #7c3aed;
}

:global(:root[data-theme="dark"]) .co-head-icon-badge.is-violet {
  background: rgba(139, 92, 246, 0.18);
  color: #a78bfa;
}

.co-head-icon-badge.is-slate {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-card-head-title h2,
.co-card-head-title h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-settings-desc {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-settings-desc code {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 12px;
}

.co-saved-login {
  margin: 12px 0 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--co-success-text);
}

.co-inline-collapse {
  margin-top: 8px;
}

.co-inline-collapse :deep(.ant-collapse-header) {
  padding: 6px 0 !important;
  color: var(--co-accent) !important;
  font-size: 13px;
  font-weight: 500;
}

.co-inline-collapse :deep(.ant-collapse-content-box) {
  padding: 6px 0 0 !important;
}

.co-collapse-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-collapse-body p {
  margin: 0;
  font-size: 13px;
}

.co-section-divider {
  margin: 16px 0 !important;
}

.co-drive-subhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}

.co-sub-alert {
  margin-bottom: 12px;
}

.co-appearance-section :deep(.ant-segmented) {
  align-self: flex-start;
}

.co-seg-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1.2;
}

.co-seg-item svg {
  flex-shrink: 0;
}

.co-settings-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 14px;
}

@media (max-width: 640px) {
  .co-settings-grid {
    grid-template-columns: 1fr;
  }
}

.co-setting-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-full-width {
  width: 100%;
}

.co-switch-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 34px;
}

.co-switch-label {
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-idle-policy-box {
  padding: 14px 16px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  margin-bottom: 10px;
}

.co-idle-policy-box strong {
  display: block;
  margin-bottom: 4px;
  font-size: 13px;
  color: var(--co-text);
}

.co-idle-policy-box p {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-hf-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  max-width: 560px;
}

.co-about-card :deep(.ant-card-body) {
  padding: 16px 22px;
}

.co-about-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.co-about-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.co-about-icon {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-about-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-about-copy {
  flex: 1;
  min-width: 240px;
}

.co-about-title {
  margin: 0 !important;
  font-size: 14px !important;
}

.co-about-pills {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

/* Active Task Dock */
.co-task-dock {
  flex-shrink: 0;
  padding: 10px var(--co-container-pad) 12px;
  background: var(--co-surface);
  border-top: 1px solid var(--co-border-secondary);
}

.co-task-card {
  max-width: calc(var(--co-container-max) - 2 * var(--co-container-pad));
  margin: 0 auto;
  border-color: var(--co-accent);
  background: var(--co-subtle);
}

.co-task-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-task-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-task-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
}

.co-download-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-secondary-text);
}

/* ColabOne Elevated Toast Card */
.co-toast {
  position: fixed;
  left: 50%;
  bottom: 24px;
  transform: translateX(-50%);
  z-index: 1200;
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 10px 18px;
  border-radius: 12px;
  background: var(--co-elevated);
  color: var(--co-text);
  border: 1px solid var(--co-success-border);
  box-shadow: 0 10px 28px rgba(15, 23, 42, 0.14);
  font-size: 13.5px;
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

/* Modals */
.co-modal-stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-modal-lead {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
}

.co-modal-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 0;
  color: var(--co-muted);
}

.co-release-toolbar {
  display: flex;
  align-items: center;
  gap: 4px;
}

.co-release-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 260px;
  overflow-y: auto;
}

.co-release-item {
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  transition: border-color 0.16s ease, background-color 0.16s ease;
}

.co-release-item.selected {
  border-color: var(--co-danger-border);
  background: var(--co-danger-bg);
}

.co-release-item-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-left: 4px;
}

.co-release-item-top {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-release-item-desc {
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 6px;
}
</style>
