<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Checkbox as ACheckbox,
  Collapse as ACollapse,
  CollapsePanel as ACollapsePanel,
  InputNumber as AInputNumber,
  Segmented as ASegmented,
  Switch as ASwitch,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowSquareOut,
  PhArrowsClockwise,
  PhCopy,
  PhDesktop,
  PhGlobe,
  PhKey,
  PhLinkSimple,
  PhShareNetwork,
  PhSignOut,
  PhWifiHigh,
} from "@phosphor-icons/vue";
import ColabOnePrivacyInput from "./ColabOnePrivacyInput.vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOnePrivacyActive } from "./colabOnePrivacy";
import {
  COLABONE_WEB_CONSOLE_DEFAULT_PORT,
  COLABONE_WEB_CONSOLE_MAX_PORT,
  COLABONE_WEB_CONSOLE_MIN_PORT,
  colabOneBuildCloudflaredQuickCommand,
  colabOneBuildCloudflaredServiceTarget,
  colabOneIsValidWebPort,
  colabOneNormalizePublicOriginInput,
  type ColabOneWebConsoleBindMode,
  type ColabOneWebConsoleState,
  type ColabOneWebConsoleUpdateRequest,
} from "./colabOneWebConsole";
import { COLABONE_STORAGE_KEYS } from "./storage";
import { t, tb } from "./i18n";

defineOptions({
  name: "ColabOneWebConsoleCard",
});

const props = defineProps<{
  state: ColabOneWebConsoleState;
  loading: boolean;
  saving: boolean;
  passcodeBusy: boolean;
  passcodeResetSeq?: number;
  ticketBusy: boolean;
  revoking: boolean;
  workflowShareActiveCount?: number;
  loadSavedPasscode: () => Promise<string>;
}>();

const emit = defineEmits<{
  toggle: [req: ColabOneWebConsoleUpdateRequest];
  saveConfig: [req: ColabOneWebConsoleUpdateRequest];
  setPasscode: [passcode: string];
  copyPasscode: [];
  createTicket: [singleUse: boolean];
  revokeSessions: [];
  openWorkflowShares: [];
  copyText: [text: string, labelKey: string];
  openUrl: [url: string];
  error: [err: unknown];
}>();

function readSavedTicketSingleUse(): boolean {
  try {
    return (
      typeof window !== "undefined" &&
      window.localStorage?.getItem(
        COLABONE_STORAGE_KEYS.webConsoleTicketSingleUse,
      ) === "1"
    );
  } catch {
    return false;
  }
}

const expanded = ref(Boolean(props.state.enabled || props.state.last_error));
const guideActiveKey = ref<string[]>([]);
const passcodeDraft = ref("");
const passcodeInputRef = ref<{ hide?: () => void } | null>(null);
const ticketSingleUse = ref<boolean>(readSavedTicketSingleUse());

watch(ticketSingleUse, (singleUse) => {
  try {
    if (typeof window !== "undefined") {
      window.localStorage?.setItem(
        COLABONE_STORAGE_KEYS.webConsoleTicketSingleUse,
        singleUse ? "1" : "0",
      );
    }
  } catch {
    // Ignore storage quota / privacy mode errors.
  }
});

const draftBindMode = ref<ColabOneWebConsoleBindMode>(
  props.state.bind_mode === "lan" ? "lan" : "loopback",
);
const draftPort = ref<number | null>(
  colabOneIsValidWebPort(props.state.port)
    ? props.state.port
    : COLABONE_WEB_CONSOLE_DEFAULT_PORT,
);
const draftPublicOrigin = ref<string>(props.state.public_origin || "");

function onPublicOriginUpdate(value: string) {
  draftPublicOrigin.value = value;
}

watch(
  () => [props.state.enabled, Boolean(props.state.last_error)] as const,
  ([enabled, hasErr], [prevEnabled, prevHasErr]) => {
    if ((enabled && !prevEnabled) || (hasErr && !prevHasErr)) {
      expanded.value = true;
    }
  },
);

