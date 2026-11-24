import { t } from "./i18n";

export interface ModelSpec {
  id: string;
  api_id?: string;
  pending_api_id?: string;
  kind?: "chat";
  context_length?: number;
  temperature?: number;
  name: string;
  description: string;
  repo?: string;
  common?: boolean;
  custom?: boolean;
  downloaded: boolean;
  loaded: boolean;
  size: number;
  cached_bytes?: number;
  min_vram_mib: number;
  files: { repo: string; path: string }[];
}
export interface CommonRepo {
  repo: string;
  name: string;
  note?: string;
  filename?: string;
  downloads?: number;
  likes?: number;
  added_at?: number;
}
export interface TaskStatus {
  id: string;
  kind: string;
  model_id: string;
  state: string;
  progress: number;
  message: string;
  error: string;
  created_at?: number;
  finished_at?: number | null;
  elapsed_seconds?: number | null;
  logs?: {
    seq: number;
    time: number;
    message: string;
    level: string;
  }[];
  download?: {
    downloaded_bytes: number;
    total_bytes: number;
    speed_bps: number;
    eta_seconds: number | null;
    elapsed_seconds: number;
    resumed_bytes: number;
    threads: number;
    connections: number;
    phase: string;
    filename: string;
    file_index: number;
    file_count: number;
  };
}
export interface ColabOneComfyNodePatch {
  node_name: string;
  kind: "wav2vec2_hidden_states_hook";
}
export type ComfyNodePatch = ColabOneComfyNodePatch;
export interface ColabOneComfyTtsVoicePreset {
  id: string;
  name: string;
  gender: "female" | "male" | "neutral";
  style?: string;
  description?: string;
  prompt?: string;
  seed?: number;
}
export type ComfyTtsVoicePreset = ColabOneComfyTtsVoicePreset;
export interface ColabOneComfyTtsEngineSpec {
  id: string;
  name: string;
  description: string;
  repo: string;
  revision: string;
  aux_repo?: string;
  aux_revision?: string;
  license: string;
  commercial_ok: boolean;
  license_notice: string;
  min_vram_mib: number;
  sample_rate: number;
  supports_voice_clone: boolean;
  supports_voice_design: boolean;
  supports_emotion_ref: boolean;
  supports_inline_tags: boolean;
  pip_packages?: string[];
  source_repo?: string;
  source_commit?: string;
  voice_presets?: ColabOneComfyTtsVoicePreset[];
}
export type ComfyTtsEngineSpec = ColabOneComfyTtsEngineSpec;
export interface ColabOneComfyPipelineSpec {
  kind: "comfy_standard" | "digital_human";
  video_backend?: "infinitetalk" | "longcat_avatar" | "custom";
  default_tts_engine?: string;
  supported_tts_engines?: string[];
  max_segment_seconds?: number;
  tail_frame_offset_seconds?: number;
  output_fps?: number;
  target_width?: number;
  target_height?: number;
  upscale_mode?: "none" | "ai_2x" | "lanczos";
  audio_node_key?: string;
  image_node_key?: string;
  prompt_node_key?: string;
  frames_node_key?: string;
  width_node_key?: string;
  height_node_key?: string;
  fps_node_key?: string;
  steps_node_key?: string;
  seed_node_key?: string;
}
export type ComfyPipelineSpec = ColabOneComfyPipelineSpec;
export interface WorkflowField {
  key: string;
  label: string;
  node_id: string;
  input_name: string;
  field_type:
    | "text"
    | "textarea"
    | "number"
    | "integer"
    | "boolean"
    | "select"
    | "image"
    | "audio";
  default?: string | number | boolean | null;
  options?: string[];
  min_value?: number | null;
  max_value?: number | null;
  step?: number | null;
  description?: string;
  group?: string;
  required?: boolean;
  visible_when?: Record<string, string | boolean> | null;
  option_labels?: Record<string, string>;
}
export interface WorkflowTemplate {
  id: string;
  name: string;
  description: string;
  source: "builtin" | "custom";
  min_vram_mib: number;
  min_disk_mib?: number;
  recommended_gpu?: string;
  license?: string;
  commercial_ok?: boolean;
  license_notice?: string;
  created_at?: number;
  updated_at?: number;
  node_count: number;
  fields: WorkflowField[];
  custom_nodes?: {
    name: string;
    repo: string;
    commit: string;
    install_requirements?: boolean;
  }[];
  node_patches?: ComfyNodePatch[];
  pip_packages?: string[];
  models?: {
    repo: string;
    revision: string;
    path: string;
    target_dir: string;
    size: number;
    sha256: string;
  }[];
  pipeline?: ComfyPipelineSpec | null;
  workflow?: Record<string, unknown>;
}
export type ColabOneWorkflowOutputExtension =
  | "png"
  | "jpg"
  | "jpeg"
  | "webp"
  | "gif"
  | "mp4"
  | "webm"
  | "wav"
  | "mp3"
  | "flac";
