<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Popover as APopover,
  Progress as AProgress,
  Space as ASpace,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowClockwise,
  PhArrowSquareOut,
  PhCheckCircle,
  PhDownloadSimple,
  PhInfo,
  PhSparkle,
  PhWarningCircle,
  PhX,
} from "@phosphor-icons/vue";
import { colabOneLocaleCode, t, type ColabOneMessageKey } from "./i18n";
import type { ColabOneUpdateState } from "./colabOneBridge";
import { COLABONE_APP_VERSION } from "./colabOneVersion";
import {
  COLABONE_RELEASES_URL,
  formatColabOneUpdateBytes,
  formatColabOneUpdateSpeed,
  hasColabOneBilingualNotes,
  parseColabOneReleaseNotes,
  resolveColabOneUpdateErrorKey,
  selectColabOneUpdateNotes,
  splitColabOneInlineCode,
} from "./colabOneUpdater";

const props = defineProps<{
  open: boolean;
  updateState: ColabOneUpdateState;
  checking: boolean;
  hasActiveRuntime: boolean;
  hasActiveOperation: boolean;
}>();

const emit = defineEmits<{
  (e: "update:open", value: boolean): void;
  (e: "check"): void;
  (e: "install"): void;
  (e: "cancel"): void;
  (e: "relaunch"): void;
  (e: "skip", version: string): void;
  (e: "openReleasePage", url: string): void;
}>();

const showFullNotes = ref(false);

watch(
  () => [props.open, props.updateState.available_version] as const,
  ([isOpen]) => {
    if (isOpen) {
      showFullNotes.value = false;
    }
  },
);

function cleanVersion(raw: string | undefined, fallback = ""): string {
  const cleaned = String(raw ?? "")
    .trim()
    .replace(/^[vV]+/, "");
  return cleaned || fallback;
}

const status = computed(() => String(props.updateState.status ?? "idle").trim());

const isAvailable = computed(() => status.value === "available");
const isDownloading = computed(() => status.value === "downloading");
const isInstalling = computed(() => status.value === "installing");
const isReadyToRestart = computed(() => status.value === "ready_to_restart");
const isUpToDate = computed(() => status.value === "up_to_date");
const isError = computed(() => status.value === "error");
const isSignatureError = computed(
  () =>
    isError.value &&
    String(props.updateState.error_code ?? "")
      .trim()
      .toLowerCase() === "signature",
);
const isChecking = computed(
  () => props.checking || status.value === "checking",
);
const isDevBuild = computed(
  () =>
    status.value === "disabled" &&
    props.updateState.disabled_reason === "dev_build",
);
const isReadOnlyAvailable = computed(
  () =>
    isAvailable.value &&
    !props.updateState.can_self_update &&
    props.updateState.disabled_reason === "read_only_install",
);
const isManifestMissingAvailable = computed(
  () =>
    isAvailable.value &&
    !props.updateState.can_self_update &&
    props.updateState.disabled_reason === "manifest_missing",
);
const isActionableStatus = computed(
  () =>
    isAvailable.value ||
    isDownloading.value ||
    isInstalling.value ||
    isReadyToRestart.value,
);

const panelSizeClass = computed(() => {
  if (isActionableStatus.value) return "is-rich";
  if (isError.value) return "is-error";
  return "is-compact";
});

const currentVersion = computed(() =>
  cleanVersion(props.updateState.current_version, COLABONE_APP_VERSION),
);
const targetVersion = computed(() =>
  cleanVersion(props.updateState.available_version),
);
const showVersionHeader = computed(
  () => isActionableStatus.value && targetVersion.value.length > 0,
);

const releasePageUrl = computed(() => {
  const raw = String(props.updateState.release_url ?? "").trim();
  return raw || COLABONE_RELEASES_URL;
});

const modalTitle = computed(() => {
  switch (status.value) {
    case "ready_to_restart":
      return t("update.modalTitleReady");
    case "available":
    case "downloading":
    case "installing":
      return t("update.modalTitleAvailable");
    case "up_to_date":
      return t("update.modalTitleUpToDate");
    case "error":
      return t("update.modalTitleError");
    default:
      return t("update.modalTitleDisabled");
  }
});