watch(
  () =>
    [
      props.state.bind_mode,
      props.state.port,
      props.state.public_origin,
    ] as const,
  ([bindMode, port, publicOrigin]) => {
    draftBindMode.value = bindMode === "lan" ? "lan" : "loopback";
    draftPort.value = colabOneIsValidWebPort(port)
      ? port
      : COLABONE_WEB_CONSOLE_DEFAULT_PORT;
    draftPublicOrigin.value = publicOrigin || "";
  },
);

watch(
  () => props.passcodeResetSeq,
  (next, prev) => {
    if (next !== undefined && next !== prev) {
      passcodeDraft.value = "";
      passcodeInputRef.value?.hide?.();
    }
  },
);

const currentBindMode = computed<ColabOneWebConsoleBindMode>(() =>
  props.state.bind_mode === "lan" ? "lan" : "loopback",
);

const currentPort = computed<number>(() =>
  colabOneIsValidWebPort(props.state.port)
    ? props.state.port
    : COLABONE_WEB_CONSOLE_DEFAULT_PORT,
);

const portValid = computed<boolean>(() =>
  colabOneIsValidWebPort(draftPort.value),
);

const normalizedDraftPublicOrigin = computed<string>(() =>
  colabOneNormalizePublicOriginInput(draftPublicOrigin.value),
);

const configDirty = computed<boolean>(
  () =>
    draftBindMode.value !== currentBindMode.value ||
    draftPort.value !== currentPort.value ||
    normalizedDraftPublicOrigin.value !==
      (props.state.public_origin || "").trim(),
);

const effectiveGuidePort = computed<number>(() =>
  portValid.value && typeof draftPort.value === "number"
    ? Math.trunc(draftPort.value)
    : currentPort.value,
);

const cloudflaredServiceTarget = computed<string>(() =>
  colabOneBuildCloudflaredServiceTarget(effectiveGuidePort.value),
);

const cloudflaredQuickCommand = computed<string>(() =>
  colabOneBuildCloudflaredQuickCommand(effectiveGuidePort.value),
);

const lanUrls = computed<string[]>(() =>
  props.state.bind_mode === "lan" && Array.isArray(props.state.lan_urls)
    ? props.state.lan_urls.filter((item) => Boolean(item && item.trim()))
    : [],
);

const bindModeOptions = computed(() => [
  {
    value: "loopback" as const,
    title: t("webConsole.bindLoopback"),
    payload: { label: t("webConsole.bindLoopback") },
  },
  {
    value: "lan" as const,
    title: t("webConsole.bindLan"),
    payload: { label: t("webConsole.bindLan") },
  },
]);

function onToggleEnabled(checked: unknown) {
  const nextEnabled = Boolean(checked);
  if (nextEnabled && !props.state.enabled && configDirty.value) {
    if (!portValid.value) {
      emit("error", t("webConsole.errPortRequired"));
      return;
    }
    emit("toggle", {
      enabled: true,
      bind_mode: draftBindMode.value,
      port: Math.trunc(draftPort.value as number),
      public_origin: normalizedDraftPublicOrigin.value,
    });
    return;
  }
  emit("toggle", { enabled: nextEnabled });
}

function onBindModeChange(value: string | number) {
  draftBindMode.value = value === "lan" ? "lan" : "loopback";
}

function onPortUpdate(value: string | number | null) {
  if (value === null || value === "") {
    draftPort.value = null;
    return;
  }
  const numeric = typeof value === "number" ? value : Number(value);
  if (Number.isFinite(numeric)) {
    draftPort.value = Math.trunc(numeric);
  } else {
    draftPort.value = null;
  }
}

function onSaveConfig() {
  if (
    !portValid.value ||
    typeof draftPort.value !== "number" ||
    !configDirty.value ||
    props.saving
  ) {
    return;
  }
  emit("saveConfig", {
    bind_mode: draftBindMode.value,
    port: Math.trunc(draftPort.value),
    public_origin: normalizedDraftPublicOrigin.value,
  });
}

