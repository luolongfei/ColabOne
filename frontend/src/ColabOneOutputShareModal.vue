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
  Popover as APopover,
  Segmented as ASegmented,
  Switch as ASwitch,
  TabPane as ATabPane,
  Tabs as ATabs,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowsClockwise,
  PhArrowSquareOut,
  PhCaretDown,
  PhCaretUp,
  PhCheck,
  PhCheckCircle,
  PhClock,
  PhCopy,
  PhDownloadSimple,
  PhImage,
  PhImages,
  PhInfo,
  PhKey,
  PhLinkSimple,
  PhLock,
  PhLockOpen,
  PhPlus,
  PhPower,
  PhProhibit,
  PhShareNetwork,
  PhTrash,
  PhUsers,
  PhVideoCamera,
  PhWaveform,
  PhX,
} from "@phosphor-icons/vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import { colabOneWriteClipboard } from "./api";
import {
  buildColabOneOutputShareGroupInputs,
  isColabOneInspectOutput,
  type ColabOneOutputGroup,
} from "./colabOneOutputGallery";
import {
  generateColabOneSharePassword,
  type ColabOneOutputShareCreateRequest,
  type ColabOneOutputShareListState,
  type ColabOneOutputShareStatus,
  type ColabOneOutputShareSummary,
  type ColabOneOutputShareTTLMode,
} from "./colabOneOutputShare";
import type { ColabOnePrivacyContext } from "./colabOnePrivacy";
import type { ColabOneWebConsoleState } from "./colabOneWebConsole";
import { colabOneLocaleCode, t, tb } from "./i18n";
import type { WorkflowOutput } from "./types";

defineOptions({
  name: "ColabOneOutputShareModal",
});

const props = withDefaults(
  defineProps<{
    open: boolean;
    initialTab?: "create" | "manage";
    allGroups: ColabOneOutputGroup[];
    initialGroupIds: string[];
    sharesState: ColabOneOutputShareListState;
    webConsoleState: ColabOneWebConsoleState;
    privacyContext?: ColabOnePrivacyContext;
    resolveMediaSrc: (item: WorkflowOutput) => string;
    canEnableWebConsole?: boolean;
    enablingWebConsole?: boolean;
    creating?: boolean;
    revokingId?: string;
    deletingId?: string;
    lastCreatedShare?: ColabOneOutputShareSummary | null;
    copyText?: (text: string, successToast?: string) => Promise<boolean>;
  }>(),
  {
    initialTab: "create",
    privacyContext: undefined,
    canEnableWebConsole: true,
    enablingWebConsole: false,
    creating: false,
    revokingId: "",
    deletingId: "",
    lastCreatedShare: null,
    copyText: undefined,
  },
);

const emit = defineEmits<{
  close: [];
  "enable-web-console": [];
  create: [req: ColabOneOutputShareCreateRequest];
  revoke: [id: string];
  delete: [id: string];
  "open-url": [url: string, shareId?: string];
  "request-load": [item: WorkflowOutput];
}>();

const activeTab = ref<"create" | "manage">("create");
const selectedGroupOrder = ref<string[]>([]);
const selectedIdsByGroup = ref<Record<string, string[]>>({});
const expandedGroupIds = ref<Record<string, boolean>>({});
const addMoreOpen = ref(false);
const openConfirmKey = ref("");

const titleDraft = ref("");
const userEditedTitle = ref(false);
const noteDraft = ref("");
const ttlMode = ref<ColabOneOutputShareTTLMode>("7d");
const allowDownload = ref(true);
const passwordEnabled = ref(false);
const passwordDraft = ref("");
const validationError = ref("");

const staleCreatedShareId = ref("");
const seenCreatedInListId = ref("");
const lastSubmittedPassword = ref("");
const copiedKey = ref("");
let copiedTimer: ReturnType<typeof setTimeout> | null = null;
let copySeq = 0;
let isUnmounted = false;

const groupById = computed<Map<string, ColabOneOutputGroup>>(() => {
  const map = new Map<string, ColabOneOutputGroup>();
  for (const group of props.allGroups || []) {
    if (group?.id) {
      map.set(group.id, group);
    }
  }
  return map;
});

const selectedGroups = computed<ColabOneOutputGroup[]>(() => {
  const result: ColabOneOutputGroup[] = [];
  for (const id of selectedGroupOrder.value) {
    const group = groupById.value.get(id);
    if (group) {
      result.push(group);
    }
  }
  return result;
});

const unselectedGroups = computed<ColabOneOutputGroup[]>(() => {
  const selectedSet = new Set(selectedGroupOrder.value);
  return (props.allGroups || []).filter((g) => g?.id && !selectedSet.has(g.id));
});

const totalSelectedItemCount = computed<number>(() => {
  let total = 0;
  for (const group of selectedGroups.value) {
    total += getGroupSelectedCount(group);
  }
  return total;
});

const nonEmptySelectedGroupCount = computed<number>(() =>
  selectedGroups.value.filter((g) => getGroupSelectedCount(g) > 0).length,
);

const suggestedTitle = computed<string>(() => {
  const active = selectedGroups.value.filter((g) => getGroupSelectedCount(g) > 0);
  if (active.length === 0) return "";
  const firstTitle = tb(active[0].title);
  if (active.length === 1) return firstTitle;
  return `${firstTitle} +${active.length - 1}`;
});

const sharesList = computed<ColabOneOutputShareSummary[]>(() =>
  Array.isArray(props.sharesState?.shares) ? props.sharesState.shares : [],
);

const hasPublicOrigin = computed<boolean>(() =>
  Boolean((props.webConsoleState?.public_origin || "").trim()),
);

const isMutating = computed<boolean>(
  () =>
    Boolean(props.creating) ||
    Boolean(props.revokingId) ||
    Boolean(props.deletingId),
);

const canClose = computed<boolean>(() => !isMutating.value);
const hasInnerOverlayOpen = computed<boolean>(
  () => addMoreOpen.value || Boolean(openConfirmKey.value),
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
    totalSelectedItemCount.value > 0 &&
    !isMutating.value,
);

const visibleCreatedShare = computed<ColabOneOutputShareSummary | null>(() => {
  const created = props.lastCreatedShare;
  if (!created) return null;
  if (staleCreatedShareId.value && created.id === staleCreatedShareId.value) {
    return null;
  }
  const live = sharesList.value.find((item) => item.id === created.id);
  if (!live && seenCreatedInListId.value === created.id) {
    return null;
  }
  const candidate = live ?? created;
  return shareCopyUrl(candidate) ? candidate : null;
});

const visibleCreatedShareUrl = computed<string>(() =>
  visibleCreatedShare.value ? shareCopyUrl(visibleCreatedShare.value) : "",
);

const visibleCreatedSharePassword = computed<string>(() => {
  const share = visibleCreatedShare.value;
  if (!share || !share.has_password) return "";
  return (share.password || lastSubmittedPassword.value || "").trim();
});

