<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Checkbox as ACheckbox,
  Empty as AEmpty,
  Input as AInput,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Segmented as ASegmented,
  Spin as ASpin,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhCheck,
  PhCheckCircle,
  PhClock,
  PhCopy,
  PhGearSix,
  PhGlobe,
  PhImage,
  PhInfo,
  PhLinkSimple,
  PhPower,
  PhProhibit,
  PhShareNetwork,
  PhTrash,
  PhTreeStructure,
  PhTShirt,
  PhUsers,
  PhVideoCamera,
} from "@phosphor-icons/vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import { colabOneWriteClipboard } from "./api";
import {
  buildColabOneStudioWorkflowEntries,
  COLABONE_DIGITAL_HUMAN_GROUP_KEY,
  type ColabOneStudioWorkflowEntry,
} from "./comfyui";
import type { ColabOneWebConsoleState } from "./colabOneWebConsole";
import type {
  ColabOneWorkflowShareCreateRequest,
  ColabOneWorkflowShareListState,
  ColabOneWorkflowShareStatus,
  ColabOneWorkflowShareSummary,
  ColabOneWorkflowShareTTLMode,
} from "./colabOneWorkflowShare";
import { colabOneLocaleCode, t, tb } from "./i18n";
import type { WorkflowTemplate } from "./types";

defineOptions({
  name: "ColabOneWorkflowShareModal",
});

const props = withDefaults(
  defineProps<{
    open: boolean;
    workflows: WorkflowTemplate[];
    webConsoleState: ColabOneWebConsoleState;
    sharesState: ColabOneWorkflowShareListState;
    loading?: boolean;
    creating?: boolean;
    mutating?: boolean;
    enablingWebConsole?: boolean;
    revokingId?: string;
    deletingId?: string;
    lastCreatedShare?: ColabOneWorkflowShareSummary | null;
    lastCreatedCopied?: boolean;
    copyLink?: (url: string) => Promise<boolean> | boolean;
  }>(),
  {
    loading: false,
    creating: false,
    mutating: false,
    enablingWebConsole: false,
    revokingId: "",
    deletingId: "",
    lastCreatedShare: null,
    lastCreatedCopied: true,
    copyLink: undefined,
  },
);

const emit = defineEmits<{
  "update:open": [value: boolean];
  create: [req: ColabOneWorkflowShareCreateRequest];
  revoke: [shareId: string];
  delete: [shareId: string];
  copyLink: [url: string];
  enableWebConsole: [];
  openWebConsoleSettings: [];
}>();

const activeTab = ref<"create" | "manage">("create");
const selectedWorkflowIds = ref<string[]>([]);
const userTouchedSelection = ref(false);
const labelDraft = ref("");
const titleDraft = ref("");
const ttlMode = ref<ColabOneWorkflowShareTTLMode>("24h");
const validationError = ref("");
const staleCreatedShareId = ref("");
const seenCreatedInListId = ref("");
const copiedKey = ref("");
const openConfirmKey = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | null = null;
let copySeq = 0;
let isUnmounted = false;
let escapeBound = false;

const studioEntries = computed<ColabOneStudioWorkflowEntry[]>(() =>
  buildColabOneStudioWorkflowEntries(props.workflows),
);

const sharesList = computed<ColabOneWorkflowShareSummary[]>(() =>
  Array.isArray(props.sharesState?.shares) ? props.sharesState.shares : [],
);

const activeShareCount = computed<number>(() => {
  if (
    typeof props.sharesState?.active_count === "number" &&
    Number.isFinite(props.sharesState.active_count) &&
    props.sharesState.active_count >= 0
  ) {
    return Math.floor(props.sharesState.active_count);
  }
  return sharesList.value.filter((item) => item.status === "active").length;
});

const publicOrigin = computed<string>(() =>
  (props.webConsoleState?.public_origin || "").trim(),
);

const publicOriginHintParts = computed<{ before: string; after: string }>(() => {
  const sentinel = "__COLABONE_ORIGIN__";
  const rendered = t("workflowShare.publicOriginHint", { origin: sentinel });
  const idx = rendered.indexOf(sentinel);
  if (idx < 0) {
    return { before: rendered, after: "" };
  }
  return {
    before: rendered.slice(0, idx),
    after: rendered.slice(idx + sentinel.length),
  };
});

const ttlOptions = computed<
  { value: ColabOneWorkflowShareTTLMode; label: string }[]
>(() => [
  { value: "24h", label: t("workflowShare.ttl24h") },
  { value: "once", label: t("workflowShare.ttlOnce") },
  { value: "7d", label: t("workflowShare.ttl7d") },
  { value: "forever", label: t("workflowShare.ttlForever") },
]);

const isMutating = computed<boolean>(
  () =>
    Boolean(props.mutating) ||
    Boolean(props.creating) ||
    Boolean(props.revokingId) ||
    Boolean(props.deletingId),
);

const canClose = computed<boolean>(() => !isMutating.value);
const hasInnerOverlayOpen = computed<boolean>(() =>
  Boolean(openConfirmKey.value),
);

