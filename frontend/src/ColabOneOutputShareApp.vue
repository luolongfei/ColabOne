<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  Button as AButton,
  ConfigProvider as AConfigProvider,
  Empty as AEmpty,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowsClockwise,
  PhClock,
  PhDownloadSimple,
  PhEye,
  PhFileArchive,
  PhImage,
  PhImages,
  PhLinkBreak,
  PhMagnifyingGlassPlus,
  PhMoon,
  PhSun,
  PhVideoCamera,
  PhWarningCircle,
  PhWaveform,
  PhWifiSlash,
} from "@phosphor-icons/vue";
import rawAppIcon from "../../console/resources/icon.svg?raw";
import ColabOneOutputGalleryModal, {
  type ColabOneGalleryModalItem,
} from "./ColabOneOutputGalleryModal.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneToastHost from "./ColabOneToastHost.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import LanguageSwitcher from "./LanguageSwitcher.vue";
import {
  colabOneEffectiveAppearance,
  setColabOneAppearance,
  type ColabOneAppearance,
} from "./appearance";
import {
  isColabOneInspectOutput,
  resolveColabOneCoverItems,
  sortColabOneGalleryItems,
} from "./colabOneOutputGallery";
import {
  COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
  colabOneOutputShareDownloadURL,
  colabOneOutputShareMediaURL,
  colabOneOutputShareMode,
  colabOneOutputShareSessionExpired,
  colabOneOutputShareZipURL,
  fetchColabOneOutputShareManifest,
  isColabOneOutputSharePath,
  probeColabOneOutputShareMediaError,
  sanitizeColabOneZipStem,
  triggerColabOneBrowserDownload,
  type ColabOneOutputShareManifest,
  type ColabOneOutputShareManifestGroup,
  type ColabOneOutputShareManifestItem,
} from "./colabOneOutputShare";
import { buildColabOneTheme } from "./colabOneTheme";
import { createColabOneToastManager } from "./colabOneToast";
import { formatOutputCreatedAt, formatOutputSize } from "./comfyui";
import { colabOneAntdLocale, colabOneLocaleCode, t, tb } from "./i18n";

defineOptions({
  name: "ColabOneOutputShareApp",
});

interface PreparedGalleryGroup {
  id: string;
  title: string;
  workflowName: string;
  items: ColabOneOutputShareManifestItem[];
  primaryCount: number;
  inspectCount: number;
  coverItems: ColabOneOutputShareManifestItem[];
  extraCount: number;
  totalSize: number;
  createdAt: number;
  hasAvailable: boolean;
}

const appIcon = rawAppIcon.replace(
  'viewBox="0 0 1254 1254"',
  'viewBox="120 120 1014 1014"',
);

const colabOneTheme = computed(() =>
  buildColabOneTheme(colabOneEffectiveAppearance.value, colabOneLocaleCode.value),
);

const manifest = ref<ColabOneOutputShareManifest | null>(null);
const loading = ref(true);
const loadError = ref("");
const sessionExpired = ref(false);

const downloadingItemId = ref("");
const downloadingZipKey = ref("");
const probedOfflineIds = ref<Record<string, boolean>>({});
const probedErrorIds = ref<Record<string, boolean>>({});
const mediaRetryTokens = ref<Record<string, number>>({});
const inFlightMediaProbes = new Set<string>();

const galleryModalOpen = ref(false);
const galleryModalGroupId = ref("");
const galleryModalInitialItemId = ref("");

const headerRef = ref<HTMLElement | null>(null);
const toastTopOffsetPx = ref(72);
const toastManager = createColabOneToastManager({ maxVisible: 2 });

let loadAbort: AbortController | null = null;
let probeAbort: AbortController | null = null;
let downloadAbort: AbortController | null = null;
let downloadGen = 0;
const probeItemGen = new Map<string, number>();
let manifestEpoch = 0;
let unmounted = false;
let headerResizeObserver: ResizeObserver | null = null;

const preparedGroups = computed<PreparedGalleryGroup[]>(() => {
  const rawGroups = manifest.value?.groups ?? [];
  const offlineMap = probedOfflineIds.value;
  return rawGroups
    .map((group: ColabOneOutputShareManifestGroup) => {
      const sorted = sortColabOneGalleryItems(group.items).map((item) =>
        offlineMap[item.id] ? { ...item, available: false } : item,
      );
      const primaryCount = sorted.filter(
        (item) => !isColabOneInspectOutput(item),
      ).length;
      const inspectCount = sorted.length - primaryCount;
      const coverItems = resolveColabOneCoverItems(sorted, 4);
      const extraCount = Math.max(0, sorted.length - coverItems.length);
      let totalSize = 0;
      let createdAt = 0;
      let hasAvailable = false;
      for (const item of sorted) {
        totalSize += item.size || 0;
        if ((item.created_at || 0) > createdAt) {
          createdAt = item.created_at || 0;
        }
        if (item.available !== false) {
          hasAvailable = true;
        }
      }
      return {
        id: group.id,
        title: group.title || group.workflow_name || sorted[0]?.filename || group.id,
        workflowName: group.workflow_name || "",
        items: sorted,
        primaryCount,
        inspectCount,
        coverItems,
        extraCount,
        totalSize,
        createdAt,
        hasAvailable,
      };
    })
    .filter((group) => group.items.length > 0);
});

const galleryModalGroup = computed<PreparedGalleryGroup | null>(() => {
  if (!galleryModalGroupId.value) return null;
  return (
    preparedGroups.value.find((g) => g.id === galleryModalGroupId.value) || null
  );
});

const totalAvailableItems = computed<number>(() =>
  preparedGroups.value.reduce(
    (sum, g) =>
      sum + g.items.filter((item) => item.available !== false).length,
    0,
  ),
);

