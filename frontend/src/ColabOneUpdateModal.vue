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
  PhArrowsClockwise,
  PhCaretDown,
  PhCaretUp,
  PhCheck,
  PhClockCounterClockwise,
  PhDownloadSimple,
  PhGithubLogo,
  PhInfo,
  PhSparkle,
  PhWarningCircle,
} from "@phosphor-icons/vue";
import { colabOneLocaleCode, t, type ColabOneMessageKey } from "./i18n";
import type {
  ColabOneRollbackVersion,
  ColabOneUpdateState,
} from "./colabOneBridge";
import { COLABONE_APP_VERSION } from "./colabOneVersion";
import {
  COLABONE_RELEASES_URL,
  formatColabOneRollbackDate,
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
  rollbackVersions: ColabOneRollbackVersion[];
  loadingRollback: boolean;
  rollbackLoaded: boolean;
  rollbackError: string;
  startingRollback?: boolean;
  hasActiveRuntime: boolean;
  hasActiveOperation: boolean;
}>();

const emit = defineEmits<{
  (e: "update:open", value: boolean): void;
  (e: "check"): void;
  (e: "install"): void;
  (e: "loadRollback", forceRefresh: boolean): void;
  (e: "rollback", version: string): void;
  (e: "cancel"): void;
  (e: "relaunch"): void;
  (e: "skip", version: string): void;
  (e: "openReleasePage", url: string): void;
}>();

const showFullNotes = ref(false);
const rollbackExpanded = ref(false);
const selectedRollbackVersion = ref("");