function setConfirmOpen(key: string, isOpen: boolean) {
  if (isOpen) {
    openConfirmKey.value = key;
  } else if (openConfirmKey.value === key) {
    openConfirmKey.value = "";
  }
}

const canCreate = computed<boolean>(
  () =>
    Boolean(props.webConsoleState?.running) &&
    selectedWorkflowIds.value.length > 0 &&
    !isMutating.value,
);

const visibleCreatedShare = computed<ColabOneWorkflowShareSummary | null>(
  () => {
    const created = props.lastCreatedShare;
    if (!created) return null;
    if (staleCreatedShareId.value && created.id === staleCreatedShareId.value) {
      return null;
    }
    const live = sharesList.value.find((item) => item.id === created.id);
    if (live) {
      return live.can_copy_link && shareCopyUrl(live) ? live : null;
    }
    if (seenCreatedInListId.value === created.id) {
      return null;
    }
    return shareCopyUrl(created) ? created : null;
  },
);

const visibleCreatedShareUrl = computed<string>(() =>
  visibleCreatedShare.value ? shareCopyUrl(visibleCreatedShare.value) : "",
);

function entryShareId(entry: ColabOneStudioWorkflowEntry): string {
  return entry.isGroup ? COLABONE_DIGITAL_HUMAN_GROUP_KEY : entry.primary.id;
}

function clearCopiedFeedback() {
  copySeq += 1;
  if (copiedTimer !== null) {
    clearTimeout(copiedTimer);
    copiedTimer = null;
  }
  copiedKey.value = "";
}

function onModalKeydown(event: KeyboardEvent) {
  if (!props.open || event.key !== "Escape") return;
  if (openConfirmKey.value) {
    event.preventDefault();
    openConfirmKey.value = "";
  }
}

function bindModalEscape() {
  if (escapeBound || typeof window === "undefined") return;
  escapeBound = true;
  window.addEventListener("keydown", onModalKeydown, true);
}

function unbindModalEscape() {
  if (!escapeBound || typeof window === "undefined") return;
  escapeBound = false;
  window.removeEventListener("keydown", onModalKeydown, true);
}

function ensureInitialSelection() {
  const validIds = new Set(studioEntries.value.map(entryShareId));
  const kept = selectedWorkflowIds.value.filter((id) => validIds.has(id));
  if (kept.length !== selectedWorkflowIds.value.length) {
    selectedWorkflowIds.value = kept;
  }
  if (
    props.open &&
    !userTouchedSelection.value &&
    selectedWorkflowIds.value.length === 0 &&
    studioEntries.value.length > 0
  ) {
    selectedWorkflowIds.value = [entryShareId(studioEntries.value[0])];
  }
}

watch(
  () => props.open,
  (isOpen, wasOpen) => {
    if (!isOpen) {
      unbindModalEscape();
      if (wasOpen) {
        staleCreatedShareId.value = props.lastCreatedShare?.id || "";
      }
      seenCreatedInListId.value = "";
      openConfirmKey.value = "";
      clearCopiedFeedback();
      return;
    }
    bindModalEscape();
    activeTab.value = "create";
    openConfirmKey.value = "";
    labelDraft.value = "";
    titleDraft.value = "";
    ttlMode.value = "24h";
    validationError.value = "";
    userTouchedSelection.value = false;
    selectedWorkflowIds.value =
      studioEntries.value.length > 0
        ? [entryShareId(studioEntries.value[0])]
        : [];
  },
  { immediate: true },
);

watch(studioEntries, () => {
  ensureInitialSelection();
});

watch(
  [activeTab, sharesList, () => props.lastCreatedShare?.id ?? "", isMutating],
  ([nextTab, list, createdId, mutating], prev) => {
    if (!prev || nextTab !== prev[0] || mutating) {
      openConfirmKey.value = "";
    }
    if (openConfirmKey.value) {
      const sepIdx = openConfirmKey.value.indexOf(":");
      const kind = sepIdx >= 0 ? openConfirmKey.value.slice(0, sepIdx) : "";
      const targetId = sepIdx >= 0 ? openConfirmKey.value.slice(sepIdx + 1) : "";
      const target = list.find((s) => s.id === targetId);
      if (!target) {
        openConfirmKey.value = "";
      } else if (kind === "revoke" && target.status === "revoked") {
        openConfirmKey.value = "";
      } else if (
        kind === "delete" &&
        !(canDeleteShare(target) && target.active_sessions > 0)
      ) {
        openConfirmKey.value = "";
      }
    }
    if (!createdId) {
      seenCreatedInListId.value = "";
    } else if (list.some((s) => s.id === createdId)) {
      seenCreatedInListId.value = createdId;
    } else if (seenCreatedInListId.value === createdId) {
      staleCreatedShareId.value = createdId;
    }
  },
  { immediate: true },
);

watch(
  () => props.lastCreatedShare,
  (next) => {
    if (!next || next.id !== staleCreatedShareId.value) {
      staleCreatedShareId.value = "";
    }
  },
);

