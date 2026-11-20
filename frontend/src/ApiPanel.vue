<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import {
  Alert as AAlert,
  Badge as ABadge,
  Button as AButton,
  Card as ACard,
  Divider as ADivider,
  Input as AInput,
  InputPassword as AInputPassword,
  Popconfirm as APopconfirm,
  Segmented as ASegmented,
  Space as ASpace,
  Spin as ASpin,
  Statistic as AStatistic,
  Tag as ATag,
  Tooltip as ATooltip,
} from "ant-design-vue";
import {
  PhArrowRight,
  PhArrowsClockwise,
  PhArrowSquareOut,
  PhCheck,
  PhCopy,
  PhCube,
  PhEye,
  PhEyeSlash,
  PhFloppyDisk,
  PhHardDrives,
  PhKey,
  PhPlay,
  PhSpeedometer,
  PhStop,
} from "@phosphor-icons/vue";
import ModelDrawer from "./ModelDrawer.vue";
import { colabOneCommand, colabOneOpenURL, colabOneWriteClipboard } from "./api";
import {
  formatDurationMs,
  formatRequestClock,
  formatTokenCount,
  formatTokenSpeed,
  requestKindLabel,
  requestStateLabel,
  resolveSpeedSummary,
} from "./apiSpeed";
import { formatIdlePolicyLabel } from "./billing";
import type { PublicAPIRequestMetric, ColabOneSnapshot } from "./types";

const props = defineProps<{
  state: ColabOneSnapshot;
  modelsOpen?: boolean;
  busy?: boolean;
}>();
const emit = defineEmits<{
  "update:modelsOpen": [boolean];
  device: [];
  sync: [];
  notify: [string];
  error: [string];
}>();

const localDrawerOpen = ref(false);
const drawerOpen = computed({
  get: () =>
    props.modelsOpen !== undefined ? props.modelsOpen : localDrawerOpen.value,
  set: (val: boolean) => {
    localDrawerOpen.value = val;
    emit("update:modelsOpen", val);
  },
});

function openModelDrawer() {
  drawerOpen.value = true;
}

const domain = ref(props.state.api_settings?.domain || "");
const settingsDirty = ref(false);
const tunnelToken = ref("");
const key = ref("");
const keyTimer = ref<ReturnType<typeof setTimeout>>();
const copyTimer = ref<ReturnType<typeof setTimeout>>();
const copiedField = ref<"" | "url" | "key" | "model" | "code">("");
const busy = ref(false);
const notice = ref("");
const language = ref<"curl" | "python" | "codex">("curl");

const languageOptions = [
  { label: "cURL", value: "curl" },
  { label: "Python", value: "python" },
  { label: "Codex", value: "codex" },
];

watch(
  () => props.state.api_settings,
  (next) => {
    if (!next || settingsDirty.value) return;
    domain.value = next.domain || "";
  },
  { deep: true },
);

const api = computed(() => props.state.runtime.public_api);
const recentRequests = computed<PublicAPIRequestMetric[]>(
  () => api.value?.recent_requests || [],
);
const speedSummary = computed(() => resolveSpeedSummary(api.value));
const ready = computed(() => props.state.runtime.state === "ready");
const gpuStarting = computed(() => props.state.runtime.state === "starting");
const enabled = computed(
  () => !!api.value?.enabled || api.value?.state === "starting",
);
const model = computed(() =>
  props.state.runtime.state === "stopped"
    ? undefined
    : props.state.models.find(
        (m) => m.loaded || m.id === props.state.runtime.loaded_model,
      ),
);
const activeTask = computed(() =>
  props.state.runtime.state === "stopped"
    ? undefined
    : props.state.runtime.tasks?.find((t) => t.state === "running"),
);
const activeModelTask = computed(() =>
  activeTask.value &&
  (activeTask.value.kind === "download" || activeTask.value.kind === "load")
    ? activeTask.value
    : undefined,
);
const activeModelTaskSpec = computed(() =>
  activeModelTask.value
    ? props.state.models.find((m) => m.id === activeModelTask.value!.model_id)
    : undefined,
);
const activeContextTokens = computed(
  () =>
    props.state.runtime.active_options?.context_length ||
    model.value?.context_length ||
    8192,
);
const activeContextK = computed(() => Math.round(activeContextTokens.value / 1024));
const activeTemp = computed(() =>
  (
    props.state.runtime.active_options?.temperature ??
    model.value?.temperature ??
    0.7
  ).toFixed(1),
);
const status = computed(
  () =>
    ({
      stopping: "正在关闭",
      stopped: ready.value
        ? "未开启"
        : props.state.runtime.state === "disconnected"
          ? "状态待确认"
          : "未开启",
      starting: "正在连接",
      ready: "已开启",
      reconnecting: "连接待恢复",
      failed: "连接失败",
    })[api.value?.state || "stopped"] || "未开启",
);
const statusTagColor = computed(() => {
  switch (api.value?.state) {
    case "ready":
      return "success";
    case "starting":
      return "processing";
    case "reconnecting":
    case "stopping":
      return "warning";
    case "failed":
      return "error";
    default:
      return "default";
  }
});
const statusBadgeStatus = computed<
  "success" | "processing" | "warning" | "error" | "default"