export interface WorkflowOutput {
  id: string;
  workflow_id: string;
  workflow_name: string;
  kind: "image" | "video" | "audio";
  filename: string;
  extension: ColabOneWorkflowOutputExtension;
  mime: string;
  size: number;
  sha256?: string;
  created_at: number;
  elapsed: number;
  prompt_id?: string;
}
export interface ComfyCanvasStatus {
  state: string;
  enabled: boolean;
  mode: "named";
  url: string;
  error: string;
  checked_at: number;
  active_connections?: number;
}
export interface ComfyStatus {
  installed: boolean;
  running: boolean;
  state: string;
  port?: number;
  tag?: string;
  active_pack?: string;
  queue_running: number;
  queue_pending: number;
  error: string;
  canvas?: ComfyCanvasStatus;
}
export interface PublicAPIRequestMetric {
  seq: number;
  id: string;
  kind: "chat" | "responses" | "compact";
  endpoint: string;
  model: string;
  stream: boolean;
  state: "running" | "completed" | "failed" | "cancelled";
  started_at: number;
  finished_at?: number | null;
  duration_ms: number;
  ttft_ms?: number | null;
  prompt_tokens?: number | null;
  original_prompt_tokens?: number | null;
  compacted?: boolean;
  compaction_stage?: "prune" | "summary" | "truncate" | "inline" | "";
  completion_tokens?: number | null;
  tokens_per_second?: number | null;
  prompt_tokens_per_second?: number | null;
  estimated?: boolean;
  error?: string;
}
export interface PublicAPIStatus {
  state: string;
  enabled: boolean;
  mode: "named";
  url: string;
  error: string;
  streaming: boolean;
  checked_at: number;
  requests: number;
  active: number;
  completed: number;
  failed: number;
  last_request: number;
  last_tokens_per_second?: number | null;
  avg_tokens_per_second?: number | null;
  recent_requests?: PublicAPIRequestMetric[];
}
export interface DriveStatus {
  connected: boolean;
  configured?: boolean;
  mode?: string;
  mount_path?: string;
  remote_folder?: string;
  pending?: number;
  error?: string;
  total_bytes?: number | null;
  free_bytes?: number | null;
}
export interface InstanceLocation {
  country: string;
  country_name?: string;
  region?: string;
  zone?: string;
}
export interface ResourceHistoryPoint {
  t: number;
  vram_pct: number;
  gpu_util?: number;
  ram_pct: number;
  cpu_pct?: number;
  disk_pct: number;
  disk_read_bps?: number;
  disk_write_bps?: number;
}
export interface SystemResources {
  cpu?: { cores: number; percent: number };
  memory?: {
    total_bytes: number;
    used_bytes: number;
    available_bytes: number;
    percent: number;
  };
  disk?: {
    total_bytes: number;
    used_bytes: number;
    free_bytes: number;
    percent: number;
    read_bps?: number;
    write_bps?: number;
  };
  gpu?: {
    name: string;
    total_mib: number;
    used_mib: number;
    util_percent?: number | null;
    temp_c?: number | null;
    power_w?: number | null;
    power_limit_w?: number | null;
  };
  history?: ResourceHistoryPoint[];
}
export interface RuntimeStatus {
  public_api?: PublicAPIStatus;
  comfyui?: ComfyStatus;
  active_engine?: "llama" | "comfyui" | null;
  drive?: DriveStatus;
  location?: InstanceLocation | null;
  id?: string;
  state: string;
  loaded_model?: string;
  gpu?: {
    name: string;
    total_mib: number;
    used_mib: number;
    util_percent?: number | null;
    temp_c?: number | null;
    power_w?: number | null;
    power_limit_w?: number | null;
  };
  disk_free?: number;
  idle_remaining?: number;
  idle_minutes?: number;
  resources?: SystemResources;
  protocol?: number;
  service_revision?: string;
  api_model_aliases?: boolean;
  upgrade_available?: boolean;
  active_options?: { context_length?: number; temperature?: number };
  capabilities?: string[];
  idle?: {
    phase: string;
    remaining_seconds: number;
    countdown_seconds: number;
    idle_seconds: number;
    server_time: number;
  };
  deadline?: number;
  tasks?: TaskStatus[];
}
export interface AccountInstance {
  id: string;
  managed: boolean;
  active?: boolean;
  accelerator?: string;
  variant?: string;
  shape?: string;
  location?: InstanceLocation | null;
  state?: string;
  gpu_name?: string;
  vram_used_mib?: number;
  vram_total_mib?: number;
  disk_free?: number;
  loaded_model?: string;
  active_task?: string;
  drive_connected?: boolean;
  resources?: SystemResources;
}
export type MonitorSource =
  | "none"
  | "local"
  | "remote_colabone"
  | "remote_readonly";