const displayTitle = computed<string>(() => {
  const raw = (manifest.value?.title || "").trim();
  return raw || t("outputShare.guestDefaultTitle");
});

const expiryLabel = computed<string>(() => {
  const m = manifest.value;
  if (!m) return "";
  if (!m.expires_at) {
    return t("outputShare.expiresNever");
  }
  const ms = m.expires_at > 1e12 ? m.expires_at : m.expires_at * 1000;
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
});

function toggleAppearance(event?: MouseEvent) {
  const next: ColabOneAppearance =
    colabOneEffectiveAppearance.value === "dark" ? "light" : "dark";
  setColabOneAppearance(next, event);
}

function updateToastOffset() {
  const el = headerRef.value;
  if (!el) return;
  const bottom = Math.round(el.getBoundingClientRect().bottom);
  if (bottom > 0) {
    toastTopOffsetPx.value = Math.min(150, Math.max(56, bottom + 10));
  }
}

function abortActiveDownloads() {
  downloadGen += 1;
  downloadAbort?.abort();
  downloadAbort = null;
  downloadingItemId.value = "";
  downloadingZipKey.value = "";
}

function markSessionExpired() {
  loadAbort?.abort();
  loadAbort = null;
  probeAbort?.abort();
  probeAbort = null;
  abortActiveDownloads();
  inFlightMediaProbes.clear();
  probeItemGen.clear();
  probedErrorIds.value = {};
  sessionExpired.value = true;
  loading.value = false;
  loadError.value = "";
  manifest.value = null;
  galleryModalOpen.value = false;
  galleryModalGroupId.value = "";
}

function resolveGuestMediaURL(id: string): string {
  const base = colabOneOutputShareMediaURL(id);
  const retryToken = mediaRetryTokens.value[id];
  return retryToken ? `${base}?retry=${retryToken}` : base;
}

async function onGuestMediaError(item: { id: string; available?: boolean }) {
  if (unmounted || sessionExpired.value || !item?.id) return;
  if (probedOfflineIds.value[item.id] || inFlightMediaProbes.has(item.id)) {
    return;
  }
  const epoch = manifestEpoch;
  const itemGen = probeItemGen.get(item.id) || 0;
  if (!probeAbort || probeAbort.signal.aborted) {
    probeAbort = new AbortController();
  }
  const signal = probeAbort.signal;
  inFlightMediaProbes.add(item.id);
  try {
    const status = await probeColabOneOutputShareMediaError(item.id, signal);
    if (
      unmounted ||
      epoch !== manifestEpoch ||
      signal.aborted ||
      (probeItemGen.get(item.id) || 0) !== itemGen
    ) {
      return;
    }
    if (status === "unauthorized" || colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
      return;
    }
    if (status === "offline") {
      probedOfflineIds.value = { ...probedOfflineIds.value, [item.id]: true };
      if (probedErrorIds.value[item.id]) {
        const nextErrors = { ...probedErrorIds.value };
        delete nextErrors[item.id];
        probedErrorIds.value = nextErrors;
      }
    } else if (status === "error") {
      probedErrorIds.value = { ...probedErrorIds.value, [item.id]: true };
    }
  } finally {
    if (epoch === manifestEpoch && (probeItemGen.get(item.id) || 0) === itemGen) {
      inFlightMediaProbes.delete(item.id);
    }
  }
}

async function loadManifest() {
  if (unmounted) return;
  manifestEpoch += 1;
  if (loadAbort) {
    loadAbort.abort();
  }
  if (probeAbort) {
    probeAbort.abort();
  }
  abortActiveDownloads();
  probeAbort = new AbortController();
  const controller = new AbortController();
  loadAbort = controller;
  loading.value = true;
  loadError.value = "";
  sessionExpired.value = false;
  probedOfflineIds.value = {};
  probedErrorIds.value = {};
  mediaRetryTokens.value = {};
  inFlightMediaProbes.clear();
  probeItemGen.clear();
  colabOneOutputShareSessionExpired.value = false;

  try {
    const data = await fetchColabOneOutputShareManifest(controller.signal);
    if (unmounted || controller.signal.aborted) return;
    if (colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
      return;
    }
    manifest.value = data;
    sessionExpired.value = false;
  } catch (err) {
    if (unmounted || controller.signal.aborted) return;
    if (err instanceof Error && err.name === "AbortError") return;
    if (colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
    } else {
      loadError.value =
        err instanceof Error && err.message
          ? tb(err.message)
          : t("outputShare.guestNetworkError");
    }
  } finally {
    if (!unmounted && loadAbort === controller) {
      loading.value = false;
    }
  }
}

function openGalleryModal(group: PreparedGalleryGroup, initialItemId = "") {
  galleryModalGroupId.value = group.id;
  galleryModalInitialItemId.value = initialItemId || group.items[0]?.id || "";
  galleryModalOpen.value = true;
}

function closeGalleryModal() {
  galleryModalOpen.value = false;
}

function resolveModalMediaSrc(item: ColabOneGalleryModalItem): string {
  if (item.available === false || probedOfflineIds.value[item.id]) return "";
  return resolveGuestMediaURL(item.id);
}

function isModalItemAvailable(item: ColabOneGalleryModalItem): boolean {
  return item.available !== false && !probedOfflineIds.value[item.id];
}

function getModalItemError(item: ColabOneGalleryModalItem): string {
  if (probedErrorIds.value[item.id]) {
    return t("outputGallery.loadFailedRetry");
  }
  return "";
}

function retryModalItemLoad(item: { id: string }) {
  if (!item?.id) return;
  probeItemGen.set(item.id, (probeItemGen.get(item.id) || 0) + 1);
  inFlightMediaProbes.delete(item.id);
  if (probedErrorIds.value[item.id]) {
    const nextErrors = { ...probedErrorIds.value };
    delete nextErrors[item.id];
    probedErrorIds.value = nextErrors;
  }
  mediaRetryTokens.value = {
    ...mediaRetryTokens.value,
    [item.id]: (mediaRetryTokens.value[item.id] || 0) + 1,
  };
}