const ttlOptions = computed<
  Array<{ value: ColabOneOutputShareTTLMode; label: string }>
>(() => [
  { value: "24h", label: t("outputShare.ttl24h") },
  { value: "7d", label: t("outputShare.ttl7d") },
  { value: "30d", label: t("outputShare.ttl30d") },
  { value: "forever", label: t("outputShare.ttlForever") },
]);

function getGroupSelectedIds(group: ColabOneOutputGroup): string[] {
  const valid = new Set(group.items.map((i) => i.id));
  const raw = selectedIdsByGroup.value[group.id] ?? [];
  return raw.filter((id) => valid.has(id));
}

function getGroupSelectedCount(group: ColabOneOutputGroup): number {
  return getGroupSelectedIds(group).length;
}

function isItemSelectedInGroup(groupId: string, itemId: string): boolean {
  const list = selectedIdsByGroup.value[groupId];
  return Array.isArray(list) && list.includes(itemId);
}

function requestLoadForItem(item: WorkflowOutput | undefined) {
  if (!item || item.kind !== "image") return;
  if (!props.resolveMediaSrc(item)) {
    emit("request-load", item);
  }
}

function requestCoverLoad(group: ColabOneOutputGroup) {
  requestLoadForItem(group.coverItems[0] ?? group.items[0]);
}

function addGroupToSelection(groupId: string) {
  const group = groupById.value.get(groupId);
  if (!group) return;
  if (!selectedGroupOrder.value.includes(group.id)) {
    selectedGroupOrder.value = [...selectedGroupOrder.value, group.id];
  }
  selectedIdsByGroup.value = {
    ...selectedIdsByGroup.value,
    [group.id]: group.items.map((item) => item.id),
  };
  validationError.value = "";
  requestCoverLoad(group);
  if (!userEditedTitle.value) {
    titleDraft.value = suggestedTitle.value;
  }
  if (unselectedGroups.value.length === 0) {
    addMoreOpen.value = false;
  }
}

function removeGroupFromSelection(groupId: string) {
  selectedGroupOrder.value = selectedGroupOrder.value.filter(
    (id) => id !== groupId,
  );
  const nextIds = { ...selectedIdsByGroup.value };
  delete nextIds[groupId];
  selectedIdsByGroup.value = nextIds;

  const nextExpanded = { ...expandedGroupIds.value };
  delete nextExpanded[groupId];
  expandedGroupIds.value = nextExpanded;

  if (!userEditedTitle.value) {
    titleDraft.value = suggestedTitle.value;
  }
}

function toggleGroupExpanded(group: ColabOneOutputGroup) {
  const nextOpen = !expandedGroupIds.value[group.id];
  expandedGroupIds.value = {
    ...expandedGroupIds.value,
    [group.id]: nextOpen,
  };
  if (nextOpen) {
    for (const item of group.items) {
      requestLoadForItem(item);
    }
  }
}

function toggleItemInGroup(
  group: ColabOneOutputGroup,
  itemId: string,
  checked: boolean,
) {
  if (isMutating.value) return;
  const current = new Set(getGroupSelectedIds(group));
  if (checked) {
    current.add(itemId);
  } else {
    current.delete(itemId);
  }
  selectedIdsByGroup.value = {
    ...selectedIdsByGroup.value,
    [group.id]: group.items
      .map((item) => item.id)
      .filter((id) => current.has(id)),
  };
  if (totalSelectedItemCount.value > 0) {
    validationError.value = "";
  }
}

function selectAllInGroup(group: ColabOneOutputGroup) {
  selectedIdsByGroup.value = {
    ...selectedIdsByGroup.value,
    [group.id]: group.items.map((item) => item.id),
  };
  validationError.value = "";
}

function excludeInspectInGroup(group: ColabOneOutputGroup) {
  const primaryIds = group.items
    .filter((item) => !isColabOneInspectOutput(item))
    .map((item) => item.id);
  if (primaryIds.length === 0) return;
  selectedIdsByGroup.value = {
    ...selectedIdsByGroup.value,
    [group.id]: primaryIds,
  };
  validationError.value = "";
}

function onTtlChange(val: string | number) {
  if (val === "24h" || val === "7d" || val === "30d" || val === "forever") {
    ttlMode.value = val;
  }
}

function onTogglePassword(checked: boolean | string | number) {
  const enabled = Boolean(checked);
  passwordEnabled.value = enabled;
  if (enabled && !passwordDraft.value.trim()) {
    passwordDraft.value = generateColabOneSharePassword(8);
  }
  if (!enabled) {
    validationError.value = "";
  }
}

function regeneratePassword() {
  passwordDraft.value = generateColabOneSharePassword(8);
  validationError.value = "";
}

function clearCopiedFeedback() {
  copySeq += 1;
  if (copiedTimer !== null) {
    clearTimeout(copiedTimer);
    copiedTimer = null;
  }
  copiedKey.value = "";
}

function shareCopyUrl(share: ColabOneOutputShareSummary): string {
  return (share.url || share.local_url || "").trim();
}

function buildLinkWithPasswordText(
  share: ColabOneOutputShareSummary,
  password: string,
): string {
  const url = shareCopyUrl(share);
  const title = share.title || t("outputShare.guestDefaultTitle");
  const lines = [
    title,
    `${t("outputShare.linkLabel")}: ${url}`,
    `${t("outputShare.passwordValueLabel")}: ${password}`,
  ];
  return lines.join("\n");
}

async function performCopy(
  text: string,
  key: string,
  successToast: string,
): Promise<void> {
  const clean = (text || "").trim();
  if (!clean) return;
  const seq = ++copySeq;
  let ok = false;
  if (props.copyText) {
    try {
      ok = Boolean(await props.copyText(clean, successToast));
    } catch {
      ok = false;
    }
  } else {
    try {
      await colabOneWriteClipboard(clean);
      ok = true;
    } catch {
      ok = false;
    }
  }
  if (!ok || isUnmounted || !props.open || seq !== copySeq) {
    return;
  }
  clearCopiedFeedback();
  copiedKey.value = key;
  copiedTimer = setTimeout(() => {
    copiedKey.value = "";
    copiedTimer = null;
  }, 1800);
}

function handleCreate() {
  const groupsInput = buildColabOneOutputShareGroupInputs(
    selectedGroups.value,
    selectedIdsByGroup.value,
  );
  if (groupsInput.length === 0) {
    validationError.value = t("outputShare.errSelectAtLeastOne");
    return;
  }
  const trimmedPassword = passwordDraft.value.trim();
  if (passwordEnabled.value) {
    if (/[\u0000-\u001F\u007F-\u009F]/.test(trimmedPassword)) {
      validationError.value = t("outputShare.errPasswordControlChars");
      return;
    }
    const pwdCodePoints = Array.from(trimmedPassword).length;
    if (pwdCodePoints < 4 || pwdCodePoints > 32) {
      validationError.value = t("outputShare.errPasswordLength");
      return;
    }
  }
  if (!props.webConsoleState?.running || isMutating.value) {
    return;
  }

  validationError.value = "";
  lastSubmittedPassword.value = passwordEnabled.value ? trimmedPassword : "";

  const req: ColabOneOutputShareCreateRequest = {
    title: titleDraft.value.trim() || suggestedTitle.value,
    groups: groupsInput,
    ttl_mode: ttlMode.value,
    allow_download: allowDownload.value,
  };
  if (noteDraft.value.trim()) {
    req.note = noteDraft.value.trim();
  }
  if (passwordEnabled.value) {
    req.password = trimmedPassword;
  }
  emit("create", req);
}