function onSavePasscode() {
  const trimmed = passcodeDraft.value.trim();
  if (!trimmed || props.passcodeBusy || props.saving) {
    return;
  }
  emit("setPasscode", trimmed);
}

function onRotatePasscode() {
  if (props.passcodeBusy || props.saving) {
    return;
  }
  emit("setPasscode", "");
}

function copyAddress(url: string) {
  if (!url) return;
  emit("copyText", url, "webConsole.toastAddressCopied");
}

function copyQuickCommand() {
  emit(
    "copyText",
    cloudflaredQuickCommand.value,
    "webConsole.toastQuickCmdCopied",
  );
}

async function expandAndFocusPublicOrigin() {
  expanded.value = true;
  await nextTick();
  if (typeof document === "undefined") return;
  const prefersReducedMotion =
    typeof window !== "undefined" &&
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  const sectionHeading = document.getElementById("settings-sec-web-console");
  sectionHeading?.scrollIntoView({
    behavior: prefersReducedMotion ? "auto" : "smooth",
    block: "start",
  });
  const inputEl = document.getElementById(
    "settings-web-console-public-origin",
  ) as HTMLInputElement | null;
  if (inputEl && typeof inputEl.focus === "function") {
    inputEl.focus({ preventScroll: true });
  }
}

defineExpose({
  expandAndFocusPublicOrigin,
});
</script>

