<script lang="ts">
import type { ColabOneOperation } from "./types";

export interface ColabOneDriveExportResult {
  id: string;
  filename: string;
  drivePath: string;
  remoteFolder: string;
  alreadyExists?: boolean;
  size?: number;
}

export function formatColabOneDrivePath(raw: string): string {
  return String(raw || "").trim().replace(/\s*\/\s*/g, " / ");
}

export function isColabOneDriveConnectOperation(
  op: ColabOneOperation | null | undefined,
): boolean {
  if (!op) return false;
  if (op.kind === "drive_connect") return true;
  if (op.kind && op.kind !== "drive_connect") return false;
  return /挂载\s*Google\s*云盘|DriveFS/i.test(op.title || "");
}

export type ColabOneDriveConnectStage =
  | ""
  | "checking"
  | "waiting_auth"
  | "mounting"
  | "initializing";

export function getColabOneDriveConnectStage(
  op: ColabOneOperation | null | undefined,
): ColabOneDriveConnectStage {
  if (!isColabOneDriveConnectOperation(op)) return "";
  const raw = String(op?.stage || "").trim();
  if (
    raw === "checking" ||
    raw === "waiting_auth" ||
    raw === "mounting" ||
    raw === "initializing"
  ) {
    return raw;
  }
  const title = String(op?.title || "");
  if (/授权已确认|初始化\s*rclone/i.test(title)) return "mounting";
  if (/初始化\s*ColabOne_Drive/i.test(title)) return "initializing";
  return "checking";
}
</script>

<script setup lang="ts">
import { computed } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Checkbox as ACheckbox,
  Modal as AModal,
  Space as ASpace,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowSquareOut,
  PhCaretRight,
  PhCloud,
  PhCloudArrowUp,
  PhCloudCheck,
  PhFolderOpen,
  PhGearSix,
  PhImage,
  PhSparkle,
  PhVideoCamera,
  PhWaveform,
} from "@phosphor-icons/vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { buildColabOnePrivacyContext } from "./colabOnePrivacy";
import { formatOutputSize } from "./comfyui";
import { t, tb } from "./i18n";
import type { ColabOneSnapshot, WorkflowOutput } from "./types";

const GOOGLE_DRIVE_HOME_URL = "https://drive.google.com/drive/my-drive";
const GCP_CONSOLE_URL = "https://console.cloud.google.com/";

const props = withDefaults(
  defineProps<{
    open: boolean;
    state: ColabOneSnapshot;
    purpose?: "config" | "export" | "auto_release";
    targetOutput?: WorkflowOutput | null;
    exporting?: boolean;
    exportResult?: ColabOneDriveExportResult | null;
    exportError?: string;
    connecting?: boolean;
    confirmingAuth?: boolean;
    checkingHeadless?: boolean;
    savingAutoDrive?: boolean;
    armingAutoRelease?: boolean;
    needsRemoteUpgrade?: boolean;
  }>(),
  {
    purpose: "config",
    targetOutput: null,
    exporting: false,
    exportResult: null,
    exportError: "",
    connecting: false,
    confirmingAuth: false,
    checkingHeadless: false,
    savingAutoDrive: false,
    armingAutoRelease: false,
    needsRemoteUpgrade: false,
  },
);

const isAutoReleasePurpose = computed(
  () => props.purpose === "auto_release" && !props.targetOutput,
);

const emit = defineEmits<{
  "update:open": [boolean];
  connect: [];
  "confirm-auth": [];
  "cancel-auth": [];
  "setup-headless": [];
  "export-now": [];
  "arm-auto-release": [];
  upgrade: [];
  "open-settings": [];
  "toggle-auto-drive": [boolean];
  "open-url": [string];
}>();