const titleTone = computed<"accent" | "success" | "danger" | "neutral">(() => {
  if (isReadyToRestart.value || isUpToDate.value) return "success";
  if (isError.value) return "danger";
  if (isAvailable.value || isDownloading.value || isInstalling.value) {
    return "accent";
  }
  return "neutral";
});

const formattedReleaseDate = computed(() => {
  const raw = String(props.updateState.release_date ?? "").trim();
  if (!raw) return "";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return "";
  try {
    return new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      year: "numeric",
      month: "short",
      day: "numeric",
    }).format(parsed);
  } catch {
    return parsed.toISOString().slice(0, 10);
  }
});

const lastCheckedText = computed(() => {
  const ts = props.updateState.last_checked_at;
  if (typeof ts !== "number" || !Number.isFinite(ts) || ts <= 0) {
    return t("update.lastCheckedNever");
  }
  const ms = ts > 1e12 ? ts : ts * 1000;
  const parsed = new Date(ms);
  if (Number.isNaN(parsed.getTime())) {
    return t("update.lastCheckedNever");
  }
  try {
    const formatted = new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }).format(parsed);
    return t("update.lastCheckedAt", { time: formatted });
  } catch {
    return t("update.lastCheckedAt", { time: parsed.toLocaleString() });
  }
});

const isBilingualNotes = computed(() =>
  hasColabOneBilingualNotes(props.updateState),
);

const renderedNoteBlocks = computed(() => {
  if (!isActionableStatus.value) {
    return [];
  }
  const selected = selectColabOneUpdateNotes(
    props.updateState,
    colabOneLocaleCode.value,
    showFullNotes.value,
  );
  return parseColabOneReleaseNotes(selected).map((block) => ({
    kind: block.kind,
    segments: splitColabOneInlineCode(block.text),
  }));
});

const progressPercent = computed(() => {
  const raw = props.updateState.progress_percent;
  if (typeof raw !== "number" || !Number.isFinite(raw)) {
    return 0;
  }
  return Math.max(0, Math.min(100, Math.round(raw)));
});

const progressBytesLabel = computed(() => {
  const downloaded = props.updateState.downloaded_bytes;
  const total = props.updateState.total_bytes;
  if (total > 0) {
    return `${formatColabOneUpdateBytes(downloaded)} / ${formatColabOneUpdateBytes(total)}`;
  }
  if (downloaded > 0) {
    return formatColabOneUpdateBytes(downloaded);
  }
  return "";
});

const progressSpeedLabel = computed(() => {
  if (!isDownloading.value) return "";
  return formatColabOneUpdateSpeed(props.updateState.speed_bps);
});

const resolvedErrorMessage = computed(() => {
  const errKey = resolveColabOneUpdateErrorKey(props.updateState.error_code);
  if (errKey) {
    return t(errKey as ColabOneMessageKey);
  }
  const fallback = String(props.updateState.error_message ?? "").trim();
  return fallback || t("update.errUnknown");
});

const resolvedErrorDetail = computed(() => {
  const detail = String(props.updateState.error_message ?? "").trim();
  if (!detail || detail === resolvedErrorMessage.value) {
    return "";
  }
  return detail;
});

function handleOpenChange(next: boolean) {
  if (!next && isInstalling.value) {
    return;
  }
  emit("update:open", next);
}

function closePopover() {
  handleOpenChange(false);
}

function handleSkip() {
  if (!targetVersion.value) return;
  emit("skip", targetVersion.value);
}

function handleOpenReleasePage() {
  emit("openReleasePage", releasePageUrl.value);
}
</script>