<template>
  <div
    class="co-settings-row co-webconsole-card"
    data-testid="settings-web-console-row"
    aria-labelledby="web-console-heading"
  >
    <div class="co-sr-main">
      <div class="co-sr-info">
        <div class="co-sr-title-line">
          <span class="co-sr-icon is-blue" aria-hidden="true">
            <PhGlobe :size="14" weight="duotone" />
          </span>
          <h3 id="web-console-heading" class="co-sr-title">
            {{ t("webConsole.title") }}
          </h3>
          <ColabOneSkeleton
            v-if="props.loading"
            :width="64"
            :height="20"
            variant="tag"
            tone="subtle"
            :delay="120"
            inline
          />
          <template v-else>
            <ATag v-if="props.state.last_error" color="error">
              {{ t("webConsole.statusError") }}
            </ATag>
            <ATag v-else-if="props.state.running" color="success">
              {{ t("webConsole.statusRunning") }}
            </ATag>
            <ATag v-else>
              {{ t("webConsole.statusStopped") }}
            </ATag>
            <ATag
              v-if="props.state.active_sessions > 0"
              color="blue"
              :bordered="false"
            >
              {{
                t("webConsole.sessionsTag", {
                  count: props.state.active_sessions,
                })
              }}
            </ATag>
            <ATag
              v-if="(props.workflowShareActiveCount || 0) > 0"
              color="purple"
              :bordered="false"
            >
              {{
                t("webConsole.workflowShareActiveTag", {
                  count: props.workflowShareActiveCount || 0,
                })
              }}
            </ATag>
          </template>
        </div>
        <p class="co-sr-desc">
          {{ t("webConsole.desc") }}
        </p>
      </div>

      <div class="co-sr-control">
        <ColabOneSkeleton
          v-if="props.loading"
          :width="92"
          :height="28"
          variant="button"
          tone="subtle"
          :delay="130"
        />
        <template v-else>
          <ASwitch
            :checked="props.state.enabled"
            :loading="props.saving"
            :aria-label="t('webConsole.enableSwitchAria')"
            data-testid="settings-web-console-switch"
            @update:checked="onToggleEnabled"
          />
          <AButton
            size="small"
            data-testid="settings-web-console-expand-btn"
            @click="expanded = !expanded"
          >
            {{
              expanded ? t("common.collapse") : t("webConsole.configureBtn")
            }}
          </AButton>
        </template>
      </div>
    </div>

    <AAlert
      v-if="!props.loading && props.state.last_error"
      type="error"
      show-icon
      class="co-webconsole-alert"
    >
      <template #message>
        <ColabOnePrivacyText :text="tb(props.state.last_error)" />
      </template>
    </AAlert>

    <div
      v-if="!props.loading && expanded"
      class="co-sr-subpanel co-webconsole-subpanel co-sk-reveal"
    >
      <!-- Access Addresses & Quick Actions (when running) -->
      <div v-if="props.state.running" class="co-webconsole-live-block">
        <div class="co-webconsole-address-list">
          <div class="co-webconsole-address-row">
            <span class="co-webconsole-address-label">
              {{ t("webConsole.localAddressLabel") }}
            </span>
            <code class="co-webconsole-address-value">
              {{ props.state.local_url }}
            </code>
            <AButton
              size="small"
              type="text"
              class="co-webconsole-copy-btn"
              @click="copyAddress(props.state.local_url)"
            >
              <template #icon>
                <PhCopy :size="13" />
              </template>
              {{ t("webConsole.copyAddressBtn") }}
            </AButton>
          </div>

          <div
            v-for="lanUrl in lanUrls"
            :key="lanUrl"
            class="co-webconsole-address-row"
          >
            <span class="co-webconsole-address-label">
              {{ t("webConsole.lanAddressesLabel") }}
            </span>
            <code class="co-webconsole-address-value">
              <ColabOnePrivacyText :text="lanUrl" kind="url" />
            </code>
            <AButton
              size="small"
              type="text"
              class="co-webconsole-copy-btn"
              @click="copyAddress(lanUrl)"
            >
              <template #icon>
                <PhCopy :size="13" />
              </template>
              {{ t("webConsole.copyAddressBtn") }}
            </AButton>
          </div>

          <div
            v-if="props.state.public_origin"
            class="co-webconsole-address-row"
          >
            <span class="co-webconsole-address-label">
              {{ t("webConsole.publicAddressLabel") }}
            </span>
            <code class="co-webconsole-address-value">
              <ColabOnePrivacyText
                :text="props.state.public_origin"
                kind="url"
              />
            </code>
            <AButton
              size="small"
              type="text"
              class="co-webconsole-copy-btn"
              @click="copyAddress(props.state.public_origin)"
            >
              <template #icon>
                <PhCopy :size="13" />
              </template>
              {{ t("webConsole.copyAddressBtn") }}
            </AButton>
          </div>
        </div>

        <div class="co-webconsole-quick-actions">
          <div class="co-webconsole-btn-row">
            <div class="co-webconsole-ticket-group">
              <AButton
                type="primary"
                size="small"
                :loading="props.ticketBusy"
                :disabled="props.saving"
                data-testid="settings-web-console-ticket-btn"
                @click="emit('createTicket', ticketSingleUse)"
              >
                <template #icon>
                  <PhLinkSimple :size="14" />
                </template>
                {{ t("webConsole.copyTicketBtn") }}
              </AButton>
              <ACheckbox
                v-model:checked="ticketSingleUse"
                :disabled="props.saving || props.ticketBusy"
                class="co-webconsole-single-use-check"
                data-testid="settings-web-console-ticket-single-use"
              >
                {{ t("webConsole.ticketSingleUseLabel") }}
              </ACheckbox>
            </div>
            <AButton
              size="small"
              :disabled="!props.state.has_passcode || props.passcodeBusy || props.saving"
              data-testid="settings-web-console-quick-copy-passcode-btn"
              @click="emit('copyPasscode')"
            >
              <template #icon>
                <PhKey :size="14" />
              </template>
              {{ t("webConsole.copyPasscodeBtn") }}
            </AButton>
            <AButton
              v-if="props.state.active_sessions > 0"
              size="small"
              danger
              :loading="props.revoking"
              :disabled="props.saving"
              data-testid="settings-web-console-revoke-btn"
              @click="emit('revokeSessions')"
            >
              <template #icon>
                <PhSignOut :size="14" />
              </template>
              {{ t("webConsole.revokeSessionsBtn") }}
            </AButton>
          </div>
          <span
            class="co-webconsole-hint"
            data-testid="settings-web-console-ticket-hint"
          >
            {{
              ticketSingleUse
                ? props.state.public_origin
                  ? t("webConsole.copyTicketPublicSingleUseHint")
                  : t("webConsole.copyTicketLocalSingleUseHint")
                : props.state.public_origin
                  ? t("webConsole.copyTicketPublicHint")
                  : t("webConsole.copyTicketLocalHint")
            }}
          </span>
        </div>
      </div>

      <!-- Connection Configuration Grid -->
      <div class="co-webconsole-section">
        <div class="co-webconsole-config-grid">
          <div class="co-webconsole-field co-webconsole-field-bind">
            <label class="co-webconsole-field-label">
              {{ t("webConsole.bindLabel") }}
            </label>
            <ASegmented
              :value="draftBindMode"
              :options="bindModeOptions"
              :disabled="props.saving"
              @change="onBindModeChange"
            >
              <template #label="{ value, title, payload }">
                <span class="co-webconsole-seg-item">
                  <PhDesktop v-if="value === 'loopback'" :size="14" />
                  <PhWifiHigh v-else :size="14" />
                  <span>{{ payload?.label ?? title ?? value }}</span>
                </span>
              </template>
            </ASegmented>
            <p class="co-webconsole-hint">
              {{ t("webConsole.bindHint") }}
            </p>
          </div>

          <div class="co-webconsole-field co-webconsole-field-port">
            <label
              for="settings-web-console-port"
              class="co-webconsole-field-label"
            >
              {{ t("webConsole.portLabel") }}
            </label>
            <AInputNumber
              id="settings-web-console-port"
              :value="draftPort ?? undefined"
              :min="COLABONE_WEB_CONSOLE_MIN_PORT"
              :max="COLABONE_WEB_CONSOLE_MAX_PORT"
              :step="1"
              :precision="0"
              :disabled="props.saving"
              :aria-label="t('webConsole.portAria')"
              class="co-webconsole-port-input"
              @update:value="onPortUpdate"
              @press-enter="onSaveConfig"
            />
          </div>

          <div class="co-webconsole-field co-webconsole-field-origin">
            <label
              for="settings-web-console-public-origin"
              class="co-webconsole-field-label"
            >
              {{ t("webConsole.publicOriginLabel") }}
            </label>
            <ColabOnePrivacyInput
              id="settings-web-console-public-origin"
              :value="draftPublicOrigin"
              :disabled="props.saving"
              :placeholder="t('webConsole.publicOriginPlaceholder')"
              @update:value="onPublicOriginUpdate"
              @press-enter="onSaveConfig"
            />
            <p class="co-webconsole-hint">
              {{ t("webConsole.publicOriginHint") }}
            </p>
          </div>
        </div>

        <div class="co-webconsole-config-footer">
          <AButton
            type="primary"
            size="small"
            :loading="props.saving"
            :disabled="!configDirty || !portValid"
            data-testid="settings-web-console-save-config-btn"
            @click="onSaveConfig"
          >
            {{ t("webConsole.saveConfigBtn") }}
          </AButton>
        </div>
      </div>

      <!-- Passcode Section -->
      <div class="co-webconsole-section">
        <div class="co-webconsole-field-head">
          <label
            for="settings-web-console-passcode"
            class="co-webconsole-field-label"
          >
            {{ t("webConsole.passcodeLabel") }}
          </label>
          <span class="co-webconsole-hint">
            {{ t("webConsole.passcodeHint") }}
          </span>
        </div>

        <div class="co-webconsole-passcode-row">
          <ColabOneSecretInput
            ref="passcodeInputRef"
            id="settings-web-console-passcode"
            v-model:value="passcodeDraft"
            :has-saved="props.state.has_passcode"
            :disabled="props.passcodeBusy || props.saving"
            autocomplete="new-password"
            :aria-label="t('webConsole.passcodeLabel')"
            :placeholder="t('webConsole.passcodePlaceholder')"
            :saved-placeholder="
              colabOnePrivacyActive
                ? t('webConsole.passcodeSavedPlaceholder')
                : props.state.passcode_preview ||
                  t('webConsole.passcodeSavedPlaceholder')
            "
            :load-saved="props.loadSavedPasscode"
            @error="emit('error', $event)"
          />
          <AButton
            type="primary"
            :loading="props.passcodeBusy"
            :disabled="!passcodeDraft.trim() || props.saving"
            data-testid="settings-web-console-save-passcode-btn"
            @click="onSavePasscode"
          >
            {{ t("webConsole.savePasscodeBtn") }}
          </AButton>
        </div>

        <div class="co-webconsole-btn-row">
          <AButton
            size="small"
            :disabled="!props.state.has_passcode || props.passcodeBusy || props.saving"
            data-testid="settings-web-console-copy-passcode-btn"
            @click="emit('copyPasscode')"
          >
            <template #icon>
              <PhCopy :size="13" />
            </template>
            {{ t("webConsole.copyPasscodeBtn") }}
          </AButton>
          <AButton
            size="small"
            :loading="props.passcodeBusy"
            :disabled="props.saving"
            data-testid="settings-web-console-rotate-passcode-btn"
            @click="onRotatePasscode"
          >
            <template #icon>
              <PhArrowsClockwise :size="13" />
            </template>
            {{ t("webConsole.rotatePasscodeBtn") }}
          </AButton>
        </div>
      </div>

      <!-- Workflow Share Management Entry -->
      <div class="co-webconsole-section co-webconsole-share-section">
        <div class="co-webconsole-share-row">
          <div class="co-webconsole-share-info">
            <div class="co-webconsole-share-title-line">
              <span class="co-webconsole-field-label">
                {{ t("webConsole.workflowShareSectionTitle") }}
              </span>
              <ATag
                v-if="(props.workflowShareActiveCount || 0) > 0"
                color="purple"
                :bordered="false"
              >
                {{
                  t("webConsole.workflowShareActiveTag", {
                    count: props.workflowShareActiveCount || 0,
                  })
                }}
              </ATag>
            </div>
            <p class="co-webconsole-hint">
              {{ t("webConsole.workflowShareSectionDesc") }}
            </p>
          </div>
          <AButton
            size="small"
            data-testid="settings-web-console-workflow-shares-btn"
            @click="emit('openWorkflowShares')"
          >
            <template #icon>
              <PhShareNetwork :size="14" />
            </template>
            {{ t("webConsole.workflowShareManageBtn") }}
          </AButton>
        </div>
      </div>

      <!-- Cloudflared Setup Guide -->
      <ACollapse
        v-model:active-key="guideActiveKey"
        ghost
        class="co-inline-collapse co-sr-collapse co-webconsole-guide"
      >
        <ACollapsePanel
          key="cloudflared"
          :header="t('webConsole.cloudflaredGuideTitle')"
        >
          <div class="co-webconsole-guide-body">
            <div class="co-webconsole-guide-step">
              <p class="co-webconsole-guide-text">
                {{
                  t("webConsole.cloudflaredGuideStep1", {
                    target: cloudflaredServiceTarget,
                  })
                }}
              </p>
              <div class="co-webconsole-code-line">
                <code>{{ cloudflaredServiceTarget }}</code>
                <AButton
                  size="small"
                  type="text"
                  class="co-webconsole-copy-btn"
                  @click="copyAddress(cloudflaredServiceTarget)"
                >
                  <template #icon>
                    <PhCopy :size="13" />
                  </template>
                  {{ t("webConsole.copyAddressBtn") }}
                </AButton>
              </div>
            </div>

            <p class="co-webconsole-guide-text">
              {{ t("webConsole.cloudflaredGuideStep2") }}
            </p>

            <div class="co-webconsole-guide-step">
              <span class="co-webconsole-field-label">
                {{ t("webConsole.cloudflaredGuideQuickCmdLabel") }}
              </span>
              <div class="co-webconsole-code-line">
                <code>{{ cloudflaredQuickCommand }}</code>
                <AButton
                  size="small"
                  type="text"
                  class="co-webconsole-copy-btn"
                  @click="copyQuickCommand"
                >
                  <template #icon>
                    <PhCopy :size="13" />
                  </template>
                  {{ t("webConsole.copyAddressBtn") }}
                </AButton>
              </div>
            </div>

            <p class="co-webconsole-hint">
              {{ t("webConsole.cloudflaredGuideTip") }}
            </p>

            <div>
              <AButton
                size="small"
                type="link"
                class="co-webconsole-dash-link"
                @click="emit('openUrl', 'https://one.dash.cloudflare.com/')"
              >
                <template #icon>
                  <PhArrowSquareOut :size="13" />
                </template>
                {{ t("webConsole.openCloudflareDash") }}
              </AButton>
            </div>
          </div>
        </ACollapsePanel>
      </ACollapse>
    </div>
  </div>