>(() => {
  switch (api.value?.state) {
    case "ready":
      return "success";
    case "starting":
      return "processing";
    case "reconnecting":
    case "stopping":
      return "warning";
    case "failed":
      return "error";
    default:
      return "default";
  }
});
const noticeType = computed<"success" | "info">(() =>
  notice.value === READY_NOTICE ||
  notice.value === "已复制" ||
  notice.value.includes("已保存")
    ? "success"
    : "info",
);
const base = computed(() =>
  api.value?.url
    ? `${api.value.url}/v1`
    : props.state.api_settings?.domain
      ? `${props.state.api_settings.domain}/v1`
      : "https://YOUR-API-HOST/v1",
);
const selectedModel = computed(() => model.value?.id || "YOUR-MODEL-ID");
const code = computed(() => {
  const id = selectedModel.value;
  if (language.value === "codex") {
    const ctx = activeContextTokens.value;
    const compactLimit = Math.max(2048, Math.round(ctx * 0.8));
    return `# ~/.codex/config.toml\nmodel = "${id}"\nmodel_provider = "colabone"\nmodel_context_window = ${ctx}\nmodel_auto_compact_token_limit = ${compactLimit}\n\n[model_providers.colabone]\nname = "ColabOne"\nbase_url = "${base.value}"\nenv_key = "COLABONE_API_KEY"\nwire_api = "responses"\nsupports_websockets = false`;
  }
  if (language.value === "python") {
    return `import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${base.value}",\n    api_key=os.environ["COLABONE_API_KEY"],\n)\nreply = client.chat.completions.create(\n    model="${id}",\n    messages=[{"role": "user", "content": "你好"}],\n    max_tokens=512,\n    stream=True,\n)\nfor chunk in reply:\n    if chunk.choices:\n        print(chunk.choices[0].delta.content or "", end="", flush=True)`;
  }
  return `curl -N "${base.value}/chat/completions" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ model: id, messages: [{ role: "user", content: "你好" }], max_tokens: 512, stream: true }, null, 2)}'`;
});

function onLanguageChange(val: string | number) {
  language.value =
    val === "python" ? "python" : val === "codex" ? "codex" : "curl";
}

async function act(action: string, payload = {}) {
  busy.value = true;
  notice.value = "";
  try {
    await colabOneCommand(action, payload);
    emit("sync");
    return true;
  } catch (e) {
    emit("error", String(e));
    return false;
  } finally {
    busy.value = false;
  }
}

async function unloadModel() {
  if (await act("model.unload")) {
    emit("notify", "已卸载当前显存模型");
  }
}

const STARTING_NOTICE =
  "正在远端启动 API 服务，公网验证通过后将显示调用地址。";
const READY_NOTICE = "对外 API 服务已开启。";

watch(
  () => api.value?.state,
  (next) => {
    if (notice.value === STARTING_NOTICE && next && next !== "starting") {
      notice.value = next === "ready" ? READY_NOTICE : "";
    } else if (notice.value === READY_NOTICE && next !== "ready") {
      notice.value = "";
    }
  },
);
watch(
  () => props.state.runtime.id,
  () => {
    if (notice.value === STARTING_NOTICE || notice.value === READY_NOTICE) {
      notice.value = "";
    }
  },
);

async function start() {
  if (!ready.value) return;
  if ((props.state.runtime.protocol || 0) < 4) {
    await act("runtime.upgrade");
    return;
  }
  if (
    await act("api.start", {
      mode: "named",
      domain: domain.value.trim(),
      tunnel_token: tunnelToken.value.trim(),
    })
  ) {
    tunnelToken.value = "";
    settingsDirty.value = false;
    notice.value =
      api.value?.state === "ready" ? READY_NOTICE : STARTING_NOTICE;
  }
}

async function save() {
  if (
    await act("api.save", {
      mode: "named",
      domain: domain.value.trim(),
      tunnel_token: tunnelToken.value.trim(),
    })
  ) {
    tunnelToken.value = "";
    settingsDirty.value = false;
    notice.value = "固定域名隧道配置已保存到本机凭据库。";
  }
}