export interface MonitorOption {
  id: string;
  managed: boolean;
  active: boolean;
  accelerator: string;
  variant: string;
  shape: string;
  location?: InstanceLocation | null;
  gpu_name?: string;
  loaded_model?: string;
  source: Exclude<MonitorSource, "none">;
  state: string;
}
export interface MonitorSnapshot {
  mode: "auto" | "manual";
  endpoint: string;
  managed: boolean;
  source: MonitorSource;
  paused: boolean;
  error: string;
  runtime: RuntimeStatus;
  loaded_model_name: string;
  drive_connected: boolean;
  drive_free_bytes?: number | null;
  options: MonitorOption[];
}
export interface ColabOneCuUsageBucket {
  ts: number;
  duration: number;
  consumed: number;
  topup: number;
  balance_start?: number;
  balance_end?: number;
  has_balance: boolean;
  rate_avg: number;
  rate_max: number;
  active_seconds: number;
  max_instances: number;
  samples: number;
  estimated: boolean;
}

export interface ColabOneCuUsageResponse {
  logged_in: boolean;
  bucket_seconds: number;
  retention_days: number;
  start: number;
  end: number;
  current_balance?: number;
  current_rate?: number;
  checked_at?: number;
  initial_balance?: number;
  earliest_ts?: number;
  latest_ts?: number;
  available_dates: string[];
  active_dates: string[];
  buckets: ColabOneCuUsageBucket[];
}

export interface ActivityEntry {
  id: number;
  time: number;
  source: string;
  level: "info" | "warning" | "error";
  message: string;
}
export interface ColabOneOperation {
  title: string;
  running: boolean;
  kind?: string;
  cancellable?: boolean;
  cancelling?: boolean;
  retryable?: boolean;
  waiting_retry?: boolean;
  retry_at?: number;
  attempt?: number;
  next_attempt?: number;
  total_attempts?: number;
  retry_kind?: "capacity" | "server_busy" | "network" | string;
  has_retried?: boolean;
  allocated?: boolean;
  gpu?: string;
}
export interface ColabOneSnapshot {
  active_endpoint?: string;
  monitor?: MonitorSnapshot;
  api_settings?: {
    mode: "named";
    domain: string;
    has_token: boolean;
    has_key: boolean;
  };
  comfy_canvas_settings?: {
    mode: "named";
    domain: string;
    has_token: boolean;
  };
  assign_proxy_settings?: {
    enabled: boolean;
    has_proxy: boolean;
    masked_url: string;
    has_auth: boolean;
  };
  logs: ActivityEntry[];
  runtime: RuntimeStatus;
  models: ModelSpec[];
  common_repos?: CommonRepo[];
  workflows: WorkflowTemplate[];
  tts_engines?: ComfyTtsEngineSpec[];
  comfy_outputs: WorkflowOutput[];
  account: {
    logged_in: boolean;
    balance?: number;
    rate?: number;
    email?: string;
    assignments?: number;
    instances?: AccountInstance[];
    checked_at?: number;
  };
  operation: null | ColabOneOperation;
  error: string;
  auth_url: string;
  drive_auth_hint?: string;
  setup: {
    colab: boolean;
    drive: boolean;
    drive_authorized?: boolean;
    drive_headless?: boolean;
    drive_quota_reason?: string;
    drive_mode?: string;
    hf: boolean;
    persistent_login?: boolean;
    start_menu_shortcut?: boolean;
    desktop_shortcut?: boolean;
  };
  settings: {
    gpu: string;
    idle_minutes: number;
    idle_countdown_seconds: number;
    auto_drive: boolean;
    download_threads: number;
    assign_max_attempts?: number;
    assign_proxy_enabled?: boolean;
    locale?: string;
  };
}
export const COLABONE_DEFAULT_GPU = "G4" as const;
export const COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS = 20 as const;
export const COLABONE_MIN_ASSIGN_MAX_ATTEMPTS = 1 as const;
export const COLABONE_MAX_ASSIGN_MAX_ATTEMPTS = 100 as const;