<template>
  <APopover
    :open="props.open"
    :trigger="['click']"
    placement="bottomLeft"
    :arrow="true"
    :destroy-tooltip-on-hide="true"
    overlay-class-name="co-update-popover"
    :overlay-inner-style="{ padding: '0' }"
    @update:open="handleOpenChange"
  >
    <slot />

    <template #content>
      <div
        class="co-update-panel"
        :class="panelSizeClass"
        role="dialog"
        :aria-label="modalTitle"
        data-testid="update-modal"
      >
        <!-- Compact anchored header -->
        <div class="co-update-header">
          <div class="co-update-title">
            <span
              class="co-update-title-icon"
              :class="`is-${titleTone}`"
              aria-hidden="true"
            >
              <PhCheckCircle
                v-if="isReadyToRestart || isUpToDate"
                :size="15"
                weight="fill"
              />
              <PhWarningCircle v-else-if="isError" :size="15" weight="fill" />
              <PhDownloadSimple
                v-else-if="isDownloading || isInstalling"
                :size="15"
                weight="bold"
              />
              <PhSparkle v-else-if="isAvailable" :size="15" weight="fill" />
              <PhInfo v-else :size="15" weight="fill" />
            </span>
            <span class="co-update-title-text">{{ modalTitle }}</span>
          </div>
          <button
            v-if="!isInstalling"
            type="button"
            class="co-update-close-btn"
            :aria-label="t('common.close')"
            data-testid="update-close-btn"
            @click="closePopover"
          >
            <PhX :size="14" weight="bold" aria-hidden="true" />
          </button>
        </div>

        <div class="co-update-body">
          <!-- Version transition header -->
          <div v-if="showVersionHeader" class="co-update-version-bar">
            <ASpace :size="6" wrap class="co-update-version-left">
              <span class="co-update-version-transition co-mono">
                {{
                  t("update.modalVersionTransition", {
                    current: currentVersion,
                    target: targetVersion,
                  })
                }}
              </span>
              <ATag v-if="props.updateState.is_prerelease" color="warning">
                {{ t("update.modalPrereleaseTag") }}
              </ATag>
            </ASpace>
            <span v-if="formattedReleaseDate" class="co-update-release-date">
              {{ t("update.modalPublishedAt", { date: formattedReleaseDate }) }}
            </span>
          </div>

          <!-- Ready to restart confirmation -->
          <AAlert
            v-if="isReadyToRestart"
            type="success"
            show-icon
            class="co-update-alert"
            :message="
              t('update.modalReadyHint', {
                version: targetVersion || currentVersion,
              })
            "
          />

          <!-- Domain context alerts -->
          <AAlert
            v-if="isActionableStatus && props.hasActiveOperation"
            type="warning"
            show-icon
            class="co-update-alert"
            :message="t('update.modalActiveOpWarning')"
          />
          <AAlert
            v-else-if="isActionableStatus && props.hasActiveRuntime"
            type="info"
            show-icon
            class="co-update-alert"
            :message="t('update.modalActiveGpuNotice')"
          />

          <AAlert
            v-if="isReadOnlyAvailable"
            type="warning"
            show-icon
            class="co-update-alert"
            :message="t('update.modalReadOnlyWarning')"
          />
          <AAlert
            v-else-if="isManifestMissingAvailable"
            type="info"
            show-icon
            class="co-update-alert"
            :message="t('update.modalManifestMissingWarning')"
          />

          <AAlert
            v-if="isDevBuild"
            type="info"
            show-icon
            class="co-update-alert"
            :message="t('update.modalDevBuildNotice')"
          />

          <!-- Download / Install progress -->
          <div
            v-if="isDownloading || isInstalling"
            class="co-update-progress-card"
            role="status"
            aria-live="polite"
          >
            <div class="co-update-progress-label">
              {{
                isInstalling
                  ? t("update.modalInstallingHint")
                  : t("update.modalDownloadingHint")
              }}
            </div>
            <AProgress
              :percent="progressPercent"
              :status="isInstalling ? 'active' : 'normal'"
              :stroke-width="7"
              size="small"
            />
            <div
              v-if="progressBytesLabel || progressSpeedLabel"
              class="co-update-progress-meta co-mono"
            >
              <span>{{ progressBytesLabel }}</span>
              <span v-if="progressSpeedLabel" class="co-update-progress-speed">
                {{ progressSpeedLabel }}
              </span>
            </div>
          </div>

          <!-- Release notes card (safe structured blocks, plain text interpolation only) -->
          <section v-if="isActionableStatus" class="co-update-notes-card">
            <div class="co-update-notes-header">
              <span class="co-update-notes-heading">
                {{ t("update.modalNotesHeading") }}
              </span>
              <AButton
                v-if="isBilingualNotes"
                type="link"
                size="small"
                class="co-update-notes-toggle"
                @click="showFullNotes = !showFullNotes"
              >
                {{
                  showFullNotes
                    ? t("update.modalToggleLocalNotes")
                    : t("update.modalToggleFullNotes")
                }}
              </AButton>
            </div>

            <div class="co-update-notes-body" data-testid="update-notes-body">
              <template v-if="renderedNoteBlocks.length > 0">
                <template v-for="(block, idx) in renderedNoteBlocks" :key="idx">
                  <h4
                    v-if="block.kind === 'heading'"
                    class="co-update-note-heading"
                  >
                    <template
                      v-for="(seg, segIdx) in block.segments"
                      :key="segIdx"
                    >
                      <code v-if="seg.code" class="co-update-inline-code">{{
                        seg.text
                      }}</code>
                      <span v-else>{{ seg.text }}</span>
                    </template>
                  </h4>

                  <div
                    v-else-if="block.kind === 'item'"
                    class="co-update-note-item"
                  >
                    <span class="co-update-note-bullet" aria-hidden="true" />
                    <span class="co-update-note-text">
                      <template
                        v-for="(seg, segIdx) in block.segments"
                        :key="segIdx"
                      >
                        <code v-if="seg.code" class="co-update-inline-code">{{
                          seg.text
                        }}</code>
                        <span v-else>{{ seg.text }}</span>
                      </template>
                    </span>
                  </div>

                  <p v-else class="co-update-note-paragraph">
                    <template
                      v-for="(seg, segIdx) in block.segments"
                      :key="segIdx"
                    >
                      <code v-if="seg.code" class="co-update-inline-code">{{
                        seg.text
                      }}</code>
                      <span v-else>{{ seg.text }}</span>
                    </template>
                  </p>
                </template>
              </template>
              <p v-else class="co-update-notes-empty">
                {{ t("update.modalNotesEmpty") }}
              </p>
            </div>
          </section>

          <!-- Up-to-date block -->
          <div
            v-else-if="isUpToDate"
            class="co-update-status-card is-success"
            role="status"
          >
            <PhCheckCircle
              :size="17"
              weight="fill"
              class="co-update-status-icon is-success"
              aria-hidden="true"
            />
            <div class="co-update-status-meta">
              <span class="co-update-status-text">
                {{ t("update.modalUpToDateDesc", { version: currentVersion }) }}
              </span>
              <span class="co-update-status-sub">
                {{ lastCheckedText }}
              </span>
            </div>
          </div>

          <!-- Error block -->
          <AAlert
            v-else-if="isError"
            type="error"
            show-icon
            class="co-update-alert"
            :message="resolvedErrorMessage"
          >
            <template v-if="resolvedErrorDetail" #description>
              <div
                class="co-update-error-detail"
                data-testid="update-error-detail"
              >
                <span class="co-update-error-detail-label">
                  {{ t("update.errDetailLabel") }}
                </span>
                <code class="co-update-error-detail-code co-mono">{{
                  resolvedErrorDetail
                }}</code>
              </div>
            </template>
          </AAlert>

          <!-- Checking block -->
          <div
            v-else-if="isChecking"
            class="co-update-status-card"
            role="status"
            aria-live="polite"
          >
            <PhArrowClockwise
              :size="16"
              class="co-update-status-icon is-spin"
              aria-hidden="true"
            />
            <span class="co-update-status-text">{{
              t("update.btnChecking")
            }}</span>
            <ATag class="co-mono">v{{ currentVersion }}</ATag>
          </div>

          <!-- Idle / disabled version context -->
          <div v-else class="co-update-status-card">
            <PhInfo
              :size="16"
              weight="fill"
              class="co-update-status-icon is-info"
              aria-hidden="true"
            />
            <div class="co-update-status-meta">
              <ASpace :size="6" wrap>
                <span class="co-update-status-version co-mono">
                  ColabOne v{{ currentVersion }}
                </span>
                <ATag v-if="isDevBuild">{{ t("update.statusDevBuild") }}</ATag>
              </ASpace>
              <span v-if="!isDevBuild" class="co-update-status-sub">
                {{ lastCheckedText }}
              </span>
            </div>
          </div>
        </div>

        <!-- Compact tailored state-driven footer -->
        <div class="co-update-footer">
          <!-- Available + can self-update -->
          <template v-if="isAvailable && props.updateState.can_self_update">
            <div class="co-update-footer-left">
              <ASpace :size="2" wrap>
                <AButton
                  type="text"
                  size="small"
                  class="co-update-subtle-btn"
                  :disabled="!targetVersion"
                  data-testid="update-skip-btn"
                  @click="handleSkip"
                >
                  {{ t("update.btnSkipVersion") }}
                </AButton>
                <AButton
                  type="link"
                  size="small"
                  class="co-update-link-btn"
                  data-testid="update-manual-btn"
                  @click="handleOpenReleasePage"
                >
                  <template #icon>
                    <PhArrowSquareOut :size="13" />
                  </template>
                  {{ t("update.btnOpenReleases") }}
                </AButton>
              </ASpace>
            </div>
            <div class="co-update-footer-right">
              <ASpace :size="6" wrap>
                <AButton size="small" @click="closePopover">
                  {{ t("update.btnRemindLater") }}
                </AButton>
                <AButton
                  type="primary"
                  size="small"
                  data-testid="update-install-btn"
                  @click="emit('install')"
                >
                  <template #icon>
                    <PhDownloadSimple :size="14" />
                  </template>
                  {{ t("update.btnUpdateNow") }}
                </AButton>
              </ASpace>
            </div>
          </template>

          <!-- Available + read-only / manifest-missing -->
          <template v-else-if="isAvailable">
            <div class="co-update-footer-left">
              <AButton
                type="text"
                size="small"
                class="co-update-subtle-btn"
                :disabled="!targetVersion"
                data-testid="update-skip-btn"
                @click="handleSkip"
              >
                {{ t("update.btnSkipVersion") }}
              </AButton>
            </div>
            <div class="co-update-footer-right">
              <ASpace :size="6" wrap>
                <AButton size="small" @click="closePopover">
                  {{ t("update.btnRemindLater") }}
                </AButton>
                <AButton
                  type="primary"
                  size="small"
                  data-testid="update-manual-btn"
                  @click="handleOpenReleasePage"
                >
                  <template #icon>
                    <PhArrowSquareOut :size="14" />
                  </template>
                  {{ t("update.btnOpenReleases") }}
                </AButton>
              </ASpace>
            </div>
          </template>

          <!-- Downloading -->
          <template v-else-if="isDownloading">
            <div class="co-update-footer-right">
              <AButton
                danger
                size="small"
                data-testid="update-cancel-btn"
                @click="emit('cancel')"
              >
                {{ t("update.btnCancelDownload") }}
              </AButton>
            </div>
          </template>

          <!-- Installing -->
          <template v-else-if="isInstalling">
            <div class="co-update-footer-right">
              <AButton type="primary" size="small" loading disabled>
                {{ t("update.statusInstalling") }}
              </AButton>
            </div>
          </template>

          <!-- Ready to restart -->
          <template v-else-if="isReadyToRestart">
            <div class="co-update-footer-right">
              <ASpace :size="6" wrap>
                <AButton size="small" @click="closePopover">
                  {{ t("update.btnRestartLater") }}
                </AButton>
                <AButton
                  type="primary"
                  size="small"
                  :disabled="props.hasActiveOperation"
                  data-testid="update-relaunch-btn"
                  @click="emit('relaunch')"
                >
                  <template #icon>
                    <PhArrowClockwise :size="14" />
                  </template>
                  {{ t("update.btnRestartNow") }}
                </AButton>
              </ASpace>
            </div>
          </template>

          <!-- Error -->
          <template v-else-if="isError">
            <div class="co-update-footer-left">
              <AButton
                v-if="!isSignatureError"
                type="link"
                size="small"
                class="co-update-link-btn"
                data-testid="update-manual-btn"
                @click="handleOpenReleasePage"
              >
                <template #icon>
                  <PhArrowSquareOut :size="13" />
                </template>
                {{ t("update.btnOpenReleases") }}
              </AButton>
              <AButton
                v-else
                type="text"
                size="small"
                class="co-update-subtle-btn"
                :loading="isChecking"
                @click="emit('check')"
              >
                <template v-if="!isChecking" #icon>
                  <PhArrowClockwise :size="13" />
                </template>
                {{ t("update.btnRetry") }}
              </AButton>
            </div>
            <div class="co-update-footer-right">
              <AButton
                v-if="isSignatureError"
                type="primary"
                size="small"
                data-testid="update-manual-btn"
                @click="handleOpenReleasePage"
              >
                <template #icon>
                  <PhArrowSquareOut :size="14" />
                </template>
                {{ t("update.btnOpenReleases") }}
              </AButton>
              <AButton
                v-else
                type="primary"
                size="small"
                :loading="isChecking"
                @click="emit('check')"
              >
                <template v-if="!isChecking" #icon>
                  <PhArrowClockwise :size="14" />
                </template>
                {{ t("update.btnRetry") }}
              </AButton>
            </div>
          </template>

          <!-- Up-to-date / idle / checking / disabled -->
          <template v-else>
            <div class="co-update-footer-left">
              <AButton
                type="link"
                size="small"
                class="co-update-link-btn"
                data-testid="update-manual-btn"
                @click="handleOpenReleasePage"
              >
                <template #icon>
                  <PhArrowSquareOut :size="13" />
                </template>
                {{ t("update.btnOpenReleases") }}
              </AButton>
            </div>
            <div class="co-update-footer-right">
              <AButton
                v-if="!isDevBuild"
                type="primary"
                size="small"
                :loading="isChecking"
                :disabled="isChecking"
                @click="emit('check')"
              >
                <template v-if="!isChecking" #icon>
                  <PhArrowClockwise :size="14" />
                </template>
                {{ t("update.btnCheck") }}
              </AButton>
            </div>
          </template>
        </div>
      </div>
    </template>
  </APopover>