const privacyContext = computed(() => buildColabOnePrivacyContext(props.state));
const gpuReady = computed(() => props.state.runtime.state === "ready");
const driveConnected = computed(() => Boolean(props.state.setup.drive));
const driveConnectOpActive = computed(() =>
  isColabOneDriveConnectOperation(props.state.operation),
);
const driveConnectStage = computed(() =>
  getColabOneDriveConnectStage(props.state.operation),
);
const driveConnectMode = computed(() =>
  driveConnectOpActive.value ? String(props.state.operation?.mode || "").trim() : "",
);
const driveAuthInteractiveConfirmed = computed(
  () =>
    driveConnectOpActive.value &&
    Boolean(props.state.operation?.interactive),
);
const driveCancelling = computed(
  () =>
    driveConnectOpActive.value &&
    Boolean(props.state.operation?.cancelling),
);
const canCancelDriveMount = computed(
  () =>
    driveConnectOpActive.value &&
    !driveCancelling.value &&
    driveConnectStage.value !== "initializing" &&
    driveConnectMode.value !== "rclone",
);
const waitingBrowserAuth = computed(
  () =>
    gpuReady.value &&
    !driveConnected.value &&
    !driveCancelling.value &&
    Boolean(props.state.auth_url) &&
    driveConnectOpActive.value,
);
const mountingDriveAfterAuth = computed(
  () =>
    !driveConnected.value &&
    !waitingBrowserAuth.value &&
    !driveCancelling.value &&
    driveConnectOpActive.value &&
    (driveConnectStage.value === "mounting" ||
      driveConnectStage.value === "initializing"),
);
const isConnectingDrive = computed(
  () =>
    !driveConnected.value &&
    !waitingBrowserAuth.value &&
    !mountingDriveAfterAuth.value &&
    !driveCancelling.value &&
    (props.connecting || driveConnectOpActive.value),
);
const canClose = computed(() => !props.exporting);

const remoteFolderDisplay = computed(() => {
  const raw = props.exportResult?.remoteFolder
    ? props.exportResult.remoteFolder
    : `${props.state.runtime.drive?.remote_folder || "ColabOne_Drive"}/outputs`;
  return formatColabOneDrivePath(raw);
});

const savedPathDisplay = computed(() => {
  if (props.exportResult?.drivePath) {
    return formatColabOneDrivePath(props.exportResult.drivePath);
  }
  return `My Drive / ${remoteFolderDisplay.value}`;
});

const statusBadge = computed(() => {
  if (props.exportResult) {
    return { color: "success" as const, label: t("comfy.driveModalTagExported") };
  }
  if (props.exporting) {
    return { color: "processing" as const, label: t("comfy.driveModalTagExporting") };
  }
  if (driveCancelling.value) {
    return { color: "default" as const, label: t("comfy.driveModalTagCancelling") };
  }
  if (waitingBrowserAuth.value) {
    return { color: "warning" as const, label: t("comfy.driveModalTagWaitingAuth") };
  }
  if (mountingDriveAfterAuth.value) {
    return {
      color: "processing" as const,
      label: driveAuthInteractiveConfirmed.value
        ? t("comfy.driveModalTagMounting")
        : t("comfy.driveModalTagMountingDirect"),
    };
  }
  if (isConnectingDrive.value) {
    return { color: "processing" as const, label: t("comfy.driveModalTagConnecting") };
  }
  if (driveConnected.value) {
    return { color: "success" as const, label: t("comfy.driveModalTagConnected") };
  }
  if (props.state.setup.drive_headless) {
    return { color: "processing" as const, label: t("comfy.driveModalTagHeadlessReady") };
  }
  return { color: "default" as const, label: t("comfy.driveModalTagNotConnected") };
});

const mountingCardTitle = computed(() => {
  if (driveConnectStage.value === "initializing") {
    return t("comfy.driveModalInitializingTitle");
  }
  if (driveConnectMode.value === "rclone" && props.state.operation?.title) {
    return tb(props.state.operation.title);
  }
  if (driveAuthInteractiveConfirmed.value) {
    return t("comfy.driveModalMountingTitle");
  }
  return t("comfy.driveModalMountingDirectTitle");
});

const mountingCardDesc = computed(() => {
  if (driveConnectMode.value === "rclone") {
    return t("comfy.driveModalMountingRcloneDesc");
  }
  if (driveAuthInteractiveConfirmed.value) {
    if (isAutoReleasePurpose.value) {
      return t("comfy.driveModalMountingAutoReleaseDesc");
    }
    return props.targetOutput
      ? t("comfy.driveModalMountingExportDesc")
      : t("comfy.driveModalMountingDesc");
  }
  if (isAutoReleasePurpose.value) {
    return t("comfy.driveModalMountingDirectAutoReleaseDesc");
  }
  return props.targetOutput
    ? t("comfy.driveModalMountingDirectExportDesc")
    : t("comfy.driveModalMountingDirectDesc");
});

