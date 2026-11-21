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
} from "ant-design-vue";
import ColabOneCodeBlock from "./ColabOneCodeBlock.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import type { ColabOneToastKind } from "./colabOneToast";
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
import { t, tb } from "./i18n";
import type { PublicAPIRequestMetric, ColabOneSnapshot } from "./types";

const props = defineProps<{
  state: ColabOneSnapshot;
  modelsOpen?: boolean;
  busy?: boolean;
  loading?: boolean;
}>();
const emit = defineEmits<{
  "update:modelsOpen": [boolean];
  device: [];
  comfyui: [];
  sync: [];
  notify: [string, ColabOneToastKind?];
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
const revealingKey = ref(false);
const keyTimer = ref<ReturnType<typeof setTimeout>>();
const copyTimer = ref<ReturnType<typeof setTimeout>>();
const copiedField = ref<"" | "url" | "key" | "model" | "code">("");
const busy = ref(false);
const awaitingApiReady = ref(false);
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
      stopping: t("api.statusStopping"),
      stopped: ready.value
        ? t("api.statusStopped")
        : props.state.runtime.state === "disconnected"
          ? t("api.statusUnconfirmed")
          : t("api.statusStopped"),
      starting: t("api.statusStarting"),
      ready: t("api.statusReady"),
      reconnecting: t("api.statusReconnecting"),
      failed: t("api.statusFailed"),
    })[api.value?.state || "stopped"] || t("api.statusStopped"),
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
  const prompt = t("api.samplePrompt");
  if (language.value === "codex") {
    const ctx = activeContextTokens.value;
    const compactLimit = Math.max(2048, Math.round(ctx * 0.8));
    return `# ~/.codex/config.toml\nmodel = "${id}"\nmodel_provider = "colabone"\nmodel_context_window = ${ctx}\nmodel_auto_compact_token_limit = ${compactLimit}\n\n[model_providers.colabone]\nname = "ColabOne"\nbase_url = "${base.value}"\nenv_key = "COLABONE_API_KEY"\nwire_api = "responses"\nsupports_websockets = false`;
  }
  if (language.value === "python") {
    return `import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${base.value}",\n    api_key=os.environ["COLABONE_API_KEY"],\n)\nreply = client.chat.completions.create(\n    model="${id}",\n    messages=[{"role": "user", "content": ${JSON.stringify(prompt)}}],\n    max_tokens=512,\n    stream=True,\n)\nfor chunk in reply:\n    if chunk.choices:\n        print(chunk.choices[0].delta.content or "", end="", flush=True)`;
  }
  return `curl -N "${base.value}/chat/completions" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ model: id, messages: [{ role: "user", content: prompt }], max_tokens: 512, stream: true }, null, 2)}'`;
});

function onLanguageChange(val: string | number) {
  language.value =
    val === "python" ? "python" : val === "codex" ? "codex" : "curl";
}

async function act(action: string, payload = {}) {
  busy.value = true;
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
    emit("notify", t("api.unloadedToast"), "success");
  }
}

watch(
  () => api.value?.state,
  (next) => {
    if (awaitingApiReady.value && next && next !== "starting") {
      awaitingApiReady.value = false;
      if (next === "ready") {
        emit("notify", t("api.readyNotice"), "success");
      }
    }
  },
);
watch(
  () => props.state.runtime.id,
  () => {
    awaitingApiReady.value = false;
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
    if (api.value?.state === "ready") {
      awaitingApiReady.value = false;
      emit("notify", t("api.readyNotice"), "success");
    } else {
      awaitingApiReady.value = true;
      emit("notify", t("api.startingNotice"), "info");
    }
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
    emit("notify", t("api.savedConfigNotice"), "success");
  }
}