</template>

<style scoped>
.co-update-panel {
  display: flex;
  flex-direction: column;
  max-width: calc(100vw - 24px);
  color: var(--co-text);
}

.co-update-panel.is-compact {
  width: 332px;
}

.co-update-panel.is-error {
  width: 368px;
}

.co-update-panel.is-rich {
  width: 404px;
}

.co-update-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 11px 12px 9px 14px;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-update-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.co-update-title-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 7px;
  flex-shrink: 0;
}

.co-update-title-icon.is-accent {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-update-title-icon.is-success {
  background: var(--co-success-bg);
  color: var(--co-success);
}

.co-update-title-icon.is-danger {
  background: var(--co-danger-bg);
  color: var(--co-danger);
}

.co-update-title-icon.is-neutral {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-update-title-text {
  font-size: 13.5px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--co-text);
}

.co-update-close-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--co-muted);
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.14s ease,
    color 0.14s ease;
}

.co-update-close-btn:hover {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.co-update-close-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-update-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px 14px;
}

.co-update-alert :deep(.ant-alert-message) {
  font-size: 12.5px;
  line-height: 1.45;
}

.co-update-version-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 11px;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-update-version-left {
  min-width: 0;
}

.co-update-version-transition {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
  letter-spacing: -0.01em;
}

