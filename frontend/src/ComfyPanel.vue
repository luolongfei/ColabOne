<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Divider as ADivider,
  Empty as AEmpty,
  Input as AInput,
  InputNumber as AInputNumber,
  InputPassword as AInputPassword,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Progress as AProgress,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Spin as ASpin,
  Switch as ASwitch,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
  Textarea as ATextarea,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  PhAppWindow,
  PhArrowRight,
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhBroom,
  PhBrowsers,
  PhCheck,
  PhDownloadSimple,
  PhFileCode,
  PhFlowArrow,
  PhGraph,
  PhHardDrives,
  PhImage,
  PhLink,
  PhPlay,
  PhPlus,
  PhShieldCheck,
  PhSlidersHorizontal,
  PhSparkle,
  PhStop,
  PhTrash,
  PhTreeStructure,
  PhUploadSimple,
  PhVideoCamera,
  PhWaveform,
} from "@phosphor-icons/vue";
import {
  colabOneCommand,
  colabOneExportWorkflowOutput,
  colabOneOpenComfyCanvas,
  colabOneOpenURL,
  colabOneWriteClipboard,
} from "./api";
import { formatIdlePolicyLabel } from "./billing";
import {
  defaultFieldValues,
  extractPublicHost,
  formatOutputSize,
  outputBlobURL,
  parseWorkflowImportJSON,
} from "./comfyui";
import type {
  ColabOneSnapshot,
  WorkflowField,
  WorkflowOutput,
  WorkflowTemplate,
} from "./types";

const props = defineProps<{ state: ColabOneSnapshot }>();
const emit = defineEmits<{
  device: [];
  error: [string];
}>();

const activeTab = ref<"studio" | "canvas">("studio");
const tabsWrapRef = ref<HTMLElement | null>(null);
const busy = ref(false);
const notice = ref("");

// Engine & runtime status
const gpuReady = computed(() => props.state.runtime.state === "ready");
const gpuStarting = computed(() => props.state.runtime.state === "starting");
const comfy = computed(() => props.state.runtime.comfyui);
const canvas = computed(() => comfy.value?.canvas);
const comfyTag = computed(() => comfy.value?.tag || "v0.46.0");

const activeTask = computed(() =>
  props.state.runtime.tasks?.find((t) => t.state === "running"),
);
const comfyTask = computed(() =>
  activeTask.value &&
  (activeTask.value.kind === "comfyui_start" ||
    activeTask.value.kind === "comfyui_run" ||
    activeTask.value.kind === "comfyui")
    ? activeTask.value
    : undefined,
);
const comfyStartTask = computed(() =>
  activeTask.value && activeTask.value.kind === "comfyui_start"
    ? activeTask.value
    : undefined,
);
const comfyRunTask = computed(() =>
  activeTask.value && activeTask.value.kind === "comfyui_run"
    ? activeTask.value
    : undefined,
);

const comfyRunning = computed(
  () =>
    gpuReady.value &&
    !!(comfy.value?.running || comfy.value?.state === "ready"),
);
const comfyStarting = computed(
  () =>
    gpuReady.value &&
    (comfy.value?.state === "starting" || !!comfyStartTask.value),
);
const queueRunning = computed(() => comfy.value?.queue_running || 0);
const queuePending = computed(() => comfy.value?.queue_pending || 0);
const queueBusy = computed(
  () =>
    comfyRunning.value &&
    (queueRunning.value > 0 || queuePending.value > 0 || !!comfyRunTask.value),
);

const engineStatus = computed(() => {
  if (!gpuReady.value) {
    return {
      label: "未启动",
      color: "default" as const,
      badge: "default" as const,
    };
  }
  if (comfyStarting.value) {
    return {
      label: "正在准备",
      color: "warning" as const,
      badge: "warning" as const,
    };
  }
  if (queueBusy.value) {
    return {
      label: "队列运行中",
      color: "processing" as const,
      badge: "processing" as const,
    };
  }
  if (comfyRunning.value) {
    return {
      label: "运行中",
      color: "success" as const,
      badge: "success" as const,
    };
  }
  if (comfy.value?.state === "error") {
    return {
      label: "启动失败",
      color: "error" as const,
      badge: "error" as const,
    };
  }
  return {
    label: "未启动",
    color: "default" as const,
    badge: "default" as const,
  };
});

const loadedModel = computed(() =>
  !gpuReady.value
    ? undefined
    : props.state.models.find(
        (m) => m.loaded || m.id === props.state.runtime.loaded_model,
      ),
);
const hasLoadedLLM = computed(
  () =>
    gpuReady.value &&
    (!!loadedModel.value ||
      !!props.state.runtime.loaded_model ||
      props.state.runtime.active_engine === "llama"),
);
const llmUnloadNotice = computed(() =>
  loadedModel.value
    ? `当前显存已装载「${loadedModel.value.name}」，启动 ComfyUI 将自动卸载该模型以释放显存，已下载的模型文件保留。`
    : "当前显存已装载语言模型，启动 ComfyUI 将自动卸载该模型以释放显存，已下载的文件保留。",
);
const engineActionBlocked = computed(
  () => busy.value || !!props.state.operation || !!activeTask.value,
);

const comfyTaskPercent = computed(() =>
  Math.max(0, Math.min(100, Math.round((comfyTask.value?.progress || 0) * 100))),
);

// Workflow Studio state
const workflows = computed(() => props.state.workflows || []);
const selectedWorkflowId = ref("");
const selectedWorkflow = computed(() =>
  workflows.value.find((w) => w.id === selectedWorkflowId.value),
);
const fieldValues = ref<Record<string, string | number | boolean>>({});

interface PendingUpload {
  filename: string;
  data: string;
  kind: "image" | "audio";
}
const pendingUploads = ref<Record<string, PendingUpload>>({});
const uploadedInputIds = ref<Record<string, string>>({});
const uploadedNames = ref<Record<string, string>>({});
const localPreviews = ref<Record<string, string>>({});
const uploadingField = ref("");
const dragOverField = ref("");
const submittingRun = ref(false);

function revokeLocalPreview(key: string) {
  const url = localPreviews.value[key];
  if (url) {
    URL.revokeObjectURL(url);
    delete localPreviews.value[key];
  }
}

function revokeAllLocalPreviews() {
  Object.values(localPreviews.value).forEach((url) => URL.revokeObjectURL(url));
  localPreviews.value = {};
}

watch(
  workflows,
  (list) => {
    if (!list.length) {
      selectedWorkflowId.value = "";
      return;
    }
    if (!list.some((w) => w.id === selectedWorkflowId.value)) {
      selectedWorkflowId.value = list[0].id;
    }
  },
  { immediate: true },
);

watch(
  () => selectedWorkflow.value?.id,
  () => {
    const wf = selectedWorkflow.value;
    fieldValues.value = wf ? defaultFieldValues(wf.fields) : {};
    pendingUploads.value = {};
    uploadedInputIds.value = {};
    uploadedNames.value = {};
    revokeAllLocalPreviews();
  },
  { immediate: true },
);

watch(
  () => props.state.runtime.id,
  () => {
    uploadedInputIds.value = {};
    uploadedNames.value = {};
    failedOutputs.value = {};
  },
);