watch(
  () => [props.open, props.updateState.available_version] as const,
  ([isOpen], [prevOpen]) => {
    if (isOpen) {
      showFullNotes.value = false;
      if (!prevOpen) {
        rollbackExpanded.value = false;
        selectedRollbackVersion.value = "";
      }
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

const isRollback = computed(() => Boolean(props.updateState.is_rollback));
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
const isCheckingView = computed(
  () => isChecking.value && !isActionableStatus.value,
);
const isUpToDateView = computed(
  () => isUpToDate.value && !isCheckingView.value,
);
const isErrorView = computed(() => isError.value && !isCheckingView.value);

const showRefreshButton = computed(
  () =>
    !isDevBuild.value &&
    !isDownloading.value &&
    !isInstalling.value &&
    !isReadyToRestart.value,
);

const showRollbackSection = computed(
  () =>
    !isDevBuild.value &&
    !isDownloading.value &&
    !isInstalling.value &&
    !isReadyToRestart.value,
);

const showDetailsSection = computed(
  () => isActionableStatus.value || isErrorView.value || isDevBuild.value,
);

const panelSizeClass = computed(() => {
  if (isAvailable.value) return "is-rich";
  if (isDownloading.value || isInstalling.value || isReadyToRestart.value) {
    return "is-active";
  }
  if (isErrorView.value) return "is-error";
  return "is-compact";
});

const currentVersion = computed(() =>
  cleanVersion(props.updateState.current_version, COLABONE_APP_VERSION),
);
const targetVersion = computed(() =>
  cleanVersion(props.updateState.available_version),
);
const showVersionHeader = computed(
  () => isAvailable.value && targetVersion.value.length > 0,
);

const releasePageUrl = computed(() => {
  const raw = String(props.updateState.release_url ?? "").trim();
  return raw || COLABONE_RELEASES_URL;
});

const heroBadgeTone = computed<"accent" | "success" | "danger" | "neutral">(
  () => {
    if (isCheckingView.value) return "accent";
    if (isReadyToRestart.value || isUpToDateView.value) return "success";
    if (isErrorView.value) return "danger";
    if (isAvailable.value || isDownloading.value || isInstalling.value) {
      return "accent";
    }
    return "neutral";
  },
);

const heroStatusText = computed(() => {
  if (isCheckingView.value) {
    return t("update.btnChecking");
  }
  if (isReadyToRestart.value) {
    return isRollback.value
      ? t("update.modalTitleRollbackReady")
      : t("update.modalTitleReady");
  }
  if (isDownloading.value) {
    return isRollback.value
      ? t("update.heroDownloadingRollback", {
          version: targetVersion.value || currentVersion.value,
        })
      : t("update.heroDownloading", {
          version: targetVersion.value || currentVersion.value,
        });
  }
  if (isInstalling.value) {
    return isRollback.value
      ? t("update.heroInstallingRollback", {
          version: targetVersion.value || currentVersion.value,
        })
      : t("update.heroInstalling", {
          version: targetVersion.value || currentVersion.value,
        });
  }
  if (isAvailable.value) {
    return t("update.heroAvailable", {
      version: targetVersion.value || currentVersion.value,
    });
  }
  if (isUpToDateView.value) {
    return t("update.modalTitleUpToDate");
  }
  if (isErrorView.value) {
    return isRollback.value
      ? t("update.modalTitleRollbackError")
      : t("update.modalTitleError");
  }
  if (isDevBuild.value) {
    return t("update.statusDevBuild");
  }
  return lastCheckedText.value;
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
  if (!isAvailable.value) {
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

const rollbackItems = computed(() => props.rollbackVersions);

const rollbackDisplayCount = computed(() => {
  const count = rollbackItems.value.length;
  return count > 0 ? count : 3;
});

const selectedRollbackItem = computed(
  () =>
    rollbackItems.value.find(
      (item) => item.version === selectedRollbackVersion.value,
    ) ?? null,
);

watch(rollbackItems, (items) => {
  if (
    selectedRollbackVersion.value &&
    !items.some((item) => item.version === selectedRollbackVersion.value)
  ) {
    selectedRollbackVersion.value = "";
  }
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

function handleRefreshClick() {
  emit("check");
  if (rollbackExpanded.value) {
    emit("loadRollback", true);
  }
}

function handleRetryError() {
  if (isRollback.value && targetVersion.value) {
    emit("rollback", targetVersion.value);
    return;
  }
  emit("check");
}

function toggleRollbackSection() {
  rollbackExpanded.value = !rollbackExpanded.value;
  if (rollbackExpanded.value) {
    emit("loadRollback", false);
  }
}

function selectRollbackVersion(version: string) {
  selectedRollbackVersion.value = version;
}

function handleConfirmRollback() {
  const item = selectedRollbackItem.value;
  if (!item) return;
  if (item.can_rollback) {
    emit("rollback", item.version);
  } else {
    emit("openReleasePage", item.release_url);
  }
}

function handleSkip() {
  if (!targetVersion.value) return;
  emit("skip", targetVersion.value);
}

function handleOpenReleasePage(url?: string) {
  emit("openReleasePage", url || releasePageUrl.value);
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
        :aria-label="t('update.popoverHeader')"
        data-testid="update-modal"
      >
        <!-- Minimalist top bar -->
        <div class="co-update-header">
          <span class="co-update-header-title">
            {{ t("update.popoverHeader") }}
          </span>
          <button
            v-if="showRefreshButton"
            type="button"
            class="co-update-refresh-btn"
            :class="{ 'is-spinning': isChecking }"
            :disabled="isChecking"
            :title="
              isChecking ? t('update.btnChecking') : t('update.btnCheckAgain')
            "
            :aria-label="
              isChecking ? t('update.btnChecking') : t('update.btnCheckAgain')
            "
            data-testid="update-check-btn"
            @click="handleRefreshClick"
          >
            <PhArrowsClockwise :size="15" aria-hidden="true" />
          </button>
        </div>

        <!-- Centered version hero -->
        <div class="co-update-hero">
          <div class="co-update-hero-version-row">
            <span class="co-update-hero-version co-mono">
              v{{ currentVersion }}
            </span>
            <span
              class="co-update-hero-badge"
              :class="`is-${heroBadgeTone}`"
              aria-hidden="true"
            >
              <PhArrowsClockwise
                v-if="isCheckingView"
                :size="13"
                class="is-spin"
              />
              <PhCheck
                v-else-if="isReadyToRestart || isUpToDateView"
                :size="13"
                weight="bold"
              />
              <PhDownloadSimple
                v-else-if="isDownloading || isInstalling"
                :size="13"
                weight="bold"
              />
              <PhSparkle v-else-if="isAvailable" :size="13" weight="fill" />
              <PhWarningCircle
                v-else-if="isErrorView"
                :size="13"
                weight="fill"
              />
              <PhInfo v-else :size="13" weight="fill" />
            </span>
          </div>

          <div
            class="co-update-hero-status"
            :class="{
              'is-accent': isAvailable || isDownloading || isInstalling,
              'is-danger': isErrorView,
            }"
            role="status"
            aria-live="polite"
            :data-testid="isCheckingView ? 'update-checking-status' : undefined"
          >
            {{ heroStatusText }}
          </div>

          <button
            type="button"
            class="co-update-releases-link"
            data-testid="update-releases-link"
            @click="handleOpenReleasePage()"
          >
            <PhGithubLogo :size="15" weight="fill" aria-hidden="true" />
            <span>{{ t("update.btnViewReleases") }}</span>
          </button>
        </div>

        <!-- Contextual actionable details (hidden in clean up-to-date / checking / idle views) -->
        <div v-if="showDetailsSection" class="co-update-details">
          <!-- Version transition bar when a newer version is available -->
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
              t(
                isRollback
                  ? 'update.modalRollbackReadyHint'
                  : 'update.modalReadyHint',
                {
                  version: targetVersion || currentVersion,
                },
              )
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
                  ? t(
                      isRollback
                        ? "update.modalRollbackInstallingHint"
                        : "update.modalInstallingHint",
                    )
                  : t(
                      isRollback
                        ? "update.modalRollbackDownloadingHint"
                        : "update.modalDownloadingHint",
                    )
              }}
            </div>
            <AProgress
              :percent="progressPercent"
              :status="isInstalling ? 'active' : 'normal'"
              :stroke-width="6"
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
          <section v-if="isAvailable" class="co-update-notes-card">
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

          <!-- Error block -->
          <AAlert
            v-if="isErrorView"
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

          <!-- Action buttons for active / available / error states -->
          <div v-if="!isDevBuild" class="co-update-actions">
            <!-- Available + can self-update -->
            <template v-if="isAvailable && props.updateState.can_self_update">
              <div class="co-update-actions-left">
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
              <div class="co-update-actions-right">
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
              <div class="co-update-actions-left">
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
              <div class="co-update-actions-right">
                <ASpace :size="6" wrap>
                  <AButton size="small" @click="closePopover">
                    {{ t("update.btnRemindLater") }}
                  </AButton>
                  <AButton
                    type="primary"
                    size="small"
                    data-testid="update-manual-btn"
                    @click="handleOpenReleasePage()"
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
              <div class="co-update-actions-right">
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
              <div class="co-update-actions-right">
                <AButton type="primary" size="small" loading disabled>
                  {{ t("update.statusInstalling") }}
                </AButton>
              </div>
            </template>

            <!-- Ready to restart -->
            <template v-else-if="isReadyToRestart">
              <div class="co-update-actions-right">
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
                    {{
                      isRollback
                        ? t("update.btnRestartRollback")
                        : t("update.btnRestartNow")
                    }}
                  </AButton>
                </ASpace>
              </div>
            </template>

            <!-- Error -->
            <template v-else-if="isErrorView">
              <div class="co-update-actions-left">
                <AButton
                  v-if="!isSignatureError"
                  type="link"
                  size="small"
                  class="co-update-link-btn"
                  data-testid="update-manual-btn"
                  @click="handleOpenReleasePage()"
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
                  @click="handleRetryError"
                >
                  <template v-if="!isChecking" #icon>
                    <PhArrowClockwise :size="13" />
                  </template>
                  {{ t("update.btnRetry") }}
                </AButton>
              </div>
              <div class="co-update-actions-right">
                <AButton
                  v-if="isSignatureError"
                  type="primary"
                  size="small"
                  data-testid="update-manual-btn"
                  @click="handleOpenReleasePage()"
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
                  @click="handleRetryError"
                >
                  <template v-if="!isChecking" #icon>
                    <PhArrowClockwise :size="14" />
                  </template>
                  {{ t("update.btnRetry") }}
                </AButton>
              </div>
            </template>
          </div>
        </div>

        <!-- Collapsible Version Rollback section -->
        <div v-if="showRollbackSection" class="co-update-rollback-section">
          <button
            type="button"
            class="co-update-rollback-toggle"
            :aria-expanded="rollbackExpanded"
            data-testid="update-rollback-toggle"
            @click="toggleRollbackSection"
          >
            <span class="co-update-rollback-toggle-left">
              <PhClockCounterClockwise :size="15" aria-hidden="true" />
              <span>{{ t("update.rollbackTitle") }}</span>
            </span>
            <PhCaretUp
              v-if="rollbackExpanded"
              :size="14"
              class="co-update-rollback-chevron"
              aria-hidden="true"
            />
            <PhCaretDown
              v-else
              :size="14"
              class="co-update-rollback-chevron"
              aria-hidden="true"
            />
          </button>

          <div
            v-if="rollbackExpanded"
            class="co-update-rollback-body"
            data-testid="update-rollback-panel"
          >
            <p
              v-if="rollbackItems.length > 0"
              class="co-update-rollback-subtitle"
            >
              {{
                t("update.rollbackSubtitle", { count: rollbackDisplayCount })
              }}
            </p>

            <!-- Loading historical releases -->
            <div
              v-if="props.loadingRollback && rollbackItems.length === 0"
              class="co-update-rollback-state"
              role="status"
            >
              <PhArrowsClockwise
                :size="14"
                class="is-spin"
                aria-hidden="true"
              />
              <span>{{ t("update.rollbackLoading") }}</span>
            </div>

            <!-- Failed to load historical releases -->
            <div
              v-else-if="props.rollbackError && rollbackItems.length === 0"
              class="co-update-rollback-state is-error"
            >
              <span>{{ t("update.rollbackLoadError") }}</span>
              <button
                type="button"
                class="co-update-rollback-retry"
                data-testid="update-rollback-retry-btn"
                @click="emit('loadRollback', true)"
              >
                {{ t("update.btnRetry") }}
              </button>
            </div>

            <!-- No older releases found -->
            <div
              v-else-if="props.rollbackLoaded && rollbackItems.length === 0"
              class="co-update-rollback-state"
            >
              <span>{{ t("update.rollbackEmpty") }}</span>
            </div>

            <!-- Radio list of up to 3 recent older releases -->
            <div
              v-else
              class="co-update-rollback-list"
              role="radiogroup"
              :aria-label="t('update.rollbackTitle')"
            >
              <button
                v-for="item in rollbackItems"
                :key="item.version"
                type="button"
                role="radio"
                class="co-update-rollback-item"
                :class="{
                  'is-selected': selectedRollbackVersion === item.version,
                }"
                :aria-checked="selectedRollbackVersion === item.version"
                :data-testid="`update-rollback-option-${item.version}`"
                @click="selectRollbackVersion(item.version)"
              >
                <span class="co-update-rollback-item-left">
                  <span class="co-update-rollback-radio" aria-hidden="true">
                    <span class="co-update-rollback-radio-dot" />
                  </span>
                  <span class="co-update-rollback-ver co-mono">
                    v{{ item.version }}
                  </span>
                  <ATag
                    v-if="item.is_prerelease"
                    color="warning"
                    class="co-update-rollback-pre-tag"
                  >
                    {{ t("update.modalPrereleaseTag") }}
                  </ATag>
                </span>
                <span
                  v-if="formatColabOneRollbackDate(item.release_date)"
                  class="co-update-rollback-date co-mono"
                >
                  {{ formatColabOneRollbackDate(item.release_date) }}
                </span>
              </button>
            </div>

            <!-- Action CTA when a rollback version is selected -->
            <div v-if="selectedRollbackItem" class="co-update-rollback-cta">
              <p
                v-if="!selectedRollbackItem.can_rollback"
                class="co-update-rollback-hint"
              >
                {{ t("update.rollbackManualHint") }}
              </p>
              <AButton
                v-if="selectedRollbackItem.can_rollback"
                type="primary"
                size="small"
                block
                :loading="Boolean(props.startingRollback)"
                :disabled="Boolean(props.startingRollback)"
                data-testid="update-rollback-confirm-btn"
                @click="handleConfirmRollback"
              >
                <template #icon>
                  <PhClockCounterClockwise :size="14" />
                </template>
                {{
                  t("update.btnRollbackTo", {
                    version: selectedRollbackItem.version,
                  })
                }}
              </AButton>
              <AButton
                v-else
                size="small"
                block
                data-testid="update-rollback-manual-btn"
                @click="handleConfirmRollback"
              >
                <template #icon>
                  <PhArrowSquareOut :size="14" />
                </template>
                {{
                  t("update.btnDownloadVersion", {
                    version: selectedRollbackItem.version,
                  })
                }}
              </AButton>
            </div>
          </div>
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
  max-height: calc(
    100vh - 80px - var(--co-safe-top, 0px) - var(--co-safe-bottom, 0px)
  );
  max-height: calc(
    100dvh - 80px - var(--co-safe-top, 0px) - var(--co-safe-bottom, 0px)
  );
  overflow-y: auto;
  overscroll-behavior: contain;
  color: var(--co-text);
}

.co-update-panel > * {
  flex-shrink: 0;
}

.co-update-panel.is-compact {
  width: 308px;
}

.co-update-panel.is-error {
  width: 344px;
}

.co-update-panel.is-active {
  width: 356px;
}

.co-update-panel.is-rich {
  width: 388px;
}

/* Top header bar */
.co-update-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 14px 10px 16px;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-update-header-title {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.3;
  color: var(--co-text);
}

.co-update-refresh-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--co-muted);
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.14s ease,
    color 0.14s ease;
}

.co-update-refresh-btn:hover:not(:disabled) {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.co-update-refresh-btn:disabled {
  cursor: default;
  opacity: 0.72;
}

.co-update-refresh-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-update-refresh-btn.is-spinning svg,
.is-spin {
  animation: co-update-spin var(--co-spin-duration, 1.2s) linear infinite;
}

/* Centered version hero */
.co-update-hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: 20px 18px 16px;
  gap: 6px;
}

.co-update-hero-version-row {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.co-update-hero-version {
  font-size: 24px;
  font-weight: 700;
  line-height: 1.2;
  letter-spacing: -0.025em;
  color: var(--co-text);
}

.co-update-hero-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  flex-shrink: 0;
}

.co-update-hero-badge.is-success {
  background: var(--co-success-bg);
  color: var(--co-success);
}

.co-update-hero-badge.is-accent {
  background: var(--co-selected);
  color: var(--co-accent);
}

.co-update-hero-badge.is-danger {
  background: var(--co-danger-bg);
  color: var(--co-danger);
}

.co-update-hero-badge.is-neutral {
  background: var(--co-track);
  color: var(--co-secondary-text);
}

.co-update-hero-status {
  font-size: 13px;
  line-height: 1.4;
  color: var(--co-secondary-text);
}

.co-update-hero-status.is-accent {
  color: var(--co-accent);
  font-weight: 500;
}

.co-update-hero-status.is-danger {
  color: var(--co-danger);
  font-weight: 500;
}

.co-update-releases-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-top: 8px;
  padding: 4px 10px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--co-muted);
  font-size: 13px;
  line-height: 1.35;
  cursor: pointer;
  transition:
    background-color 0.14s ease,
    color 0.14s ease;
}