onBeforeUnmount(() => {
  isUnmounted = true;
  unbindModalEscape();
  clearCopiedFeedback();
});

function requestClose() {
  if (!canClose.value) return;
  emit("update:open", false);
}

function isWorkflowSelected(id: string): boolean {
  return selectedWorkflowIds.value.includes(id);
}

function toggleWorkflow(id: string, checked: boolean) {
  if (isMutating.value) return;
  userTouchedSelection.value = true;
  const set = new Set(selectedWorkflowIds.value);
  if (checked) {
    set.add(id);
  } else {
    set.delete(id);
  }
  selectedWorkflowIds.value = studioEntries.value
    .map(entryShareId)
    .filter((entryId) => set.has(entryId));
  if (selectedWorkflowIds.value.length > 0) {
    validationError.value = "";
  }
}

function onTtlChange(value: string | number) {
  if (
    value === "24h" ||
    value === "once" ||
    value === "7d" ||
    value === "forever"
  ) {
    ttlMode.value = value;
  }
}

function handleCreate() {
  if (selectedWorkflowIds.value.length === 0) {
    validationError.value = t("workflowShare.errSelectWorkflow");
    return;
  }
  if (!props.webConsoleState?.running || isMutating.value) {
    return;
  }
  validationError.value = "";
  emit("create", {
    label: labelDraft.value.trim(),
    title: titleDraft.value.trim(),
    workflow_ids: [...selectedWorkflowIds.value],
    ttl_mode: ttlMode.value,
  });
}

function shareCopyUrl(share: ColabOneWorkflowShareSummary): string {
  return (share.url || share.local_url || "").trim();
}

async function handleCopyLink(url: string, key: string) {
  const cleanUrl = (url || "").trim();
  if (!cleanUrl) return;
  const seq = ++copySeq;
  let copied = false;
  if (props.copyLink) {
    try {
      copied = Boolean(await props.copyLink(cleanUrl));
    } catch {
      copied = false;
    }
  } else {
    try {
      await colabOneWriteClipboard(cleanUrl);
      copied = true;
    } catch {
      copied = false;
    }
    emit("copyLink", cleanUrl);
  }
  if (!copied || isUnmounted || !props.open || seq !== copySeq) {
    return;
  }
  clearCopiedFeedback();
  copiedKey.value = key;
  copiedTimer = setTimeout(() => {
    copiedKey.value = "";
    copiedTimer = null;
  }, 1800);
}

function statusTagColor(status: ColabOneWorkflowShareStatus): string {
  switch (status) {
    case "active":
      return "success";
    case "used":
      return "warning";
    case "revoked":
      return "error";
    default:
      return "default";
  }
}

function statusLabel(status: ColabOneWorkflowShareStatus): string {
  switch (status) {
    case "active":
      return t("workflowShare.statusActive");
    case "used":
      return t("workflowShare.statusUsed");
    case "revoked":
      return t("workflowShare.statusRevoked");
    default:
      return t("workflowShare.statusExpired");
  }
}

function canDeleteShare(share: ColabOneWorkflowShareSummary): boolean {
  return (
    share.status === "revoked" ||
    share.status === "expired" ||
    share.status === "used"
  );
}

function resolveShareWorkflowNames(workflowIds: readonly string[]): string[] {
  const list = Array.isArray(workflowIds) ? workflowIds : [];
  const entryById = new Map<string, ColabOneStudioWorkflowEntry>();
  for (const entry of studioEntries.value) {
    entryById.set(entryShareId(entry), entry);
    entryById.set(entry.key, entry);
  }
  const workflowById = new Map<string, WorkflowTemplate>();
  for (const wf of props.workflows || []) {
    if (wf?.id) {
      workflowById.set(wf.id, wf);
    }
  }

  const names: string[] = [];
  const seen = new Set<string>();
  for (const rawId of list) {
    const id = typeof rawId === "string" ? rawId.trim() : "";
    if (!id) continue;
    let resolved = id;
    if (id === COLABONE_DIGITAL_HUMAN_GROUP_KEY) {
      resolved = t("comfy.digitalHumanUnifiedName");
    } else {
      const matchedEntry = entryById.get(id);
      if (matchedEntry) {
        resolved = matchedEntry.isGroup
          ? t("comfy.digitalHumanUnifiedName")
          : tb(matchedEntry.primary.name);
      } else {
        const matchedWorkflow = workflowById.get(id);
        if (matchedWorkflow) {
          resolved = tb(matchedWorkflow.name);
        }
      }
    }
    if (!seen.has(resolved)) {
      seen.add(resolved);
      names.push(resolved);
    }
  }
  return names;
}