async function copy(
  value: string,
  field: "" | "url" | "key" | "model" | "code" = "",
) {
  const markCopied = () => {
    notice.value = "已复制";
    if (field) {
      copiedField.value = field;
      clearTimeout(copyTimer.value);
      copyTimer.value = setTimeout(() => {
        if (copiedField.value === field) copiedField.value = "";
      }, 1800);
    }
  };
  try {
    await colabOneWriteClipboard(value);
    await navigator.clipboard?.writeText(value).catch(() => {});
    markCopied();
  } catch {
    try {
      await navigator.clipboard.writeText(value);
      markCopied();
    } catch {
      emit("error", "复制失败，请手动选择并复制");
    }
  }
}

async function copyKey() {
  try {
    const value = await colabOneCommand("api.key");
    await copy(value.key, "key");
  } catch (e) {
    emit("error", String(e));
  }
}

async function reveal() {
  if (key.value) {
    key.value = "";
    return;
  }
  try {
    key.value = (await colabOneCommand("api.key")).key;
    clearTimeout(keyTimer.value);
    keyTimer.value = setTimeout(() => (key.value = ""), 30000);
  } catch (e) {
    emit("error", String(e));
  }
}

async function rotate() {
  if (await act("api.rotate")) {
    key.value = "";
    notice.value = "API Key 已更换，请重新开启服务并在调用方同步更新密钥。";
  }
}

onUnmounted(() => {
  key.value = "";
  clearTimeout(keyTimer.value);
  clearTimeout(copyTimer.value);
});
</script>

