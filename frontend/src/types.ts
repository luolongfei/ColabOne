import { t } from "./i18n";

export interface ModelSpec {
  id: string;
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
}
export interface WorkflowTemplate {
  id: string;
  name: string;
  description: string;
  source: "builtin" | "custom";
  min_vram_mib: number;
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
  pip_packages?: string[];
  models?: {
    repo: string;
    revision: string;
    path: string;
    target_dir: string;
    size: number;
    sha256: string;
  }[];
  workflow?: Record<string, unknown>;
}
export interface WorkflowOutput {
  id: string;
  workflow_id: string;
  workflow_name: string;
  kind: "image" | "video" | "audio";
  filename: string;
  extension: string;
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
export interface ActivityEntry {
  id: number;
  time: number;
  source: string;
  level: "info" | "warning" | "error";
  message: string;
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
  logs: ActivityEntry[];
  runtime: RuntimeStatus;
  models: ModelSpec[];
  common_repos?: CommonRepo[];
  workflows: WorkflowTemplate[];
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
  operation: null | { title: string; running: boolean };
  error: string;
  auth_url: string;
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
    locale?: string;
  };
}
export const createColabOneSnapshot = (): ColabOneSnapshot => ({
  active_endpoint: "",
  logs: [],
  runtime: { state: "stopped" },
  models: [],
  common_repos: [],
  workflows: [],
  comfy_outputs: [],
  account: { logged_in: false },
  operation: null,
  error: "",
  auth_url: "",
  setup: { colab: false, drive: false, drive_authorized: false, hf: false },
  settings: {
    gpu: "T4",
    idle_minutes: 30,
    idle_countdown_seconds: 60,
    auto_drive: true,
    download_threads: 16,
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