async function copy(
  value: string,
  field: "" | "url" | "key" | "model" | "code" = "",
) {
  const markCopied = () => {
    emit("notify", t("common.copied"), "success");
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
      emit("error", t("common.copyFailed"));
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
  if (revealingKey.value) return;
  if (key.value) {
    key.value = "";
    return;
  }
  revealingKey.value = true;
  try {
    key.value = (await colabOneCommand("api.key")).key;
    clearTimeout(keyTimer.value);
    keyTimer.value = setTimeout(() => (key.value = ""), 30000);
  } catch (e) {
    emit("error", String(e));
  } finally {
    revealingKey.value = false;
  }
}

async function rotate() {
  if (await act("api.rotate")) {
    key.value = "";
    emit("notify", t("api.rotatedKeyNotice"), "info");
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
          <h1>{{ t("api.title") }}</h1>
          <ColabOneSkeleton
            v-if="props.loading"
            :width="78"
            :height="22"
            variant="pill"
            inline
          />
          <ATag v-else :color="statusTagColor" class="co-api-status-tag co-sk-reveal">
            <ABadge :status="statusBadgeStatus" />
            <span>{{ status }}</span>
          </ATag>
        </div>
        <p class="co-api-header-sub">
          <ColabOneSkeleton
            v-if="props.loading"
            width="62%"
            :height="14"
            variant="text"
            tone="subtle"
            :delay="60"
          />
          <template v-else>
            {{
              t("api.subtitle", {
                policy: formatIdlePolicyLabel(props.state.settings.idle_minutes),
              })
            }}
          </template>
        </p>
      </div>

      <div class="co-api-header-action">
        <ColabOneSkeleton
          v-if="props.loading"
          :width="112"
          :height="32"
          variant="button"
          tone="accent"
          :delay="80"
        />
        <ASpace v-else :size="8" wrap>
          <AButton
            v-if="enabled"
            type="primary"
            danger
            :loading="busy"
            :disabled="busy || !!state.operation"
            @click="act('api.stop')"
          >
            <template #icon><PhStop :size="14" weight="fill" /></template>
            {{ t("api.stopService") }}
          </AButton>
          <template v-else>
            <ColabOneTooltip
              :title="
                !ready
                  ? gpuStarting
                    ? t('api.gpuStartingTooltip')
                    : t('api.startGpuFirstTooltip')
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
                    ? t("api.updateRemoteService")
                    : t("api.startService")
                }}
              </AButton>
            </ColabOneTooltip>
            <AButton
              v-if="!ready"
              :type="gpuStarting ? 'default' : 'primary'"
              @click="emit('device')"
            >
              <template #icon><PhHardDrives :size="15" /></template>
              {{ gpuStarting ? t("api.viewGpuProgress") : t("api.goStartGpu") }}
              <PhArrowRight :size="14" />
            </AButton>
          </template>
        </ASpace>
      </div>
    </header>

    <ACard
      class="co-api-model-slot"
      :class="{
        loaded: !props.loading && !!model,
        busy: !props.loading && !!activeModelTask && !model,
        warning: !props.loading && ready && !model && !activeModelTask,
      }"
      :aria-label="t('api.currentModelAria')"
    >
      <div
        v-if="props.loading"
        class="co-api-model-row"
        role="status"
        aria-busy="true"
      >
        <span class="co-sr-only">{{ t("common.loading") }}</span>
        <div class="co-api-model-main">
          <div class="co-api-model-head">
            <ColabOneSkeleton
              :width="18"
              :height="18"
              :radius="5"
              variant="rect"
              tone="accent"
            />
            <ColabOneSkeleton :width="72" :height="13" variant="text" tone="subtle" :delay="40" />
            <ColabOneSkeleton :width="154" :height="17" variant="text" :delay="75" />
            <ColabOneSkeleton :width="68" :height="22" variant="pill" tone="subtle" :delay="110" />
          </div>
          <div class="co-api-model-sub">
            <ColabOneSkeleton width="58%" :height="13" variant="text" tone="subtle" :delay="130" />
          </div>
        </div>
        <div class="co-api-model-actions">
          <ColabOneSkeleton
            :width="88"
            :height="24"
            variant="button"
            :radius="6"
            tone="subtle"
            :delay="150"
          />
        </div>
      </div>

      <div v-else class="co-api-model-row co-sk-reveal">
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
            <span class="co-api-model-eyebrow">{{ t("api.servingModelEyebrow") }}</span>
            <template v-if="model">
              <strong class="co-api-model-name">{{ model.name }}</strong>
              <ATag color="success" class="co-api-model-tag">
                <ABadge status="processing" color="#52c41a" />
                <span>{{ t("api.loadedInVram") }}</span>
              </ATag>
              <ATag :bordered="false" class="co-api-model-spec-tag">
                {{ t("api.contextAndTemp", { ctx: activeContextK, temp: activeTemp }) }}
              </ATag>
            </template>
            <template v-else-if="activeModelTask">
              <strong class="co-api-model-name">
                {{
                  activeModelTask.kind === "download"
                    ? activeModelTaskSpec
                      ? t("api.downloadingModel", { name: activeModelTaskSpec.name })
                      : t("api.downloadingModelGeneric")
                    : activeModelTaskSpec
                      ? t("api.loadingModel", { name: activeModelTaskSpec.name })
                      : t("api.loadingModelGeneric")
                }}
              </strong>
              <ATag color="processing" class="co-api-model-tag">
                {{
                  activeModelTask.kind === "download"
                    ? `${Math.round((activeModelTask.progress || 0) * 100)}%`
                    : t("api.loadingTag")
                }}
              </ATag>
            </template>
            <template v-else-if="ready">
              <strong class="co-api-model-name">{{ t("api.noModelSelected") }}</strong>
              <ATag color="warning" class="co-api-model-tag">{{ t("api.notLoadedTag") }}</ATag>
            </template>
            <template v-else>
              <strong class="co-api-model-name">{{ t("api.noModelLoaded") }}</strong>
              <ATag
                :color="gpuStarting ? 'processing' : 'default'"
                class="co-api-model-tag"
              >
                {{ gpuStarting ? t("api.gpuStartingTag") : t("api.gpuStoppedTag") }}
              </ATag>
            </template>
          </div>

          <div class="co-api-model-sub">
            <template v-if="model">
              <span>{{ t("api.modelIdLabel") }}<code>{{ model.id }}</code></span>
              <template v-if="state.runtime.gpu">
                <span>·</span>
                <span class="co-tabular">
                  {{
                    t("api.vramUsage", {
                      vram: `${(state.runtime.gpu.used_mib / 1024).toFixed(1)} / ${(state.runtime.gpu.total_mib / 1024).toFixed(0)} GB`,
                    })
                  }}
                </span>
              </template>
            </template>
            <template v-else-if="activeModelTask">
              <span>
                {{
                  activeModelTask.message
                    ? tb(activeModelTask.message)
                    : t("api.taskProgressFallback")
                }}
              </span>
            </template>
            <template v-else-if="ready">
              <span>{{ t("api.pickModelHintReady") }}</span>
            </template>
            <template v-else>
              <span>{{ t("api.pickModelHintOffline") }}</span>
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
            {{ t("api.unload") }}
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
                ? t("api.switchModel")
                : activeModelTask
                  ? t("api.browseModels")
                  : t("api.chooseModel")
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
      :message="tb(api.error)"
      role="alert"
    />

    <div class="co-api-grid">
      <ACard :title="t('api.connectionCardTitle')" class="co-api-connection">
        <div class="co-mode-panel">
          <p class="co-field-help">
            {{ t("api.connectionCardDesc", { stream: "stream=true" }) }}
          </p>

          <div class="co-api-form-grid">
            <div class="co-field-group">
              <label class="co-field-label" for="api-domain">{{ t("api.domainLabel") }}</label>
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
                {{ t("api.tunnelTokenLabel") }}
              </label>
              <AInputPassword
                id="api-tunnel"
                v-model:value="tunnelToken"
                :disabled="enabled || busy"
                :placeholder="
                  state.api_settings?.has_token
                    ? t('api.tunnelTokenSavedPlaceholder')
                    : t('api.tunnelTokenEmptyPlaceholder')
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
              {{ t("api.saveConfig") }}
            </AButton>
          </div>

          <details class="co-api-guide">
            <summary>{{ t("api.guideSummary") }}</summary>
            <ol>
              <li>
                {{ t("api.guideStep1") }}
                <AButton
                  type="link"
                  size="small"
                  class="co-inline-link co-inline-link-btn"
                  @click="colabOneOpenURL('https://one.dash.cloudflare.com/')"
                >
                  {{ t("api.openDashboard") }}
                  <PhArrowSquareOut :size="13" />
                </AButton>
              </li>
              <li>
                {{ t("api.guideStep2Prefix") }}
                <code>http://127.0.0.1:7861</code>
              </li>
              <li>
                {{ t("api.guideStep3") }}
              </li>
            </ol>
            <p class="co-field-help">
              {{ t("api.guideNote") }}
            </p>
          </details>
        </div>

        <ADivider class="co-section-divider" />

        <div class="co-api-access">
          <div class="co-section-subhead">
            <h3>{{ t("api.credentialsHeading") }}</h3>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="66"
              :height="20"
              variant="tag"
            />
            <ATag v-else-if="api?.state === 'ready'" color="success">{{ t("api.credPublicReady") }}</ATag>
            <ATag
              v-else-if="api?.state === 'starting' || api?.state === 'reconnecting'"
              color="processing"
            >
              {{ status }}
            </ATag>
            <ATag v-else-if="key" color="blue">{{ t("api.credKeyVisible") }}</ATag>
            <ATag v-else color="default">{{ t("api.credOff") }}</ATag>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">Base URL</span>
            <div class="co-api-copy-row">
              <span
                v-if="props.loading || ((api?.state === 'starting' || api?.state === 'reconnecting') && !api?.url)"
                class="co-api-code-sk"
                role="status"
                aria-busy="true"
              >
                <ColabOneSkeleton
                  :width="props.loading ? '70%' : '64%'"
                  :height="14"
                  variant="text"
                  :tone="props.loading ? 'default' : 'accent'"
                />
                <span class="co-sr-only">{{ props.loading ? t("common.loading") : status }}</span>
              </span>
              <code v-else class="co-sk-reveal">{{ api?.url ? base : t("api.urlPlaceholder") }}</code>
              <ColabOneTooltip
                :title="copiedField === 'url' ? t('common.copied') : t('api.copyUrlTooltip')"
                :disabled="!api?.url || api.state !== 'ready'"
              >
                <AButton
                  size="small"
                  :aria-label="t('api.copyUrlTooltip')"
                  :disabled="!api?.url || api.state !== 'ready'"
                  @click="copy(base, 'url')"
                >
                  <template #icon>
                    <PhCheck v-if="copiedField === 'url'" :size="14" />
                    <PhCopy v-else :size="14" />
                  </template>
                  {{ copiedField === "url" ? t("common.copied") : t("common.copy") }}
                </AButton>
              </ColabOneTooltip>
            </div>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">API Key</span>
            <div class="co-api-copy-row">
              <span
                v-if="props.loading || revealingKey"
                class="co-api-code-sk"
                role="status"
                aria-busy="true"
              >
                <ColabOneSkeleton
                  :width="revealingKey ? 220 : 176"
                  :height="14"
                  variant="text"
                  :tone="revealingKey ? 'accent' : 'default'"
                  :delay="props.loading ? 50 : 0"
                />
                <span class="co-sr-only">{{ t("common.loading") }}</span>
              </span>
              <code v-else :class="{ 'co-sk-reveal': !!key }">{{ key || "••••••••••••••••••••••••" }}</code>
              <ASpace :size="6" class="co-cred-actions">
                <ColabOneTooltip
                  :title="
                    key ? t('api.hideKeyTooltip') : t('api.showKeyTooltip')
                  "
                  :disabled="props.loading || revealingKey"
                >
                  <AButton
                    size="small"
                    :disabled="props.loading || revealingKey"
                    :aria-label="key ? t('api.hideKeyAria') : t('api.showKeyAria')"
                    @click="reveal"
                  >
                    <template #icon>
                      <PhEyeSlash v-if="key" :size="14" />
                      <PhEye v-else :size="14" />
                    </template>
                  </AButton>
                </ColabOneTooltip>

                <AButton
                  size="small"
                  :disabled="props.loading"
                  :aria-label="t('api.copyKeyAria')"
                  @click="copyKey"
                >
                  <template #icon>
                    <PhCheck v-if="copiedField === 'key'" :size="14" />
                    <PhCopy v-else :size="14" />
                  </template>
                  {{ copiedField === "key" ? t("common.copied") : t("common.copy") }}
                </AButton>

                <APopconfirm
                  :title="t('api.rotateConfirmTitle')"
                  :description="t('api.rotateConfirmDesc')"
                  :ok-text="t('api.rotateConfirmOk')"
                  :cancel-text="t('common.cancel')"
                  :ok-button-props="{ danger: true }"
                  @confirm="rotate"
                >
                  <AButton danger size="small" :disabled="busy || props.loading">
                    <template #icon><PhArrowsClockwise :size="14" /></template>
                    {{ t("api.rotateBtn") }}
                  </AButton>
                </APopconfirm>
              </ASpace>
            </div>
          </div>

          <div class="co-cred-field">
            <span class="co-field-label">{{ t("api.currentModelIdLabel") }}</span>
            <div class="co-api-copy-row">
              <span
                v-if="props.loading"
                class="co-api-code-sk"
                aria-hidden="true"
              >
                <ColabOneSkeleton
                  width="54%"
                  :height="14"
                  variant="text"
                  tone="subtle"
                  :delay="100"
                />
              </span>
              <code v-else>{{ model?.id || t("api.modelIdEmpty") }}</code>
              <AButton
                size="small"
                :aria-label="t('api.copyModelIdAria')"
                :disabled="!model"
                @click="copy(model!.id, 'model')"
              >
                <template #icon>
                  <PhCheck v-if="copiedField === 'model'" :size="14" />
                  <PhCopy v-else :size="14" />
                </template>
                {{ copiedField === "model" ? t("common.copied") : t("common.copy") }}
              </AButton>
            </div>
          </div>

          <p class="co-field-help co-key-help">
            <PhKey :size="14" />
            <span>
              {{ t("api.credentialsFootPrefix") }}
              <code>Authorization: Bearer</code>{{ t("api.credentialsFootSuffix") }}
            </span>
          </p>
        </div>
      </ACard>

      <ACard :title="t('api.examplesCardTitle')" class="co-api-examples">
        <template #extra>
          <ATag color="green" class="co-stream-tag">{{ t("api.sseStreamTag") }}</ATag>
        </template>

        <div v-if="props.loading" class="co-api-stats-grid" aria-hidden="true">
          <div v-for="idx in 4" :key="idx" class="co-stat-tile co-stat-tile-sk">
            <ColabOneSkeleton
              :width="48"
              :height="12"
              variant="text"
              tone="subtle"
              :delay="(idx - 1) * 55"
            />
            <ColabOneSkeleton
              :width="44"
              :height="26"
              variant="metric"
              :tone="idx === 2 ? 'accent' : 'default'"
              :delay="(idx - 1) * 55 + 45"
            />
          </div>
        </div>
        <div v-else class="co-api-stats-grid co-sk-reveal">
          <div class="co-stat-tile">
            <AStatistic :title="t('api.statTotalRequests')" :value="api?.requests || 0" />
          </div>
          <div class="co-stat-tile">
            <AStatistic
              :title="t('api.statActive')"
              :value="api?.active || 0"
              :value-style="
                (api?.active || 0) > 0 ? { color: 'var(--co-accent)' } : undefined
              "
            />
          </div>
          <div class="co-stat-tile">
            <AStatistic :title="t('api.statCompleted')" :value="api?.completed || 0" />
          </div>
          <div class="co-stat-tile">
            <AStatistic
              :title="t('api.statFailed')"
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
          :aria-label="t('api.speedSectionAria')"
        >
          <div class="co-api-speed-summary">
            <div v-if="props.loading" class="co-api-speed-summary-top" aria-hidden="true">
              <div class="co-api-speed-summary-left">
                <PhSpeedometer
                  :size="16"
                  weight="duotone"
                  class="co-api-speed-icon"
                />
                <ColabOneSkeleton :width="56" :height="13" variant="text" tone="subtle" />
                <ColabOneSkeleton :width="96" :height="16" variant="text" tone="accent" :delay="65" />
              </div>
              <div class="co-api-speed-summary-right">
                <ColabOneSkeleton :width="72" :height="13" variant="text" tone="subtle" :delay="110" />
              </div>
            </div>
            <div v-else class="co-api-speed-summary-top co-sk-reveal">
              <div class="co-api-speed-summary-left">
                <PhSpeedometer
                  :size="16"
                  weight="duotone"
                  class="co-api-speed-icon"
                />
                <span class="co-api-speed-label">
                  {{ speedSummary.live ? t("api.speedLive") : t("api.speedRecent") }}
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
                  {{ t("api.speedAvg", { speed: formatTokenSpeed(speedSummary.avgTps) }) }}
                </span>
                <span class="co-api-speed-count co-tabular">
                  {{
                    recentRequests.length
                      ? t("api.speedRecentCount", { count: recentRequests.length })
                      : enabled
                        ? t("api.speedWaiting")
                        : t("api.speedEmpty")
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
                    {{ t("api.reqStreamingTag") }}
                  </ATag>
                  <time class="co-api-request-time co-mono">
                    {{ formatRequestClock(req.started_at) }}
                  </time>
                </div>

                <ColabOneTooltip
                  :title="
                    req.estimated
                      ? t('api.reqEstimatedTooltip')
                      : req.prompt_tokens_per_second
                        ? t('api.reqPrefillTooltip', {
                            speed: formatTokenSpeed(req.prompt_tokens_per_second),
                          })
                        : t('api.reqSpeedTooltip')
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
                    <template
                      v-if="req.tokens_per_second && req.tokens_per_second > 0"
                    >
                      {{
                        formatTokenSpeed(
                          req.tokens_per_second,
                          !!req.estimated,
                        )
                      }}
                    </template>
                    <span
                      v-else-if="req.state === 'running'"
                      class="co-api-tps-pending"
                    >
                      <ColabOneSkeleton
                        :width="76"
                        :height="14"
                        variant="pill"
                        tone="accent"
                        inline
                      />
                      <span class="co-sr-only">{{
                        t("api.reqWaitingFirstToken")
                      }}</span>
                    </span>
                    <template v-else>—</template>
                  </span>
                </ColabOneTooltip>
              </div>

              <div class="co-api-request-meta co-tabular">
                <span v-if="req.completion_tokens != null">
                  {{
                    t("api.reqOutputTokens", {
                      count: formatTokenCount(req.completion_tokens),
                    })
                  }}
                </span>
                <span v-if="req.prompt_tokens != null && req.prompt_tokens > 0">
                  {{
                    t("api.reqInputTokens", {
                      count: formatTokenCount(req.prompt_tokens),
                    })
                  }}
                </span>
                <span v-if="req.ttft_ms != null">
                  {{
                    t("api.reqTtft", {
                      duration: formatDurationMs(req.ttft_ms),
                    })
                  }}
                </span>
                <span>{{
                  t("api.reqDuration", {
                    duration: formatDurationMs(req.duration_ms),
                  })
                }}</span>
                <span
                  v-if="
                    req.prompt_tokens_per_second &&
                    req.prompt_tokens_per_second > 0
                  "
                >
                  {{
                    t("api.reqPrefill", {
                      speed: formatTokenSpeed(req.prompt_tokens_per_second),
                    })
                  }}
                </span>
                <span
                  v-if="req.state === 'failed' && req.error"
                  class="co-api-request-error"
                >
                  {{ tb(req.error) }}
                </span>
              </div>
            </article>
          </div>
          <p v-else-if="enabled" class="co-api-request-empty-hint">
            {{ t("api.reqReadyEmpty") }}
          </p>
        </section>

        <ADivider class="co-section-divider" />

        <div class="co-example-toolbar">
          <ASegmented
            size="small"
            :value="language"
            :options="languageOptions"
            :aria-label="t('api.codeLanguageAria')"
            @change="onLanguageChange"
          />
          <AButton size="small" @click="copy(code, 'code')">
            <template #icon>
              <PhCheck v-if="copiedField === 'code'" :size="14" />
              <PhCopy v-else :size="14" />
            </template>
            {{ copiedField === "code" ? t("common.copied") : t("api.copyCode") }}
          </AButton>
        </div>

        <p v-if="language === 'codex'" class="co-field-help co-example-help">
          {{ t("api.codexHintPrefix") }} <code>COLABONE_API_KEY</code>{{ t("api.codexHintMid") }}
          <code>~/.codex/config.toml</code>{{ t("api.codexHintMid2") }}
          <code>/compact</code>{{ t("api.codexHintSuffix") }}
        </p>
        <p v-else class="co-field-help co-example-help">
          {{ t("api.sdkHintPrefix") }} <code>COLABONE_API_KEY</code>{{ t("api.sdkHintSuffix") }}
        </p>

        <div
          v-if="props.loading"
          class="co-api-code-block co-api-code-skeleton"
          aria-hidden="true"
        >
          <ColabOneSkeleton width="58%" :height="13" variant="text" />
          <ColabOneSkeleton width="84%" :height="13" variant="text" tone="subtle" :delay="55" />
          <ColabOneSkeleton width="72%" :height="13" variant="text" tone="subtle" :delay="110" />
          <ColabOneSkeleton width="90%" :height="13" variant="text" :delay="165" />
          <ColabOneSkeleton width="46%" :height="13" variant="text" tone="subtle" :delay="220" />
        </div>
        <ColabOneCodeBlock
          v-else
          :code="code"
          :language="language"
          max-height="330px"
          class="co-api-code-block co-sk-reveal"
        />

        <p class="co-field-help">
          {{ t("api.compatSummary") }}
        </p>

        <details class="co-api-guide">
          <summary>{{ t("api.endpointsSummary") }}</summary>
          <ul>
            <li>
              <code>GET /v1/models</code> / <code>GET /v1/models/{id}</code>{{ t("api.endpointModelsDesc") }}
            </li>
            <li>
              <code>POST /v1/responses</code>{{ t("api.endpointResponsesDesc") }}
            </li>
            <li>
              <code>POST /v1/responses/compact</code>{{ t("api.endpointCompactDesc") }}
            </li>
            <li>
              <code>POST /v1/chat/completions</code>{{ t("api.endpointChatDesc") }}
            </li>
          </ul>
          <p class="co-field-help">
            {{ t("api.endpointFootNote") }}
          </p>
        </details>
      </ACard>
    </div>

    <ModelDrawer
      v-model:open="drawerOpen"
      :state="props.state"
      :busy="props.busy"
      :loading="props.loading"
      @device="emit('device')"
      @comfyui="emit('comfyui')"
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
  min-height: 21px;
  display: flex;
  align-items: center;
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

.co-api-code-sk {
  flex: 1;
  min-width: 140px;
  display: inline-flex;
  align-items: center;
  min-height: 20px;
}

.co-stat-tile-sk {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 4px;
  padding-bottom: 4px;
}

.co-api-tps-pending {
  display: inline-flex;
  align-items: center;
}

.co-api-code-skeleton {
  display: flex;
  flex-direction: column;
  gap: 9px;
  min-height: 122px;
  padding: 14px 16px;
  border: 1px solid var(--co-border-secondary);
  border-radius: 12px;
  background: var(--co-skeleton-card-bg);
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
