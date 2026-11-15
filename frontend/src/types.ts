import type { Creation } from "./creative";
export interface ModelSpec {
  id: string;
  kind?: "chat" | "image" | "video";
  recipe?: string;
  context_length?: number;
  temperature?: number;
  name: string;
  description: string;
  downloaded: boolean;
  loaded: boolean;
  size: number;
  min_vram_mib: number;
  files: { repo: string; path: string }[];
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
export interface Message {
  role: "user" | "assistant";
  content: string;
  status: string;
  model?: string;
}
export interface Conversation {
  id: string;
  title: string;
  updated_at: number;
  messages: Message[];
}
export interface RuntimeStatus {
  id?: string;
  state: string;
  loaded_model?: string;
  gpu?: { name: string; total_mib: number; used_mib: number };
  disk_free?: number;
  idle_remaining?: number;
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
  sync?: { pending: number; error: string; connected?: boolean };
}
export interface ActivityEntry {
  id: number;
  time: number;
  source: string;
  level: "info" | "warning" | "error";
  message: string;
}
export interface Snapshot {
  logs: ActivityEntry[];
  runtime: RuntimeStatus;
  models: ModelSpec[];
  conversations: Conversation[];
  creations: Creation[];
  account: {
    logged_in: boolean;
    balance?: number;
    rate?: number;
    email?: string;
    assignments?: number;
    instances?: { id: string; managed: boolean }[];
    checked_at?: number;
  };
  operation: null | { title: string; running: boolean };
  error: string;
  auth_url: string;
  setup: {
    colab: boolean;
    drive: boolean;
    hf: boolean;
    drive_waiting?: boolean;
    persistent_login?: boolean;
  };
  settings: {
    gpu: string;
    max_cu: number;
    idle_minutes: number;
    idle_countdown_seconds: number;
    download_threads: number;
    auto_drive: boolean;
  };
  stream?: {
    conversation_id: string;
    text: string;
    content: string;
    state: string;
  };
}
export const initialSnapshot = (): Snapshot => ({
  logs: [],
  runtime: { state: "stopped" },
  models: [],
  conversations: [],
  creations: [],
  account: { logged_in: false },
  operation: null,
  error: "",
  auth_url: "",
  setup: { colab: false, drive: false, hf: false },
  settings: {
    gpu: "T4",
    max_cu: 5,
    idle_minutes: 3,
    idle_countdown_seconds: 60,
    download_threads: 8,
    auto_drive: true,
  },
});
export const labels: Record<string, string> = {
  stopped: "未启动",
  starting: "正在启动",
  ready: "已连接",
  disconnected: "连接中断",
  releasing: "正在释放",
  failed: "操作失败",
};
export function canLoad(model: ModelSpec, state: Snapshot) {
  return (
    state.runtime.state === "ready" &&
    model.downloaded &&
    !model.loaded &&
    !state.operation &&
    !state.runtime.tasks?.some((t) => t.state === "running")
  );
}