function formatShareExpiry(share: ColabOneWorkflowShareSummary): string {
  if (share.single_use || share.ttl_mode === "once") {
    return t("workflowShare.metaSingleUse");
  }
  if (share.ttl_mode === "forever" || !share.expires_at) {
    return t("workflowShare.metaNeverExpires");
  }
  const epoch = share.expires_at;
  const ms = epoch > 1e12 ? epoch : epoch * 1000;
  const dt = new Date(ms);
  if (Number.isNaN(dt.getTime())) {
    return t("workflowShare.metaNeverExpires");
  }
  const sameYear = dt.getFullYear() === new Date().getFullYear();
  let formatted = "";
  try {
    formatted = new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      ...(sameYear ? {} : { year: "numeric" }),
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }).format(dt);
  } catch {
    const pad = (n: number) => String(n).padStart(2, "0");
    formatted = `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())} ${pad(dt.getHours())}:${pad(dt.getMinutes())}`;
  }
  return t("workflowShare.metaExpiresAt", { time: formatted });
}

function entrySubtitle(entry: ColabOneStudioWorkflowEntry): string {
  if (entry.isGroup) {
    return t("workflowShare.digitalHumanGroupSub");
  }
  return entry.primary.description ? tb(entry.primary.description) : "";
}
</script>