function formatFreeSpace(bytes?: number | null): string {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return "";
  }
  if (bytes >= 1024 ** 4) {
    return `${(bytes / 1024 ** 4).toFixed(2)} TB`;
  }
  if (bytes >= 1024 ** 3) {
    return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  }
  return formatOutputSize(bytes);
}

const driveFreeSpaceText = computed(() =>
  formatFreeSpace(props.state.runtime.drive?.free_bytes),
);

function requestClose() {
  if (!canClose.value) return;
  emit("update:open", false);
}

function onAutoDriveToggle(event: { target?: { checked?: boolean } }) {
  emit("toggle-auto-drive", Boolean(event?.target?.checked));
}
</script>

<template>
  <AModal
    :open="open"
    :width="540"
    :centered="true"
    :closable="canClose"
    :mask-closable="canClose"
    :keyboard="canClose"
    :z-index="1050"
    wrap-class-name="co-drive-export-modal-wrap"
    @cancel="requestClose"
  >
    <template #title>
      <div class="co-drive-modal-title">
        <span
          class="co-drive-modal-icon"
          :class="{ 'is-connected': driveConnected || !!exportResult }"
          aria-hidden="true"
        >
          <PhCloudCheck v-if="driveConnected || exportResult" :size="18" weight="duotone" />
          <PhCloudArrowUp v-else :size="18" weight="duotone" />
        </span>
        <span class="co-drive-modal-title-text">
          {{
            targetOutput
              ? t("comfy.driveModalTitleExport")
              : isAutoReleasePurpose
                ? t("comfy.driveModalTitleAutoRelease")
                : t("comfy.driveModalTitleConfig")
          }}
        </span>
        <ATag :color="statusBadge.color" class="co-drive-modal-status-tag">
          {{ statusBadge.label }}
        </ATag>
      </div>
    </template>

    <div class="co-drive-modal-body" data-testid="drive-export-modal">
      <!-- Target Artwork Summary Card -->
      <div v-if="targetOutput" class="co-drive-target-card">
        <div class="co-drive-target-thumb" aria-hidden="true">
          <PhVideoCamera
            v-if="targetOutput.kind === 'video'"
            :size="18"
            weight="duotone"
          />
          <PhWaveform
            v-else-if="targetOutput.kind === 'audio'"
            :size="18"
            weight="duotone"
          />
          <PhImage v-else :size="18" weight="duotone" />
        </div>
        <div class="co-drive-target-copy">
          <div class="co-drive-target-head">
            <strong class="co-drive-target-name">
              {{
                targetOutput.workflow_name
                  ? tb(targetOutput.workflow_name)
                  : t("comfy.defaultWorkflowName")
              }}
            </strong>
            <ATag class="co-drive-mono-tag">
              {{ formatOutputSize(targetOutput.size) }}
            </ATag>
          </div>
          <ColabOneTooltip :title="targetOutput.filename">
            <span class="co-drive-target-filename">
              {{ targetOutput.filename }}
            </span>
          </ColabOneTooltip>
          <div class="co-drive-target-dest">
            <PhFolderOpen :size="13" aria-hidden="true" />
            <span>{{ t("comfy.driveModalDestPrefix") }}</span>
            <code>My Drive / {{ remoteFolderDisplay }}</code>
          </div>
        </div>
      </div>

      <!-- STATE 1: Export Completed -->
      <div v-if="exportResult" class="co-drive-state-card is-success" role="status">
        <div class="co-drive-state-head">
          <PhCloudCheck :size="20" weight="fill" class="co-drive-state-icon" />
          <strong>
            {{
              exportResult.alreadyExists
                ? t("comfy.driveExportAlreadySavedTitle")
                : t("comfy.driveExportSuccessTitle")
            }}
          </strong>
        </div>
        <p class="co-drive-state-desc">
          {{ t("comfy.driveExportSuccessDesc") }}
        </p>
        <div class="co-drive-saved-path">
          <code>{{ savedPathDisplay }}</code>
        </div>
      </div>

      <!-- STATE 2: Exporting in Progress -->
      <div v-else-if="exporting" class="co-drive-state-card is-active" role="status" aria-busy="true">
        <div class="co-drive-state-head">
          <ASpin size="small" />
          <strong>{{ t("comfy.driveExportingTitle") }}</strong>
        </div>
        <p class="co-drive-state-desc">
          {{ t("comfy.driveExportingDesc") }}
        </p>
      </div>

      <!-- STATE 3: Waiting for Browser OAuth / DriveFS Consent -->
      <div v-else-if="waitingBrowserAuth" class="co-drive-state-card is-warning" role="status">
        <div class="co-drive-state-head">
          <ASpin size="small" />
          <strong>{{ t("comfy.driveModalWaitingAuthTitle") }}</strong>
        </div>
        <ol class="co-drive-auth-steps">
          <li>
            <span>{{ t("comfy.driveModalAuthStep1") }}</span>
            <div v-if="state.account.email" class="co-drive-account-row">
              <span class="co-drive-account-chip">
                <ColabOnePrivacyText
                  :text="state.account.email"
                  kind="email"
                  :context="privacyContext"
                />
              </span>
            </div>
          </li>
          <li>{{ t("comfy.driveModalAuthStep2") }}</li>
          <li>
            {{
              targetOutput
                ? t("comfy.driveModalAuthStep3Export")
                : isAutoReleasePurpose
                  ? t("comfy.driveModalAuthStep3AutoRelease")
                  : t("comfy.driveModalAuthStep3Connect")
            }}
          </li>
        </ol>
        <AAlert
          v-if="state.drive_auth_hint"
          type="warning"
          show-icon
          class="co-drive-inline-alert"
        >
          <template #message>
            <ColabOnePrivacyText
              :text="tb(state.drive_auth_hint)"
              :context="privacyContext"
            />
          </template>
        </AAlert>
      </div>

      <!-- STATE 3a: Cancelling Drive Mount -->
      <div
        v-else-if="driveCancelling"
        class="co-drive-state-card"
        role="status"
        aria-busy="true"
      >
        <div class="co-drive-state-head">
          <ASpin size="small" />
          <strong>{{ t("comfy.driveModalCancellingTitle") }}</strong>
        </div>
      </div>

      <!-- STATE 3b: Mounting DriveFS / rclone / Initializing Workspace -->
      <div
        v-else-if="mountingDriveAfterAuth"
        class="co-drive-state-card is-active"
        role="status"
        aria-busy="true"
      >
        <div class="co-drive-state-head">
          <ASpin size="small" />
          <strong>{{ mountingCardTitle }}</strong>
        </div>
        <p class="co-drive-state-desc">
          {{ mountingCardDesc }}
        </p>
      </div>

      <!-- STATE 4: Connecting Drive -->
      <div v-else-if="isConnectingDrive" class="co-drive-state-card is-active" role="status" aria-busy="true">
        <div class="co-drive-state-head">
          <ASpin size="small" />
          <strong>
            {{
              state.operation?.title
                ? tb(state.operation.title)
                : t("comfy.driveModalConnectingTitle")
            }}
          </strong>
        </div>
        <p class="co-drive-state-desc">
          {{ t("comfy.driveModalConnectingDesc") }}
        </p>
      </div>

      <!-- STATE 5: Drive Connected, Remote Needs Hot-Update -->
      <div v-else-if="driveConnected && needsRemoteUpgrade" class="co-drive-state-card">
        <AAlert
          type="info"
          show-icon
          :message="t('comfy.driveModalUpgradeTitle')"
          :description="t('comfy.driveModalUpgradeDesc')"
        />
      </div>

      <!-- STATE 6: Drive Connected & Ready -->
      <div v-else-if="driveConnected" class="co-drive-state-card is-success">
        <div class="co-drive-state-head">
          <PhCloudCheck :size="20" weight="fill" class="co-drive-state-icon" />
          <strong>{{ t("comfy.driveModalConnectedTitle") }}</strong>
        </div>
        <p class="co-drive-state-desc">
          {{
            targetOutput
              ? t("comfy.driveModalConnectedExportDesc", {
                  folder: remoteFolderDisplay,
                  free: driveFreeSpaceText
                    ? t("device.driveFreeSegment", { size: driveFreeSpaceText })
                    : "",
                })
              : isAutoReleasePurpose
                ? t("comfy.driveModalConnectedAutoReleaseDesc", {
                    folder: state.runtime.drive?.remote_folder || "ColabOne_Drive",
                    free: driveFreeSpaceText
                      ? t("device.driveFreeSegment", { size: driveFreeSpaceText })
                      : "",
                  })
                : t("comfy.driveModalConnectedOnlyDesc", {
                    folder: state.runtime.drive?.remote_folder || "ColabOne_Drive",
                    free: driveFreeSpaceText
                      ? t("device.driveFreeSegment", { size: driveFreeSpaceText })
                      : "",
                  })
          }}
        </p>
        <AAlert
          v-if="exportError"
          type="error"
          show-icon
          class="co-drive-inline-alert"
        >
          <template #message>
            <ColabOnePrivacyText
              :text="tb(exportError)"
              :context="privacyContext"
            />
          </template>
        </AAlert>
      </div>

      <!-- STATE 7: Drive Not Connected Yet -->
      <template v-else>
        <AAlert
          v-if="!state.setup.colab"
          type="warning"
          show-icon
          :message="t('comfy.driveModalNeedLoginTitle')"
          :description="t('comfy.driveModalNeedLoginDesc')"
        />
        <AAlert
          v-else-if="!gpuReady"
          type="info"
          show-icon
          :message="t('comfy.driveModalNeedGpuTitle')"
          :description="t('comfy.driveModalNeedGpuDesc')"
        />
        <template v-else>
          <div class="co-drive-setup-card">
            <div class="co-drive-setup-copy">
              <strong>{{
                isAutoReleasePurpose
                  ? t("comfy.driveModalSetupAutoReleaseHeadline")
                  : t("comfy.driveModalSetupHeadline")
              }}</strong>
              <p>
                {{
                  isAutoReleasePurpose
                    ? state.setup.drive_headless
                      ? t("comfy.driveModalSetupHeadlessAutoReleaseDesc")
                      : t("comfy.driveModalSetupAuthAutoReleaseDesc")
                    : state.setup.drive_headless
                      ? targetOutput
                        ? t("comfy.driveModalSetupHeadlessExportDesc")
                        : t("comfy.driveModalSetupHeadlessOnlyDesc")
                      : targetOutput
                        ? t("comfy.driveModalSetupAuthExportDesc")
                        : t("comfy.driveModalSetupAuthOnlyDesc")
                }}
              </p>
            </div>
            <ul class="co-drive-benefits">
              <li>{{
                isAutoReleasePurpose
                  ? t("comfy.driveModalBenefitAutoRelease")
                  : t("comfy.driveModalBenefitSpeed")
              }}</li>
              <li>{{ t("comfy.driveModalBenefitPersist") }}</li>
              <li v-if="!targetOutput">
                {{ t("comfy.driveModalBenefitPath") }}
                <code>My Drive / {{ remoteFolderDisplay }}</code>
              </li>
            </ul>
          </div>

          <AAlert
            v-if="exportError || state.runtime.drive?.error"
            type="error"
            show-icon
            class="co-drive-inline-alert"
          >
            <template #message>
              <ColabOnePrivacyText
                :text="tb(exportError || state.runtime.drive?.error || '')"
                :context="privacyContext"
              />
            </template>
          </AAlert>

          <div class="co-drive-autodrive-row">
            <ACheckbox
              :checked="Boolean(state.settings.auto_drive)"
              :disabled="savingAutoDrive"
              @change="onAutoDriveToggle"
            >
              <span>{{ t("comfy.driveModalAutoMountLabel") }}</span>
            </ACheckbox>
          </div>

          <details
            v-if="state.setup.drive_authorized && !state.setup.drive_headless"
            class="co-drive-headless-disclosure"
          >
            <summary>
              <span class="co-drive-headless-summary-main">
                <PhSparkle :size="14" aria-hidden="true" />
                <span>{{ t("settings.driveHeadlessTipTitle") }}</span>
              </span>
              <PhCaretRight :size="14" class="co-drive-headless-caret" aria-hidden="true" />
            </summary>
            <div class="co-drive-headless-body">
              <p>{{ t("settings.driveHeadlessTipDesc") }}</p>
              <ASpace :size="8" wrap>
                <AButton
                  size="small"
                  @click="emit('open-url', GCP_CONSOLE_URL)"
                >
                  <template #icon><PhArrowSquareOut :size="14" /></template>
                  {{ t("settings.driveOpenGcpConsole") }}
                </AButton>
                <AButton
                  size="small"
                  type="primary"
                  ghost
                  :loading="checkingHeadless"
                  :disabled="Boolean(state.operation)"
                  @click="emit('setup-headless')"
                >
                  {{ t("settings.driveCheckHeadless") }}
                </AButton>
              </ASpace>
            </div>
          </details>
        </template>
      </template>
    </div>

    <template #footer>
      <div class="co-drive-modal-footer">
        <template v-if="exportResult">
          <AButton @click="emit('open-url', GOOGLE_DRIVE_HOME_URL)">
            <template #icon><PhArrowSquareOut :size="14" /></template>
            {{ t("comfy.driveOpenGoogleDrive") }}
          </AButton>
          <AButton type="primary" @click="requestClose">
            {{ t("common.done") }}
          </AButton>
        </template>

        <template v-else-if="exporting">
          <AButton disabled>
            {{ t("common.close") }}
          </AButton>
          <AButton type="primary" loading>
            {{ t("comfy.driveExportingBtn") }}
          </AButton>
        </template>

        <template v-else-if="waitingBrowserAuth">
          <AButton @click="emit('cancel-auth')">
            {{ t("device.driveCancelMount") }}
          </AButton>
          <AButton
            v-if="state.auth_url"
            @click="emit('open-url', state.auth_url)"
          >
            <template #icon><PhArrowSquareOut :size="14" /></template>
            {{ t("device.driveReopenAuth") }}
          </AButton>
          <AButton
            type="primary"
            :loading="confirmingAuth"
            @click="emit('confirm-auth')"
          >
            {{ t("device.driveConfirmAuthorized") }}
          </AButton>
        </template>

        <template v-else-if="driveCancelling">
          <AButton @click="requestClose">
            {{ t("common.close") }}
          </AButton>
          <AButton loading disabled>
            {{ t("comfy.driveModalCancellingBtn") }}
          </AButton>
        </template>

        <template v-else-if="mountingDriveAfterAuth">
          <AButton v-if="canCancelDriveMount" @click="emit('cancel-auth')">
            {{ t("device.driveCancelMount") }}
          </AButton>
          <AButton @click="requestClose">
            {{ t("comfy.driveModalBackgroundBtn") }}
          </AButton>
          <AButton type="primary" loading>
            {{ t("comfy.driveModalMountingBtn") }}
          </AButton>
        </template>

        <template v-else-if="isConnectingDrive">
          <AButton @click="driveConnectOpActive ? emit('cancel-auth') : requestClose()">
            {{ t("common.cancel") }}
          </AButton>
          <AButton type="primary" loading>
            {{ t("comfy.driveModalConnectingBtn") }}
          </AButton>
        </template>

        <template v-else-if="driveConnected && needsRemoteUpgrade">
          <AButton @click="requestClose">
            {{ t("common.close") }}
          </AButton>
          <AButton
            type="primary"
            :disabled="Boolean(state.operation)"
            @click="emit('upgrade')"
          >
            {{ t("comfy.driveModalUpgradeBtn") }}
          </AButton>
        </template>

        <template v-else-if="driveConnected">
          <AButton @click="emit('open-url', GOOGLE_DRIVE_HOME_URL)">
            <template #icon><PhArrowSquareOut :size="14" /></template>
            {{ t("comfy.driveOpenGoogleDrive") }}
          </AButton>
          <AButton
            v-if="targetOutput"
            type="primary"
            @click="emit('export-now')"
          >
            <template #icon><PhCloudArrowUp :size="15" /></template>
            {{
              exportError
                ? t("comfy.driveRetryExportBtn")
                : t("comfy.driveExportNowBtn")
            }}
          </AButton>
          <AButton
            v-else-if="isAutoReleasePurpose"
            type="primary"
            :loading="armingAutoRelease"
            @click="emit('arm-auto-release')"
          >
            <template #icon><PhCloudCheck :size="15" /></template>
            {{
              exportError
                ? t("comfy.driveRetryAutoReleaseBtn")
                : t("comfy.driveAutoReleaseEnableNowBtn")
            }}
          </AButton>
          <AButton v-else type="primary" @click="requestClose">
            {{ t("common.done") }}
          </AButton>
        </template>

        <template v-else-if="!state.setup.colab">
          <AButton @click="requestClose">
            {{ t("common.cancel") }}
          </AButton>
          <AButton type="primary" @click="emit('open-settings')">
            <template #icon><PhGearSix :size="15" /></template>
            {{ t("comfy.driveModalGoSettings") }}
          </AButton>
        </template>

        <template v-else-if="!gpuReady">
          <AButton type="primary" @click="requestClose">
            {{ t("common.close") }}
          </AButton>
        </template>

        <template v-else>
          <AButton @click="requestClose">
            {{ t("common.cancel") }}
          </AButton>
          <AButton
            type="primary"
            :disabled="Boolean(state.operation)"
            @click="emit('connect')"
          >
            <template #icon><PhCloud :size="15" /></template>
            {{
              isAutoReleasePurpose
                ? state.setup.drive_headless
                  ? t("comfy.driveModalConnectAndAutoReleaseBtn")
                  : t("comfy.driveModalAuthAndAutoReleaseBtn")
                : state.setup.drive_headless
                  ? targetOutput
                    ? t("comfy.driveModalConnectAndExportBtn")
                    : t("comfy.driveModalConnectBtn")
                  : targetOutput
                    ? t("comfy.driveModalAuthAndExportBtn")
                    : t("comfy.driveModalAuthBtn")
            }}
          </AButton>
        </template>
      </div>
    </template>
  </AModal>