async function downloadSingleItem(item: ColabOneGalleryModalItem) {
  if (
    !manifest.value?.allow_download ||
    item.available === false ||
    probedOfflineIds.value[item.id]
  ) {
    return;
  }
  if (downloadingItemId.value || downloadingZipKey.value) return;
  const controller = new AbortController();
  downloadAbort = controller;
  const myGen = ++downloadGen;
  downloadingItemId.value = item.id;
  try {
    await triggerColabOneBrowserDownload(
      colabOneOutputShareDownloadURL(item.id),
      { fallbackFilename: item.filename, signal: controller.signal },
    );
  } catch (err) {
    if (
      unmounted ||
      myGen !== downloadGen ||
      controller.signal.aborted ||
      (err instanceof Error && err.name === "AbortError")
    ) {
      return;
    }
    if (colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
      return;
    }
    const msg =
      err instanceof Error && err.message
        ? tb(err.message)
        : t("outputShare.guestNetworkError");
    toastManager.push({ kind: "error", message: msg });
  } finally {
    if (!unmounted && myGen === downloadGen) {
      if (downloadAbort === controller) {
        downloadAbort = null;
      }
      downloadingItemId.value = "";
    }
  }
}

async function downloadGroupZip(group: PreparedGalleryGroup) {
  if (!manifest.value?.allow_download || !group.hasAvailable) return;
  if (downloadingZipKey.value || downloadingItemId.value) return;
  const controller = new AbortController();
  downloadAbort = controller;
  const myGen = ++downloadGen;
  downloadingZipKey.value = group.id;
  try {
    await triggerColabOneBrowserDownload(colabOneOutputShareZipURL(group.id), {
      fallbackFilename: `${sanitizeColabOneZipStem(group.title, "ColabOne-album")}.zip`,
      signal: controller.signal,
    });
  } catch (err) {
    if (
      unmounted ||
      myGen !== downloadGen ||
      controller.signal.aborted ||
      (err instanceof Error && err.name === "AbortError")
    ) {
      return;
    }
    if (colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
      return;
    }
    const msg =
      err instanceof Error && err.message
        ? tb(err.message)
        : t("outputShare.guestNetworkError");
    toastManager.push({ kind: "error", message: msg });
  } finally {
    if (!unmounted && myGen === downloadGen) {
      if (downloadAbort === controller) {
        downloadAbort = null;
      }
      downloadingZipKey.value = "";
    }
  }
}

async function downloadAllZip() {
  if (!manifest.value?.allow_download || totalAvailableItems.value <= 1) return;
  if (downloadingZipKey.value || downloadingItemId.value) return;
  const controller = new AbortController();
  downloadAbort = controller;
  const myGen = ++downloadGen;
  downloadingZipKey.value = "__all__";
  try {
    await triggerColabOneBrowserDownload(colabOneOutputShareZipURL(), {
      fallbackFilename: `${sanitizeColabOneZipStem(displayTitle.value, "ColabOne-gallery")}.zip`,
      signal: controller.signal,
    });
  } catch (err) {
    if (
      unmounted ||
      myGen !== downloadGen ||
      controller.signal.aborted ||
      (err instanceof Error && err.name === "AbortError")
    ) {
      return;
    }
    if (colabOneOutputShareSessionExpired.value) {
      markSessionExpired();
      return;
    }
    const msg =
      err instanceof Error && err.message
        ? tb(err.message)
        : t("outputShare.guestNetworkError");
    toastManager.push({ kind: "error", message: msg });
  } finally {
    if (!unmounted && myGen === downloadGen) {
      if (downloadAbort === controller) {
        downloadAbort = null;
      }
      downloadingZipKey.value = "";
    }
  }
}

function handleAuthRequired() {
  if (unmounted) return;
  markSessionExpired();
}

watch(
  [displayTitle, colabOneLocaleCode],
  () => {
    if (typeof document !== "undefined") {
      document.title = `${displayTitle.value} · ColabOne`;
    }
  },
  { immediate: true },
);

colabOneOutputShareMode.value = true;

onMounted(() => {
  colabOneOutputShareMode.value = true;
  window.addEventListener(
    COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
    handleAuthRequired,
  );
  window.addEventListener("resize", updateToastOffset, { passive: true });
  updateToastOffset();
  if (headerRef.value && typeof ResizeObserver !== "undefined") {
    headerResizeObserver = new ResizeObserver(() => {
      updateToastOffset();
    });
    headerResizeObserver.observe(headerRef.value);
  }
  void loadManifest();
});

onUnmounted(() => {
  unmounted = true;
  loadAbort?.abort();
  loadAbort = null;
  probeAbort?.abort();
  probeAbort = null;
  abortActiveDownloads();
  probeItemGen.clear();
  toastManager.clear();
  window.removeEventListener(
    COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
    handleAuthRequired,
  );
  window.removeEventListener("resize", updateToastOffset);
  headerResizeObserver?.disconnect();
  headerResizeObserver = null;
  if (!isColabOneOutputSharePath()) {
    colabOneOutputShareMode.value = false;
  }
});
</script>