<template>
  <div class="co-api-page">
    <header class="co-api-page-header">
      <div class="co-api-header-main">
        <div class="co-api-header-title">
          <h1>对外 API</h1>
          <ATag :color="statusTagColor" class="co-api-status-tag">
            <ABadge :status="statusBadgeStatus" />
            <span>{{ status }}</span>
          </ATag>
        </div>
        <p class="co-api-header-sub">
          通过远端 GPU 提供 OpenAI Responses / Chat Completions 兼容接口与 Codex 上下文压缩；连续
          {{ formatIdlePolicyLabel(props.state.settings.idle_minutes) }}
          无请求或操作后自动释放远端资源。
        </p>
      </div>

      <div class="co-api-header-action">
        <ASpace :size="8" wrap>
          <AButton
            v-if="enabled"
            type="primary"
            danger
            :loading="busy"
            :disabled="busy || !!state.operation"
            @click="act('api.stop')"
          >
            <template #icon><PhStop :size="14" weight="fill" /></template>
            关闭服务
          </AButton>
          <template v-else>
            <ATooltip
              :title="
                !ready
                  ? gpuStarting
                    ? '远端 GPU 正在启动中'
                    : '请先启动远端 GPU'
                  : ''
              "
            >
              <AButton
                type="primary"
                :loading="busy"
                :disabled="!ready || busy || !!state.operation"
                @click="start"
              >
                <template #icon><PhPlay :size="14" weight="fill" /></template>
                {{
                  ready && (state.runtime.protocol || 0) < 4
                    ? "更新远端服务"
                    : "开启 API 服务"
                }}
              </AButton>
            </ATooltip>
            <AButton
              v-if="!ready"
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
    </header>

    <ACard
      class="co-api-model-slot"
      :class="{
        loaded: !!model,
        busy: !!activeModelTask && !model,
        warning: ready && !model && !activeModelTask,
      }"
      aria-label="当前服务模型"
    >
      <div class="co-api-model-row">
        <div class="co-api-model-main">
          <div class="co-api-model-head">
            <ASpin v-if="activeModelTask && !model" size="small" />
            <PhCube
              v-else
              :size="18"
              weight="duotone"
              class="co-api-model-icon"
              aria-hidden="true"
            />
            <span class="co-api-model-eyebrow">服务模型</span>
            <template v-if="model">
              <strong class="co-api-model-name">{{ model.name }}</strong>
              <ATag color="success" class="co-api-model-tag">
                <ABadge status="processing" color="#52c41a" />
                <span>已装载显存</span>
              </ATag>
              <ATag :bordered="false" class="co-api-model-spec-tag">
                {{ activeContextK }}K · 温度 {{ activeTemp }}
              </ATag>
            </template>
            <template v-else-if="activeModelTask">
              <strong class="co-api-model-name">
                {{
                  activeModelTask.kind === "download"
                    ? `正在下载${activeModelTaskSpec ? ` ${activeModelTaskSpec.name}` : "模型"}`
                    : `正在装载${activeModelTaskSpec ? ` ${activeModelTaskSpec.name}` : "模型"}到显存`
                }}
              </strong>
              <ATag color="processing" class="co-api-model-tag">
                {{
                  activeModelTask.kind === "download"
                    ? `${Math.round((activeModelTask.progress || 0) * 100)}%`
                    : "装载中"
                }}
              </ATag>
            </template>
            <template v-else-if="ready">
              <strong class="co-api-model-name">尚未选择模型</strong>
              <ATag color="warning" class="co-api-model-tag">未装载显存</ATag>
            </template>
            <template v-else>
              <strong class="co-api-model-name">尚未装载模型</strong>
              <ATag
                :color="gpuStarting ? 'processing' : 'default'"
                class="co-api-model-tag"
              >
                {{ gpuStarting ? "远端 GPU 启动中" : "远端 GPU 未启动" }}
              </ATag>
            </template>
          </div>

          <div class="co-api-model-sub">
            <template v-if="model">
              <span>模型 ID：<code>{{ model.id }}</code></span>
              <template v-if="state.runtime.gpu">
                <span>·</span>
                <span class="co-tabular">
                  显存
                  {{ (state.runtime.gpu.used_mib / 1024).toFixed(1) }} /
                  {{ (state.runtime.gpu.total_mib / 1024).toFixed(0) }} GB
                </span>
              </template>
            </template>
            <template v-else-if="activeModelTask">
              <span>
                {{
                  activeModelTask.message || "可在模型抽屉查看详细进度"
                }}
              </span>
            </template>
            <template v-else-if="ready">
              <span>
                从「我的」或「社区」选择并装载 GGUF 模型后，即可对外提供推理服务。
              </span>
            </template>
            <template v-else>
              <span>
                可先在「我的」或「社区」浏览并管理模型；启动远端 GPU 后一键下载并装载到显存。
              </span>
            </template>
          </div>
        </div>

        <ASpace :size="8" wrap class="co-api-model-actions">
          <AButton
            v-if="model"
            size="small"
            :disabled="!!activeTask || busy || !!props.busy"
            @click="unloadModel"
          >
            卸载
          </AButton>
          <AButton
            :type="ready && !model && !activeModelTask ? 'primary' : 'default'"
            :ghost="ready && !model && !activeModelTask"
            size="small"
            @click="openModelDrawer"
          >
            <template #icon><PhCube :size="14" weight="duotone" /></template>
            {{
              model
                ? "切换模型"
                : activeModelTask
                  ? "查看模型"
                  : "选择模型"
            }}
            <PhArrowRight :size="13" />
          </AButton>
        </ASpace>
      </div>
    </ACard>

    <AAlert
      v-if="api?.error"
      type="error"
      show-icon
      :message="api.error"
      role="alert"
    />

    <AAlert
      v-if="notice"
      :type="noticeType"
      show-icon
      closable
      :message="notice"
      role="status"
      @close="notice = ''"
    />

    <div class="co-api-grid">
      <ACard title="固定域名与隧道配置" class="co-api-connection">
        <div class="co-mode-panel">
          <p class="co-field-help">
            通过固定公网 HTTPS 域名接入外部客户端与 SDK，支持 SSE 流式输出（<code>stream=true</code>）。域名与隧道保存在你的 Cloudflare 账户中。
          </p>

          <div class="co-api-form-grid">
            <div class="co-field-group">
              <label class="co-field-label" for="api-domain">API 域名</label>
              <AInput
                id="api-domain"
                v-model:value="domain"
                :disabled="enabled || busy"
                placeholder="api.example.com"
                autocomplete="off"
                spellcheck="false"
                allow-clear
                @change="settingsDirty = true"
              />
            </div>

            <div class="co-field-group">
              <label class="co-field-label" for="api-tunnel">
                Cloudflare 安装命令或 Tunnel Token
              </label>
              <AInputPassword
                id="api-tunnel"
                v-model:value="tunnelToken"
                :disabled="enabled || busy"
                :placeholder="
                  state.api_settings?.has_token
                    ? '已安全保存；留空继续使用'
                    : '直接粘贴整段安装命令，自动提取 Token'
                "
                autocomplete="off"
                spellcheck="false"
              />
            </div>
          </div>

          <div v-if="!enabled" class="co-config-actions">
            <AButton
              :loading="busy"
              :disabled="busy"
              @click="save"
            >
              <template #icon><PhFloppyDisk :size="15" /></template>
              保存配置
            </AButton>
          </div>

          <details class="co-api-guide">
            <summary>首次配置？查看 3 步指引</summary>
            <ol>
              <li>
                在 Cloudflare 创建一个 Tunnel，选择 Cloudflared。
                <AButton
                  type="link"
                  size="small"
                  class="co-inline-link co-inline-link-btn"
                  @click="colabOneOpenURL('https://one.dash.cloudflare.com/')"
                >
                  打开控制台
                  <PhArrowSquareOut :size="13" />
                </AButton>
              </li>
              <li>
                添加一个公开域名，服务类型选 HTTP，地址填
                <code>http://127.0.0.1:7861</code>。
              </li>
              <li>
                将 Cloudflare 给出的安装命令粘贴到上方，保存并开启服务。客户端会在远端安装并验证连接。
              </li>
            </ol>
            <p class="co-field-help">
              请勿为 API 域名开启 Cloudflare 交互式登录验证，以免拦截客户端请求。
            </p>
          </details>
        </div>

        <ADivider class="co-section-divider" />

        <div class="co-api-access">
          <div class="co-section-subhead">
            <h3>实时接入凭据</h3>
            <ATag v-if="api?.state === 'ready'" color="success">公网就绪</ATag>
            <ATag v-else-if="key" color="blue">密钥可见</ATag>
            <ATag v-else color="default">未开启</ATag>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">Base URL</span>
            <div class="co-api-copy-row">
              <code>{{ api?.url ? base : "开启服务后显示" }}</code>
              <ATooltip :title="copiedField === 'url' ? '已复制' : '复制 API 地址'">
                <AButton
                  size="small"
                  aria-label="复制 API 地址"
                  :disabled="!api?.url || api.state !== 'ready'"
                  @click="copy(base, 'url')"
                >
                  <template #icon>
                    <PhCheck v-if="copiedField === 'url'" :size="14" />
                    <PhCopy v-else :size="14" />
                  </template>
                  {{ copiedField === "url" ? "已复制" : "复制" }}
                </AButton>
              </ATooltip>
            </div>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">API Key</span>
            <div class="co-api-copy-row">
              <code>{{ key || "••••••••••••••••••••••••" }}</code>
              <ASpace :size="6" class="co-cred-actions">
                <ATooltip
                  :title="
                    key ? '隐藏 API 密钥' : '显示 API 密钥（30 秒后自动隐藏）'
                  "
                >
                  <AButton
                    size="small"
                    :aria-label="key ? '隐藏 API 密钥' : '显示 API 密钥'"
                    @click="reveal"
                  >
                    <template #icon>
                      <PhEyeSlash v-if="key" :size="14" />
                      <PhEye v-else :size="14" />
                    </template>
                  </AButton>
                </ATooltip>

                <AButton
                  size="small"
                  aria-label="复制 API 密钥"
                  @click="copyKey"
                >
                  <template #icon>
                    <PhCheck v-if="copiedField === 'key'" :size="14" />
                    <PhCopy v-else :size="14" />
                  </template>
                  {{ copiedField === "key" ? "已复制" : "复制" }}
                </AButton>

                <APopconfirm
                  title="确认更换 API Key？"
                  description="将关闭对外 API 并使旧密钥失效，调用方需要同步更新密钥。"
                  ok-text="确认更换"
                  cancel-text="取消"
                  :ok-button-props="{ danger: true }"
                  @confirm="rotate"
                >
                  <AButton danger size="small" :disabled="busy">
                    <template #icon><PhArrowsClockwise :size="14" /></template>
                    更换
                  </AButton>
                </APopconfirm>
              </ASpace>
            </div>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">当前模型 ID</span>
            <div class="co-api-copy-row">
              <code>{{ model?.id || "请先点击上方「选择模型」装载 GGUF 模型" }}</code>
              <AButton
                size="small"
                aria-label="复制模型 ID"
                :disabled="!model"
                @click="copy(model!.id, 'model')"
              >
                <template #icon>
                  <PhCheck v-if="copiedField === 'model'" :size="14" />
                  <PhCopy v-else :size="14" />
                </template>
                {{ copiedField === "model" ? "已复制" : "复制" }}
              </AButton>
            </div>
          </div>

          <p class="co-field-help co-key-help">
            <PhKey :size="14" />
            <span>
              密钥保存在本机系统凭据库。调用时使用
              <code>Authorization: Bearer</code>，仅分享给受信任的应用或成员。
            </span>
          </p>
        </div>
      </ACard>

      <ACard title="调用示例与统计" class="co-api-examples">
        <template #extra>
          <ATag color="green" class="co-stream-tag">SSE 流式</ATag>
        </template>

        <div class="co-api-stats-grid">
          <div class="co-stat-tile">
            <AStatistic title="总请求" :value="api?.requests || 0" />
          </div>
          <div class="co-stat-tile">
            <AStatistic
              title="进行中"
              :value="api?.active || 0"
              :value-style="
                (api?.active || 0) > 0 ? { color: 'var(--co-accent)' } : undefined
              "
            />
          </div>
          <div class="co-stat-tile">
            <AStatistic title="已完成" :value="api?.completed || 0" />
          </div>
          <div class="co-stat-tile">
            <AStatistic
              title="失败"
              :value="api?.failed || 0"
              :value-style="
                (api?.failed || 0) > 0
                  ? { color: 'var(--co-danger-text)' }
                  : undefined
              "
            />
          </div>
        </div>

        <!-- Per-Request External API Token Speed Telemetry -->
        <section
          class="co-api-speed-section"
          aria-label="每次外部调用 Token 速度"
        >
          <div class="co-api-speed-summary">
            <div class="co-api-speed-summary-top">
              <div class="co-api-speed-summary-left">
                <PhSpeedometer
                  :size="16"
                  weight="duotone"
                  class="co-api-speed-icon"
                />
                <span class="co-api-speed-label">
                  {{ speedSummary.live ? "实时生成速度" : "最近生成速度" }}
                </span>
                <strong
                  class="co-api-speed-value co-mono"
                  :class="{ 'is-live': speedSummary.live }"
                >
                  {{
                    speedSummary.latestTps
                      ? formatTokenSpeed(
                          speedSummary.latestTps,
                          speedSummary.latestEstimated,
                        )
                      : "-- tokens/s"
                  }}
                </strong>
              </div>
              <div class="co-api-speed-summary-right">
                <span
                  v-if="
                    speedSummary.avgTps &&
                    speedSummary.completedWithSpeedCount >= 2
                  "
                  class="co-api-speed-avg co-mono"
                >
                  均值 {{ formatTokenSpeed(speedSummary.avgTps) }}
                </span>
                <span class="co-api-speed-count co-tabular">
                  {{
                    recentRequests.length
                      ? `最近 ${recentRequests.length} 次`
                      : enabled
                        ? "等待调用"
                        : "暂无调用"
                  }}
                </span>
              </div>
            </div>
          </div>

          <div
            v-if="recentRequests.length"
            class="co-api-request-list"
            role="list"
          >
            <article
              v-for="req in recentRequests"
              :key="`${req.id || req.seq}-${req.started_at}`"
              class="co-api-request-row"
              :class="`is-${req.state}`"
              role="listitem"
            >
              <div class="co-api-request-top">
                <div class="co-api-request-ident">
                  <span class="co-api-request-seq co-mono">#{{ req.seq }}</span>
                  <ATag
                    :color="
                      req.state === 'running'
                        ? 'processing'
                        : req.state === 'completed'
                          ? 'success'
                          : req.state === 'failed'
                            ? 'error'
                            : 'default'
                    "
                    :bordered="false"
                    class="co-api-request-tag"
                  >
                    <ABadge
                      v-if="req.state === 'running'"
                      status="processing"
                      class="co-api-request-dot"
                    />
                    <span>{{ requestStateLabel(req.state) }}</span>
                  </ATag>
                  <ATag :bordered="false" class="co-api-request-tag">
                    {{ requestKindLabel(req.kind) }}
                  </ATag>
                  <ATag
                    v-if="req.stream"
                    color="blue"
                    :bordered="false"
                    class="co-api-request-tag"
                  >
                    流式
                  </ATag>
                  <time class="co-api-request-time co-mono">
                    {{ formatRequestClock(req.started_at) }}
                  </time>
                </div>

                <ATooltip
                  :title="
                    req.estimated
                      ? '上游未返回精确 timings，按输出内容与耗时估算'
                      : req.prompt_tokens_per_second
                        ? `提示词预填充速度 ${formatTokenSpeed(req.prompt_tokens_per_second)}`
                        : '本次请求生成速度'
                  "
                >
                  <span
                    class="co-api-request-tps co-mono"
                    :class="{
                      'is-live': req.state === 'running',
                      'is-empty':
                        !req.tokens_per_second || req.tokens_per_second <= 0,
                    }"
                  >
                    {{
                      req.tokens_per_second && req.tokens_per_second > 0
                        ? formatTokenSpeed(
                            req.tokens_per_second,
                            !!req.estimated,
                          )
                        : req.state === "running"
                          ? "等待首字…"
                          : "—"
                    }}
                  </span>
                </ATooltip>
              </div>

              <div class="co-api-request-meta co-tabular">
                <span v-if="req.completion_tokens != null">
                  输出 {{ formatTokenCount(req.completion_tokens) }} tokens
                </span>
                <span v-if="req.prompt_tokens != null && req.prompt_tokens > 0">
                  输入 {{ formatTokenCount(req.prompt_tokens) }} tokens
                </span>
                <span v-if="req.ttft_ms != null">
                  首字 {{ formatDurationMs(req.ttft_ms) }}
                </span>
                <span>耗时 {{ formatDurationMs(req.duration_ms) }}</span>
                <span
                  v-if="
                    req.prompt_tokens_per_second &&
                    req.prompt_tokens_per_second > 0
                  "
                >
                  预填充 {{ formatTokenSpeed(req.prompt_tokens_per_second) }}
                </span>
                <span
                  v-if="req.state === 'failed' && req.error"
                  class="co-api-request-error"
                >
                  {{ req.error }}
                </span>
              </div>
            </article>
          </div>
          <p v-else-if="enabled" class="co-api-request-empty-hint">
            服务已开启，收到外部客户端请求后将在此实时显示每次调用的 Token 速度与耗时。
          </p>
        </section>

        <ADivider class="co-section-divider" />

        <div class="co-example-toolbar">
          <ASegmented
            size="small"
            :value="language"
            :options="languageOptions"
            aria-label="调用示例语言"
            @change="onLanguageChange"
          />
          <AButton size="small" @click="copy(code, 'code')">
            <template #icon>
              <PhCheck v-if="copiedField === 'code'" :size="14" />
              <PhCopy v-else :size="14" />
            </template>
            {{ copiedField === "code" ? "已复制" : "复制代码" }}
          </AButton>
        </div>

        <p v-if="language === 'codex'" class="co-field-help co-example-help">
          先将密钥设置为环境变量 <code>COLABONE_API_KEY</code>，再将下方配置写入
          <code>~/.codex/config.toml</code>，即可在 Codex 中无缝使用远端模型并支持
          <code>/compact</code> 与自动上下文压缩。
        </p>
        <p v-else class="co-field-help co-example-help">
          先将密钥设置为环境变量 <code>COLABONE_API_KEY</code>，即可用 cURL、OpenAI Python SDK、Codex 或任意兼容客户端接入。
        </p>

        <pre class="co-api-code-block" tabindex="0"><code>{{ code }}</code></pre>

        <p class="co-field-help">
          兼容 OpenAI Responses API、Chat Completions、工具调用（<code>function</code> / <code>custom</code>）与 Codex 上下文压缩（<code>/v1/responses/compact</code>）；暂不支持图像或音频输入。单实例串行处理请求，并发繁忙时返回 409。
        </p>

        <details class="co-api-guide">
          <summary>接口列表与限制</summary>
          <ul>
            <li>
              <code>GET /v1/models</code>、<code>GET /v1/models/{id}</code>：返回当前已装载到显存的 GGUF 模型。
            </li>
            <li>
              <code>POST /v1/responses</code>（及 <code>GET</code> / <code>DELETE /v1/responses/{id}</code>）：OpenAI Responses API，支持 SSE 流式、工具调用、推理摘要与 <code>compaction</code> 续写，适配 Codex <code>wire_api = "responses"</code>。
            </li>
            <li>
              <code>POST /v1/responses/compact</code>：Codex / OpenAI 上下文压缩接口，按当前模型上下文长度自动汇总长对话并返回标准 <code>response.compaction</code> 结构。
            </li>
            <li>
              <code>POST /v1/chat/completions</code>：OpenAI Chat Completions 对话补全接口，支持 <code>stream=true/false</code>、<code>tools</code> 与 <code>developer</code> 角色。
            </li>
          </ul>
          <p class="co-field-help">
            关闭对外 API 不会释放远端 GPU；停止实例或启动 ComfyUI 引擎时会自动卸载当前 GGUF 模型并回收显存。
          </p>
        </details>
      </ACard>
    </div>

    <ModelDrawer
      v-model:open="drawerOpen"
      :state="props.state"
      :busy="props.busy"
      @device="emit('device')"
      @sync="emit('sync')"
      @notify="(msg) => emit('notify', msg)"
      @error="(msg) => emit('error', msg)"
    />
  </div>