<template>
  <AModal
    :open="open"
    :width="640"
    :style="{ maxWidth: 'calc(100vw - 24px)' }"
    :centered="true"
    :closable="canClose"
    :mask-closable="canClose"
    :keyboard="canClose && !hasInnerOverlayOpen"
    :footer="null"
    :z-index="1050"
    wrap-class-name="co-workflow-share-modal-wrap"
    @cancel="requestClose"
  >
    <template #title>
      <div class="co-ws-title">
        <span class="co-ws-title-icon" aria-hidden="true">
          <PhShareNetwork :size="17" weight="duotone" />
        </span>
        <span class="co-ws-title-text">{{ t("workflowShare.modalTitle") }}</span>
        <ATag
          v-if="activeShareCount > 0"
          color="blue"
          :bordered="false"
          class="co-ws-title-tag"
        >
          {{
            t("webConsole.workflowShareActiveTag", { count: activeShareCount })
          }}
        </ATag>
      </div>
    </template>

    <div class="co-ws-body" data-testid="workflow-share-modal">
      <!-- Web Console status banner -->
      <AAlert
        v-if="!webConsoleState.running"
        type="warning"
        show-icon
        :message="t('workflowShare.webConsoleOffTitle')"
        class="co-ws-offline-alert"
        data-testid="workflow-share-webconsole-off-alert"
      >
        <template #description>
          <div class="co-ws-offline-desc">
            <p class="co-ws-offline-copy">
              {{ t("workflowShare.webConsoleOffDesc") }}
            </p>
            <div class="co-ws-offline-actions">
              <AButton
                type="primary"
                size="small"
                :loading="enablingWebConsole"
                data-testid="workflow-share-enable-webconsole-btn"
                @click="emit('enableWebConsole')"
              >
                <template #icon><PhPower :size="14" /></template>
                {{ t("workflowShare.enableWebConsoleBtn") }}
              </AButton>
              <AButton
                size="small"
                type="link"
                class="co-ws-inline-link-btn"
                data-testid="workflow-share-open-settings-btn"
                @click="emit('openWebConsoleSettings')"
              >
                <template #icon><PhGearSix :size="14" /></template>
                {{ t("workflowShare.openWebConsoleSettingsBtn") }}
              </AButton>
            </div>
          </div>
        </template>
      </AAlert>

      <div
        v-else-if="!publicOrigin"
        class="co-ws-origin-banner is-local"
        data-testid="workflow-share-local-origin-hint"
      >
        <PhInfo :size="15" weight="duotone" class="co-ws-origin-icon" aria-hidden="true" />
        <span class="co-ws-origin-text">
          {{ t("workflowShare.noPublicOriginHint") }}
        </span>
        <AButton
          size="small"
          type="link"
          class="co-ws-origin-settings-btn"
          data-testid="workflow-share-open-settings-btn"
          @click="emit('openWebConsoleSettings')"
        >
          {{ t("workflowShare.openWebConsoleSettingsBtn") }}
        </AButton>
      </div>

      <div
        v-else
        class="co-ws-origin-banner is-public"
        data-testid="workflow-share-public-origin-hint"
      >
        <PhGlobe :size="15" weight="duotone" class="co-ws-origin-icon" aria-hidden="true" />
        <span class="co-ws-origin-text">
          {{ publicOriginHintParts.before
          }}<code class="co-ws-inline-code"
            ><ColabOnePrivacyText :text="publicOrigin" kind="url" /></code
          >{{ publicOriginHintParts.after }}
        </span>
      </div>

      <ATabs
        v-model:active-key="activeTab"
        class="co-ws-tabs"
        data-testid="workflow-share-tabs"
      >
        <!-- =============================================================== -->
        <!-- TAB 1: CREATE NEW SHARE                                         -->
        <!-- =============================================================== -->
        <ATabPane key="create" :tab="t('workflowShare.tabCreate')">
          <div class="co-ws-tab-panel" data-testid="workflow-share-create-panel">
            <!-- Workflow checklist -->
            <div class="co-ws-section">
              <div class="co-ws-section-head">
                <span class="co-ws-section-title">
                  {{ t("workflowShare.selectWorkflowsLabel") }}
                </span>
                <span class="co-ws-section-hint">
                  {{ t("workflowShare.selectWorkflowsHint") }}
                </span>
              </div>

              <div v-if="studioEntries.length === 0" class="co-ws-empty-wf">
                {{ t("workflowShare.noWorkflowsHint") }}
              </div>

              <div v-else class="co-ws-wf-list">
                <div
                  v-for="entry in studioEntries"
                  :key="entry.key"
                  class="co-ws-wf-item"
                  :class="{
                    'is-selected': isWorkflowSelected(entryShareId(entry)),
                    'is-disabled': isMutating,
                  }"
                  :data-testid="`workflow-share-wf-${entryShareId(entry)}`"
                  @click="
                    toggleWorkflow(
                      entryShareId(entry),
                      !isWorkflowSelected(entryShareId(entry)),
                    )
                  "
                >
                  <span class="co-ws-wf-checkbox-wrap" @click.stop>
                    <ACheckbox
                      :checked="isWorkflowSelected(entryShareId(entry))"
                      :disabled="isMutating"
                      :aria-label="
                        entry.isGroup
                          ? t('comfy.digitalHumanUnifiedName')
                          : tb(entry.primary.name)
                      "
                      @update:checked="
                        (val) => toggleWorkflow(entryShareId(entry), Boolean(val))
                      "
                    />
                  </span>
                  <span
                    class="co-ws-wf-icon"
                    :class="{
                      'is-pipeline':
                        entry.primary.pipeline?.kind === 'digital_human' ||
                        entry.primary.pipeline?.kind === 'fashion_tryon',
                    }"
                    aria-hidden="true"
                  >
                    <PhVideoCamera
                      v-if="entry.primary.pipeline?.kind === 'digital_human'"
                      :size="16"
                      weight="duotone"
                    />
                    <PhTShirt
                      v-else-if="
                        entry.primary.pipeline?.kind === 'fashion_tryon'
                      "
                      :size="16"
                      weight="duotone"
                    />
                    <PhTreeStructure v-else :size="16" weight="duotone" />
                  </span>
                  <div class="co-ws-wf-copy">
                    <div class="co-ws-wf-head">
                      <span class="co-ws-wf-name">
                        {{
                          entry.isGroup
                            ? t("comfy.digitalHumanUnifiedName")
                            : tb(entry.primary.name)
                        }}
                      </span>
                      <ATag
                        :color="
                          entry.isGroup || entry.primary.source === 'builtin'
                            ? 'blue'
                            : 'default'
                        "
                        :bordered="false"
                        class="co-ws-wf-tag"
                      >
                        {{
                          entry.isGroup || entry.primary.source === "builtin"
                            ? t("comfy.sourceBuiltin")
                            : t("comfy.sourceCustom")
                        }}
                      </ATag>
                    </div>
                    <p v-if="entrySubtitle(entry)" class="co-ws-wf-desc">
                      {{ entrySubtitle(entry) }}
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <!-- Optional Remark & Guest Page Title -->
            <div class="co-ws-fields-grid">
              <div class="co-ws-field">
                <label for="co-ws-label-input" class="co-ws-field-label">
                  {{ t("workflowShare.labelField") }}
                </label>
                <AInput
                  id="co-ws-label-input"
                  v-model:value="labelDraft"
                  :maxlength="80"
                  allow-clear
                  :disabled="creating"
                  :placeholder="t('workflowShare.labelPlaceholder')"
                  data-testid="workflow-share-label-input"
                />
              </div>

              <div class="co-ws-field">
                <label for="co-ws-title-input" class="co-ws-field-label">
                  {{ t("workflowShare.titleField") }}
                </label>
                <AInput
                  id="co-ws-title-input"
                  v-model:value="titleDraft"
                  :maxlength="80"
                  allow-clear
                  :disabled="creating"
                  :placeholder="t('workflowShare.titlePlaceholder')"
                  data-testid="workflow-share-title-input"
                />
              </div>
            </div>

            <!-- TTL Validity Mode -->
            <div class="co-ws-field">
              <span class="co-ws-field-label">
                {{ t("workflowShare.ttlLabel") }}
              </span>
              <div class="co-ws-ttl-wrap">
                <ASegmented
                  :value="ttlMode"
                  :options="ttlOptions"
                  :disabled="creating"
                  block
                  class="co-ws-ttl-segmented"
                  data-testid="workflow-share-ttl-segmented"
                  @change="onTtlChange"
                />
              </div>
              <p class="co-ws-hint">
                {{ t("workflowShare.ttlHint") }}
              </p>
            </div>

            <p
              v-if="validationError"
              class="co-ws-field-error"
              role="alert"
              data-testid="workflow-share-validation-error"
            >
              {{ validationError }}
            </p>

            <!-- Create CTA -->
            <AButton
              type="primary"
              size="large"
              block
              :loading="creating"
              :disabled="!canCreate"
              class="co-ws-create-btn"
              data-testid="workflow-share-create-btn"
              @click="handleCreate"
            >
              <template #icon><PhLinkSimple :size="16" /></template>
              {{ t("workflowShare.createAndCopyBtn") }}
            </AButton>

            <!-- Highlight card for freshly created link -->
            <div
              v-if="visibleCreatedShare"
              class="co-ws-created-card"
              data-testid="workflow-share-created-card"
            >
              <div class="co-ws-created-head">
                <PhCheckCircle
                  :size="16"
                  weight="fill"
                  class="co-ws-created-icon"
                  aria-hidden="true"
                />
                <span class="co-ws-created-title">
                  {{
                    lastCreatedCopied
                      ? t("workflowShare.createdLinkTitle")
                      : t("workflowShare.createdLinkTitleOnly")
                  }}
                </span>
              </div>
              <div class="co-ws-created-url-row">
                <code class="co-ws-created-url">
                  <ColabOnePrivacyText
                    :text="visibleCreatedShareUrl"
                    kind="url"
                  />
                </code>
                <AButton
                  size="small"
                  type="primary"
                  ghost
                  class="co-ws-created-copy-btn"
                  data-testid="workflow-share-copy-created-btn"
                  @click="handleCopyLink(visibleCreatedShareUrl, 'created')"
                >
                  <template #icon>
                    <PhCheck v-if="copiedKey === 'created'" :size="13" />
                    <PhCopy v-else :size="13" />
                  </template>
                  {{ t("workflowShare.copyLinkBtn") }}
                </AButton>
              </div>
            </div>
          </div>
        </ATabPane>

        <!-- =============================================================== -->
        <!-- TAB 2: MANAGE EXISTING SHARES                                   -->
        <!-- =============================================================== -->
        <ATabPane key="manage">
          <template #tab>
            <span class="co-ws-manage-tab-label">
              <span>{{ t("workflowShare.tabManage") }}</span>
              <span
                v-if="activeShareCount > 0"
                class="co-ws-tab-badge"
                data-testid="workflow-share-active-badge"
              >
                {{ activeShareCount }}
              </span>
            </span>
          </template>

          <div class="co-ws-tab-panel" data-testid="workflow-share-manage-panel">
            <div
              v-if="loading && sharesList.length === 0"
              class="co-ws-loading"
              role="status"
              aria-busy="true"
            >
              <ASpin size="small" />
              <span>{{ t("common.loading") }}</span>
            </div>

            <div
              v-else-if="sharesList.length === 0"
              class="co-ws-empty-state"
              data-testid="workflow-share-empty-state"
            >
              <AEmpty :image="AEmpty.PRESENTED_IMAGE_SIMPLE">
                <template #description>
                  <div class="co-ws-empty-copy">
                    <strong class="co-ws-empty-title">
                      {{ t("workflowShare.emptyShares") }}
                    </strong>
                    <span class="co-ws-empty-desc">
                      {{ t("workflowShare.emptySharesDesc") }}
                    </span>
                  </div>
                </template>
                <AButton
                  type="primary"
                  size="small"
                  @click="activeTab = 'create'"
                >
                  {{ t("workflowShare.tabCreate") }}
                </AButton>
              </AEmpty>
            </div>

            <div v-else class="co-ws-share-list">
              <div
                v-for="share in sharesList"
                :key="share.id"
                class="co-ws-share-card"
                :class="{ 'is-inactive': share.status !== 'active' }"
                :data-testid="`workflow-share-card-${share.id}`"
              >
                <div class="co-ws-share-head">
                  <div class="co-ws-share-title-block">
                    <div class="co-ws-share-title-line">
                      <strong class="co-ws-share-remark">
                        {{ share.label || t("workflowShare.defaultRemark") }}
                      </strong>
                      <ATag
                        :color="statusTagColor(share.status)"
                        :bordered="false"
                        class="co-ws-status-tag"
                      >
                        {{ statusLabel(share.status) }}
                      </ATag>
                    </div>
                    <span v-if="share.title" class="co-ws-share-subtitle">
                      {{ share.title }}
                    </span>
                  </div>
                </div>

                <!-- Shared workflow chips -->
                <div
                  v-if="resolveShareWorkflowNames(share.workflow_ids).length > 0"
                  class="co-ws-chip-row"
                >
                  <span
                    v-for="wfName in resolveShareWorkflowNames(share.workflow_ids)"
                    :key="wfName"
                    class="co-ws-wf-chip"
                  >
                    {{ wfName }}
                  </span>
                </div>

                <!-- Meta strip -->
                <div class="co-ws-meta-strip">
                  <span class="co-ws-meta-item">
                    <PhClock :size="13" aria-hidden="true" />
                    <span>{{ formatShareExpiry(share) }}</span>
                  </span>
                  <span class="co-ws-meta-dot" aria-hidden="true">·</span>
                  <span class="co-ws-meta-item">
                    <PhUsers :size="13" aria-hidden="true" />
                    <span>
                      {{
                        t("workflowShare.metaSessions", {
                          count: share.active_sessions,
                        })
                      }}
                    </span>
                  </span>
                  <span class="co-ws-meta-dot" aria-hidden="true">·</span>
                  <span class="co-ws-meta-item">
                    <PhImage :size="13" aria-hidden="true" />
                    <span>
                      {{
                        t("workflowShare.metaOutputs", {
                          count: share.output_count,
                        })
                      }}
                    </span>
                  </span>
                </div>

                <!-- Card actions -->
                <div class="co-ws-share-actions">
                  <AButton
                    v-if="share.can_copy_link && shareCopyUrl(share)"
                    size="small"
                    :data-testid="`workflow-share-copy-${share.id}`"
                    @click="handleCopyLink(shareCopyUrl(share), share.id)"
                  >
                    <template #icon>
                      <PhCheck v-if="copiedKey === share.id" :size="13" />
                      <PhCopy v-else :size="13" />
                    </template>
                    {{ t("workflowShare.copyLinkBtn") }}
                  </AButton>

                  <APopconfirm
                    v-if="share.status !== 'revoked'"
                    :open="openConfirmKey === `revoke:${share.id}`"
                    :title="
                      t('workflowShare.revokeConfirmTitle', {
                        name: share.label || t('workflowShare.defaultRemark'),
                      })
                    "
                    :description="t('workflowShare.revokeConfirmDesc')"
                    :ok-text="t('workflowShare.revokeBtn')"
                    :cancel-text="t('common.cancel')"
                    :ok-button-props="{ danger: true }"
                    :overlay-style="{ zIndex: 1080 }"
                    :disabled="
                      isMutating ||
                      revokingId === share.id ||
                      deletingId === share.id
                    "
                    @open-change="(val) => setConfirmOpen(`revoke:${share.id}`, val)"
                    @confirm="emit('revoke', share.id)"
                  >
                    <AButton
                      size="small"
                      danger
                      :loading="revokingId === share.id"
                      :disabled="isMutating || deletingId === share.id"
                      :data-testid="`workflow-share-revoke-${share.id}`"
                    >
                      <template #icon><PhProhibit :size="13" /></template>
                      {{ t("workflowShare.revokeBtn") }}
                    </AButton>
                  </APopconfirm>

                  <APopconfirm
                    v-if="canDeleteShare(share) && share.active_sessions > 0"
                    :open="openConfirmKey === `delete:${share.id}`"
                    :title="
                      t('workflowShare.deleteConfirmActiveTitle', {
                        name: share.label || t('workflowShare.defaultRemark'),
                      })
                    "
                    :description="
                      t('workflowShare.deleteConfirmActiveDesc', {
                        count: share.active_sessions,
                      })
                    "
                    :ok-text="t('workflowShare.deleteRecordBtn')"
                    :cancel-text="t('common.cancel')"
                    :ok-button-props="{ danger: true }"
                    :overlay-style="{ zIndex: 1080 }"
                    :disabled="
                      isMutating ||
                      revokingId === share.id ||
                      deletingId === share.id
                    "
                    @open-change="(val) => setConfirmOpen(`delete:${share.id}`, val)"
                    @confirm="emit('delete', share.id)"
                  >
                    <AButton
                      size="small"
                      :loading="deletingId === share.id"
                      :disabled="isMutating || revokingId === share.id"
                      :data-testid="`workflow-share-delete-${share.id}`"
                    >
                      <template #icon><PhTrash :size="13" /></template>
                      {{ t("workflowShare.deleteRecordBtn") }}
                    </AButton>
                  </APopconfirm>

                  <AButton
                    v-else-if="canDeleteShare(share)"
                    size="small"
                    :loading="deletingId === share.id"
                    :disabled="isMutating || revokingId === share.id"
                    :data-testid="`workflow-share-delete-${share.id}`"
                    @click="emit('delete', share.id)"
                  >
                    <template #icon><PhTrash :size="13" /></template>
                    {{ t("workflowShare.deleteRecordBtn") }}
                  </AButton>
                </div>
              </div>
            </div>
          </div>
        </ATabPane>
      </ATabs>
    </div>
  </AModal>