.co-update-releases-link:hover {
  background: var(--co-soft-hover);
  color: var(--co-text);
}

.co-update-releases-link:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

/* Actionable contextual details */
.co-update-details {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 0 16px 14px;
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
  max-height: 176px;
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

.co-update-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  padding-top: 2px;
}

.co-update-actions-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
}

.co-update-actions-right {
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

/* Collapsible Version Rollback section */
.co-update-rollback-section {
  border-top: 1px solid var(--co-border-secondary);
  margin: 0 14px;
  padding: 10px 0 12px;
}

.co-update-rollback-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 4px 2px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--co-muted);
  font-size: 12.5px;
  line-height: 1.35;
  cursor: pointer;
  transition: color 0.14s ease;
}

.co-update-rollback-toggle:hover {
  color: var(--co-text);
}

.co-update-rollback-toggle:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-update-rollback-toggle-left {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-update-rollback-chevron {
  flex-shrink: 0;
  opacity: 0.85;
}

.co-update-rollback-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 10px;
}

.co-update-rollback-subtitle {
  margin: 0 0 2px;
  font-size: 12.5px;
  line-height: 1.4;
  color: var(--co-muted);
}

.co-update-rollback-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 12px 8px;
  border-radius: 9px;
  border: 1px dashed var(--co-border);
  font-size: 12px;
  color: var(--co-muted);
}