export const createColabOneSnapshot = (): ColabOneSnapshot => ({
  active_endpoint: "",
  logs: [],
  runtime: { state: "stopped" },
  models: [],
  common_repos: [],
  workflows: [],
  tts_engines: [],
  comfy_outputs: [],
  account: { logged_in: false },
  operation: null,
  error: "",
  auth_url: "",
  drive_auth_hint: "",
  setup: { colab: false, drive: false, drive_authorized: false, hf: false },
  assign_proxy_settings: {
    enabled: false,
    has_proxy: false,
    masked_url: "",
    has_auth: false,
  },
  settings: {
    gpu: COLABONE_DEFAULT_GPU,
    idle_minutes: 30,
    idle_countdown_seconds: 60,
    auto_drive: true,
    download_threads: 16,
    assign_max_attempts: COLABONE_DEFAULT_ASSIGN_MAX_ATTEMPTS,
    assign_proxy_enabled: false,
  },
});
export const COLABONE_STATUS_LABELS: Record<string, string> = {
  get stopped() {
    return t("status.stopped");
  },
  get starting() {
    return t("status.starting");
  },
  get connecting() {
    return t("monitor.badgeConnecting");
  },
  get ready() {
    return t("status.ready");
  },
  get running() {
    return t("comfy.tagRunning");
  },
  get disconnected() {
    return t("status.disconnected");
  },
  get releasing() {
    return t("status.releasing");
  },
  get failed() {
    return t("status.failed");
  },
};
export const COLABONE_AUTO_API_MODEL_ID = "colabone/auto";

const COLABONE_QUANT_TAG_PATTERNS = [
  /(?<![A-Za-z0-9])(UD-(?:IQ|TQ|Q)[1-8](?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4})?)(?![A-Za-z0-9])/i,
  /(?<![A-Za-z0-9])((?:IQ|TQ|Q)[1-8](?:_[01K](?:_[A-Za-z0-9]+)?|_[A-Za-z]{1,4}))(?![A-Za-z0-9])/i,
  /(?<![A-Za-z0-9])(MXFP4(?:_MOE)?|NVFP4|FP8(?:_E4M3(?:FN)?|_E5M2)?|BF16|FP16|F16|F32)(?![A-Za-z0-9])/i,
  /(?<![A-Za-z0-9])(Q[2-8])(?![A-Za-z0-9])/i,
];

export function colabOneModelApiId(
  model?: Pick<ModelSpec, "id" | "api_id"> | null,
): string {
  return model?.api_id?.trim() || model?.id?.trim() || "";
}

export function colabOneModelQuantTag(
  model?: Pick<ModelSpec, "description" | "files"> | null,
): string {
  const candidates = [
    model?.description || "",
    model?.files?.[0]?.path || "",
  ];
  for (const raw of candidates) {
    if (!raw) continue;
    const text = raw.replace(/(?:[-_]\d{5}-of-\d{5})?\.gguf$/i, "");
    for (const pattern of COLABONE_QUANT_TAG_PATTERNS) {
      const m = text.match(pattern);
      if (m?.[1]) return m[1].toUpperCase();
    }
  }
  return "";
}