.co-update-release-date {
  font-size: 11.5px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-update-progress-card {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 10px 12px;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-update-progress-label {
  font-size: 12.5px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--co-text);
}

.co-update-progress-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-update-progress-speed {
  color: var(--co-secondary-text);
  font-weight: 500;
}

.co-update-notes-card {
  display: flex;
  flex-direction: column;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  overflow: hidden;
}

.co-update-notes-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 11px;
  border-bottom: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
}

.co-update-notes-heading {
  font-size: 12px;
  font-weight: 600;
  color: var(--co-text);
}

.co-update-notes-toggle {
  height: auto;
  padding: 0 2px;
  font-size: 11.5px;
}

.co-update-notes-body {
  max-height: 192px;
  overflow-y: auto;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: var(--co-secondary-text);
  font-size: 12.5px;
  line-height: 1.55;
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.35) transparent;
}

.co-update-notes-body::-webkit-scrollbar-thumb {
  background: rgba(148, 163, 184, 0.32);
}

.co-update-notes-body:hover::-webkit-scrollbar-thumb {
  background: rgba(100, 116, 139, 0.55);
}

.co-update-note-heading {
  margin: 3px 0 0;
  font-size: 12.5px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--co-text);
}

.co-update-note-heading:first-child {
  margin-top: 0;
}