</template>

<style scoped>
.co-settings-row {
  padding: 15px 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-sr-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.co-sr-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.co-sr-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-sr-icon {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-sr-icon.is-blue {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-sr-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
}

.co-sr-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--co-muted);
}

.co-sr-control {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex-shrink: 0;
}

.co-sr-subpanel {
  padding: 12px 14px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-webconsole-alert {
  margin-top: 2px;
}

.co-webconsole-live-block {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
}

.co-webconsole-address-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-webconsole-address-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-webconsole-address-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-muted);
  min-width: 72px;
}

.co-webconsole-address-value {
  font-family: var(--co-font-mono);
  font-size: 12px;
  padding: 2px 7px;
  border-radius: 6px;
  background: var(--co-track);
  color: var(--co-text);
  word-break: break-all;
}

.co-webconsole-copy-btn {
  padding: 0 6px;
  height: 24px;
  font-size: 12px;
}

.co-webconsole-quick-actions {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 2px;
}

.co-webconsole-btn-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
}

.co-webconsole-ticket-group {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.co-webconsole-single-use-check {
  font-size: 12.5px;
  color: var(--co-secondary-text);
  user-select: none;
}

.co-webconsole-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 10px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-webconsole-subpanel > .co-webconsole-section:first-child {
  padding-top: 0;
  border-top: 0;
}

.co-webconsole-config-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 128px;
  gap: 12px;
  align-items: start;
}