</template>

<style scoped>
.co-api-page {
  width: 100%;
  max-width: var(--co-container-max);
  margin: 0 auto;
  padding: 16px var(--co-container-pad) 32px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-api-page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.co-api-header-main {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  flex: 1;
}

.co-api-header-title {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.co-api-header-title h1 {
  margin: 0;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.28;
  letter-spacing: -0.025em;
  color: var(--co-text);
}

.co-api-header-sub {
  margin: 0;
  font-size: 13.5px;
  line-height: 1.55;
  color: var(--co-muted);
}

.co-api-header-action {
  flex-shrink: 0;
}

.co-api-status-tag,
.co-api-model-tag,
.co-api-model-spec-tag {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border-radius: 9999px;
  margin-inline-end: 0;
}

/* Elevated Service Model Card */
.co-api-model-slot {
  border-radius: 16px;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.co-api-model-slot :deep(.ant-card-body) {
  padding: 14px 18px;
}

.co-api-model-slot.loaded {
  border-color: var(--co-success-border);
}

.co-api-model-slot.busy {
  border-color: var(--co-accent);
}

.co-api-model-slot.warning {
  border-color: var(--co-warning-border);
}

.co-api-model-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
  flex-wrap: wrap;
}

.co-api-model-main {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.co-api-model-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-api-model-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-api-model-slot.loaded .co-api-model-icon {
  color: var(--co-success-text);
}

.co-api-model-eyebrow {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-muted);
}

.co-api-model-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-api-model-sub {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 13px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-api-model-sub code {
  font-family: var(--co-font-mono);
  font-size: 12px;
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-text);
}

.co-api-model-actions {
  flex-shrink: 0;
}

.co-api-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 18px;
  align-items: stretch;
}

.co-api-connection,
.co-api-examples {
  min-width: 0;
  border-radius: 16px;
  display: flex;
  flex-direction: column;
}

.co-api-connection :deep(.ant-card-body),
.co-api-examples :deep(.ant-card-body) {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.co-mode-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-api-form-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 12px;
}

@media (min-width: 1440px) {
  .co-api-form-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.co-field-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-field-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-field-help {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-muted);
}

.co-field-help code,
.co-api-guide code {
  font-family: var(--co-font-mono);
  font-size: 12px;
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--co-subtle);
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-config-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-top: 2px;
}