.co-update-rollback-state.is-error {
  justify-content: space-between;
  padding: 9px 11px;
  border-style: solid;
  border-color: var(--co-danger-border);
  background: var(--co-danger-bg);
  color: var(--co-danger);
}

.co-update-rollback-retry {
  border: none;
  background: transparent;
  padding: 0 2px;
  font-size: 12px;
  font-weight: 600;
  color: var(--co-accent);
  cursor: pointer;
  flex-shrink: 0;
}

.co-update-rollback-retry:hover {
  text-decoration: underline;
}

.co-update-rollback-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-update-rollback-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border);
  background: transparent;
  color: var(--co-text);
  cursor: pointer;
  text-align: left;
  transition:
    border-color 0.14s ease,
    background-color 0.14s ease;
}

.co-update-rollback-item:hover {
  border-color: var(--co-accent);
  background: var(--co-soft-hover);
}

.co-update-rollback-item.is-selected {
  border-color: var(--co-accent);
  background: var(--co-selected);
}

.co-update-rollback-item:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-update-rollback-item-left {
  display: inline-flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.co-update-rollback-radio {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  border: 1.5px solid var(--co-muted);
  flex-shrink: 0;
  transition: border-color 0.14s ease;
}

.co-update-rollback-radio-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--co-accent);
  opacity: 0;
  transform: scale(0.5);
  transition:
    opacity 0.14s ease,
    transform 0.14s ease;
}

.co-update-rollback-item.is-selected .co-update-rollback-radio {
  border-color: var(--co-accent);
}

.co-update-rollback-item.is-selected .co-update-rollback-radio-dot {
  opacity: 1;
  transform: scale(1);
}

.co-update-rollback-ver {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  letter-spacing: -0.01em;
}

.co-update-rollback-pre-tag {
  margin-inline-end: 0;
  font-size: 10.5px;
  line-height: 16px;
  padding: 0 5px;
}

.co-update-rollback-date {
  font-size: 12.5px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.co-update-rollback-cta {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 4px;
}

.co-update-rollback-hint {
  margin: 0;
  font-size: 11.5px;
  line-height: 1.45;
  color: var(--co-muted);
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
  .co-update-refresh-btn.is-spinning svg,
  .is-spin {
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
:global(:root[data-theme="dark"] .co-update-notes-card) {
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