function fieldInputId(field: WorkflowField): string {
  return `comfy-field-${field.key.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
}

function stringFieldValue(key: string): string {
  const value = fieldValues.value[key];
  return value !== undefined && value !== null ? String(value) : "";
}

function numberFieldValue(field: WorkflowField): number | undefined {
  const value = fieldValues.value[field.key];
  if (value === "" || value === undefined || value === null) return undefined;
  const num = Number(value);
  return Number.isFinite(num) ? num : undefined;
}

function onNumberFieldChange(
  field: WorkflowField,
  value: string | number | null,
  isInteger: boolean,
) {
  if (value === null || value === "") {
    fieldValues.value[field.key] = "";
    return;
  }
  const num = Number(value);
  if (Number.isFinite(num)) {
    fieldValues.value[field.key] = isInteger ? Math.round(num) : num;
  }
}

function resetFields() {
  if (!selectedWorkflow.value) return;
  fieldValues.value = defaultFieldValues(selectedWorkflow.value.fields);
  pendingUploads.value = {};
  uploadedInputIds.value = {};
  uploadedNames.value = {};
  revokeAllLocalPreviews();
  notice.value = "已恢复该工作流的默认参数。";
}

const MAX_IMAGE_INPUT_BYTES = 16 * 1024 ** 2;
const MAX_AUDIO_INPUT_BYTES = 64 * 1024 ** 2;

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result || "");
      const idx = result.indexOf(",");
      resolve(idx >= 0 ? result.slice(idx + 1) : result);
    };
    reader.onerror = () => reject(new Error("读取本地文件失败"));
    reader.readAsDataURL(file);
  });
}

async function handleMediaFile(field: WorkflowField, file: File) {
  const kind: "image" | "audio" =
    field.field_type === "audio" ? "audio" : "image";
  const maxBytes =
    kind === "audio" ? MAX_AUDIO_INPUT_BYTES : MAX_IMAGE_INPUT_BYTES;
  if (file.size > maxBytes) {
    notice.value =
      kind === "audio"
        ? "请选择不超过 64 MB 的音频素材文件。"
        : "请选择不超过 16 MB 的图片素材文件。";
    return;
  }

  uploadingField.value = field.key;
  notice.value = "";
  try {
    const data = await readFileAsBase64(file);
    revokeLocalPreview(field.key);
    if (kind === "image" && /^image\/(png|jpeg|webp)$/i.test(file.type)) {
      localPreviews.value[field.key] = URL.createObjectURL(file);
    }

    if (gpuReady.value) {
      const res = await colabOneCommand("comfyui.input.upload", {
        filename: file.name,
        data,
      });
      const assetId = typeof res?.id === "string" ? res.id : "";
      if (!assetId) {
        throw new Error("远端未返回有效素材编号，请重试");
      }
      uploadedInputIds.value[field.key] = assetId;
      fieldValues.value[field.key] = res?.filename || file.name;
      uploadedNames.value[field.key] = file.name;
      delete pendingUploads.value[field.key];
      notice.value = `素材「${file.name}」已上传到远端 GPU。`;
    } else {
      pendingUploads.value[field.key] = {
        filename: file.name,
        data,
        kind,
      };
      delete uploadedInputIds.value[field.key];
      fieldValues.value[field.key] = file.name;
      uploadedNames.value[field.key] = file.name;
      notice.value = `已暂存素材「${file.name}」，启动并运行工作流时将自动上传。`;
    }
  } catch (e) {
    revokeLocalPreview(field.key);
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  } finally {
    uploadingField.value = "";
  }
}

async function onMediaSelected(field: WorkflowField, event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  await handleMediaFile(field, file);
}

async function onMediaDrop(field: WorkflowField, event: DragEvent) {
  dragOverField.value = "";
  if (uploadingField.value === field.key) return;
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;
  await handleMediaFile(field, file);
}

function clearMediaField(field: WorkflowField) {
  delete pendingUploads.value[field.key];
  delete uploadedInputIds.value[field.key];
  delete uploadedNames.value[field.key];
  revokeLocalPreview(field.key);
  fieldValues.value[field.key] =
    field.default !== undefined && field.default !== null
      ? String(field.default)
      : "";
}

// Import modal
const importOpen = ref(false);
const importName = ref("");
const importDescription = ref("");
const importRaw = ref("");
const importing = ref(false);
const importDragging = ref(false);
const importFileInput = ref<HTMLInputElement | null>(null);

const importValidation = computed(() =>
  importRaw.value.trim() ? parseWorkflowImportJSON(importRaw.value) : null,
);
const importNodeCount = computed(() =>
  importValidation.value && importValidation.value.ok
    ? Object.keys(importValidation.value.workflow).length
    : 0,
);
const canSubmitImport = computed(
  () =>
    !importing.value &&
    !!importName.value.trim() &&
    !!importValidation.value?.ok,
);

function openImportModal() {
  importOpen.value = true;
}

function closeImportModal() {
  if (importing.value) return;
  importOpen.value = false;
  importDragging.value = false;
}

async function readImportFile(file: File) {
  try {
    const text = await file.text();
    importRaw.value = text;
    if (!importName.value.trim()) {
      importName.value = file.name.replace(/\.json$/i, "");
    }
  } catch (e) {
    emit("error", String(e));
  }
}

async function onImportFileChange(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  await readImportFile(file);
}

async function onImportFileDrop(event: DragEvent) {
  importDragging.value = false;
  if (importing.value) return;
  const file = event.dataTransfer?.files?.[0];
  if (!file) return;
  await readImportFile(file);
}

async function submitImport() {
  if (!canSubmitImport.value || !importValidation.value?.ok) return;
  importing.value = true;
  notice.value = "";
  try {
    const res = await colabOneCommand("comfyui.workflow.import", {
      name: importName.value.trim(),
      description: importDescription.value.trim(),
      workflow: importValidation.value.workflow,
    });
    const createdId = res?.id || res?.workflow?.id;
    if (typeof createdId === "string" && createdId) {
      selectedWorkflowId.value = createdId;
    }
    importOpen.value = false;
    importName.value = "";
    importDescription.value = "";
    importRaw.value = "";
    notice.value = "工作流已导入，已自动提取可调参数表单。";
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  } finally {
    importing.value = false;
  }
}

async function deleteWorkflow(target: WorkflowTemplate) {
  busy.value = true;
  notice.value = "";
  try {
    await colabOneCommand("comfyui.workflow.delete", { id: target.id });
    notice.value = `已删除工作流「${target.name}」。`;
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  } finally {
    busy.value = false;
  }
}

// Engine lifecycle actions
async function actEngine(
  action: string,
  payload: Record<string, unknown> = {},
  successNotice = "",
) {
  busy.value = true;
  notice.value = "";
  try {
    await colabOneCommand(action, payload);
    if (successNotice) notice.value = successNotice;
    return true;
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
    return false;
  } finally {
    busy.value = false;
  }
}

async function startComfy() {
  if (!gpuReady.value) return;
  await actEngine(
    "comfyui.start",
    {},
    "正在远端准备并启动 ComfyUI 引擎，首次启动需要拉取并初始化环境，请稍候。",
  );
}

async function freeComfyVram() {
  await actEngine("comfyui.free", {}, "已请求 ComfyUI 释放显存与缓存模型。");
}

async function stopComfy() {
  await actEngine("comfyui.stop", {}, "正在停止远端 ComfyUI 引擎。");
}

async function cancelActiveTask() {
  if (!comfyTask.value) return;
  try {
    await colabOneCommand("task.cancel", { id: comfyTask.value.id });
    notice.value = "已请求取消当前任务。";
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  }
}

async function runWorkflow() {
  if (!gpuReady.value) return;
  if (!comfyRunning.value) {
    await startComfy();
    return;
  }
  if (
    !selectedWorkflow.value ||
    submittingRun.value ||
    engineActionBlocked.value
  ) {
    return;
  }

  submittingRun.value = true;
  notice.value = "";
  try {
    const wf = selectedWorkflow.value;
    // Upload any queued media files before submitting workflow
    for (const [key, item] of Object.entries(pendingUploads.value)) {
      const res = await colabOneCommand("comfyui.input.upload", {
        filename: item.filename,
        data: item.data,
      });
      const assetId = typeof res?.id === "string" ? res.id : "";
      if (!assetId) {
        throw new Error(`素材「${item.filename}」上传失败，未返回有效编号`);
      }
      uploadedInputIds.value[key] = assetId;
      fieldValues.value[key] = res?.filename || item.filename;
      delete pendingUploads.value[key];
    }

    const overrides: Record<string, string | number | boolean> = {};
    const inputs: Record<string, string> = {};
    for (const field of wf.fields) {
      if (field.field_type === "image" || field.field_type === "audio") {
        const assetId = uploadedInputIds.value[field.key];
        if (assetId) {
          inputs[field.key] = assetId;
          continue;
        }
        const val = fieldValues.value[field.key];
        if (typeof val === "string" && val.trim()) {
          overrides[field.key] = val;
        }
        continue;
      }

      const val = fieldValues.value[field.key];
      if (field.field_type === "boolean") {
        overrides[field.key] = Boolean(val);
      } else if (field.field_type === "integer") {
        const num = Number(val);
        overrides[field.key] = Number.isFinite(num)
          ? Math.round(num)
          : Number(field.default ?? 0);
      } else if (field.field_type === "number") {
        const num = Number(val);
        overrides[field.key] = Number.isFinite(num)
          ? num
          : Number(field.default ?? 0);
      } else {
        overrides[field.key] =
          val !== undefined && val !== null ? String(val) : "";
      }
    }

    await colabOneCommand("comfyui.run", {
      workflow_id: wf.id,
      overrides,
      inputs,
    });
    notice.value = `工作流「${wf.name}」已提交至远端队列；运行期间暂停空闲释放计时。`;
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  } finally {
    submittingRun.value = false;
  }
}

// Output gallery & lazy Blob URL preview
const outputs = computed(() => props.state.comfy_outputs || []);
const blobUrls = ref<Record<string, string>>({});
const fetchingOutputs = ref<Record<string, boolean>>({});
const failedOutputs = ref<Record<string, string>>({});
let disposed = false;

async function loadOutputPreview(item: WorkflowOutput) {
  if (
    blobUrls.value[item.id] ||
    fetchingOutputs.value[item.id] ||
    !gpuReady.value
  ) {
    return;
  }
  fetchingOutputs.value[item.id] = true;
  delete failedOutputs.value[item.id];
  try {
    const url = await outputBlobURL(item.id);
    if (disposed) {
      URL.revokeObjectURL(url);
    } else {
      blobUrls.value[item.id] = url;
    }
  } catch (e) {
    failedOutputs.value[item.id] = String(e);
  } finally {
    fetchingOutputs.value[item.id] = false;
  }
}

watch(
  [outputs, gpuReady],
  ([list, ready]) => {
    const activeIds = new Set(list.map((item) => item.id));
    for (const [id, url] of Object.entries(blobUrls.value)) {
      if (!activeIds.has(id)) {
        URL.revokeObjectURL(url);
        delete blobUrls.value[id];
      }
    }
    if (!ready) return;
    // Automatically load previews for recent image outputs without saturating the bridge.
    for (const item of list.slice(0, 6)) {
      if (
        item.kind === "image" &&
        !blobUrls.value[item.id] &&
        !failedOutputs.value[item.id] &&
        !Object.values(fetchingOutputs.value).some(Boolean)
      ) {
        void loadOutputPreview(item);
      }
    }
  },
  { immediate: true },
);

async function exportOutput(item: WorkflowOutput) {
  try {
    const path = await colabOneExportWorkflowOutput(item.id);
    if (path) {
      notice.value = "作品已导出到本地。";
    }
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  }
}

async function deleteOutput(target: WorkflowOutput) {
  try {
    await colabOneCommand("comfyui.output.delete", { id: target.id });
    if (blobUrls.value[target.id]) {
      URL.revokeObjectURL(blobUrls.value[target.id]);
      delete blobUrls.value[target.id];
    }
    notice.value = "已删除该输出作品。";
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  }
}

function outputKindLabel(kind: WorkflowOutput["kind"]) {
  if (kind === "video") return "视频";
  if (kind === "audio") return "音频";
  return "图片";
}

function outputKindColor(kind: WorkflowOutput["kind"]) {
  if (kind === "video") return "purple";
  if (kind === "audio") return "cyan";
  return "blue";
}

// Native Canvas tab state
const canvasDomain = ref(props.state.comfy_canvas_settings?.domain || "");
const canvasTunnelToken = ref("");
const canvasSettingsEdited = ref(false);
const copiedCanvasUrl = ref(false);
let copyTimer: ReturnType<typeof setTimeout> | undefined;

watch(
  () => props.state.comfy_canvas_settings,
  (settings) => {
    if (!settings || canvasSettingsEdited.value) return;
    canvasDomain.value = settings.domain || "";
  },
  { deep: true },
);

function onTabChange(key: string | number) {
  activeTab.value = key === "canvas" ? "canvas" : "studio";
}

const canvasState = computed(() => canvas.value?.state || "stopped");
const canvasEnabled = computed(
  () =>
    !!canvas.value?.enabled ||
    canvasState.value === "starting" ||
    canvasState.value === "ready" ||
    canvasState.value === "reconnecting",
);
const canvasReady = computed(
  () => canvasState.value === "ready" && !!canvas.value?.url,
);
const canvasStatusLabel = computed(
  () =>
    ({
      stopped: "未开启",
      starting: "正在连接",
      ready: "已开启",
      reconnecting: "连接待恢复",
      stopping: "正在关闭",
      failed: "连接失败",
    })[canvasState.value] || "未开启",
);
const canvasTagColor = computed(() => {
  if (canvasState.value === "ready") return "success";
  if (
    canvasState.value === "starting" ||
    canvasState.value === "reconnecting"
  ) {
    return "warning";
  }
  if (canvasState.value === "failed") return "error";
  return "default";
});
const canvasBadgeStatus = computed(() => {
  if (canvasState.value === "ready") return "success";
  if (
    canvasState.value === "starting" ||
    canvasState.value === "reconnecting"
  ) {
    return "processing";
  }
  if (canvasState.value === "failed") return "error";
  return "default";
});
const canvasPublicHost = computed(() => extractPublicHost(canvas.value?.url));

async function startCanvas() {
  if (!gpuReady.value) return;
  if (!comfyRunning.value) {
    await startComfy();
    return;
  }
  const ok = await actEngine(
    "comfyui.canvas.start",
    {
      mode: "named",
      domain: canvasDomain.value.trim(),
      tunnel_token: canvasTunnelToken.value.trim(),
    },
    "正在开启原生画布固定域名隧道，验证通过后即可打开。",
  );
  if (ok) {
    canvasTunnelToken.value = "";
    canvasSettingsEdited.value = false;
  }
}

async function saveCanvasSettings() {
  const ok = await actEngine(
    "comfyui.canvas.save",
    {
      mode: "named",
      domain: canvasDomain.value.trim(),
      tunnel_token: canvasTunnelToken.value.trim(),
    },
    "原生画布固定域名配置已保存到本机凭据库。",
  );
  if (ok) {
    canvasTunnelToken.value = "";
    canvasSettingsEdited.value = false;
  }
}

async function stopCanvas() {
  await actEngine("comfyui.canvas.stop", {}, "已关闭原生画布安全隧道。");
}

async function launchCanvas(target: "window" | "browser") {
  notice.value = "";
  try {
    await colabOneOpenComfyCanvas(target);
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  }
}

async function copyCanvasAuthUrl() {
  notice.value = "";
  try {
    const res = await colabOneCommand("comfyui.canvas.url");
    const url =
      typeof res === "string"
        ? res
        : res?.url || res?.auth_url || canvas.value?.url || "";
    if (!url) {
      throw new Error("暂未获取到鉴权链接，请确认画布隧道已就绪");
    }
    try {
      await colabOneWriteClipboard(url);
      await navigator.clipboard?.writeText(url).catch(() => {});
    } catch {
      await navigator.clipboard.writeText(url);
    }
    copiedCanvasUrl.value = true;
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copiedCanvasUrl.value = false;
    }, 2000);
    notice.value = "鉴权链接已复制（含访问令牌，请勿分享给他人）。";
  } catch (e) {
    const msg = String(e);
    notice.value = msg;
    emit("error", msg);
  }
}

onMounted(() => {
  tabsWrapRef.value
    ?.querySelector('.ant-tabs-nav[role="tablist"]')
    ?.setAttribute("aria-label", "ComfyUI 模式");
});

onUnmounted(() => {
  clearTimeout(copyTimer);
  disposed = true;
  Object.values(blobUrls.value).forEach((url) => URL.revokeObjectURL(url));
  revokeAllLocalPreviews();
});
</script>

<template>
  <div class="co-comfy-page">
    <div class="co-page-header">
      <div>
        <h1 class="co-page-title">ComfyUI</h1>
        <p class="co-page-subtitle">
          在远端 GPU 一键运行工作流表单，或通过安全隧道访问完整原生节点画布。
        </p>
      </div>
    </div>

    <!-- Top Engine Status Card -->
    <ACard
      class="co-comfy-engine-card"
      :bordered="true"
      aria-label="ComfyUI 引擎状态"
    >
      <div class="co-comfy-engine-head">
        <div class="co-comfy-engine-icon" aria-hidden="true">
          <PhTreeStructure :size="22" />
        </div>

        <div class="co-comfy-engine-meta">
          <div class="co-comfy-engine-title-row">
            <h2>ComfyUI 工作流引擎</h2>
            <ATag :color="engineStatus.color" class="co-comfy-status-tag">
              <ABadge :status="engineStatus.badge" />
              <span>{{ engineStatus.label }}</span>
            </ATag>
            <ATag class="co-comfy-mono">{{ comfyTag }}</ATag>
            <ATag
              v-if="comfyRunning"
              :color="queueBusy ? 'processing' : 'default'"
              class="co-comfy-queue-tag"
            >
              运行中 {{ queueRunning }} · 排队 {{ queuePending }}
            </ATag>
          </div>

          <p class="co-comfy-engine-sub">
            <template v-if="!gpuReady">
              {{
                gpuStarting
                  ? "远端 GPU 正在启动中；就绪后即可运行 ComfyUI 工作流或打开原生节点画布。"
                  : "远端 GPU 未启动；启动后即可运行 ComfyUI 工作流或打开原生节点画布。"
              }}
            </template>
            <template v-else-if="comfyRunning">
              任务与画布操作期间自动暂停空闲释放（空闲策略：{{
                formatIdlePolicyLabel(props.state.settings.idle_minutes)
              }}）
            </template>
            <template v-else-if="comfyStarting">
              正在远端 GPU 准备 ComfyUI 运行环境，完成后即可直接运行工作流。
            </template>
            <template v-else>
              远端 GPU 已就绪，启动 ComfyUI 引擎后即可运行工作流或打开原生画布。
            </template>
          </p>
        </div>

        <div class="co-comfy-engine-actions">
          <ASpace wrap :size="8">
            <template v-if="comfyRunning">
              <AButton
                :disabled="engineActionBlocked"
                @click="freeComfyVram"
              >
                <template #icon><PhBroom :size="15" /></template>
                释放显存
              </AButton>
              <AButton
                danger
                :disabled="engineActionBlocked"
                @click="stopComfy"
              >
                <template #icon><PhStop :size="14" weight="fill" /></template>
                停止 ComfyUI
              </AButton>
            </template>
            <template v-else>
              <ATooltip
                :title="
                  !gpuReady
                    ? gpuStarting
                      ? '远端 GPU 正在启动中'
                      : '请先启动远端 GPU'
                    : ''
                "
              >
                <AButton
                  type="primary"
                  :loading="comfyStarting"
                  :disabled="!gpuReady || engineActionBlocked || comfyStarting"
                  @click="startComfy"
                >
                  <template #icon><PhPlay :size="14" weight="fill" /></template>
                  {{ comfyStarting ? "正在准备 ComfyUI…" : "启动 ComfyUI" }}
                </AButton>
              </ATooltip>
              <AButton
                v-if="!gpuReady"
                :type="gpuStarting ? 'default' : 'primary'"
                @click="emit('device')"
              >
                <template #icon><PhHardDrives :size="15" /></template>
                {{ gpuStarting ? "查看 GPU 启动进度" : "去启动 GPU" }}
                <PhArrowRight :size="14" />
              </AButton>
            </template>
          </ASpace>
        </div>
      </div>

      <!-- Notice when a GGUF LLM is currently loaded in VRAM -->
      <AAlert
        v-if="gpuReady && !comfyRunning && !comfyStarting && hasLoadedLLM"
        type="info"
        show-icon
        class="co-comfy-card-alert"
        :message="llmUnloadNotice"
      />

      <!-- Engine error banner -->
      <AAlert
        v-if="comfy?.error"
        type="error"
        show-icon
        class="co-comfy-card-alert"
        :message="comfy.error"
      />

      <!-- Active ComfyUI task progress bar -->
      <div v-if="comfyTask" class="co-comfy-task-banner" role="status">
        <div class="co-comfy-task-info">
          <div class="co-comfy-task-head">
            <strong>{{
              comfyTask.message || "远端 ComfyUI 任务进行中…"
            }}</strong>
            <span class="co-comfy-mono">{{ comfyTaskPercent }}%</span>
          </div>
          <AProgress
            :percent="comfyTaskPercent"
            status="active"
            :show-info="false"
            size="small"
          />
        </div>
        <AButton
          size="small"
          danger
          @click="cancelActiveTask"
        >
          取消任务
        </AButton>
      </div>
    </ACard>

    <!-- Status Notice -->
    <AAlert
      v-if="notice"
      :key="notice"
      type="info"
      show-icon
      closable
      role="status"
      class="co-comfy-notice"
      :message="notice"
      @close="notice = ''"
    />

    <!-- Dual-layer Workspace Tabs -->
    <div ref="tabsWrapRef" class="co-comfy-tabs-wrap">
      <ATabs
        :active-key="activeTab"
        class="co-comfy-tabs"
        @update:active-key="onTabChange"
      >
        <!-- TAB 1: 工作流工坊 -->
        <ATabPane key="studio">
          <template #tab>
            <span class="co-comfy-tab-label">
              <PhSlidersHorizontal :size="16" />
              <span>工作流工坊</span>
              <ABadge
                v-if="workflows.length"
                :count="workflows.length"
                class="co-comfy-tab-badge"
              />
            </span>
          </template>

          <section class="co-comfy-studio-section" aria-label="工作流工坊">
            <!-- Studio Action Bar -->
            <div class="co-comfy-studio-toolbar">
              <div class="co-comfy-studio-toolbar-copy">
                <div class="co-comfy-toolbar-title-row">
                  <h2>工作流工坊</h2>
                  <ATag class="co-comfy-count-tag">
                    {{ workflows.length }} 个工作流
                  </ATag>
                </div>
                <p>
                  自动解析 ComfyUI API JSON 为简洁表单，在远端 GPU
                  一键运行并归档图片、视频与音频输出。
                </p>
              </div>
              <AButton
                type="primary"
                @click="openImportModal"
              >
                <template #icon><PhPlus :size="15" /></template>
                导入 API 工作流 (JSON)
              </AButton>
            </div>

            <div class="co-comfy-studio-grid">
              <!-- Left Column: Workflow Library + Dynamic Parameter Runner -->
              <div class="co-comfy-left-col">
                <!-- Empty state when no workflows exist -->
                <ACard
                  v-if="!workflows.length"
                  class="co-comfy-card co-comfy-empty-card"
                >
                  <AEmpty description="还没有可用的工作流">
                    <template #image>
                      <div class="co-comfy-empty-emblem">
                        <PhFlowArrow :size="30" />
                      </div>
                    </template>
                    <p class="co-comfy-empty-copy">
                      支持导入任意 ComfyUI「Save (API Format)」JSON
                      工作流并自动生成一键参数表单，未启动 GPU 时也可提前导入管理。
                    </p>
                    <ASpace wrap :size="10" class="co-comfy-empty-actions">
                      <AButton
                        type="primary"
                        @click="openImportModal"
                      >
                        <template #icon><PhUploadSimple :size="15" /></template>
                        导入工作流 JSON
                      </AButton>
                      <AButton
                        @click="activeTab = 'canvas'"
                      >
                        <template #icon><PhGraph :size="15" /></template>
                        前往「原生画布」搭建流程
                      </AButton>
                    </ASpace>
                  </AEmpty>
                </ACard>

                <!-- Workflow library + dynamic parameter form -->
                <template v-else>
                  <ACard
                    size="small"
                    class="co-comfy-card"
                    title="已导入工作流"
                    aria-label="工作流列表"
                  >
                    <template #extra>
                      <ATag class="co-comfy-count-tag">
                        {{ workflows.length }} 个
                      </ATag>
                    </template>

                    <div
                      class="co-comfy-workflow-list"
                      role="listbox"
                      aria-label="选择工作流"
                    >
                      <div
                        v-for="wf in workflows"
                        :key="wf.id"
                        role="option"
                        tabindex="0"
                        class="co-comfy-workflow-item"
                        :class="{ selected: wf.id === selectedWorkflowId }"
                        :aria-selected="wf.id === selectedWorkflowId"
                        @click="selectedWorkflowId = wf.id"
                        @keydown.enter.prevent="selectedWorkflowId = wf.id"
                        @keydown.space.prevent="selectedWorkflowId = wf.id"
                      >
                        <div class="co-comfy-workflow-item-main">
                          <div class="co-comfy-workflow-item-title">
                            <strong>{{ wf.name }}</strong>
                            <ATag
                              :color="
                                wf.source === 'builtin' ? 'blue' : 'default'
                              "
                            >
                              {{ wf.source === "builtin" ? "内置" : "自定义" }}
                            </ATag>
                          </div>
                          <p
                            v-if="wf.description"
                            class="co-comfy-workflow-desc"
                          >
                            {{ wf.description }}
                          </p>
                          <div class="co-comfy-workflow-badges">
                            <ATag class="co-comfy-meta-tag">
                              {{ wf.node_count }} 个节点
                            </ATag>
                            <ATag class="co-comfy-meta-tag">
                              {{ wf.fields.length }} 个参数
                            </ATag>
                            <ATag
                              v-if="wf.min_vram_mib > 0"
                              color="gold"
                              class="co-comfy-meta-tag"
                            >
                              建议显存
                              {{
                                Math.max(1, Math.round(wf.min_vram_mib / 1024))
                              }}
                              GB+
                            </ATag>
                          </div>
                        </div>

                        <div
                          v-if="wf.source === 'custom'"
                          class="co-comfy-wf-actions"
                          @click.stop
                        >
                          <APopconfirm
                            :title="`删除工作流「${wf.name}」？`"
                            description="删除后无法从本地工作流库恢复，已生成的输出作品保留。"
                            ok-text="确认删除"
                            cancel-text="取消"
                            :ok-button-props="{ danger: true }"
                            placement="topRight"
                            @confirm="deleteWorkflow(wf)"
                          >
                            <ATooltip title="删除自定义工作流">
                              <AButton
                                type="text"
                                size="small"
                                danger
                                aria-label="删除自定义工作流"
                                :disabled="busy"
                              >
                                <template #icon>
                                  <PhTrash :size="15" />
                                </template>
                              </AButton>
                            </ATooltip>
                          </APopconfirm>
                        </div>
                      </div>
                    </div>
                  </ACard>

                  <!-- Dynamic Parameter Runner Card -->
                  <ACard
                    v-if="selectedWorkflow"
                    size="small"
                    class="co-comfy-card co-comfy-param-card"
                  >
                    <template #title>
                      <div class="co-comfy-param-title-wrap">
                        <span>{{ selectedWorkflow.name }}</span>
                        <small
                          v-if="selectedWorkflow.description"
                          class="co-comfy-param-subtitle"
                        >
                          {{ selectedWorkflow.description }}
                        </small>
                      </div>
                    </template>
                    <template #extra>
                      <AButton
                        v-if="selectedWorkflow.fields.length"
                        type="text"
                        size="small"
                        @click="resetFields"
                      >
                        <template #icon>
                          <PhArrowsClockwise :size="14" />
                        </template>
                        恢复默认
                      </AButton>
                    </template>

                    <form
                      class="co-comfy-param-form"
                      @submit.prevent="runWorkflow"
                    >
                      <AAlert
                        v-if="!selectedWorkflow.fields.length"
                        type="info"
                        show-icon
                        message="该工作流未检测到可调输入参数，将按 JSON 原始参数直接运行。"
                      />

                      <div v-else class="co-comfy-fields-stack">
                        <div
                          v-for="field in selectedWorkflow.fields"
                          :key="field.key"
                          class="co-comfy-field"
                        >
                          <!-- Boolean Switch -->
                          <div
                            v-if="field.field_type === 'boolean'"
                            class="co-comfy-switch-row"
                          >
                            <div class="co-comfy-switch-copy">
                              <strong>{{ field.label }}</strong>
                              <small class="co-comfy-field-node">
                                节点 #{{ field.node_id }} ·
                                {{ field.input_name }}
                              </small>
                            </div>
                            <ASwitch
                              :checked="Boolean(fieldValues[field.key])"
                              @update:checked="
                                (val) => (fieldValues[field.key] = Boolean(val))
                              "
                            />
                          </div>

                          <!-- Image / Audio Drag-and-Drop Upload Box -->
                          <div
                            v-else-if="
                              field.field_type === 'image' ||
                              field.field_type === 'audio'
                            "
                            class="co-comfy-media-field"
                          >
                            <div class="co-comfy-field-label-row">
                              <span>{{ field.label }}</span>
                              <small class="co-comfy-field-node">
                                节点 #{{ field.node_id }} ·
                                {{ field.input_name }}
                              </small>
                            </div>

                            <label
                              class="co-comfy-media-dropzone"
                              :class="{
                                dragging: dragOverField === field.key,
                                busy: uploadingField === field.key,
                              }"
                              @dragover.prevent="dragOverField = field.key"
                              @dragleave="
                                if (dragOverField === field.key)
                                  dragOverField = '';
                              "
                              @drop.prevent="onMediaDrop(field, $event)"
                            >
                              <input
                                class="co-visually-hidden"
                                type="file"
                                :accept="
                                  field.field_type === 'audio'
                                    ? '.wav,.mp3,.flac,.ogg,.m4a,audio/*'
                                    : 'image/png,image/jpeg,image/webp'
                                "
                                :disabled="uploadingField === field.key"
                                @change="onMediaSelected(field, $event)"
                              />
                              <ASpin
                                v-if="uploadingField === field.key"
                                size="small"
                              />
                              <img
                                v-else-if="
                                  field.field_type === 'image' &&
                                  localPreviews[field.key]
                                "
                                :src="localPreviews[field.key]"
                                :alt="uploadedNames[field.key] || field.label"
                                class="co-comfy-media-thumb"
                              />
                              <span v-else class="co-comfy-media-icon-wrap">
                                <component
                                  :is="
                                    field.field_type === 'audio'
                                      ? PhWaveform
                                      : PhImage
                                  "
                                  :size="20"
                                />
                              </span>
                              <div class="co-comfy-media-dropzone-copy">
                                <template v-if="uploadingField === field.key">
                                  <strong>正在上传至远端 ComfyUI…</strong>
                                  <small>请稍候，上传完成后即可运行</small>
                                </template>
                                <template v-else-if="uploadedNames[field.key]">
                                  <strong>{{ uploadedNames[field.key] }}</strong>
                                  <small>点击或拖拽新文件以替换</small>
                                </template>
                                <template v-else-if="fieldValues[field.key]">
                                  <strong>
                                    当前文件：{{ fieldValues[field.key] }}
                                  </strong>
                                  <small>点击或拖拽新文件以替换</small>
                                </template>
                                <template v-else>
                                  <strong>
                                    {{
                                      field.field_type === "audio"
                                        ? "拖拽或点击选择音频文件"
                                        : "拖拽或点击选择图片文件"
                                    }}
                                  </strong>
                                  <small>
                                    {{
                                      field.field_type === "audio"
                                        ? "支持 WAV / MP3 / FLAC / OGG / M4A（≤ 64 MB）"
                                        : "支持 PNG / JPEG / WebP（≤ 16 MB）"
                                    }}
                                  </small>
                                </template>
                              </div>
                            </label>

                            <div
                              v-if="
                                uploadedNames[field.key] ||
                                pendingUploads[field.key]
                              "
                              class="co-comfy-media-status"
                            >
                              <ASpace :size="6" wrap>
                                <ATag
                                  :color="
                                    pendingUploads[field.key]
                                      ? 'warning'
                                      : 'success'
                                  "
                                >
                                  {{
                                    pendingUploads[field.key]
                                      ? "待提交时上传"
                                      : "已上传至远端"
                                  }}
                                </ATag>
                                <ATag
                                  v-if="uploadedNames[field.key]"
                                  class="co-comfy-filename-pill"
                                >
                                  {{ uploadedNames[field.key] }}
                                </ATag>
                              </ASpace>
                              <AButton
                                type="link"
                                size="small"
                                danger
                                @click="clearMediaField(field)"
                              >
                                清除
                              </AButton>
                            </div>
                          </div>

                          <!-- Standard Labeled AntD Controls -->
                          <div v-else class="co-comfy-field-control">
                            <label
                              :for="fieldInputId(field)"
                              class="co-comfy-field-label-row"
                            >
                              <span>{{ field.label }}</span>
                              <small class="co-comfy-field-node">
                                节点 #{{ field.node_id }} ·
                                {{ field.input_name }}
                              </small>
                            </label>

                            <ATextarea
                              v-if="field.field_type === 'textarea'"
                              :id="fieldInputId(field)"
                              :value="stringFieldValue(field.key)"
                              :auto-size="{ minRows: 3, maxRows: 8 }"
                              @update:value="
                                (val) => (fieldValues[field.key] = val)
                              "
                            />

                            <ASelect
                              v-else-if="field.field_type === 'select'"
                              :id="fieldInputId(field)"
                              :value="stringFieldValue(field.key)"
                              class="co-comfy-full-width"
                              @update:value="
                                (val) =>
                                  (fieldValues[field.key] = String(val ?? ''))
                              "
                            >
                              <ASelectOption
                                v-if="
                                  fieldValues[field.key] &&
                                  !(field.options || []).includes(
                                    String(fieldValues[field.key]),
                                  )
                                "
                                :value="String(fieldValues[field.key])"
                              >
                                {{ fieldValues[field.key] }}
                              </ASelectOption>
                              <ASelectOption
                                v-for="opt in field.options || []"
                                :key="opt"
                                :value="opt"
                              >
                                {{ opt }}
                              </ASelectOption>
                            </ASelect>

                            <AInputNumber
                              v-else-if="
                                field.field_type === 'integer' ||
                                field.field_type === 'number'
                              "
                              :id="fieldInputId(field)"
                              :value="numberFieldValue(field)"
                              :min="field.min_value ?? undefined"
                              :max="field.max_value ?? undefined"
                              :step="
                                field.step ??
                                (field.field_type === 'integer' ? 1 : 0.1)
                              "
                              :precision="
                                field.field_type === 'integer' ? 0 : undefined
                              "
                              class="co-comfy-full-width"
                              @update:value="
                                (val) =>
                                  onNumberFieldChange(
                                    field,
                                    val,
                                    field.field_type === 'integer',
                                  )
                              "
                            />

                            <AInput
                              v-else
                              :id="fieldInputId(field)"
                              :value="stringFieldValue(field.key)"
                              @update:value="
                                (val) => (fieldValues[field.key] = val)
                              "
                            />
                          </div>
                        </div>
                      </div>

                      <ADivider class="co-comfy-form-divider" />

                      <!-- Submit Bar -->
                      <div class="co-comfy-form-actions">
                        <ATooltip :title="!gpuReady ? '请先启动远端 GPU' : ''">
                          <AButton
                            type="primary"
                            html-type="submit"
                            class="co-comfy-run-btn"
                            :loading="submittingRun"
                            :disabled="
                              !gpuReady ||
                              submittingRun ||
                              !!uploadingField ||
                              engineActionBlocked
                            "
                          >
                            <template #icon>
                              <PhSparkle v-if="comfyRunning" :size="15" />
                              <PhPlay v-else :size="14" weight="fill" />
                            </template>
                            {{
                              !gpuReady
                                ? "运行工作流"
                                : !comfyRunning
                                  ? "先启动 ComfyUI 引擎"
                                  : submittingRun
                                    ? "正在提交…"
                                    : "运行工作流"
                            }}
                          </AButton>
                        </ATooltip>
                        <AButton
                          v-if="comfyRunTask || comfyTask"
                          danger
                          @click="cancelActiveTask"
                        >
                          取消任务
                        </AButton>
                      </div>
                    </form>
                  </ACard>
                </template>
              </div>

              <!-- Right Column: Output Gallery Card -->
              <section class="co-comfy-gallery-col" aria-label="作品输出区">
                <ACard
                  size="small"
                  class="co-comfy-card co-comfy-gallery-card"
                  title="作品输出区"
                >
                  <template #extra>
                    <ATag class="co-comfy-count-tag">{{ outputs.length }} 件</ATag>
                  </template>

                  <div
                    v-if="!outputs.length"
                    class="co-comfy-gallery-empty-wrap"
                  >
                    <AEmpty description="暂无输出作品">
                      <template #image>
                        <div class="co-comfy-empty-emblem">
                          <PhImage :size="30" />
                        </div>
                      </template>
                      <p class="co-comfy-empty-copy">
                        运行任意工作流后，生成的图片、视频与音频会自动展示在这里，支持在线预览与一键导出到本地。
                      </p>
                      <small class="co-comfy-empty-hint">
                        {{
                          state.setup.drive
                            ? "已挂载 Google 云盘，输出产物将自动持久化。"
                            : "输出暂存在当前远端 GPU，建议在释放实例前导出到本地。"
                        }}
                      </small>
                    </AEmpty>
                  </div>

                  <div v-else class="co-comfy-output-grid">
                    <article
                      v-for="item in outputs"
                      :key="item.id"
                      class="co-comfy-output-item"
                    >
                      <div class="co-comfy-output-frame">
                        <img
                          v-if="item.kind === 'image' && blobUrls[item.id]"
                          :src="blobUrls[item.id]"
                          :alt="item.filename"
                        />
                        <video
                          v-else-if="item.kind === 'video' && blobUrls[item.id]"
                          :src="blobUrls[item.id]"
                          controls
                          preload="metadata"
                        />
                        <div
                          v-else-if="item.kind === 'audio' && blobUrls[item.id]"
                          class="co-comfy-audio-box"
                        >
                          <PhWaveform :size="28" class="co-comfy-audio-icon" />
                          <audio
                            :src="blobUrls[item.id]"
                            controls
                            preload="metadata"
                          />
                        </div>
                        <div
                          v-else-if="fetchingOutputs[item.id]"
                          class="co-comfy-preview-loading"
                        >
                          <ASpin />
                          <span>正在读取远端作品…</span>
                        </div>
                        <button
                          v-else
                          type="button"
                          class="co-comfy-preview-trigger"
                          :disabled="!gpuReady"
                          @click="loadOutputPreview(item)"
                        >
                          <component
                            :is="
                              item.kind === 'video'
                                ? PhVideoCamera
                                : item.kind === 'audio'
                                  ? PhWaveform
                                  : PhImage
                            "
                            :size="28"
                          />
                          <span>
                            {{
                              failedOutputs[item.id]
                                ? "重试预览"
                                : item.kind === "video"
                                  ? "加载视频预览"
                                  : item.kind === "audio"
                                    ? "加载音频预览"
                                    : "加载图片预览"
                            }}
                          </span>
                        </button>
                      </div>

                      <div class="co-comfy-output-body">
                        <div class="co-comfy-output-title-row">
                          <ATooltip :title="item.workflow_name || 'ComfyUI 工作流'">
                            <strong>{{
                              item.workflow_name || "ComfyUI 工作流"
                            }}</strong>
                          </ATooltip>
                          <ATag :color="outputKindColor(item.kind)">
                            {{ outputKindLabel(item.kind) }}
                          </ATag>
                        </div>

                        <ATooltip :title="item.filename">
                          <small class="co-comfy-mono co-comfy-output-filename">
                            {{ item.filename }}
                          </small>
                        </ATooltip>

                        <div class="co-comfy-output-meta-tags">
                          <ATag class="co-comfy-meta-tag">
                            {{ formatOutputSize(item.size) }}
                          </ATag>
                          <ATag class="co-comfy-meta-tag">
                            {{ item.elapsed.toFixed(1) }} 秒
                          </ATag>
                        </div>

                        <AAlert
                          v-if="failedOutputs[item.id]"
                          type="error"
                          class="co-comfy-output-error"
                          :message="failedOutputs[item.id]"
                        />

                        <div class="co-comfy-output-actions">
                          <AButton
                            size="small"
                            :disabled="!gpuReady"
                            @click="exportOutput(item)"
                          >
                            <template #icon>
                              <PhDownloadSimple :size="14" />
                            </template>
                            导出到本地
                          </AButton>

                          <APopconfirm
                            title="删除这件输出作品？"
                            :description="`文件「${item.filename}」将从远端输出目录移除；已导出到本地的文件不受影响。`"
                            ok-text="确认删除"
                            cancel-text="取消"
                            :disabled="!gpuReady"
                            :ok-button-props="{ danger: true }"
                            placement="topRight"
                            @confirm="deleteOutput(item)"
                          >
                            <AButton
                              size="small"
                              danger
                              :disabled="!gpuReady"
                              aria-label="删除作品"
                            >
                              <template #icon>
                                <PhTrash :size="14" />
                              </template>
                              删除
                            </AButton>
                          </APopconfirm>
                        </div>
                      </div>
                    </article>
                  </div>
                </ACard>
              </section>
            </div>
          </section>
        </ATabPane>

        <!-- TAB 2: 原生画布 -->
        <ATabPane key="canvas">
          <template #tab>
            <span class="co-comfy-tab-label">
              <PhGraph :size="16" />
              <span>原生画布</span>
              <ABadge
                v-if="canvasReady"
                status="success"
                aria-label="画布已开启"
              />
            </span>
          </template>

          <section class="co-comfy-canvas-section" aria-label="ComfyUI 原生画布">
            <!-- Security & Idle-Protection Banner -->
            <AAlert type="info" show-icon class="co-comfy-canvas-security-alert">
              <template #icon>
                <PhShieldCheck :size="20" />
              </template>
              <template #message>
                <div class="co-comfy-canvas-banner-head">
                  <h2>原生节点画布安全访问</h2>
                  <ATag :color="canvasTagColor" class="co-comfy-status-tag">
                    <ABadge :status="canvasBadgeStatus" />
                    <span>{{ canvasStatusLabel }}</span>
                  </ATag>
                </div>
              </template>
              <template #description>
                完整 ComfyUI 节点编辑器运行在远端 GPU，通过独立令牌鉴权与 Cloudflare
                安全隧道访问；操作与队列运行期间自动暂停空闲释放倒计时。
              </template>
            </AAlert>

            <AAlert
              v-if="canvas?.error"
              type="error"
              show-icon
              :message="canvas.error"
            />

            <div class="co-comfy-canvas-grid">
              <!-- Fixed-Domain Tunnel & Lifecycle Card -->
              <ACard class="co-comfy-card" title="固定域名与隧道配置">
                <div class="co-comfy-named-fields">
                  <p class="co-comfy-canvas-help">
                    通过 Cloudflare Named Tunnel 绑定固定公网 HTTPS
                    域名访问原生画布，首次打开自动完成安全鉴权。域名与隧道凭证保存在你的
                    Cloudflare 账户与本机凭据库中。
                  </p>

                  <div class="co-comfy-field-control">
                    <label
                      for="comfy-canvas-domain"
                      class="co-comfy-field-label-row"
                    >
                      <span>画布域名</span>
                    </label>
                    <AInput
                      id="comfy-canvas-domain"
                      v-model:value="canvasDomain"
                      :disabled="canvasEnabled || busy"
                      placeholder="comfy.example.com"
                      autocomplete="off"
                      spellcheck="false"
                      allow-clear
                      @change="canvasSettingsEdited = true"
                    />
                  </div>

                  <div class="co-comfy-field-control">
                    <label
                      for="comfy-canvas-token"
                      class="co-comfy-field-label-row"
                    >
                      <span>Cloudflare 安装命令或 Tunnel Token</span>
                    </label>
                    <AInputPassword
                      id="comfy-canvas-token"
                      v-model:value="canvasTunnelToken"
                      :disabled="canvasEnabled || busy"
                      :placeholder="
                        state.comfy_canvas_settings?.has_token
                          ? '已安全保存；留空继续使用'
                          : '直接粘贴 Cloudflare 安装命令或 Tunnel Token'
                      "
                      autocomplete="off"
                      spellcheck="false"
                    />
                  </div>

                  <details class="co-comfy-canvas-guide">
                    <summary>首次配置？查看 3 步指引</summary>
                    <ol>
                      <li>
                        在 Cloudflare Zero Trust 创建一个 Cloudflared 隧道。
                        <AButton
                          type="link"
                          size="small"
                          class="co-inline-link co-comfy-inline-link"
                          @click="colabOneOpenURL('https://one.dash.cloudflare.com/')"
                        >
                          打开控制台
                          <PhArrowSquareOut :size="14" />
                        </AButton>
                      </li>
                      <li>
                        添加一个公开域名，服务类型选 HTTP，地址填
                        <code>http://127.0.0.1:8189</code>（无需在 Cloudflare
                        重复开启交互式登录）。
                      </li>
                      <li>
                        将 Cloudflare 安装命令或 Tunnel Token
                        粘贴到上方，保存并点击「开启原生画布」。
                      </li>
                    </ol>
                  </details>
                </div>

                <ADivider class="co-comfy-form-divider" />

                <ASpace wrap :size="10">
                  <ATooltip v-if="!gpuReady" title="请先启动远端 GPU">
                    <AButton type="primary" disabled>
                      <template #icon>
                        <PhPlay :size="14" weight="fill" />
                      </template>
                      开启原生画布
                    </AButton>
                  </ATooltip>
                  <AButton
                    v-else-if="!comfyRunning"
                    type="primary"
                    :loading="comfyStarting"
                    :disabled="engineActionBlocked || comfyStarting"
                    @click="startComfy"
                  >
                    <template #icon>
                      <PhPlay :size="14" weight="fill" />
                    </template>
                    {{
                      comfyStarting ? "正在准备 ComfyUI…" : "先启动 ComfyUI 引擎"
                    }}
                  </AButton>
                  <AButton
                    v-else-if="canvasEnabled"
                    type="primary"
                    danger
                    :disabled="busy || !!state.operation"
                    @click="stopCanvas"
                  >
                    <template #icon>
                      <PhStop :size="14" weight="fill" />
                    </template>
                    关闭画布隧道
                  </AButton>
                  <AButton
                    v-else
                    type="primary"
                    :disabled="busy || !!state.operation"
                    @click="startCanvas"
                  >
                    <template #icon>
                      <PhPlay :size="14" weight="fill" />
                    </template>
                    开启原生画布
                  </AButton>

                  <AButton
                    v-if="!canvasEnabled"
                    :disabled="busy"
                    @click="saveCanvasSettings"
                  >
                    保存配置
                  </AButton>
                </ASpace>
              </ACard>

              <!-- Canvas Status & Launch Card -->
              <ACard
                class="co-comfy-card co-comfy-canvas-access"
                title="画布访问入口"
              >
                <template #extra>
                  <ABadge
                    :status="canvasBadgeStatus"
                    :text="canvasStatusLabel"
                  />
                </template>

                <!-- Live Status Indicators -->
                <div class="co-comfy-canvas-metrics">
                  <div class="co-comfy-metric-tile">
                    <span>活跃连接</span>
                    <strong class="co-comfy-mono">
                      {{ canvas?.active_connections ?? 0 }}
                    </strong>
                  </div>
                  <div class="co-comfy-metric-tile">
                    <span>执行队列</span>
                    <strong class="co-comfy-mono">
                      运行 {{ queueRunning }} · 排队 {{ queuePending }}
                    </strong>
                  </div>
                </div>

                <!-- Public Host Display (never renders secret auth token) -->
                <div class="co-comfy-host-field">
                  <span class="co-comfy-host-label">公网安全地址</span>
                  <div class="co-comfy-host-box">
                    <code class="co-comfy-mono">{{
                      canvasPublicHost || "开启画布隧道后显示"
                    }}</code>
                    <ATag v-if="canvasReady" color="success">HTTPS</ATag>
                  </div>
                </div>

                <!-- Launch Controls -->
                <div v-if="canvasReady" class="co-comfy-canvas-launch-actions">
                  <AButton
                    type="primary"
                    block
                    @click="launchCanvas('window')"
                  >
                    <template #icon><PhAppWindow :size="15" /></template>
                    在独立窗口打开
                  </AButton>

                  <div class="co-comfy-canvas-secondary-row">
                    <AButton
                      block
                      @click="launchCanvas('browser')"
                    >
                      <template #icon><PhBrowsers :size="15" /></template>
                      在系统浏览器打开
                    </AButton>
                    <AButton
                      block
                      @click="copyCanvasAuthUrl"
                    >
                      <template #icon>
                        <PhCheck v-if="copiedCanvasUrl" :size="15" />
                        <PhLink v-else :size="15" />
                      </template>
                      {{ copiedCanvasUrl ? "已复制链接" : "复制鉴权链接" }}
                    </AButton>
                  </div>
                </div>

                <div v-else class="co-comfy-canvas-waiting">
                  <ASpin v-if="canvasState === 'starting'" size="small" />
                  <p>
                    <template v-if="!gpuReady">
                      {{
                        gpuStarting
                          ? "远端 GPU 正在启动中，就绪后即可启动 ComfyUI 引擎并开启原生画布。"
                          : "请先在「远端设备」页启动远端 GPU，再启动 ComfyUI 引擎并开启原生画布。"
                      }}
                      <AButton
                        type="link"
                        size="small"
                        class="co-inline-link co-comfy-inline-link"
                        @click="emit('device')"
                      >
                        {{ gpuStarting ? "查看 GPU 启动进度" : "前往启动 GPU" }}
                        <PhArrowRight :size="13" />
                      </AButton>
                    </template>
                    <template v-else-if="!comfyRunning">
                      请先点击左侧「先启动 ComfyUI
                      引擎」，引擎就绪后即可开启安全画布隧道。
                    </template>
                    <template v-else-if="canvasState === 'starting'">
                      正在建立远端安全代理与 Cloudflared 隧道，请稍候…
                    </template>
                    <template v-else>
                      点击左侧「开启原生画布」后，即可在独立桌面窗口或系统浏览器中免密打开完整
                      ComfyUI 节点编辑器。
                    </template>
                  </p>
                </div>

                <!-- Dev Mode Save (API Format) Guidance -->
                <AAlert
                  type="info"
                  show-icon
                  class="co-comfy-canvas-dev-tip"
                  message="将原生画布导出为一键表单"
                >
                  <template #icon>
                    <PhFileCode :size="18" />
                  </template>
                  <template #description>
                    <p class="co-comfy-dev-tip-copy">
                      在画布中调试完成后，打开 Settings → Enable Dev Mode
                      Options → Save (API Format) 导出 JSON，即可导入「工作流工坊」生成参数表单。
                    </p>
                    <AButton
                      type="link"
                      size="small"
                      class="co-inline-link co-comfy-tip-link"
                      @click="
                        activeTab = 'studio';
                        openImportModal();
                      "
                    >
                      立即导入 API JSON
                      <PhArrowRight :size="14" />
                    </AButton>
                  </template>
                </AAlert>
              </ACard>
            </div>
          </section>
        </ATabPane>
      </ATabs>
    </div>

    <!-- Import Workflow Modal -->
    <AModal
      v-model:open="importOpen"
      title="导入 ComfyUI 工作流 (API JSON)"
      :width="640"
      :centered="true"
      :destroy-on-close="true"
      :closable="!importing"
      :mask-closable="!importing"
      :keyboard="!importing"
      @cancel="closeImportModal"
    >
      <div class="co-comfy-import-body">
        <p class="co-comfy-import-intro">
          上传或粘贴从 ComfyUI「Save (API Format)」导出的 JSON
          文件，客户端会自动识别可调参数节点。未启动远端 GPU 也可离线导入管理。
        </p>

        <!-- Drag/Drop or Choose .json File Box -->
        <div
          class="co-comfy-import-dropzone"
          :class="{ dragging: importDragging, disabled: importing }"
          @click="!importing && importFileInput?.click()"
          @dragover.prevent="if (!importing) importDragging = true;"
          @dragleave="importDragging = false"
          @drop.prevent="onImportFileDrop"
        >
          <input
            ref="importFileInput"
            type="file"
            accept=".json,application/json"
            class="co-visually-hidden"
            @change="onImportFileChange"
          />
          <PhUploadSimple :size="22" class="co-comfy-import-drop-icon" />
          <div class="co-comfy-import-drop-copy">
            <strong>拖拽 .json 工作流文件到此处，或点击选择</strong>
            <small>仅支持 ComfyUI「Save (API Format)」导出的 API JSON</small>
          </div>
          <AButton
            size="small"
            :disabled="importing"
            @click.stop="importFileInput?.click()"
          >
            选择 .json 文件
          </AButton>
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-name" class="co-comfy-field-label-row">
            <span>工作流名称</span>
          </label>
          <AInput
            id="comfy-import-name"
            v-model:value="importName"
            placeholder="例如：Flux 文生图 / 数字人驱动"
            :disabled="importing"
            allow-clear
          />
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-desc" class="co-comfy-field-label-row">
            <span>简介（可选）</span>
          </label>
          <AInput
            id="comfy-import-desc"
            v-model:value="importDescription"
            placeholder="简要说明用途或所需模型权重…"
            :disabled="importing"
            allow-clear
          />
        </div>

        <div class="co-comfy-field-control">
          <label for="comfy-import-json" class="co-comfy-field-label-row">
            <span>API 工作流 JSON</span>
          </label>
          <ATextarea
            id="comfy-import-json"
            v-model:value="importRaw"
            :rows="7"
            class="co-comfy-json-input"
            spellcheck="false"
            placeholder='{"3": {"class_type": "KSampler", "inputs": {...}}}'
            :disabled="importing"
          />
        </div>

        <AAlert
          v-if="importValidation"
          :type="importValidation.ok ? 'success' : 'error'"
          :role="importValidation.ok ? 'status' : 'alert'"
          show-icon
          :message="
            importValidation.ok
              ? `格式校验通过，检测到 ${importNodeCount} 个节点。`
              : importValidation.error
          "
        />
      </div>

      <template #footer>
        <ASpace :size="10">
          <AButton
            :disabled="importing"
            @click="closeImportModal"
          >
            取消
          </AButton>
          <AButton
            type="primary"
            :loading="importing"
            :disabled="!canSubmitImport"
            @click="submitImport"
          >
            {{ importing ? "正在导入…" : "导入工作流" }}
          </AButton>
        </ASpace>
      </template>
    </AModal>
  </div>
</template>

<style scoped>
.co-comfy-page {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.co-comfy-mono {
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
}

.co-comfy-full-width {
  width: 100%;
}

/* Engine Status Header Card */
.co-comfy-engine-card {
  border-radius: 16px;
  flex-shrink: 0;
}

.co-comfy-engine-head {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-comfy-engine-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  border: 1px solid rgba(59, 130, 246, 0.18);
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-comfy-engine-meta {
  min-width: 0;
  flex: 1 1 280px;
}

.co-comfy-engine-title-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-comfy-engine-title-row h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: -0.01em;
  line-height: 1.35;
  color: var(--co-text);
}

.co-comfy-status-tag :deep(.ant-badge-status-dot) {
  top: 0;
}

.co-comfy-queue-tag {
  font-variant-numeric: tabular-nums;
}

.co-comfy-engine-sub {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--co-muted);
  line-height: 1.55;
}

.co-comfy-engine-actions {
  margin-left: auto;
  flex-shrink: 0;
}

.co-comfy-card-alert {
  margin-top: 12px;
  border-radius: 8px;
}

.co-comfy-task-banner {
  margin-top: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.co-comfy-task-info {
  flex: 1 1 220px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-task-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 13px;
}

.co-comfy-task-head strong {
  color: var(--co-text);
  font-weight: 550;
  overflow-wrap: anywhere;
}

.co-comfy-notice {
  border-radius: 8px;
  flex-shrink: 0;
}

/* Dual-layer Workspace Tabs */
.co-comfy-tabs-wrap {
  min-width: 0;
}

.co-comfy-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 16px;
}

.co-comfy-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  line-height: 1.2;
  font-weight: 550;
}

.co-comfy-tab-label svg {
  flex-shrink: 0;
}

.co-comfy-tab-badge {
  display: inline-flex;
  align-items: center;
  line-height: 1;
}

.co-comfy-tab-badge :deep(.ant-badge-count) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
  box-shadow: none;
  font-weight: 600;
  min-width: 20px;
  height: 18px;
  line-height: 18px;
  padding: 0 6px;
}

.co-comfy-tabs :deep(.ant-tabs-tab-active .co-comfy-tab-badge .ant-badge-count) {
  background: var(--co-selected);
  color: var(--co-accent-strong);
}

:global(:root[data-theme="dark"])
  .co-comfy-tabs
  :deep(.ant-tabs-tab-active .co-comfy-tab-badge .ant-badge-count) {
  background: rgba(59, 130, 246, 0.24);
  color: var(--co-accent-strong);
}

/* Tab 1: Workflow Studio */
.co-comfy-studio-section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-studio-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.co-comfy-studio-toolbar-copy {
  min-width: 0;
}

.co-comfy-toolbar-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-studio-toolbar h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-studio-toolbar p {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--co-muted);
}

.co-comfy-count-tag {
  font-variant-numeric: tabular-nums;
}

.co-comfy-studio-grid {
  display: grid;
  grid-template-columns: minmax(340px, 440px) minmax(0, 1fr);
  gap: 18px;
  align-items: stretch;
}

.co-comfy-left-col {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}

.co-comfy-card {
  border-radius: 16px;
  min-width: 0;
}

.co-comfy-empty-card {
  padding: 16px 8px;
}

.co-comfy-empty-emblem {
  width: 54px;
  height: 54px;
  margin: 0 auto;
  border-radius: 14px;
  border: 1px solid rgba(59, 130, 246, 0.18);
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
}

.co-comfy-empty-copy {
  max-width: 360px;
  margin: 0 auto 14px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-muted);
}

.co-comfy-empty-hint {
  display: block;
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-empty-actions {
  justify-content: center;
}

/* Workflow Library List */
.co-comfy-workflow-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 280px;
  overflow-y: auto;
  scrollbar-width: thin;
}

.co-comfy-workflow-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  cursor: pointer;
  transition:
    background 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.co-comfy-workflow-item:hover {
  background: var(--co-soft-hover);
  border-color: var(--co-border);
}

.co-comfy-workflow-item.selected {
  background: var(--co-selected);
  border: 1.5px solid var(--co-accent);
  box-shadow: none;
}

.co-comfy-workflow-item-main {
  min-width: 0;
  flex: 1;
}

.co-comfy-workflow-item-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-comfy-workflow-item-title strong {
  font-size: 14px;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-comfy-workflow-desc {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-secondary-text);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.co-comfy-workflow-badges {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.co-comfy-meta-tag {
  font-size: 12px;
  line-height: 18px;
  padding-inline: 7px;
}

.co-comfy-wf-actions {
  flex-shrink: 0;
}

/* Dynamic Parameter Runner */
.co-comfy-param-title-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 2px 0;
}

.co-comfy-param-subtitle {
  font-size: 12px;
  font-weight: 400;
  color: var(--co-muted);
  white-space: normal;
  line-height: 1.45;
}

.co-comfy-fields-stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-comfy-field-control {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-field-label-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
}

.co-comfy-field-node {
  font-size: 12px;
  font-weight: 400;
  color: var(--co-muted);
  font-family: var(--co-font-mono);
}

.co-comfy-switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-switch-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-comfy-switch-copy strong {
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
}

/* Image / Audio Drag-and-Drop Field */
.co-comfy-media-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-media-dropzone {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  border: 1.5px dashed var(--co-border);
  background: var(--co-subtle);
  cursor: pointer;
  margin: 0;
  transition:
    border-color 0.16s ease,
    background 0.16s ease;
}

.co-comfy-media-dropzone:hover,
.co-comfy-media-dropzone.dragging {
  border-color: var(--co-accent);
  background: var(--co-hover);
}

.co-comfy-media-dropzone.busy {
  opacity: 0.72;
  cursor: wait;
}

.co-comfy-media-icon-wrap {
  width: 38px;
  height: 38px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-comfy-media-thumb {
  width: 42px;
  height: 42px;
  border-radius: 6px;
  object-fit: cover;
  border: 1px solid var(--co-border-secondary);
  flex-shrink: 0;
}

.co-comfy-media-dropzone-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-comfy-media-dropzone-copy strong {
  font-size: 13px;
  font-weight: 550;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-comfy-media-dropzone-copy small {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-media-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-comfy-filename-pill {
  max-width: 210px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-form-divider {
  margin: 16px 0 14px;
}

.co-comfy-form-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-comfy-run-btn {
  flex: 1;
}

/* Output Gallery */
.co-comfy-gallery-col {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.co-comfy-gallery-card {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.co-comfy-gallery-card :deep(.ant-card-body) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.co-comfy-gallery-empty-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 36px 16px;
}

.co-comfy-output-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr));
  gap: 14px;
  align-items: start;
}

.co-comfy-output-item {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.co-comfy-output-item:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
}

.co-comfy-output-frame {
  min-height: 164px;
  background: var(--co-subtle);
  border-bottom: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-comfy-output-frame img,
.co-comfy-output-frame video {
  width: 100%;
  max-height: 240px;
  object-fit: contain;
  display: block;
}

.co-comfy-audio-box {
  width: 100%;
  padding: 20px 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.co-comfy-audio-icon {
  color: var(--co-accent);
}

.co-comfy-audio-box audio {
  width: 100%;
}

.co-comfy-preview-loading {
  padding: 24px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--co-muted);
}

.co-comfy-preview-trigger {
  width: 100%;
  min-height: 164px;
  border: 0;
  background: transparent;
  color: var(--co-secondary-text);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 18px;
  font-size: 13px;
  cursor: pointer;
  transition:
    background 0.15s ease,
    color 0.15s ease;
}

.co-comfy-preview-trigger:hover:not(:disabled) {
  background: var(--co-hover);
  color: var(--co-accent);
}

.co-comfy-preview-trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.co-comfy-output-body {
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-output-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-comfy-output-title-row strong {
  font-size: 14px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-output-filename {
  font-size: 12px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-output-meta-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
}

.co-comfy-output-error {
  margin-top: 4px;
}

.co-comfy-output-actions {
  margin-top: 6px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

/* Tab 2: Native Canvas */
.co-comfy-canvas-section {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-comfy-canvas-security-alert {
  border-radius: 10px;
}

.co-comfy-canvas-banner-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-comfy-canvas-banner-head h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-canvas-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
  align-items: stretch;
}

.co-comfy-canvas-help {
  margin: 0;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-muted);
}

.co-comfy-named-fields {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-comfy-canvas-guide {
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-comfy-canvas-guide summary {
  cursor: pointer;
  font-weight: 500;
  color: var(--co-text);
}

.co-comfy-canvas-guide ol {
  margin: 8px 0 0;
  padding-left: 18px;
  line-height: 1.7;
}

.co-comfy-canvas-guide code {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-family: var(--co-font-mono);
  font-size: 12px;
}

.co-comfy-canvas-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}

.co-comfy-metric-tile {
  padding: 12px 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-comfy-metric-tile span {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-metric-tile strong {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-comfy-host-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-comfy-host-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-comfy-host-box {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 9px 14px;
  border-radius: 12px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-comfy-host-box code {
  font-size: 13px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-comfy-canvas-launch-actions {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-comfy-canvas-secondary-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.co-comfy-canvas-waiting {
  margin-top: 14px;
  padding: 14px;
  border-radius: 8px;
  background: var(--co-subtle);
  border: 1px dashed var(--co-border);
  display: flex;
  align-items: center;
  gap: 12px;
}

.co-comfy-canvas-waiting p {
  margin: 0;
  font-size: 13px;
  line-height: 1.65;
  color: var(--co-secondary-text);
}

.co-comfy-canvas-dev-tip {
  margin-top: 16px;
  border-radius: 8px;
}

.co-comfy-dev-tip-copy {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

/* Import Modal */
.co-comfy-import-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-comfy-import-intro {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-muted);
}

.co-comfy-import-dropzone {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1.5px dashed var(--co-border);
  background: var(--co-subtle);
  cursor: pointer;
  transition:
    border-color 0.16s ease,
    background 0.16s ease;
}

.co-comfy-import-dropzone:hover,
.co-comfy-import-dropzone.dragging {
  border-color: var(--co-accent);
  background: var(--co-hover);
}

.co-comfy-import-dropzone.disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-comfy-import-drop-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-comfy-import-drop-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-comfy-import-drop-copy strong {
  font-size: 14px;
  font-weight: 550;
  color: var(--co-text);
}

.co-comfy-import-drop-copy small {
  font-size: 12px;
  color: var(--co-muted);
}

.co-comfy-json-input {
  font-family: var(--co-font-mono);
  font-size: 13px;
  line-height: 1.55;
}

@media (max-width: 1050px) {
  .co-comfy-studio-grid {
    grid-template-columns: minmax(290px, 360px) minmax(0, 1fr);
    gap: 14px;
  }
}

@media (max-width: 860px) {
  .co-comfy-studio-grid,
  .co-comfy-canvas-grid,
  .co-comfy-canvas-secondary-row,
  .co-comfy-canvas-metrics {
    grid-template-columns: 1fr;
  }

  .co-comfy-engine-actions {
    width: 100%;
    margin-left: 0;
  }
}
</style>