</template>

<style scoped>
.co-drive-modal-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
  flex-wrap: wrap;
}

.co-drive-modal-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-drive-modal-icon.is-connected {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

:root[data-theme="dark"] .co-drive-modal-icon:not(.is-connected) {
  color: var(--co-accent-strong);
}

.co-drive-modal-title-text {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-drive-modal-status-tag {
  margin-inline-end: 0;
  font-size: 11.5px;
}

.co-drive-modal-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-drive-target-card {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-drive-target-thumb {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-drive-target-thumb {
  color: var(--co-accent-strong);
}

.co-drive-target-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.co-drive-target-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-drive-target-name {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-drive-mono-tag {
  margin-inline-end: 0;
  font-family: var(--co-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 11.5px;
  flex-shrink: 0;
}

.co-drive-target-filename {
  display: block;
  font-family: var(--co-font-mono);
  font-size: 12px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-drive-target-dest {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  margin-top: 3px;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-drive-target-dest code,
.co-drive-benefits code,
.co-drive-saved-path code {
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-family: var(--co-font-mono);
  font-size: 11.5px;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-drive-setup-card,
.co-drive-state-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-drive-state-card.is-success {
  background: var(--co-success-bg);
  border-color: var(--co-success-border);
}

.co-drive-state-card.is-warning {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-drive-state-card.is-active {
  background: var(--co-selected);
  border-color: rgba(59, 130, 246, 0.28);
}

.co-drive-state-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: var(--co-text);
}

.co-drive-state-card.is-success .co-drive-state-icon {
  color: var(--co-success-text);
  flex-shrink: 0;
}

.co-drive-state-desc,
.co-drive-setup-copy p {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-drive-setup-copy {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-drive-setup-copy strong {
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
}

.co-drive-benefits {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--co-secondary-text);
}

.co-drive-auth-steps {
  margin: 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-text);
}

.co-drive-account-row {
  margin-top: 4px;
}

.co-drive-account-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: 9999px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-drive-saved-path {
  display: flex;
  align-items: center;
}

.co-drive-saved-path code {
  padding: 5px 10px;
  font-size: 12px;
  width: 100%;
}

.co-drive-inline-alert {
  border-radius: 8px;
}

.co-drive-autodrive-row {
  padding: 2px 2px 0;
}

.co-drive-autodrive-row :deep(.ant-checkbox-wrapper) {
  color: var(--co-text);
  font-size: 13px;
}

.co-drive-headless-disclosure {
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12.5px;
  color: var(--co-secondary-text);
}

.co-drive-headless-disclosure summary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  cursor: pointer;
  font-weight: 550;
  color: var(--co-text);
  list-style: none;
}

.co-drive-headless-summary-main {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-drive-headless-caret {
  color: var(--co-muted);
  flex-shrink: 0;
  transition: transform 0.16s ease;
}

.co-drive-headless-disclosure[open] .co-drive-headless-caret {
  transform: rotate(90deg);
}

.co-drive-headless-disclosure summary::-webkit-details-marker {
  display: none;
}

.co-drive-headless-body {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-drive-headless-body p {
  margin: 0;
  line-height: 1.6;
  color: var(--co-muted);
}

.co-drive-modal-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