</template>

<style scoped>
.co-ws-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-wrap: wrap;
}

.co-ws-title-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-ws-title-icon {
  color: var(--co-accent-strong);
}

.co-ws-title-text {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-ws-title-tag {
  margin-inline-end: 0;
  font-size: 11.5px;
  line-height: 19px;
}

.co-ws-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 2px;
}

.co-ws-offline-alert {
  border-radius: 10px;
}

.co-ws-offline-desc {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-ws-offline-copy {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-ws-offline-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
}

.co-ws-inline-link-btn {
  padding-inline: 4px;
}

.co-ws-origin-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-ws-origin-banner.is-public {
  border-color: rgba(59, 130, 246, 0.24);
  background: var(--co-selected);
}

.co-ws-origin-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-ws-origin-icon {
  color: var(--co-accent-strong);
}

.co-ws-origin-text {
  flex: 1;
  min-width: 180px;
}

.co-ws-origin-settings-btn {
  padding: 0 4px;
  height: auto;
  font-size: 12px;
  flex-shrink: 0;
}

.co-ws-inline-code {
  font-family: var(--co-font-mono);
  font-size: 11.5px;
  padding: 1px 6px;
  border-radius: 5px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-text);
  word-break: break-all;
}

.co-ws-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 12px;
}

.co-ws-manage-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-ws-tab-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--co-selected);
  color: var(--co-accent-strong);
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.co-ws-tab-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.co-ws-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.co-ws-section-head {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-ws-section-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-ws-section-hint {
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-ws-empty-wf {
  padding: 14px 12px;
  border-radius: 10px;
  border: 1px dashed var(--co-border);
  background: var(--co-subtle);
  font-size: 12.5px;
  color: var(--co-muted);
  text-align: center;
}

.co-ws-wf-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 224px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-ws-wf-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 9px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}

.co-ws-wf-item:hover:not(.is-disabled) {
  border-color: var(--co-border);
}

.co-ws-wf-item.is-selected {
  border-color: rgba(59, 130, 246, 0.36);
  background: var(--co-selected);
}

.co-ws-wf-item.is-disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.co-ws-wf-icon {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  margin-top: 1px;
  background: var(--co-surface);
  color: var(--co-secondary-text);
  border: 1px solid var(--co-border-secondary);
}

.co-ws-wf-icon.is-pipeline {
  color: var(--co-accent);
}

:root[data-theme="dark"] .co-ws-wf-icon.is-pipeline {
  color: var(--co-accent-strong);
}

.co-ws-wf-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-ws-wf-head {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.co-ws-wf-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
  line-height: 1.35;
}

.co-ws-wf-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 17px;
}