.co-update-note-item {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  min-width: 0;
}

.co-update-note-bullet {
  width: 4.5px;
  height: 4.5px;
  margin-top: 7.5px;
  border-radius: 50%;
  background: var(--co-accent);
  flex-shrink: 0;
}

.co-update-note-text {
  flex: 1;
  min-width: 0;
  color: var(--co-text);
  overflow-wrap: anywhere;
}

.co-update-note-paragraph {
  margin: 0;
  color: var(--co-secondary-text);
  overflow-wrap: anywhere;
}

.co-update-inline-code {
  display: inline;
  padding: 0.5px 4px;
  margin-inline: 1px;
  border-radius: 4px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-text);
  font-family: var(--co-font-mono);
  font-size: 11.5px;
  line-height: 1.4;
}

.co-update-error-detail {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 4px;
}

.co-update-error-detail-label {
  font-size: 11px;
  font-weight: 600;
  color: var(--co-secondary-text);
}

.co-update-error-detail-code {
  display: block;
  padding: 5px 7px;
  border-radius: 6px;
  border: 1px solid var(--co-danger-border);
  background: var(--co-surface);
  color: var(--co-text);
  font-family: var(--co-font-mono);
  font-size: 11px;
  line-height: 1.45;
  overflow-wrap: anywhere;
  user-select: text;
}