.co-webconsole-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.co-webconsole-field-origin {
  grid-column: 1 / -1;
}

.co-webconsole-field-head {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-webconsole-field-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-text);
}

.co-webconsole-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-webconsole-seg-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-webconsole-port-input {
  width: 100%;
}

.co-webconsole-port-input :deep(.ant-input-number-input) {
  font-variant-numeric: tabular-nums;
}

.co-webconsole-config-footer {
  display: flex;
  justify-content: flex-start;
}

.co-webconsole-passcode-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-webconsole-passcode-row > :first-child {
  flex: 1;
  min-width: 0;
}

.co-webconsole-guide {
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-webconsole-guide :deep(.ant-collapse-header) {
  padding: 2px 0 !important;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-webconsole-guide :deep(.ant-collapse-content-box) {
  padding: 8px 0 2px !important;
}

.co-webconsole-guide-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-webconsole-guide-step {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.co-webconsole-guide-text {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--co-secondary-text);
}

.co-webconsole-code-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
}

.co-webconsole-code-line code {
  font-family: var(--co-font-mono);
  font-size: 12px;
  color: var(--co-text);
  word-break: break-all;
}

.co-webconsole-dash-link {
  padding-left: 0;
  padding-right: 0;
}

.co-webconsole-share-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.co-webconsole-share-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  flex: 1 1 220px;
}

.co-webconsole-share-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

@media (max-width: 720px) {
  .co-sr-main {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .co-sr-control {
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .co-webconsole-config-grid {
    grid-template-columns: 1fr;
  }

  .co-webconsole-passcode-row {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