.co-ws-wf-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.co-ws-fields-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.co-ws-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.co-ws-field-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-text);
}

.co-ws-ttl-wrap {
  width: 100%;
  overflow-x: auto;
}

.co-ws-ttl-segmented {
  width: 100%;
}

.co-ws-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-ws-field-error {
  margin: 0;
  font-size: 12px;
  color: var(--co-danger-text);
}

.co-ws-create-btn {
  font-weight: 500;
}

.co-ws-created-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 11px 13px;
  border-radius: 10px;
  border: 1px solid var(--co-success-border);
  background: var(--co-success-bg);
}

.co-ws-created-head {
  display: flex;
  align-items: center;
  gap: 6px;
}

.co-ws-created-icon {
  color: var(--co-success);
  flex-shrink: 0;
}

.co-ws-created-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-success-text);
}

.co-ws-created-url-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 9px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-success-border);
}

.co-ws-created-url {
  flex: 1;
  min-width: 0;
  font-family: var(--co-font-mono);
  font-size: 12px;
  color: var(--co-text);
  word-break: break-all;
}

.co-ws-created-copy-btn {
  flex-shrink: 0;
}

.co-ws-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 28px 0;
  color: var(--co-muted);
  font-size: 13px;
}

.co-ws-empty-state {
  padding: 16px 0 8px;
}