.co-update-notes-empty {
  margin: 0;
  padding: 4px 0;
  color: var(--co-muted);
  font-size: 12px;
}

.co-update-status-card {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 10px 12px;
  border-radius: var(--co-radius-md);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  color: var(--co-text);
}

.co-update-status-card.is-success {
  border-color: var(--co-success-border);
  background: var(--co-success-bg);
}

.co-update-status-icon {
  margin-top: 1px;
  flex-shrink: 0;
}

.co-update-status-icon.is-success {
  color: var(--co-success);
}

.co-update-status-icon.is-info {
  color: var(--co-accent);
}

.co-update-status-icon.is-spin {
  color: var(--co-accent);
  animation: co-update-spin var(--co-spin-duration, 4s) linear infinite;
}

.co-update-status-text {
  flex: 1;
  min-width: 0;
  font-size: 12.5px;
  line-height: 1.45;
  color: var(--co-text);
}

.co-update-status-meta {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.co-update-status-version {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-update-status-sub {
  font-size: 11.5px;
  color: var(--co-muted);
}

.co-update-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  padding: 9px 14px 10px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-update-footer-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
}

.co-update-footer-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  margin-inline-start: auto;
}

.co-update-subtle-btn {
  padding-inline: 5px;
  color: var(--co-muted);
  font-size: 12px;
}

.co-update-subtle-btn:hover:not(:disabled) {
  color: var(--co-text);
}

.co-update-link-btn {
  padding-inline: 4px;
  font-size: 12px;
}

@keyframes co-update-spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .co-update-status-icon.is-spin {
    animation: none;
  }
}

/* Anchored popover chrome (light & dark) */
:global(.co-update-popover) {
  z-index: 1050;
}

:global(.co-update-popover .ant-popover-inner) {
  border-radius: 14px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  box-shadow:
    0 12px 32px rgba(15, 23, 42, 0.14),
    0 2px 8px rgba(15, 23, 42, 0.06);
  overflow: hidden;
}

:global(.co-update-popover .ant-popover-arrow::before) {
  background: var(--co-surface);
}

:global(:root[data-theme="dark"] .co-update-popover .ant-popover-inner) {
  border-color: var(--co-border);
  background: var(--co-elevated) !important;
  box-shadow:
    0 16px 40px rgba(0, 0, 0, 0.54),
    0 3px 10px rgba(0, 0, 0, 0.34);
}

:global(:root[data-theme="dark"] .co-update-popover .ant-popover-arrow::before) {
  background: var(--co-elevated);
}

:global(:root[data-theme="dark"] .co-update-version-bar),
:global(:root[data-theme="dark"] .co-update-progress-card),
:global(:root[data-theme="dark"] .co-update-notes-card),
:global(:root[data-theme="dark"] .co-update-status-card:not(.is-success)) {
  border-color: var(--co-border);
  background: var(--co-subtle);
}

:global(:root[data-theme="dark"] .co-update-notes-header) {
  border-bottom-color: var(--co-border);
  background: var(--co-surface);
}

:global(:root[data-theme="dark"] .co-update-inline-code) {
  border-color: var(--co-border);
  background: var(--co-elevated);
  color: var(--co-text);
}

:global(:root[data-theme="dark"] .co-update-notes-body) {
  scrollbar-color: rgba(255, 255, 255, 0.2) transparent;
}

:global(:root[data-theme="dark"] .co-update-notes-body::-webkit-scrollbar-thumb) {
  background: rgba(255, 255, 255, 0.2);
}

:global(:root[data-theme="dark"] .co-update-notes-body:hover::-webkit-scrollbar-thumb) {
  background: rgba(255, 255, 255, 0.38);
}
</style>