.co-section-divider {
  margin: 12px 0;
}

.co-section-subhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}

.co-section-subhead h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-api-access {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-cred-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-api-copy-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-height: 42px;
  padding: 6px 8px 6px 14px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 12px;
  background: var(--co-subtle);
  flex-wrap: wrap;
}

.co-api-copy-row code {
  flex: 1;
  min-width: 140px;
  font-family: var(--co-font-mono);
  font-size: 13px;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-cred-actions {
  flex-shrink: 0;
}

.co-key-help {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 2px;
}

.co-key-help svg {
  flex-shrink: 0;
  margin-top: 3px;
}

.co-api-stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0;
}

.co-stat-tile {
  padding: 2px 12px;
  font-variant-numeric: tabular-nums;
}

.co-stat-tile:first-child {
  padding-left: 0;
}

.co-stat-tile + .co-stat-tile {
  border-left: 1px solid var(--co-border-secondary);
}

.co-stat-tile :deep(.ant-statistic-title) {
  font-size: 12px;
  margin-bottom: 2px;
}

.co-stat-tile :deep(.ant-statistic-content) {
  font-family: var(--co-font-mono);
  font-size: 22px;
  font-weight: 700;
  line-height: 1.25;
}

.co-api-speed-section {
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-api-speed-summary {
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-api-speed-summary-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px 10px;
  flex-wrap: wrap;
}

.co-api-speed-summary-left {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  white-space: nowrap;
}

.co-api-speed-summary-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  white-space: nowrap;
}

