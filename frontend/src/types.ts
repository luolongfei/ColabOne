export interface ModelSpec {
  id: string;
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
  deadline?: number;
  tasks?: TaskStatus[];
  sync?: { pending: number; error: string; connected?: boolean };
}
export interface Snapshot {
  runtime: RuntimeStatus;
  models: ModelSpec[];
  conversations: Conversation[];
  account: {
    logged_in: boolean;
    balance?: number;
    rate?: number;
    email?: string;
  };
  operation: null | { title: string; running: boolean };
  error: string;
  auth_url: string;
  setup: {
    colab: boolean;
    drive: boolean;
    hf: boolean;
    drive_waiting?: boolean;
  };
  settings: {
    gpu: string;
    max_cu: number;
    idle_minutes: number;
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
  runtime: { state: "stopped" },
  models: [],
  conversations: [],
  account: { logged_in: false },
  operation: null,
  error: "",
  auth_url: "",
  setup: { colab: false, drive: false, hf: false },
  settings: { gpu: "T4", max_cu: 5, idle_minutes: 30, auto_drive: true },
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