.co-ws-empty-copy {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.co-ws-empty-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-ws-empty-desc {
  font-size: 12px;
  color: var(--co-muted);
}

.co-ws-share-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 410px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-ws-share-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  transition: border-color 0.15s ease;
}

.co-ws-share-card:hover {
  border-color: var(--co-border);
}

.co-ws-share-card.is-inactive {
  opacity: 0.78;
}

.co-ws-share-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.co-ws-share-title-block {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.co-ws-share-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-ws-share-remark {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  word-break: break-word;
}

.co-ws-status-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
}

.co-ws-share-subtitle {
  font-size: 12px;
  color: var(--co-secondary-text);
  word-break: break-word;
}

.co-ws-chip-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
}

.co-ws-wf-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 6px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-size: 11.5px;
  color: var(--co-secondary-text);
  line-height: 1.4;
}

.co-ws-meta-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 12px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-ws-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-ws-meta-dot {
  color: var(--co-muted);
  opacity: 0.65;
}

.co-ws-share-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 4px;
  border-top: 1px solid var(--co-border-secondary);
}

@media (max-width: 560px) {
  .co-ws-fields-grid {
    grid-template-columns: 1fr;
  }

  .co-ws-created-url-row {
    flex-direction: column;
    align-items: stretch;
  }

  .co-ws-ttl-wrap {
    -webkit-overflow-scrolling: touch;
    scrollbar-width: none;
  }

  .co-ws-ttl-wrap::-webkit-scrollbar {
    display: none;
  }

  .co-ws-ttl-segmented {
    width: max-content;
    min-width: 100%;
  }

  .co-ws-ttl-segmented :deep(.ant-segmented-group) {
    flex-wrap: nowrap;
  }

  .co-ws-ttl-segmented :deep(.ant-segmented-item) {
    flex: 0 0 auto;
  }

  .co-ws-ttl-segmented :deep(.ant-segmented-item-label) {
    padding-inline: 10px;
    font-size: 12px;
    white-space: nowrap;
    text-overflow: clip;
    overflow: visible;
  }
}
</style>