.co-api-speed-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-api-speed-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-api-speed-value {
  font-size: 15px;
  font-weight: 650;
  color: var(--co-text);
}

.co-api-speed-value.is-live {
  color: var(--co-accent);
}

.co-api-speed-avg {
  font-size: 12px;
  color: var(--co-secondary-text);
  padding: 1px 6px;
  border-radius: 4px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
}

.co-api-speed-count {
  font-size: 12px;
  color: var(--co-muted);
}

.co-api-request-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 224px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-api-request-row {
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-api-request-row.is-running {
  border-color: var(--co-accent);
}

.co-api-request-row.is-failed {
  border-color: var(--co-danger-border);
  background: var(--co-danger-bg);
}

.co-api-request-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: nowrap;
}

.co-api-request-ident {
  display: flex;
  align-items: center;
  gap: 4px 6px;
  flex-wrap: wrap;
  min-width: 0;
  flex: 1;
}

.co-api-request-seq {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-secondary-text);
}

.co-api-request-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
  padding-inline: 6px;
}

.co-api-request-dot {
  line-height: 1;
}

.co-api-request-time {
  font-size: 12px;
  color: var(--co-muted);
}

.co-api-request-tps {
  font-size: 13px;
  font-weight: 650;
  color: var(--co-accent-strong);
  white-space: nowrap;
  flex-shrink: 0;
}