export function canColabOneLoadModel(
  model: ModelSpec,
  state: ColabOneSnapshot,
) {
  return (
    state.runtime.state === "ready" &&
    model.downloaded &&
    !model.loaded &&
    !state.operation &&
    !state.runtime.tasks?.some((t) => t.state === "running")
  );
}

export type ColabOneRuntimeUpgradeKind = "none" | "protocol" | "optional";

export interface ColabOneRuntimeUpgradeState {
  visible: boolean;
  kind: ColabOneRuntimeUpgradeKind;
  dismissible: boolean;
  dismissKey: string;
  message: string;
  canUpgrade: boolean;
  needsConfirm: boolean;
  activeItems: string[];
  disabledReason: string;
}

export function evaluateColabOneRuntimeUpgrade(
  state: ColabOneSnapshot,
): ColabOneRuntimeUpgradeState {
  const runtime = state.runtime;
  if (runtime.state !== "ready") {
    return {
      visible: false,
      kind: "none",
      dismissible: false,
      dismissKey: "",
      message: "",
      canUpgrade: false,
      needsConfirm: false,
      activeItems: [],
      disabledReason: "",
    };
  }

  const protocol = runtime.protocol || 0;
  const kind: ColabOneRuntimeUpgradeKind =
    protocol < 7
      ? "protocol"
      : runtime.upgrade_available
        ? "optional"
        : "none";

  if (kind === "none") {
    return {
      visible: false,
      kind: "none",
      dismissible: false,
      dismissKey: "",
      message: "",
      canUpgrade: false,
      needsConfirm: false,
      activeItems: [],
      disabledReason: "",
    };
  }

  const hasRunningTask = Boolean(
    runtime.tasks?.some((item) => item.state === "running"),
  );
  const hasLoadedModel = Boolean(
    runtime.loaded_model || runtime.active_engine === "llama",
  );
  const hasPublicApi = Boolean(
    runtime.public_api?.enabled ||
      ["starting", "ready", "reconnecting"].includes(
        runtime.public_api?.state || "",
      ),
  );
  const hasActiveComfy = Boolean(
    runtime.active_engine === "comfyui" ||
      runtime.comfyui?.running ||
      ["starting", "running", "ready"].includes(runtime.comfyui?.state || "") ||
      runtime.comfyui?.canvas?.enabled ||
      ["starting", "ready", "reconnecting"].includes(
        runtime.comfyui?.canvas?.state || "",
      ),
  );
  const hasOtherEngine = Boolean(
    runtime.active_engine &&
      runtime.active_engine !== "llama" &&
      runtime.active_engine !== "comfyui",
  );

  const loadedModelName =
    state.models.find(
      (m) => m.loaded || (runtime.loaded_model && m.id === runtime.loaded_model),
    )?.name ||
    runtime.loaded_model ||
    "";

  const activeItems: string[] = [];
  if (hasRunningTask) {
    activeItems.push(t("status.upgradeActiveTask"));
  }
  if (hasLoadedModel) {
    activeItems.push(
      loadedModelName
        ? t("status.upgradeActiveModel", { name: loadedModelName })
        : t("status.upgradeActiveModelGeneric"),
    );
  }
  if (hasPublicApi) {
    activeItems.push(t("status.upgradeActiveApi"));
  }
  if (hasActiveComfy) {
    activeItems.push(t("status.upgradeActiveComfy"));
  } else if (hasOtherEngine) {
    activeItems.push(t("status.upgradeActiveEngine"));
  }

  const needsConfirm = activeItems.length > 0;
  const disabledReason = state.operation
    ? t("status.upgradeBlockedOperation")
    : "";

  let message: string;
  if (kind === "protocol") {
    message = t("status.upgradeBanner");
  } else if (state.operation) {
    message = t("status.upgradeOptionalBlockedTask");
  } else if (needsConfirm) {
    message = t("status.upgradeOptionalReadyWithConfirm");
  } else {
    message = t("status.upgradeOptionalReady");
  }

  const dismissKey = `${kind}::${runtime.id || "default"}::${runtime.service_revision || protocol}`;

  return {
    visible: true,
    kind,
    dismissible: kind === "optional",
    dismissKey,
    message,
    canUpgrade: !disabledReason,
    needsConfirm,
    activeItems,
    disabledReason,
  };
}