<template>
  <AConfigProvider :theme="colabOneTheme" :locale="colabOneAntdLocale">
    <div class="co-gal-shell">
      <div class="co-gal-ambient" aria-hidden="true" />

      <!-- Sticky Frosted Guest Gallery Header -->
      <header ref="headerRef" class="co-gal-header">
        <div class="co-gal-header-inner">
          <div class="co-gal-brand-group">
            <span
              class="co-gal-brand-icon"
              aria-hidden="true"
              v-html="appIcon"
            />
            <div class="co-gal-title-stack">
              <div class="co-gal-title-row">
                <h1 class="co-gal-title" data-testid="output-share-guest-title">
                  {{ displayTitle }}
                </h1>
                <ATag color="blue" :bordered="false" class="co-gal-guest-tag">
                  {{ t("outputShare.guestBadge") }}
                </ATag>
                <ATag
                  v-if="manifest && preparedGroups.length > 0"
                  :bordered="false"
                  class="co-gal-count-tag"
                >
                  {{ t("comfy.outputCount", { count: preparedGroups.length }) }}
                </ATag>
                <span v-if="expiryLabel" class="co-gal-expiry-pill">
                  <PhClock :size="12" aria-hidden="true" />
                  <span>{{ expiryLabel }}</span>
                </span>
              </div>
            </div>
          </div>

          <div class="co-gal-header-actions">
            <ColabOneTooltip
              v-if="manifest && manifest.allow_download && totalAvailableItems > 1"
              :title="t('outputGallery.downloadAllZip')"
              placement="bottom"
            >
              <AButton
                type="primary"
                class="co-gal-zip-all-btn"
                :loading="downloadingZipKey === '__all__'"
                :aria-label="t('outputGallery.downloadAllZip')"
                data-testid="output-share-download-all-zip"
                @click="downloadAllZip"
              >
                <template #icon><PhFileArchive :size="15" /></template>
                <span class="co-gal-zip-all-label">
                  {{ t("outputGallery.downloadAllZip") }}
                </span>
              </AButton>
            </ColabOneTooltip>

            <ColabOneTooltip
              :title="
                colabOneEffectiveAppearance === 'dark'
                  ? t('nav.switchLight')
                  : t('nav.switchDark')
              "
              placement="bottom"
            >
              <button
                type="button"
                class="co-gal-icon-btn"
                :aria-label="
                  colabOneEffectiveAppearance === 'dark'
                    ? t('nav.switchLight')
                    : t('nav.switchDark')
                "
                data-testid="output-share-theme-toggle"
                @click="toggleAppearance($event)"
              >
                <PhSun
                  v-if="colabOneEffectiveAppearance === 'dark'"
                  :size="17"
                />
                <PhMoon v-else :size="17" />
              </button>
            </ColabOneTooltip>

            <LanguageSwitcher mode="topbar" />
          </div>
        </div>
      </header>

      <!-- Main Gallery Content -->
      <main class="co-gal-main">
        <!-- Session Expired / Revoked -->
        <section
          v-if="sessionExpired"
          class="co-gal-state-wrap"
          role="alert"
          data-testid="output-share-expired-card"
        >
          <div class="co-gal-state-card">
            <div class="co-gal-state-emblem is-warning" aria-hidden="true">
              <PhLinkBreak :size="30" weight="duotone" />
            </div>
            <h2 class="co-gal-state-title">
              {{ t("outputShare.guestExpiredTitle") }}
            </h2>
            <p class="co-gal-state-desc">
              {{ t("outputShare.guestExpiredDesc") }}
            </p>
            <AButton
              type="primary"
              :loading="loading"
              data-testid="output-share-expired-retry"
              @click="loadManifest"
            >
              <template #icon><PhArrowsClockwise :size="15" /></template>
              {{ t("outputShare.guestRetryBtn") }}
            </AButton>
          </div>
        </section>

        <!-- Transient Network Error -->
        <section
          v-else-if="loadError && !manifest"
          class="co-gal-state-wrap"
          role="alert"
          data-testid="output-share-error-card"
        >
          <div class="co-gal-state-card">
            <div class="co-gal-state-emblem" aria-hidden="true">
              <PhWifiSlash :size="30" weight="duotone" />
            </div>
            <h2 class="co-gal-state-title">
              {{ t("outputShare.guestNetworkError") }}
            </h2>
            <p class="co-gal-state-desc">{{ loadError }}</p>
            <AButton
              type="primary"
              :loading="loading"
              data-testid="output-share-error-retry"
              @click="loadManifest"
            >
              <template #icon><PhArrowsClockwise :size="15" /></template>
              {{ t("outputShare.guestRetryBtn") }}
            </AButton>
          </div>
        </section>

        <!-- Initial Loading Skeleton Grid -->
        <div
          v-else-if="loading && !manifest"
          class="co-gal-grid"
          role="status"
          aria-busy="true"
        >
          <article
            v-for="idx in 6"
            :key="idx"
            class="co-gal-card co-gal-card-skeleton"
          >
            <div class="co-gal-media-frame">
              <ColabOneSkeleton
                width="100%"
                height="100%"
                :radius="0"
                variant="rect"
                :delay="(idx - 1) * 70"
              />
            </div>
            <div class="co-gal-card-body">
              <ColabOneSkeleton
                width="65%"
                :height="15"
                variant="text"
                :delay="(idx - 1) * 70 + 40"
              />
              <ColabOneSkeleton
                width="48%"
                :height="12"
                variant="text"
                tone="subtle"
                :delay="(idx - 1) * 70 + 80"
              />
            </div>
          </article>
        </div>

        <!-- Empty Gallery -->
        <section
          v-else-if="preparedGroups.length === 0"
          class="co-gal-state-wrap"
          data-testid="output-share-empty-card"
        >
          <div class="co-gal-state-card">
            <AEmpty :description="t('outputShare.guestEmptyTitle')">
              <template #image>
                <div class="co-gal-state-emblem" aria-hidden="true">
                  <PhImages :size="30" weight="duotone" />
                </div>
              </template>
              <p class="co-gal-state-desc">
                {{ t("outputShare.guestEmptyDesc") }}
              </p>
            </AEmpty>
          </div>
        </section>

        <!-- Active Gallery Grid -->
        <template v-else>
          <p class="co-gal-readonly-hint">
            <span>{{ t("outputShare.guestReadonlyHint") }}</span>
            <span v-if="expiryLabel" class="co-gal-mobile-expiry">
              <PhClock :size="12" aria-hidden="true" />
              <span>{{ expiryLabel }}</span>
            </span>
          </p>

          <div class="co-gal-grid" data-testid="output-share-gallery-grid">
            <article
              v-for="group in preparedGroups"
              :key="group.id"
              class="co-gal-card"
              :data-testid="`output-share-group-${group.id}`"
            >
              <!-- MULTI-ITEM ALBUM CARD -->
              <template v-if="group.items.length > 1">
                <button
                  type="button"
                  class="co-gal-collage-btn"
                  :aria-label="`${tb(group.title)} - ${t('outputGallery.openAlbum')}`"
                  @click="openGalleryModal(group)"
                >
                  <div
                    class="co-gal-collage"
                    :class="`is-count-${Math.min(4, group.coverItems.length)}`"
                  >
                    <div
                      v-for="(tile, tileIdx) in group.coverItems"
                      :key="tile.id"
                      class="co-gal-collage-tile"
                    >
                      <div
                        v-if="tile.available === false"
                        class="co-gal-tile-placeholder"
                      >
                        <PhWifiSlash :size="20" weight="duotone" />
                      </div>
                      <img
                        v-else-if="tile.kind === 'image'"
                        :src="resolveGuestMediaURL(tile.id)"
                        :alt="tile.filename"
                        loading="lazy"
                        decoding="async"
                        @error="onGuestMediaError(tile)"
                      />
                      <div
                        v-else-if="tile.kind === 'video'"
                        class="co-gal-tile-placeholder"
                      >
                        <PhVideoCamera :size="22" weight="duotone" />
                      </div>
                      <div v-else class="co-gal-tile-placeholder">
                        <PhWaveform :size="22" weight="duotone" />
                      </div>

                      <div
                        v-if="
                          group.coverItems.length > 1 &&
                          group.extraCount > 0 &&
                          tileIdx === group.coverItems.length - 1
                        "
                        class="co-gal-collage-more"
                      >
                        +{{ group.extraCount }}
                      </div>
                    </div>
                  </div>

                  <span class="co-gal-cover-badge">
                    <PhImages :size="13" weight="duotone" aria-hidden="true" />
                    <span>
                      {{
                        group.items.every((item) => item.kind === "image")
                          ? t("outputGallery.albumTag", {
                              count: group.items.length,
                            })
                          : t("outputGallery.albumMixedTag", {
                              count: group.items.length,
                            })
                      }}
                    </span>
                  </span>
                </button>

                <div class="co-gal-card-body">
                  <div class="co-gal-card-title-row">
                    <strong class="co-gal-card-title" :title="tb(group.title)">
                      {{ tb(group.title) }}
                    </strong>
                  </div>

                  <div v-if="group.inspectCount > 0" class="co-gal-card-tags">
                    <ATag
                      color="orange"
                      :bordered="false"
                      class="co-gal-small-tag"
                    >
                      {{
                        t("outputGallery.inspectIncludedHint", {
                          count: group.inspectCount,
                          inspect: group.inspectCount,
                        })
                      }}
                    </ATag>
                  </div>

                  <div class="co-gal-card-meta">
                    <span v-if="group.totalSize > 0">
                      {{ formatOutputSize(group.totalSize) }}
                    </span>
                    <span
                      v-if="group.totalSize > 0 && group.createdAt > 0"
                      aria-hidden="true"
                    >
                      ·
                    </span>
                    <span v-if="group.createdAt > 0">
                      {{ formatOutputCreatedAt(group.createdAt) }}
                    </span>
                  </div>

                  <div class="co-gal-card-actions">
                    <div class="co-gal-action-primary">
                      <ColabOneTooltip :title="t('outputGallery.openAlbum')">
                        <AButton
                          size="small"
                          type="primary"
                          ghost
                          class="co-gal-primary-action-btn"
                          :aria-label="t('outputGallery.openAlbum')"
                          @click="openGalleryModal(group)"
                        >
                          <template #icon><PhEye :size="14" /></template>
                          <span class="co-gal-action-label">{{
                            t("outputGallery.openAlbumShort")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                    </div>
                    <div class="co-gal-action-utils">
                      <ColabOneTooltip
                        v-if="manifest?.allow_download && group.hasAvailable"
                        :title="t('outputGallery.downloadAlbumZip')"
                      >
                        <AButton
                          size="small"
                          class="co-gal-icon-action-btn"
                          :loading="downloadingZipKey === group.id"
                          :aria-label="t('outputGallery.downloadAlbumZip')"
                          @click="downloadGroupZip(group)"
                        >
                          <template #icon><PhFileArchive :size="14" /></template>
                          <span class="co-sr-only">{{
                            t("outputGallery.downloadAlbumZip")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                    </div>
                  </div>
                </div>
              </template>

              <!-- SINGLE-ITEM CARD -->
              <template v-else>
                <div class="co-gal-media-frame">
                  <div
                    v-if="group.items[0].available === false"
                    class="co-gal-offline-box"
                  >
                    <PhWifiSlash :size="28" weight="duotone" />
                    <span>{{ t("outputGallery.itemOfflineNotice") }}</span>
                  </div>

                  <div
                    v-else-if="probedErrorIds[group.items[0].id]"
                    class="co-gal-offline-box"
                    role="alert"
                    :data-testid="`output-share-item-error-${group.items[0].id}`"
                  >
                    <PhWarningCircle :size="26" weight="duotone" />
                    <span>{{ t("outputGallery.loadFailedRetry") }}</span>
                    <AButton
                      size="small"
                      type="primary"
                      ghost
                      :data-testid="`output-share-item-retry-${group.items[0].id}`"
                      @click="retryModalItemLoad(group.items[0])"
                    >
                      <template #icon><PhArrowsClockwise :size="13" /></template>
                      {{ t("outputGallery.retryLoadBtn") }}
                    </AButton>
                  </div>

                  <button
                    v-else-if="group.items[0].kind === 'image'"
                    type="button"
                    class="co-gal-single-image-btn"
                    :aria-label="`${group.items[0].filename} - ${t('outputGallery.viewFullImage')}`"
                    @click="openGalleryModal(group, group.items[0].id)"
                  >
                    <img
                      :src="resolveGuestMediaURL(group.items[0].id)"
                      :alt="group.items[0].filename"
                      loading="lazy"
                      decoding="async"
                      @error="onGuestMediaError(group.items[0])"
                    />
                    <span class="co-gal-zoom-hover-pill">
                      <PhMagnifyingGlassPlus :size="13" />
                      <span>{{ t("outputGallery.viewFullImage") }}</span>
                    </span>
                  </button>

                  <video
                    v-else-if="group.items[0].kind === 'video'"
                    :src="resolveGuestMediaURL(group.items[0].id)"
                    controls
                    :controlslist="
                      !manifest?.allow_download ? 'nodownload' : undefined
                    "
                    playsinline
                    preload="metadata"
                    class="co-gal-single-video"
                    @error="onGuestMediaError(group.items[0])"
                  />

                  <div v-else class="co-gal-single-audio">
                    <PhWaveform :size="28" class="co-gal-audio-icon" />
                    <audio
                      :src="resolveGuestMediaURL(group.items[0].id)"
                      controls
                      :controlslist="
                        !manifest?.allow_download ? 'nodownload' : undefined
                      "
                      preload="metadata"
                      @error="onGuestMediaError(group.items[0])"
                    />
                  </div>
                </div>

                <div class="co-gal-card-body">
                  <div class="co-gal-card-title-row">
                    <strong class="co-gal-card-title" :title="tb(group.title)">
                      {{ tb(group.title) }}
                    </strong>
                    <ATag
                      v-if="isColabOneInspectOutput(group.items[0])"
                      color="orange"
                      :bordered="false"
                      class="co-gal-small-tag"
                    >
                      {{ t("outputGallery.inspectBadge") }}
                    </ATag>
                  </div>

                  <span
                    class="co-gal-filename"
                    :title="group.items[0].filename"
                  >
                    {{ group.items[0].filename }}
                  </span>

                  <div class="co-gal-card-meta">
                    <span v-if="group.items[0].size > 0">
                      {{ formatOutputSize(group.items[0].size) }}
                    </span>
                    <span
                      v-if="
                        group.items[0].size > 0 && group.items[0].created_at > 0
                      "
                      aria-hidden="true"
                    >
                      ·
                    </span>
                    <span v-if="group.items[0].created_at > 0">
                      {{ formatOutputCreatedAt(group.items[0].created_at) }}
                    </span>
                  </div>

                  <div
                    v-if="
                      group.items[0].available !== false &&
                      (group.items[0].kind === 'image' ||
                        Boolean(manifest?.allow_download))
                    "
                    class="co-gal-card-actions"
                  >
                    <div class="co-gal-action-primary">
                      <ColabOneTooltip
                        v-if="group.items[0].kind === 'image'"
                        :title="t('outputGallery.viewFullImage')"
                      >
                        <AButton
                          size="small"
                          class="co-gal-primary-action-btn"
                          :aria-label="t('outputGallery.viewFullImage')"
                          @click="openGalleryModal(group, group.items[0].id)"
                        >
                          <template #icon><PhImage :size="14" /></template>
                          <span class="co-gal-action-label">{{
                            t("outputGallery.openAlbumShort")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                      <ColabOneTooltip
                        v-if="manifest?.allow_download"
                        :title="t('outputGallery.downloadCurrent')"
                      >
                        <AButton
                          size="small"
                          type="primary"
                          ghost
                          class="co-gal-primary-action-btn"
                          :loading="downloadingItemId === group.items[0].id"
                          :aria-label="t('outputGallery.downloadCurrent')"
                          @click="downloadSingleItem(group.items[0])"
                        >
                          <template #icon><PhDownloadSimple :size="14" /></template>
                          <span class="co-gal-action-label">{{
                            t("outputGallery.downloadCurrent")
                          }}</span>
                        </AButton>
                      </ColabOneTooltip>
                    </div>
                  </div>
                </div>
              </template>
            </article>
          </div>
        </template>
      </main>

      <!-- Minimal Guest Footer -->
      <footer class="co-gal-footer">
        <span>{{ t("outputShare.guestFooter") }}</span>
      </footer>

      <!-- Full-Window Album & Zoomable Lightbox -->
      <ColabOneOutputGalleryModal
        :open="galleryModalOpen"
        :title="galleryModalGroup?.title || displayTitle"
        :items="galleryModalGroup?.items || []"
        :initial-item-id="galleryModalInitialItemId"
        :resolve-media-src="resolveModalMediaSrc"
        :is-item-available="isModalItemAvailable"
        :get-item-error="getModalItemError"
        :allow-download="Boolean(manifest?.allow_download)"
        :allow-zip-download="
          Boolean(
            manifest?.allow_download &&
              galleryModalGroup &&
              galleryModalGroup.items.length > 1 &&
              galleryModalGroup.hasAvailable,
          )
        "
        :allow-delete="false"
        :downloading-item-id="downloadingItemId"
        :downloading-zip="
          Boolean(
            galleryModalGroup && downloadingZipKey === galleryModalGroup.id,
          )
        "
        @close="closeGalleryModal"
        @retry-load="retryModalItemLoad"
        @download-item="downloadSingleItem"
        @download-zip="
          galleryModalGroup && downloadGroupZip(galleryModalGroup)
        "
        @media-error="onGuestMediaError"
      />

      <ColabOneToastHost
        :items="toastManager.items.value"
        placement="top"
        :top-offset-px="toastTopOffsetPx"
        @pause="toastManager.pause"
        @resume="toastManager.resume"
        @dismiss="toastManager.dismiss"
      />
    </div>
  </AConfigProvider>
</template>

<style scoped>
.co-gal-shell {
  position: relative;
  width: 100%;
  height: 100%;
  overflow-y: auto;
  overflow-x: hidden;
  display: flex;
  flex-direction: column;
  background: var(--co-canvas);
  color: var(--co-text);
  isolation: isolate;
}

.co-gal-ambient {
  position: fixed;
  inset: 0 0 auto 0;
  height: 360px;
  pointer-events: none;
  z-index: -1;
  background:
    radial-gradient(
      760px 280px at 18% -12%,
      rgba(59, 130, 246, 0.11),
      transparent 70%
    ),
    radial-gradient(
      620px 260px at 84% -8%,
      rgba(99, 102, 241, 0.08),
      transparent 72%
    );
}

:root[data-theme="dark"] .co-gal-ambient {
  background:
    radial-gradient(
      760px 280px at 18% -12%,
      rgba(59, 130, 246, 0.16),
      transparent 70%
    ),
    radial-gradient(
      620px 260px at 84% -8%,
      rgba(129, 140, 248, 0.12),
      transparent 72%
    );
}

/* Sticky Frosted Header */
.co-gal-header {
  position: sticky;
  top: 0;
  z-index: 20;
  padding-top: var(--co-safe-top, 0px);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  background: color-mix(in srgb, var(--co-canvas) 82%, transparent);
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-gal-header-inner {
  width: 100%;
  max-width: 1240px;
  margin: 0 auto;
  padding: 12px clamp(16px, 3vw, 32px);
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
}

.co-gal-brand-group {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 1 1 auto;
}

.co-gal-brand-icon {
  width: 34px;
  height: 34px;
  border-radius: 10px;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  box-shadow:
    0 1px 3px rgba(15, 23, 42, 0.12),
    0 0 0 1px rgba(15, 23, 42, 0.06);
}

:root[data-theme="dark"] .co-gal-brand-icon {
  box-shadow:
    0 2px 6px rgba(0, 0, 0, 0.4),
    0 0 0 1px rgba(255, 255, 255, 0.08);
}

.co-gal-brand-icon :deep(svg) {
  width: 100%;
  height: 100%;
  display: block;
}

.co-gal-title-stack {
  min-width: 0;
  flex: 1 1 auto;
}

.co-gal-title-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  min-width: 0;
}

.co-gal-title {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.3;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 420px;
}

.co-gal-guest-tag,
.co-gal-count-tag {
  margin-inline-end: 0;
  font-size: 11.5px;
  line-height: 19px;
  white-space: nowrap;
  flex-shrink: 0;
  border-radius: var(--co-radius-pill);
}

.co-gal-expiry-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--co-muted);
  white-space: nowrap;
}

.co-gal-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.co-gal-header-actions :deep(.co-locale-trigger) {
  height: 38px;
}

.co-gal-zip-all-btn {
  height: 38px;
  border-radius: 9999px;
  font-weight: 500;
}

.co-gal-icon-btn {
  width: 38px;
  height: 38px;
  border-radius: 9999px;
  border: 1px solid transparent;
  background: var(--co-track);
  color: var(--co-secondary-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

:root[data-theme="dark"] .co-gal-icon-btn {
  border-color: var(--co-border-secondary);
}

.co-gal-icon-btn:hover {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-gal-icon-btn:active {
  transform: scale(0.95);
}

.co-gal-icon-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

/* Main Content */
.co-gal-main {
  width: 100%;
  max-width: 1240px;
  margin: 0 auto;
  padding: 22px clamp(16px, 3vw, 32px) 40px;
  box-sizing: border-box;
  flex: 1 0 auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.co-gal-readonly-hint {
  margin: 0;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 10px;
  font-size: 13px;
  color: var(--co-secondary-text);
}

.co-gal-mobile-expiry {
  display: none;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: var(--co-radius-pill);
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--co-muted);
  white-space: nowrap;
}

/* Responsive Gallery Grid */
.co-gal-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
  gap: 16px;
  align-items: stretch;
}

.co-gal-card {
  container-type: inline-size;
  container-name: output-card;
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  box-shadow: var(--co-card-shadow);
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.14s ease;
}

.co-gal-card:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
}

.co-gal-card-skeleton {
  pointer-events: none;
}

/* Multi-thumbnail Album Collage Cover */
.co-gal-collage-btn {
  position: relative;
  width: 100%;
  aspect-ratio: 4 / 3;
  padding: 0;
  border: 0;
  border-bottom: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  overflow: hidden;
  cursor: pointer;
  display: block;
}

.co-gal-collage {
  width: 100%;
  height: 100%;
  display: grid;
  gap: 2px;
  background: var(--co-border-secondary);
}

.co-gal-collage.is-count-1 {
  grid-template-columns: 1fr;
  grid-template-rows: 1fr;
}

.co-gal-collage.is-count-2 {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr;
}

.co-gal-collage.is-count-3 {
  grid-template-columns: 1.35fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.co-gal-collage.is-count-3 .co-gal-collage-tile:first-child {
  grid-row: 1 / span 2;
}

.co-gal-collage.is-count-4 {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.co-gal-collage-tile {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--co-subtle);
}

.co-gal-collage-btn:focus-visible,
.co-gal-single-image-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: -2px;
}

.co-gal-collage-tile img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center top;
  display: block;
  transition: transform 0.24s ease;
}

.co-gal-collage-btn:hover .co-gal-collage-tile img {
  transform: scale(1.03);
}

.co-gal-tile-placeholder {
  width: 100%;
  height: 100%;
  display: grid;
  place-items: center;
  color: var(--co-muted);
}

.co-gal-collage-more {
  position: absolute;
  inset: 0;
  background: rgba(15, 23, 42, 0.58);
  backdrop-filter: blur(2px);
  color: #ffffff;
  font-size: 20px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  display: grid;
  place-items: center;
}

.co-gal-cover-badge {
  position: absolute;
  top: 10px;
  left: 10px;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 9px;
  border-radius: 9999px;
  background: rgba(15, 23, 42, 0.74);
  backdrop-filter: blur(8px);
  color: #ffffff;
  font-size: 11.5px;
  font-weight: 600;
}

/* Single Item Media Frame */
.co-gal-media-frame {
  position: relative;
  width: 100%;
  aspect-ratio: 4 / 3;
  background: var(--co-subtle);
  border-bottom: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-gal-single-image-btn {
  position: relative;
  width: 100%;
  height: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-gal-single-image-btn img {
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
}

.co-gal-zoom-hover-pill {
  position: absolute;
  bottom: 10px;
  right: 10px;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border-radius: 9999px;
  background: rgba(15, 23, 42, 0.74);
  backdrop-filter: blur(8px);
  color: #ffffff;
  font-size: 11.5px;
  font-weight: 500;
  opacity: 0;
  transition: opacity 0.16s ease;
}

.co-gal-single-image-btn:hover .co-gal-zoom-hover-pill,
.co-gal-single-image-btn:focus-visible .co-gal-zoom-hover-pill {
  opacity: 1;
}

.co-gal-single-video {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000000;
}

.co-gal-single-audio {
  width: 100%;
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.co-gal-audio-icon {
  color: var(--co-accent);
}

.co-gal-single-audio audio {
  width: 100%;
}

.co-gal-offline-box {
  width: 100%;
  height: 100%;
  padding: 20px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--co-muted);
  font-size: 12.5px;
  text-align: center;
}

/* Card Body */
.co-gal-card-body {
  padding: 13px 15px 14px;
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  gap: 6px;
  min-width: 0;
}

.co-gal-card-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.co-gal-card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-gal-card-tags {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}

.co-gal-small-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
  white-space: nowrap;
  flex-shrink: 0;
  border-radius: var(--co-radius-pill);
}

.co-gal-filename {
  font-family: var(--co-font-mono);
  font-size: 11.5px;
  color: var(--co-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-gal-card-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

.co-gal-card-actions {
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid var(--co-border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: nowrap;
  gap: 6px;
}

.co-gal-action-primary {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-shrink: 1;
}

.co-gal-action-utils {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.co-gal-icon-action-btn {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0 !important;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.co-gal-primary-action-btn {
  height: 28px;
  padding-inline: 8px !important;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

@container output-card (max-width: 295px) {
  .co-gal-action-label {
    display: none;
  }

  .co-gal-primary-action-btn {
    width: 28px;
    min-width: 28px;
    padding-inline: 0 !important;
    justify-content: center;
    gap: 0;
  }
}

@container output-card (max-width: 175px) {
  .co-gal-card-actions {
    flex-wrap: wrap;
  }
}

/* Expired / Error / Empty Centered States */
.co-gal-state-wrap {
  flex: 1 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 36px 0 48px;
}

.co-gal-state-wrap :deep(.ant-empty-image) {
  height: auto;
  margin-bottom: 12px;
}

.co-gal-state-card {
  width: 100%;
  max-width: 440px;
  padding: 32px 28px;
  border-radius: 18px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.co-gal-state-emblem {
  width: 56px;
  height: 56px;
  border-radius: 16px;
  display: grid;
  place-items: center;
  background: var(--co-track);
  border: 1px solid var(--co-border);
  color: var(--co-secondary-text);
  margin: 0 auto 4px;
}

.co-gal-state-emblem.is-warning {
  background: var(--co-warning-bg);
  border-color: var(--co-warning-border);
  color: var(--co-warning-text);
}

.co-gal-state-title {
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  color: var(--co-text);
}

.co-gal-state-desc {
  margin: 0 0 8px;
  font-size: 13.5px;
  line-height: 1.65;
  color: var(--co-secondary-text);
}

/* Minimal Footer */
.co-gal-footer {
  width: 100%;
  max-width: 1240px;
  margin: 0 auto;
  padding: 14px clamp(16px, 3vw, 32px) calc(24px + var(--co-safe-bottom, 0px));
  box-sizing: border-box;
  text-align: center;
  font-size: 12px;
  color: var(--co-muted);
}

@media (max-width: 680px) {
  .co-gal-header-inner {
    padding: 9px 14px;
    gap: 8px;
  }

  .co-gal-brand-group {
    gap: 8px;
  }

  .co-gal-brand-icon {
    width: 30px;
    height: 30px;
    border-radius: 9px;
  }

  .co-gal-title-row {
    gap: 4px 6px;
  }

  .co-gal-title {
    font-size: 15px;
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .co-gal-zip-all-label,
  .co-gal-expiry-pill {
    display: none;
  }

  .co-gal-mobile-expiry {
    display: inline-flex;
  }

  .co-gal-zip-all-btn {
    width: 34px;
    min-width: 34px;
    height: 34px;
    padding-inline: 0 !important;
  }

  .co-gal-icon-btn {
    width: 34px;
    height: 34px;
  }

  .co-gal-header-actions :deep(.co-locale-trigger) {
    height: 34px;
  }

  .co-gal-main {
    padding: 16px 14px 32px;
  }
}

@media (max-width: 480px) {
  .co-gal-guest-tag {
    display: none;
  }
}

@media (max-width: 420px) {
  .co-gal-zip-all-btn {
    width: 32px;
    min-width: 32px;
    height: 32px;
  }

  .co-gal-icon-btn {
    width: 32px;
    height: 32px;
  }

  .co-gal-header-actions :deep(.co-locale-trigger) {
    height: 32px;
  }
}
</style>