.co-api-request-tps.is-live {
  color: var(--co-accent);
}

.co-api-request-tps.is-empty {
  color: var(--co-muted);
  font-weight: 500;
}

.co-api-request-meta {
  display: flex;
  align-items: center;
  gap: 6px 10px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-api-request-error {
  color: var(--co-danger-text);
}

.co-api-request-empty-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-stream-tag {
  border-radius: 9999px;
  margin-inline-end: 0;
  font-family: var(--co-font-mono);
  font-size: 12px;
}

.co-example-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.co-example-help {
  margin-bottom: 8px;
}

.co-api-code-block {
  margin: 0 0 10px;
  padding: 12px 14px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 12px;
  background: var(--co-subtle);
  color: var(--co-text);
  font-family: var(--co-font-mono);
  font-size: 13px;
  line-height: 1.5;
  overflow: auto;
  max-height: 330px;
}

.co-api-guide {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--co-secondary-text);
}

.co-api-guide summary {
  cursor: pointer;
  font-weight: 500;
  color: var(--co-secondary-text);
  user-select: none;
}

.co-api-guide ol,
.co-api-guide ul {
  margin: 8px 0;
  padding-left: 20px;
}

.co-api-guide li {
  margin: 6px 0;
}

@media (max-width: 1020px) {
  .co-api-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 680px) {
  .co-api-page {
    padding: 16px 16px 28px;
  }

  .co-api-stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 0;
  }

  .co-stat-tile:nth-child(odd) {
    padding-left: 0;
    border-left: none;
  }
}
</style>