function resetCreateFormForAnother() {
  if (visibleCreatedShare.value) {
    staleCreatedShareId.value = visibleCreatedShare.value.id;
  }
  lastSubmittedPassword.value = "";
  validationError.value = "";
  noteDraft.value = "";
  clearCopiedFeedback();
}

function requestClose() {
  if (!canClose.value) return;
  addMoreOpen.value = false;
  emit("close");
}

function statusTagColor(status: ColabOneOutputShareStatus): string {
  switch (status) {
    case "active":
      return "success";
    case "revoked":
      return "error";
    default:
      return "default";
  }
}

function statusLabel(status: ColabOneOutputShareStatus): string {
  switch (status) {
    case "active":
      return t("outputShare.statusActive");
    case "revoked":
      return t("outputShare.statusRevoked");
    default:
      return t("outputShare.statusExpired");
  }
}

function formatShareExpiry(share: ColabOneOutputShareSummary): string {
  if (share.ttl_mode === "forever" || !share.expires_at) {
    return t("outputShare.expiresNever");
  }
  const epoch = share.expires_at;
  const ms = epoch > 1e12 ? epoch : epoch * 1000;
  const dt = new Date(ms);
  if (Number.isNaN(dt.getTime())) {
    return t("outputShare.expiresNever");
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
  return t("outputShare.expiresAt", { time: formatted });
}

function hydrateSelectionFromInitialProps() {
  const order: string[] = [];
  const idsMap: Record<string, string[]> = {};
  for (const rawId of props.initialGroupIds || []) {
    const group = groupById.value.get(rawId);
    if (!group || idsMap[group.id]) continue;
    order.push(group.id);
    idsMap[group.id] = group.items.map((item) => item.id);
    requestCoverLoad(group);
  }
  selectedGroupOrder.value = order;
  selectedIdsByGroup.value = idsMap;
  expandedGroupIds.value = {};
  userEditedTitle.value = false;
  titleDraft.value = suggestedTitle.value;
}

let escapeBound = false;

function onModalKeydown(event: KeyboardEvent) {
  if (!props.open || event.key !== "Escape") return;
  if (openConfirmKey.value) {
    event.preventDefault();
    openConfirmKey.value = "";
  } else if (addMoreOpen.value) {
    event.preventDefault();
    addMoreOpen.value = false;
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

watch(
  () => props.open,
  (isOpen, wasOpen) => {
    if (!isOpen) {
      unbindModalEscape();
      if (wasOpen) {
        staleCreatedShareId.value = props.lastCreatedShare?.id || "";
      }
      seenCreatedInListId.value = "";
      addMoreOpen.value = false;
      openConfirmKey.value = "";
      lastSubmittedPassword.value = "";
      passwordDraft.value = "";
      passwordEnabled.value = false;
      noteDraft.value = "";
      titleDraft.value = "";
      validationError.value = "";
      clearCopiedFeedback();
      return;
    }
    bindModalEscape();
    activeTab.value = props.initialTab || "create";
    noteDraft.value = "";
    ttlMode.value = "7d";
    allowDownload.value = true;
    passwordEnabled.value = false;
    passwordDraft.value = "";
    validationError.value = "";
    addMoreOpen.value = false;
    openConfirmKey.value = "";
    hydrateSelectionFromInitialProps();
  },
  { immediate: true },
);

watch(
  [activeTab, sharesList, () => props.lastCreatedShare?.id ?? "", isMutating],
  ([nextTab, list, createdId, mutating], prev) => {
    if (!prev || nextTab !== prev[0] || mutating) {
      addMoreOpen.value = false;
      openConfirmKey.value = "";
    }
    if (openConfirmKey.value) {
      const sepIdx = openConfirmKey.value.indexOf(":");
      const kind = sepIdx >= 0 ? openConfirmKey.value.slice(0, sepIdx) : "";
      const targetId = sepIdx >= 0 ? openConfirmKey.value.slice(sepIdx + 1) : "";
      const target = list.find((s) => s.id === targetId);
      if (!target || (kind === "revoke" && target.status === "revoked")) {
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

watch(addMoreOpen, (isOpen) => {
  if (!isOpen || !props.open) return;
  for (const cand of unselectedGroups.value) {
    requestCoverLoad(cand);
  }
});

watch(
  () => props.allGroups,
  (nextGroups) => {
    if (!props.open || selectedGroupOrder.value.length === 0) return;
    const byId = new Map<string, ColabOneOutputGroup>();
    const byItemId = new Map<string, ColabOneOutputGroup>();
    for (const g of nextGroups || []) {
      if (!g?.id) continue;
      byId.set(g.id, g);
      for (const it of g.items) {
        if (it?.id) byItemId.set(it.id, g);
      }
    }
    let changed = false;
    const nextOrder: string[] = [];
    const seenOrder = new Set<string>();
    const nextIds: Record<string, string[]> = {};
    const nextExpanded: Record<string, boolean> = {};

    for (const oldGroupId of selectedGroupOrder.value) {
      const oldSelected = selectedIdsByGroup.value[oldGroupId] ?? [];
      const exact = byId.get(oldGroupId);
      if (exact) {
        if (!seenOrder.has(exact.id)) {
          seenOrder.add(exact.id);
          nextOrder.push(exact.id);
        }
        const validSet = new Set(exact.items.map((it) => it.id));
        const kept = oldSelected.filter((id) => validSet.has(id));
        const bucket = nextIds[exact.id] ?? [];
        for (const id of kept) {
          if (!bucket.includes(id)) {
            bucket.push(id);
          }
        }
        nextIds[exact.id] = bucket;
        if (expandedGroupIds.value[oldGroupId]) {
          nextExpanded[exact.id] = true;
        }
        if (kept.length !== oldSelected.length) changed = true;
        continue;
      }

      // Group ID morphed (e.g. item:<id> <-> album:<taskId>): remap by surviving item IDs.
      changed = true;
      for (const itemId of oldSelected) {
        const remapped = byItemId.get(itemId);
        if (!remapped) continue;
        if (!seenOrder.has(remapped.id)) {
          seenOrder.add(remapped.id);
          nextOrder.push(remapped.id);
        }
        const bucket = nextIds[remapped.id] ?? [];
        if (!bucket.includes(itemId)) {
          bucket.push(itemId);
        }
        nextIds[remapped.id] = bucket;
        if (expandedGroupIds.value[oldGroupId]) {
          nextExpanded[remapped.id] = true;
        }
      }
    }

    if (changed) {
      selectedGroupOrder.value = nextOrder;
      selectedIdsByGroup.value = nextIds;
      expandedGroupIds.value = nextExpanded;
      if (!userEditedTitle.value) {
        titleDraft.value = suggestedTitle.value;
      }
    }
  },
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
</script>

<template>
  <AModal
    :open="open"
    :width="660"
    :style="{ maxWidth: 'calc(100vw - 24px)' }"
    :centered="true"
    :closable="canClose"
    :mask-closable="canClose"
    :keyboard="canClose && !hasInnerOverlayOpen"
    :footer="null"
    :z-index="1060"
    wrap-class-name="co-output-share-modal-wrap"
    @cancel="requestClose"
  >
    <template #title>
      <div class="co-os-title">
        <span class="co-os-title-icon" aria-hidden="true">
          <PhShareNetwork :size="17" weight="duotone" />
        </span>
        <span class="co-os-title-text">{{ t("outputShare.modalTitle") }}</span>
      </div>
    </template>

    <div class="co-os-body" data-testid="output-share-modal">
      <!-- Web Console Offline Warning -->
      <AAlert
        v-if="!webConsoleState.running"
        type="warning"
        show-icon
        :message="t('outputShare.webConsoleOffTitle')"
        class="co-os-offline-alert"
        data-testid="output-share-webconsole-off-alert"
      >
        <template #description>
          <div class="co-os-offline-desc">
            <p class="co-os-offline-copy">
              {{ t("outputShare.webConsoleOffDesc") }}
            </p>
            <div v-if="canEnableWebConsole" class="co-os-offline-actions">
              <AButton
                type="primary"
                size="small"
                :loading="enablingWebConsole"
                data-testid="output-share-enable-webconsole-btn"
                @click="emit('enable-web-console')"
              >
                <template #icon><PhPower :size="14" /></template>
                {{ t("outputShare.webConsoleEnableBtn") }}
              </AButton>
            </div>
          </div>
        </template>
      </AAlert>

      <!-- LAN-only subtle tip when Public Origin is not yet configured -->
      <div
        v-else-if="!hasPublicOrigin"
        class="co-os-lan-tip"
        data-testid="output-share-lan-tip"
      >
        <PhInfo :size="15" weight="duotone" class="co-os-lan-icon" aria-hidden="true" />
        <span>{{ t("outputShare.lanOnlyTip") }}</span>
      </div>

      <ATabs
        v-model:active-key="activeTab"
        class="co-os-tabs"
        data-testid="output-share-tabs"
      >
        <!-- =============================================================== -->
        <!-- TAB 1: CREATE OUTPUT SHARE                                      -->
        <!-- =============================================================== -->
        <ATabPane key="create" :tab="t('outputShare.tabCreate')">
          <div class="co-os-tab-panel" data-testid="output-share-create-panel">
            <!-- Created Result Banner -->
            <div
              v-if="visibleCreatedShare"
              class="co-os-created-card"
              data-testid="output-share-created-card"
            >
              <div class="co-os-created-head">
                <PhCheckCircle
                  :size="17"
                  weight="fill"
                  class="co-os-created-icon"
                  aria-hidden="true"
                />
                <span class="co-os-created-title">
                  {{ t("outputShare.createdBannerTitle") }}
                </span>
              </div>

              <div class="co-os-created-field">
                <span class="co-os-created-label">
                  {{ t("outputShare.linkLabel") }}
                </span>
                <code class="co-os-url-code" data-testid="output-share-created-url">
                  <ColabOnePrivacyText
                    :text="visibleCreatedShareUrl"
                    kind="url"
                    :context="privacyContext"
                  />
                </code>
              </div>

              <div
                v-if="visibleCreatedSharePassword"
                class="co-os-created-field"
              >
                <span class="co-os-created-label">
                  {{ t("outputShare.passwordValueLabel") }}
                </span>
                <code
                  class="co-os-pwd-code"
                  data-testid="output-share-created-password"
                >
                  <ColabOnePrivacyText
                    :text="visibleCreatedSharePassword"
                    kind="secret"
                    :context="privacyContext"
                  />
                </code>
              </div>

              <div class="co-os-created-actions">
                <AButton
                  size="small"
                  type="primary"
                  data-testid="output-share-copy-created-link"
                  @click="
                    performCopy(
                      visibleCreatedShareUrl,
                      'created-link',
                      t('outputShare.noticeCopiedLink'),
                    )
                  "
                >
                  <template #icon>
                    <PhCheck v-if="copiedKey === 'created-link'" :size="14" />
                    <PhCopy v-else :size="14" />
                  </template>
                  {{
                    copiedKey === "created-link"
                      ? t("outputShare.copiedBtn")
                      : t("outputShare.copyLinkBtn")
                  }}
                </AButton>

                <AButton
                  v-if="visibleCreatedSharePassword"
                  size="small"
                  type="primary"
                  ghost
                  data-testid="output-share-copy-created-link-pwd"
                  @click="
                    performCopy(
                      buildLinkWithPasswordText(
                        visibleCreatedShare,
                        visibleCreatedSharePassword,
                      ),
                      'created-link-pwd',
                      t('outputShare.noticeCopiedLinkPwd'),
                    )
                  "
                >
                  <template #icon>
                    <PhCheck v-if="copiedKey === 'created-link-pwd'" :size="14" />
                    <PhKey v-else :size="14" />
                  </template>
                  {{
                    copiedKey === "created-link-pwd"
                      ? t("outputShare.copiedBtn")
                      : t("outputShare.copyLinkAndPwdBtn")
                  }}
                </AButton>

                <AButton
                  v-if="webConsoleState.running && visibleCreatedShareUrl"
                  size="small"
                  data-testid="output-share-open-created-preview"
                  @click="
                    emit(
                      'open-url',
                      visibleCreatedShareUrl,
                      visibleCreatedShare.id,
                    )
                  "
                >
                  <template #icon><PhArrowSquareOut :size="14" /></template>
                  {{ t("outputShare.openPreviewBtn") }}
                </AButton>

                <AButton
                  size="small"
                  type="text"
                  class="co-os-create-another-btn"
                  data-testid="output-share-create-another"
                  @click="resetCreateFormForAnother"
                >
                  {{ t("outputShare.createAnotherBtn") }}
                </AButton>
              </div>
            </div>

            <!-- Selected Content List + Cross-Workflow Add More -->
            <div class="co-os-section">
              <div class="co-os-section-row">
                <div class="co-os-section-title-group">
                  <span class="co-os-field-label">
                    {{ t("outputShare.selectedContentLabel") }}
                  </span>
                  <ATag color="blue" :bordered="false" class="co-os-summary-tag">
                    {{
                      t("outputShare.selectedContentSummary", {
                        groups: nonEmptySelectedGroupCount,
                        groupCount: nonEmptySelectedGroupCount,
                        items: totalSelectedItemCount,
                        itemCount: totalSelectedItemCount,
                        count: totalSelectedItemCount,
                      })
                    }}
                  </ATag>
                </div>

                <APopover
                  v-model:open="addMoreOpen"
                  trigger="click"
                  placement="bottomRight"
                  :overlay-style="{ zIndex: 1080 }"
                >
                  <template #content>
                    <div class="co-os-add-popover">
                      <div
                        v-if="unselectedGroups.length === 0"
                        class="co-os-add-empty"
                      >
                        {{ t("outputShare.addMoreEmpty") }}
                      </div>
                      <div v-else class="co-os-add-list">
                        <button
                          v-for="cand in unselectedGroups"
                          :key="cand.id"
                          type="button"
                          class="co-os-add-option"
                          :data-testid="`output-share-add-group-${cand.id}`"
                          @click="addGroupToSelection(cand.id)"
                        >
                          <div class="co-os-add-thumb">
                            <img
                              v-if="
                                cand.coverItems[0]?.kind === 'image' &&
                                resolveMediaSrc(cand.coverItems[0])
                              "
                              :src="resolveMediaSrc(cand.coverItems[0])"
                              :alt="cand.title"
                            />
                            <PhVideoCamera
                              v-else-if="cand.hasVideo && !cand.hasImage"
                              :size="16"
                            />
                            <PhWaveform
                              v-else-if="cand.hasAudio && !cand.hasImage"
                              :size="16"
                            />
                            <PhImages v-else-if="cand.isAlbum" :size="16" />
                            <PhImage v-else :size="16" />
                          </div>
                          <div class="co-os-add-copy">
                            <span class="co-os-add-name">{{ tb(cand.title) }}</span>
                            <span class="co-os-add-count">
                              {{ t("comfy.outputCount", { count: cand.items.length }) }}
                            </span>
                          </div>
                          <PhPlus :size="14" class="co-os-add-plus" />
                        </button>
                      </div>
                    </div>
                  </template>

                  <AButton
                    size="small"
                    :disabled="isMutating"
                    data-testid="output-share-add-more-btn"
                  >
                    <template #icon><PhPlus :size="13" /></template>
                    {{ t("outputShare.addMoreBtn") }}
                  </AButton>
                </APopover>
              </div>

              <div v-if="selectedGroups.length === 0" class="co-os-empty-selection">
                {{ t("outputShare.errSelectAtLeastOne") }}
              </div>

              <div v-else class="co-os-selected-list">
                <div
                  v-for="group in selectedGroups"
                  :key="group.id"
                  class="co-os-group-card"
                  :data-testid="`output-share-selected-${group.id}`"
                >
                  <div class="co-os-group-head">
                    <div class="co-os-group-thumb">
                      <img
                        v-if="
                          group.coverItems[0]?.kind === 'image' &&
                          resolveMediaSrc(group.coverItems[0])
                        "
                        :src="resolveMediaSrc(group.coverItems[0])"
                        :alt="group.title"
                      />
                      <PhVideoCamera
                        v-else-if="group.hasVideo && !group.hasImage"
                        :size="18"
                      />
                      <PhWaveform
                        v-else-if="group.hasAudio && !group.hasImage"
                        :size="18"
                      />
                      <PhImages v-else-if="group.isAlbum" :size="18" />
                      <PhImage v-else :size="18" />
                    </div>

                    <div class="co-os-group-info">
                      <div class="co-os-group-title-row">
                        <strong class="co-os-group-name">{{ tb(group.title) }}</strong>
                        <span class="co-os-group-ratio">
                          {{ getGroupSelectedCount(group) }} / {{ group.items.length }}
                        </span>
                      </div>
                      <span
                        v-if="!group.isAlbum && group.items[0]"
                        class="co-os-group-sub"
                      >
                        {{ group.items[0].filename }}
                      </span>
                    </div>

                    <div class="co-os-group-actions">
                      <AButton
                        v-if="group.items.length > 1"
                        size="small"
                        type="link"
                        class="co-os-expand-btn"
                        :data-testid="`output-share-expand-${group.id}`"
                        @click="toggleGroupExpanded(group)"
                      >
                        {{
                          expandedGroupIds[group.id]
                            ? t("outputShare.collapseItems", {
                                selected: getGroupSelectedCount(group),
                                count: getGroupSelectedCount(group),
                                total: group.items.length,
                              })
                            : t("outputShare.expandItems", {
                                selected: getGroupSelectedCount(group),
                                count: getGroupSelectedCount(group),
                                total: group.items.length,
                              })
                        }}
                        <PhCaretUp v-if="expandedGroupIds[group.id]" :size="13" />
                        <PhCaretDown v-else :size="13" />
                      </AButton>

                      <AButton
                        size="small"
                        type="text"
                        class="co-os-remove-btn"
                        :disabled="isMutating"
                        :aria-label="t('outputShare.removeGroup')"
                        :data-testid="`output-share-remove-${group.id}`"
                        @click="removeGroupFromSelection(group.id)"
                      >
                        <template #icon><PhX :size="14" /></template>
                      </AButton>
                    </div>
                  </div>

                  <!-- Expandable Single-Shot Picker for Albums -->
                  <div
                    v-if="group.items.length > 1 && expandedGroupIds[group.id]"
                    class="co-os-item-picker"
                  >
                    <div class="co-os-item-picker-bar">
                      <AButton
                        size="small"
                        type="link"
                        class="co-os-picker-quick-btn"
                        :disabled="isMutating"
                        @click="selectAllInGroup(group)"
                      >
                        {{ t("outputShare.selectAllInGroup") }}
                      </AButton>
                      <AButton
                        v-if="group.inspectCount > 0 && group.primaryCount > 0"
                        size="small"
                        type="link"
                        class="co-os-picker-quick-btn"
                        :disabled="isMutating"
                        @click="excludeInspectInGroup(group)"
                      >
                        {{ t("outputShare.excludeInspectInGroup") }}
                      </AButton>
                    </div>

                    <div class="co-os-item-grid">
                      <div
                        v-for="item in group.items"
                        :key="item.id"
                        class="co-os-item-tile"
                        :class="{
                          'is-checked': isItemSelectedInGroup(group.id, item.id),
                          'is-disabled': isMutating,
                        }"
                        @click="
                          !isMutating &&
                            toggleItemInGroup(
                              group,
                              item.id,
                              !isItemSelectedInGroup(group.id, item.id),
                            )
                        "
                      >
                        <span class="co-os-item-checkbox" @click.stop>
                          <ACheckbox
                            :checked="isItemSelectedInGroup(group.id, item.id)"
                            :disabled="isMutating"
                            :aria-label="item.filename"
                            @update:checked="
                              (val) =>
                                toggleItemInGroup(group, item.id, Boolean(val))
                            "
                          />
                        </span>
                        <div class="co-os-item-thumb">
                          <img
                            v-if="
                              item.kind === 'image' && resolveMediaSrc(item)
                            "
                            :src="resolveMediaSrc(item)"
                            :alt="item.filename"
                            loading="lazy"
                          />
                          <PhVideoCamera
                            v-else-if="item.kind === 'video'"
                            :size="18"
                          />
                          <PhWaveform
                            v-else-if="item.kind === 'audio'"
                            :size="18"
                          />
                          <PhImage v-else :size="18" />
                          <span
                            v-if="isColabOneInspectOutput(item)"
                            class="co-os-item-inspect-badge"
                          >
                            {{ t("outputGallery.inspectBadge") }}
                          </span>
                        </div>
                        <span class="co-os-item-name" :title="item.filename">
                          {{ item.filename }}
                        </span>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <!-- Title & Host Note -->
            <div class="co-os-fields-grid">
              <div class="co-os-field">
                <label for="co-os-title-input" class="co-os-field-label">
                  {{ t("outputShare.titleLabel") }}
                </label>
                <AInput
                  id="co-os-title-input"
                  v-model:value="titleDraft"
                  :maxlength="80"
                  allow-clear
                  :disabled="creating"
                  :placeholder="t('outputShare.titlePlaceholder')"
                  data-testid="output-share-title-input"
                  @input="userEditedTitle = true"
                />
              </div>

              <div class="co-os-field">
                <label for="co-os-note-input" class="co-os-field-label">
                  {{ t("outputShare.noteLabel") }}
                </label>
                <AInput
                  id="co-os-note-input"
                  v-model:value="noteDraft"
                  :maxlength="120"
                  allow-clear
                  :disabled="creating"
                  :placeholder="t('outputShare.notePlaceholder')"
                  data-testid="output-share-note-input"
                />
              </div>
            </div>

            <!-- TTL Validity -->
            <div class="co-os-field">
              <span class="co-os-field-label">{{ t("outputShare.ttlLabel") }}</span>
              <div class="co-os-ttl-wrap">
                <ASegmented
                  :value="ttlMode"
                  :options="ttlOptions"
                  :disabled="creating"
                  block
                  class="co-os-ttl-segmented"
                  data-testid="output-share-ttl-segmented"
                  @change="onTtlChange"
                />
              </div>
            </div>

            <!-- Permission & Password Switches -->
            <div class="co-os-switches-box">
              <div class="co-os-switch-row">
                <div class="co-os-switch-copy">
                  <span class="co-os-switch-label">
                    {{ t("outputShare.allowDownloadLabel") }}
                  </span>
                  <span class="co-os-switch-desc">
                    {{ t("outputShare.allowDownloadDesc") }}
                  </span>
                </div>
                <ASwitch
                  v-model:checked="allowDownload"
                  :disabled="creating"
                  data-testid="output-share-allow-download-switch"
                />
              </div>

              <div class="co-os-switch-divider" />

              <div class="co-os-switch-row">
                <div class="co-os-switch-copy">
                  <span class="co-os-switch-label">
                    {{ t("outputShare.passwordLabel") }}
                  </span>
                  <span class="co-os-switch-desc">
                    {{
                      passwordEnabled
                        ? t("outputShare.passwordDescOn")
                        : t("outputShare.passwordDescOff")
                    }}
                  </span>
                </div>
                <ASwitch
                  :checked="passwordEnabled"
                  :disabled="creating"
                  data-testid="output-share-password-switch"
                  @update:checked="onTogglePassword"
                />
              </div>

              <div v-if="passwordEnabled" class="co-os-pwd-input-row">
                <ColabOneSecretInput
                  v-model:value="passwordDraft"
                  :maxlength="32"
                  :disabled="creating"
                  autocomplete="new-password"
                  :aria-label="t('outputShare.passwordLabel')"
                  :placeholder="t('outputShare.passwordPlaceholder')"
                  class="co-os-pwd-input"
                  data-testid="output-share-password-input"
                />
                <AButton
                  size="middle"
                  :disabled="creating"
                  data-testid="output-share-password-regenerate"
                  @click="regeneratePassword"
                >
                  <template #icon><PhArrowsClockwise :size="14" /></template>
                  {{ t("outputShare.passwordRegenerate") }}
                </AButton>
              </div>
            </div>

            <p
              v-if="validationError"
              class="co-os-field-error"
              role="alert"
              data-testid="output-share-validation-error"
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
              class="co-os-create-btn"
              data-testid="output-share-create-btn"
              @click="handleCreate"
            >
              <template #icon><PhLinkSimple :size="16" /></template>
              {{
                creating
                  ? t("outputShare.creatingBtn")
                  : t("outputShare.createBtn")
              }}
            </AButton>
          </div>
        </ATabPane>

        <!-- =============================================================== -->
        <!-- TAB 2: MANAGE CREATED SHARES                                    -->
        <!-- =============================================================== -->
        <ATabPane key="manage">
          <template #tab>
            <span
              class="co-os-manage-tab-label"
              data-testid="output-share-manage-tab"
            >
              <span>{{
                t("outputShare.tabManage", { count: sharesList.length })
              }}</span>
            </span>
          </template>

          <div class="co-os-tab-panel" data-testid="output-share-manage-panel">
            <div
              v-if="sharesList.length === 0"
              class="co-os-empty-state"
              data-testid="output-share-empty-state"
            >
              <AEmpty :image="AEmpty.PRESENTED_IMAGE_SIMPLE">
                <template #description>
                  <div class="co-os-empty-copy">
                    <strong class="co-os-empty-title">
                      {{ t("outputShare.emptyShares") }}
                    </strong>
                    <span class="co-os-empty-desc">
                      {{ t("outputShare.emptySharesDesc") }}
                    </span>
                  </div>
                </template>
                <AButton
                  type="primary"
                  size="small"
                  @click="activeTab = 'create'"
                >
                  {{ t("outputShare.tabCreate") }}
                </AButton>
              </AEmpty>
            </div>

            <div v-else class="co-os-share-list">
              <div
                v-for="share in sharesList"
                :key="share.id"
                class="co-os-share-card"
                :class="{ 'is-inactive': share.status !== 'active' }"
                :data-testid="`output-share-card-${share.id}`"
              >
                <div class="co-os-share-head">
                  <div class="co-os-share-title-block">
                    <div class="co-os-share-title-line">
                      <strong class="co-os-share-title">
                        {{ share.title || t("outputShare.guestDefaultTitle") }}
                      </strong>
                      <ATag
                        :color="statusTagColor(share.status)"
                        :bordered="false"
                        class="co-os-status-tag"
                      >
                        {{ statusLabel(share.status) }}
                      </ATag>
                    </div>
                    <span v-if="share.note" class="co-os-share-note">
                      {{ share.note }}
                    </span>
                  </div>
                </div>

                <!-- Feature Badges -->
                <div class="co-os-badge-row">
                  <span class="co-os-meta-chip">
                    <PhImages :size="12" aria-hidden="true" />
                    <span>
                      {{
                        t("outputShare.selectedContentSummary", {
                          groups: share.group_count,
                          groupCount: share.group_count,
                          items: share.item_count,
                          itemCount: share.item_count,
                          count: share.item_count,
                        })
                      }}
                    </span>
                  </span>

                  <span
                    class="co-os-meta-chip"
                    :class="{ 'is-protected': share.has_password }"
                  >
                    <PhLock v-if="share.has_password" :size="12" aria-hidden="true" />
                    <PhLockOpen v-else :size="12" aria-hidden="true" />
                    <span>
                      {{
                        share.has_password
                          ? t("outputShare.badgePassword")
                          : t("outputShare.badgeNoPassword")
                      }}
                    </span>
                  </span>

                  <span class="co-os-meta-chip">
                    <PhDownloadSimple :size="12" aria-hidden="true" />
                    <span>
                      {{
                        share.allow_download
                          ? t("outputShare.badgeDownloadOn")
                          : t("outputShare.badgeDownloadOff")
                      }}
                    </span>
                  </span>
                </div>

                <!-- Meta Strip -->
                <div class="co-os-meta-strip">
                  <span class="co-os-meta-item">
                    <PhClock :size="13" aria-hidden="true" />
                    <span>{{ formatShareExpiry(share) }}</span>
                  </span>
                  <span class="co-os-meta-dot" aria-hidden="true">·</span>
                  <span class="co-os-meta-item">
                    <PhUsers :size="13" aria-hidden="true" />
                    <span>
                      {{
                        t("outputShare.badgeSessions", {
                          count: share.active_sessions,
                        })
                      }}
                    </span>
                  </span>
                </div>

                <!-- Card Actions -->
                <div class="co-os-share-actions">
                  <AButton
                    v-if="share.can_copy_link && shareCopyUrl(share)"
                    size="small"
                    :data-testid="`output-share-copy-${share.id}`"
                    @click="
                      performCopy(
                        shareCopyUrl(share),
                        `link-${share.id}`,
                        t('outputShare.noticeCopiedLink'),
                      )
                    "
                  >
                    <template #icon>
                      <PhCheck v-if="copiedKey === `link-${share.id}`" :size="13" />
                      <PhCopy v-else :size="13" />
                    </template>
                    {{
                      copiedKey === `link-${share.id}`
                        ? t("outputShare.copiedBtn")
                        : t("outputShare.copyLinkBtn")
                    }}
                  </AButton>

                  <AButton
                    v-if="
                      share.can_copy_link &&
                      shareCopyUrl(share) &&
                      share.password
                    "
                    size="small"
                    :data-testid="`output-share-copy-pwd-${share.id}`"
                    @click="
                      performCopy(
                        buildLinkWithPasswordText(share, share.password),
                        `pwd-${share.id}`,
                        t('outputShare.noticeCopiedLinkPwd'),
                      )
                    "
                  >
                    <template #icon>
                      <PhCheck v-if="copiedKey === `pwd-${share.id}`" :size="13" />
                      <PhKey v-else :size="13" />
                    </template>
                    {{
                      copiedKey === `pwd-${share.id}`
                        ? t("outputShare.copiedBtn")
                        : t("outputShare.copyLinkAndPwdBtn")
                    }}
                  </AButton>

                  <AButton
                    v-if="
                      webConsoleState.running &&
                      share.status === 'active' &&
                      shareCopyUrl(share)
                    "
                    size="small"
                    :data-testid="`output-share-open-${share.id}`"
                    @click="emit('open-url', shareCopyUrl(share), share.id)"
                  >
                    <template #icon><PhArrowSquareOut :size="13" /></template>
                    {{ t("outputShare.openPreviewBtn") }}
                  </AButton>

                  <APopconfirm
                    v-if="share.status !== 'revoked'"
                    :open="openConfirmKey === `revoke:${share.id}`"
                    :title="
                      t('outputShare.revokeConfirm', {
                        name: share.title || t('outputShare.guestDefaultTitle'),
                        title: share.title || t('outputShare.guestDefaultTitle'),
                      })
                    "
                    :ok-text="t('outputShare.revokeBtn')"
                    :cancel-text="t('common.cancel')"
                    :ok-button-props="{ danger: true }"
                    :overlay-style="{ zIndex: 1080 }"
                    :disabled="
                      isMutating ||
                      revokingId === share.id ||
                      deletingId === share.id
                    "
                    @open-change="
                      (val) => setConfirmOpen(`revoke:${share.id}`, val)
                    "
                    @confirm="emit('revoke', share.id)"
                  >
                    <AButton
                      size="small"
                      danger
                      :loading="revokingId === share.id"
                      :disabled="isMutating || deletingId === share.id"
                      :data-testid="`output-share-revoke-${share.id}`"
                    >
                      <template #icon><PhProhibit :size="13" /></template>
                      {{ t("outputShare.revokeBtn") }}
                    </AButton>
                  </APopconfirm>

                  <APopconfirm
                    :open="openConfirmKey === `delete:${share.id}`"
                    :title="
                      t('outputShare.deleteConfirm', {
                        name: share.title || t('outputShare.guestDefaultTitle'),
                        title: share.title || t('outputShare.guestDefaultTitle'),
                      })
                    "
                    :ok-text="t('outputShare.deleteBtn')"
                    :cancel-text="t('common.cancel')"
                    :ok-button-props="{ danger: true }"
                    :overlay-style="{ zIndex: 1080 }"
                    :disabled="
                      isMutating ||
                      revokingId === share.id ||
                      deletingId === share.id
                    "
                    @open-change="
                      (val) => setConfirmOpen(`delete:${share.id}`, val)
                    "
                    @confirm="emit('delete', share.id)"
                  >
                    <AButton
                      size="small"
                      :loading="deletingId === share.id"
                      :disabled="isMutating || revokingId === share.id"
                      :data-testid="`output-share-delete-${share.id}`"
                    >
                      <template #icon><PhTrash :size="13" /></template>
                      {{ t("outputShare.deleteBtn") }}
                    </AButton>
                  </APopconfirm>
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
.co-os-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-os-title-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-os-title-icon {
  color: var(--co-accent-strong);
}

.co-os-title-text {
  font-size: 15px;
  font-weight: 600;
  color: var(--co-text);
}

.co-os-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding-top: 2px;
}

.co-os-offline-alert {
  border-radius: 10px;
}

.co-os-offline-desc {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-os-offline-copy {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-os-offline-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-os-lan-tip {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-os-lan-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-os-tabs :deep(.ant-tabs-nav) {
  margin-bottom: 12px;
}

.co-os-manage-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-os-tab-badge {
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

.co-os-tab-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* Created Share Banner */
.co-os-created-card {
  display: flex;
  flex-direction: column;
  gap: 9px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-success-border);
  background: var(--co-success-bg);
}

.co-os-created-head {
  display: flex;
  align-items: center;
  gap: 6px;
}

.co-os-created-icon {
  color: var(--co-success);
  flex-shrink: 0;
}

.co-os-created-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-success-text);
}

.co-os-created-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-os-created-label {
  font-size: 11.5px;
  font-weight: 500;
  color: var(--co-secondary-text);
}

.co-os-url-code,
.co-os-pwd-code {
  display: block;
  padding: 6px 10px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-success-border);
  font-family: var(--co-font-mono);
  font-size: 12px;
  color: var(--co-text);
  word-break: break-all;
}

.co-os-pwd-code {
  display: inline-block;
  width: fit-content;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.co-os-created-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 2px;
}

.co-os-create-another-btn {
  margin-left: auto;
  font-size: 12px;
}

/* Selected Content Section */
.co-os-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.co-os-section-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-os-section-title-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.co-os-summary-tag {
  margin-inline-end: 0;
  font-size: 11.5px;
  line-height: 18px;
}

.co-os-empty-selection {
  padding: 16px 12px;
  border-radius: 10px;
  border: 1px dashed var(--co-border);
  background: var(--co-subtle);
  font-size: 12.5px;
  color: var(--co-muted);
  text-align: center;
}

.co-os-selected-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 240px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-os-group-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 9px 11px;
  border-radius: 11px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-os-group-head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.co-os-group-thumb {
  width: 42px;
  height: 42px;
  border-radius: 8px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  overflow: hidden;
  display: grid;
  place-items: center;
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-os-group-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-os-group-info {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-os-group-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.co-os-group-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-os-group-ratio {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: 9999px;
  background: var(--co-selected);
  color: var(--co-accent-strong);
  font-family: var(--co-font-mono);
  font-size: 11px;
  font-weight: 600;
}

.co-os-group-sub {
  font-size: 11.5px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--co-font-mono);
}

.co-os-group-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.co-os-expand-btn {
  padding-inline: 4px;
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-os-remove-btn {
  width: 26px;
  height: 26px;
  padding: 0;
  color: var(--co-muted);
}

.co-os-remove-btn:hover {
  color: var(--co-danger-text);
}

/* Expandable Single-Item Picker */
.co-os-item-picker {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-os-item-picker-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-os-picker-quick-btn {
  padding: 0 4px;
  height: auto;
  font-size: 12px;
}

.co-os-item-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(84px, 1fr));
  gap: 7px;
}

.co-os-item-tile {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 5px;
  border-radius: 8px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  cursor: pointer;
  transition: border-color 0.14s ease, background-color 0.14s ease;
}

.co-os-item-tile.is-checked {
  border-color: rgba(59, 130, 246, 0.45);
  background: var(--co-selected);
}

.co-os-item-checkbox {
  position: absolute;
  top: 7px;
  left: 7px;
  z-index: 2;
}

.co-os-item-thumb {
  position: relative;
  width: 100%;
  aspect-ratio: 1 / 1;
  border-radius: 6px;
  background: var(--co-track);
  overflow: hidden;
  display: grid;
  place-items: center;
  color: var(--co-muted);
}

.co-os-item-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-os-item-inspect-badge {
  position: absolute;
  bottom: 3px;
  right: 3px;
  padding: 0 4px;
  border-radius: 4px;
  background: rgba(245, 158, 11, 0.92);
  color: #ffffff;
  font-size: 9.5px;
  font-weight: 600;
  line-height: 14px;
}

.co-os-item-name {
  font-size: 10.5px;
  color: var(--co-secondary-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Add More Popover */
.co-os-add-popover {
  width: 280px;
  max-height: 260px;
  overflow-y: auto;
}

.co-os-add-empty {
  padding: 12px 8px;
  text-align: center;
  font-size: 12.5px;
  color: var(--co-muted);
}

.co-os-add-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.co-os-add-option {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 6px 8px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--co-text);
  cursor: pointer;
  text-align: left;
  transition: background-color 0.14s ease;
}

.co-os-add-option:hover {
  background: var(--co-soft-hover);
}

.co-os-add-thumb {
  width: 34px;
  height: 34px;
  border-radius: 7px;
  background: var(--co-track);
  overflow: hidden;
  display: grid;
  place-items: center;
  color: var(--co-muted);
  flex-shrink: 0;
}

.co-os-add-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.co-os-add-copy {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.co-os-add-name {
  font-size: 12.5px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-os-add-count {
  font-size: 11px;
  color: var(--co-muted);
}

.co-os-add-plus {
  color: var(--co-accent);
  flex-shrink: 0;
}

/* Form Controls */
.co-os-fields-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.co-os-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.co-os-field-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-text);
}

.co-os-ttl-wrap {
  width: 100%;
  overflow-x: auto;
}

.co-os-ttl-segmented {
  width: 100%;
}

.co-os-switches-box {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 11px 13px;
  border-radius: 11px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-os-switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.co-os-switch-copy {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.co-os-switch-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--co-text);
}

.co-os-switch-desc {
  font-size: 12px;
  color: var(--co-muted);
}

.co-os-switch-divider {
  height: 1px;
  background: var(--co-border-secondary);
}

.co-os-pwd-input-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.co-os-pwd-input {
  flex: 1 1 auto;
  font-family: var(--co-font-mono);
}

.co-os-field-error {
  margin: 0;
  font-size: 12px;
  color: var(--co-danger-text);
}

.co-os-create-btn {
  font-weight: 500;
}

/* Manage Tab */
.co-os-empty-state {
  padding: 18px 0 8px;
}

.co-os-empty-copy {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.co-os-empty-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-os-empty-desc {
  font-size: 12px;
  color: var(--co-muted);
}

.co-os-share-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 430px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-os-share-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  transition: border-color 0.15s ease;
}

.co-os-share-card:hover {
  border-color: var(--co-border);
}

.co-os-share-card.is-inactive {
  opacity: 0.78;
}

.co-os-share-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.co-os-share-title-block {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}

.co-os-share-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.co-os-share-title {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  word-break: break-word;
}

.co-os-status-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
}

.co-os-share-note {
  font-size: 12px;
  color: var(--co-secondary-text);
  word-break: break-word;
}

.co-os-badge-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-os-meta-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 6px;
  background: var(--co-surface);
  border: 1px solid var(--co-border-secondary);
  font-size: 11.5px;
  color: var(--co-secondary-text);
}

.co-os-meta-chip.is-protected {
  color: var(--co-warning-text);
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
}

.co-os-meta-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 12px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-os-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-os-meta-dot {
  opacity: 0.65;
}

.co-os-share-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 6px;
  border-top: 1px solid var(--co-border-secondary);
}

@media (max-width: 560px) {
  .co-os-fields-grid {
    grid-template-columns: 1fr;
  }

  .co-os-pwd-input-row {
    flex-direction: column;
    align-items: stretch;
  }

  .co-os-create-another-btn {
    margin-left: 0;
  }
}
</style>
