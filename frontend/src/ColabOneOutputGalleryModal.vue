<script lang="ts">
export interface ColabOneGalleryModalItem {
  id: string;
  filename: string;
  kind: "image" | "video" | "audio";
  size: number;
  created_at?: number;
  workflow_name?: string;
  is_inspect?: boolean;
  available?: boolean;
  elapsed?: number;
}
</script>

<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  ref,
  watch,
} from "vue";
import {
  Button as AButton,
  Modal as AModal,
  Popconfirm as APopconfirm,
  Segmented as ASegmented,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhArrowsClockwise,
  PhArrowsIn,
  PhCaretLeft,
  PhCaretRight,
  PhCornersOut,
  PhDownloadSimple,
  PhFileArchive,
  PhImage,
  PhMagnifyingGlassMinus,
  PhMagnifyingGlassPlus,
  PhTrash,
  PhVideoCamera,
  PhWarningCircle,
  PhWaveform,
  PhWifiSlash,
  PhX,
} from "@phosphor-icons/vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { isColabOneInspectOutput } from "./colabOneOutputGallery";
import { formatOutputSize } from "./comfyui";
import { t, tb } from "./i18n";

defineOptions({
  name: "ColabOneOutputGalleryModal",
});

const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    items: ColabOneGalleryModalItem[];
    initialItemId?: string;
    initialMode?: "stage" | "grid";
    resolveMediaSrc: (item: ColabOneGalleryModalItem) => string;
    isItemLoading?: (item: ColabOneGalleryModalItem) => boolean;
    isItemAvailable?: (item: ColabOneGalleryModalItem) => boolean;
    getItemError?: (item: ColabOneGalleryModalItem) => string;
    isItemDownloading?: (item: ColabOneGalleryModalItem) => boolean;
    isItemDeleting?: (item: ColabOneGalleryModalItem) => boolean;
    canDeleteItem?: (item: ColabOneGalleryModalItem) => boolean;
    allowDownload?: boolean;
    allowZipDownload?: boolean;
    allowDelete?: boolean;
    downloadingItemId?: string;
    downloadingZip?: boolean;
    deletingItemId?: string;
  }>(),
  {
    initialItemId: "",
    initialMode: "stage",
    isItemLoading: undefined,
    isItemAvailable: undefined,
    getItemError: undefined,
    isItemDownloading: undefined,
    isItemDeleting: undefined,
    canDeleteItem: undefined,
    allowDownload: true,
    allowZipDownload: true,
    allowDelete: false,
    downloadingItemId: "",
    downloadingZip: false,
    deletingItemId: "",
  },
);

const emit = defineEmits<{
  close: [];
  "active-change": [itemId: string];
  "request-load": [
    item: ColabOneGalleryModalItem,
    priority?: "active" | "prefetch",
  ];
  "retry-load": [item: ColabOneGalleryModalItem];
  "media-error": [item: ColabOneGalleryModalItem];
  "download-item": [item: ColabOneGalleryModalItem];
  "download-zip": [];
  "delete-item": [item: ColabOneGalleryModalItem];
}>();

const MIN_ZOOM = 0.5;
const MAX_ZOOM = 4.0;
const ZOOM_STEP = 0.25;

const viewMode = ref<"stage" | "grid">("stage");
const activeIndex = ref(0);
const zoom = ref(1);
const panX = ref(0);
const panY = ref(0);
const isDragging = ref(false);
const naturalWidth = ref(0);
const naturalHeight = ref(0);
const stageImageLoaded = ref(false);
const stageImageError = ref(false);
const deleteConfirmOpen = ref(false);

const stageRef = ref<HTMLElement | null>(null);
const stageImageRef = ref<HTMLImageElement | null>(null);
const filmstripRef = ref<HTMLElement | null>(null);
const thumbElements = new Map<string, HTMLElement>();

let dragPointerId: number | null = null;
let dragStartX = 0;
let dragStartY = 0;
let panStartX = 0;
let panStartY = 0;
let keyboardBound = false;
let lastEmittedActiveId = "";

const safeItems = computed<ColabOneGalleryModalItem[]>(() =>
  Array.isArray(props.items) ? props.items : [],
);

const activeItem = computed<ColabOneGalleryModalItem | null>(() => {
  const list = safeItems.value;
  if (list.length === 0) return null;
  const idx = Math.max(0, Math.min(activeIndex.value, list.length - 1));
  return list[idx] ?? null;
});

const activeItemSrc = computed<string>(() =>
  activeItem.value ? (props.resolveMediaSrc(activeItem.value) || "") : "",
);

const activeItemAvailable = computed<boolean>(() =>
  activeItem.value ? checkItemAvailable(activeItem.value) : false,
);

const activeItemLoading = computed<boolean>(() =>
  activeItem.value ? checkItemLoading(activeItem.value) : false,
);

const activeItemError = computed<string>(() =>
  activeItem.value && props.getItemError
    ? props.getItemError(activeItem.value) || ""
    : "",
);

const isActiveItemDownloading = computed<boolean>(() => {
  if (!activeItem.value) return false;
  if (props.isItemDownloading) {
    return Boolean(props.isItemDownloading(activeItem.value));
  }
  return props.downloadingItemId === activeItem.value.id;
});

const isActiveItemDeleting = computed<boolean>(() => {
  if (!activeItem.value) return false;
  if (props.isItemDeleting) {
    return Boolean(props.isItemDeleting(activeItem.value));
  }
  return props.deletingItemId === activeItem.value.id;
});

const canDeleteActiveItem = computed<boolean>(() => {
  if (!props.allowDelete || !activeItem.value) return false;
  if (props.canDeleteItem) {
    return Boolean(props.canDeleteItem(activeItem.value));
  }
  return true;
});

const activeItemIsInspect = computed<boolean>(() =>
  activeItem.value ? isColabOneInspectOutput(activeItem.value) : false,
);

const canZoomActiveImage = computed<boolean>(
  () =>
    viewMode.value === "stage" &&
    activeItem.value?.kind === "image" &&
    activeItemAvailable.value &&
    Boolean(activeItemSrc.value) &&
    !stageImageError.value &&
    !activeItemError.value,
);

const zoomPercentLabel = computed<string>(
  () => `${Math.round(zoom.value * 100)}%`,
);

const counterLabel = computed<string>(() => {
  const total = safeItems.value.length;
  if (total === 0) return "0 / 0";
  const current = Math.min(activeIndex.value + 1, total);
  return t("outputGallery.counter", {
    current,
    index: current,
    idx: current,
    total,
  });
});

const modeOptions = computed<
  Array<{ value: "stage" | "grid"; label: string }>
>(() => [
  { value: "stage", label: t("outputGallery.viewModeStage") },
  { value: "grid", label: t("outputGallery.viewModeGrid") },
]);

const displayTitle = computed<string>(() =>
  props.title ? tb(props.title) : "",
);

const stageImageStyle = computed<Record<string, string>>(() => ({
  transform: `translate3d(${panX.value}px, ${panY.value}px, 0) scale(${zoom.value})`,
  transition: isDragging.value
    ? "none"
    : "transform 0.16s cubic-bezier(0.16, 1, 0.3, 1)",
}));

function checkItemAvailable(item: ColabOneGalleryModalItem): boolean {
  if (props.isItemAvailable) {
    return Boolean(props.isItemAvailable(item));
  }
  return item.available !== false;
}

function checkItemLoading(item: ColabOneGalleryModalItem): boolean {
  if (props.isItemLoading) {
    return Boolean(props.isItemLoading(item));
  }
  return false;
}

function getMediaSrc(item: ColabOneGalleryModalItem): string {
  return props.resolveMediaSrc(item) || "";
}

function setThumbRef(id: string, el: unknown) {
  if (el instanceof HTMLElement) {
    thumbElements.set(id, el);
  } else {
    thumbElements.delete(id);
  }
}

function pauseStageMedia() {
  const root = stageRef.value;
  if (!root) return;
  root.querySelectorAll<HTMLMediaElement>("video, audio").forEach((el) => {
    try {
      el.pause();
    } catch {
      // Ignore media elements not yet initialized.
    }
  });
}

function clampPan(nextX: number, nextY: number): { x: number; y: number } {
  if (zoom.value <= 1) {
    return { x: 0, y: 0 };
  }
  const stageRect = stageRef.value?.getBoundingClientRect();
  const imgEl = stageImageRef.value;
  const baseW = imgEl?.offsetWidth || stageRect?.width || 640;
  const baseH = imgEl?.offsetHeight || stageRect?.height || 480;
  const viewW = stageRect?.width || baseW;
  const viewH = stageRect?.height || baseH;
  const maxX = Math.max(80, (baseW * zoom.value - viewW * 0.35) / 2);
  const maxY = Math.max(80, (baseH * zoom.value - viewH * 0.35) / 2);
  return {
    x: Math.max(-maxX, Math.min(maxX, nextX)),
    y: Math.max(-maxY, Math.min(maxY, nextY)),
  };
}

function resetZoom() {
  zoom.value = 1;
  panX.value = 0;
  panY.value = 0;
  isDragging.value = false;
  dragPointerId = null;
}

function setZoomLevel(next: number) {
  const clamped = Math.max(
    MIN_ZOOM,
    Math.min(MAX_ZOOM, Math.round(next * 100) / 100),
  );
  zoom.value = clamped;
  if (clamped <= 1) {
    panX.value = 0;
    panY.value = 0;
  } else {
    const bounded = clampPan(panX.value, panY.value);
    panX.value = bounded.x;
    panY.value = bounded.y;
  }
}

function zoomIn() {
  if (!canZoomActiveImage.value) return;
  setZoomLevel(zoom.value + ZOOM_STEP);
}

function zoomOut() {
  if (!canZoomActiveImage.value) return;
  setZoomLevel(zoom.value - ZOOM_STEP);
}

function syncStageImageNaturalSize() {
  const img = stageImageRef.value;
  if (!img || !img.complete) return;
  if (img.naturalWidth > 0) {
    naturalWidth.value = img.naturalWidth;
    naturalHeight.value = img.naturalHeight;
    stageImageLoaded.value = true;
    stageImageError.value = false;
  } else if (activeItemSrc.value) {
    stageImageLoaded.value = false;
    stageImageError.value = true;
  }
}

function setActualZoom() {
  if (!canZoomActiveImage.value) return;
  syncStageImageNaturalSize();
  const imgEl = stageImageRef.value;
  const renderedW = imgEl?.offsetWidth || 0;
  if (renderedW > 0 && naturalWidth.value > 0) {
    const ratio = naturalWidth.value / renderedW;
    const target = ratio > 1.02 ? ratio : 2;
    panX.value = 0;
    panY.value = 0;
    setZoomLevel(target);
    return;
  }
  const stageEl = stageRef.value;
  if (stageEl && naturalWidth.value > 0 && naturalHeight.value > 0) {
    const rect = stageEl.getBoundingClientRect();
    const availW = Math.max(1, rect.width - 48);
    const availH = Math.max(1, rect.height - 48);
    const fitScale = Math.min(
      availW / naturalWidth.value,
      availH / naturalHeight.value,
      1,
    );
    const target = fitScale < 1 ? 1 / fitScale : 2;
    panX.value = 0;
    panY.value = 0;
    setZoomLevel(target);
    return;
  }
  panX.value = 0;
  panY.value = 0;
  setZoomLevel(2);
}

function onStageImageLoad(event: Event) {
  const img = (event.target as HTMLImageElement | null) || stageImageRef.value;
  if (!img) return;
  naturalWidth.value = img.naturalWidth || 0;
  naturalHeight.value = img.naturalHeight || 0;
  stageImageLoaded.value = true;
  stageImageError.value = false;
}

function onStageImageError(item: ColabOneGalleryModalItem) {
  stageImageLoaded.value = false;
  stageImageError.value = true;
  emit("media-error", item);
}

function handleStageRetry(item: ColabOneGalleryModalItem) {
  stageImageError.value = false;
  stageImageLoaded.value = false;
  emit("retry-load", item);
  if (props.open && viewMode.value === "stage") {
    void nextTick(syncStageImageNaturalSize);
  }
}

function onStageWheel(event: WheelEvent) {
  if (!canZoomActiveImage.value) return;
  if (event.deltaY === 0) return;
  let delta: number;
  if (event.deltaMode === 0) {
    const scaled = -event.deltaY * 0.0025;
    const mag = Math.max(0.04, Math.min(ZOOM_STEP, Math.abs(scaled)));
    delta = scaled >= 0 ? mag : -mag;
  } else {
    delta = event.deltaY < 0 ? ZOOM_STEP : -ZOOM_STEP;
  }
  setZoomLevel(zoom.value + delta);
}

function onImageDblClick() {
  if (!canZoomActiveImage.value) return;
  if (zoom.value > 1.05) {
    resetZoom();
  } else {
    panX.value = 0;
    panY.value = 0;
    setZoomLevel(2);
  }
}

function onImagePointerDown(event: PointerEvent) {
  if (!canZoomActiveImage.value || zoom.value <= 1) return;
  if (event.button !== 0) return;
  const target = event.currentTarget as HTMLElement | null;
  dragPointerId = event.pointerId;
  dragStartX = event.clientX;
  dragStartY = event.clientY;
  panStartX = panX.value;
  panStartY = panY.value;
  isDragging.value = true;
  if (target && typeof target.setPointerCapture === "function") {
    try {
      target.setPointerCapture(event.pointerId);
    } catch {
      // Ignore capture errors in synthetic/test environments.
    }
  }
}

function onImagePointerMove(event: PointerEvent) {
  if (!isDragging.value || dragPointerId !== event.pointerId) return;
  const bounded = clampPan(
    panStartX + (event.clientX - dragStartX),
    panStartY + (event.clientY - dragStartY),
  );
  panX.value = bounded.x;
  panY.value = bounded.y;
}

function endImageDrag(event: PointerEvent) {
  if (!isDragging.value || dragPointerId !== event.pointerId) return;
  const target = event.currentTarget as HTMLElement | null;
  if (
    target &&
    typeof target.releasePointerCapture === "function" &&
    typeof target.hasPointerCapture === "function" &&
    target.hasPointerCapture(event.pointerId)
  ) {
    try {
      target.releasePointerCapture(event.pointerId);
    } catch {
      // Ignore release failures.
    }
  }
  isDragging.value = false;
  dragPointerId = null;
}

function emitActiveChangeIfNeeded(itemId: string) {
  if (!props.open || !itemId || itemId === lastEmittedActiveId) return;
  lastEmittedActiveId = itemId;
  emit("active-change", itemId);
}

function requestLoadIfNeeded(
  item: ColabOneGalleryModalItem | undefined | null,
  priority: "active" | "prefetch" = "prefetch",
) {
  if (!item || !props.open) return;
  if (!checkItemAvailable(item)) return;
  if (props.getItemError && props.getItemError(item)) return;
  if (getMediaSrc(item) || checkItemLoading(item)) return;
  emit("request-load", item, priority);
}

function requestActiveAndNeighborMedia() {
  const list = safeItems.value;
  if (!props.open || list.length === 0) return;
  if (viewMode.value === "grid") {
    for (const item of list) {
      if (item.kind === "image") {
        requestLoadIfNeeded(item, "prefetch");
      }
    }
    return;
  }
  const idx = activeIndex.value;
  requestLoadIfNeeded(list[idx], "active");
  if (list.length > 1) {
    requestLoadIfNeeded(list[(idx + 1) % list.length], "prefetch");
    requestLoadIfNeeded(list[(idx - 1 + list.length) % list.length], "prefetch");
  }
  if (list.length > 2) {
    requestLoadIfNeeded(list[(idx + 2) % list.length], "prefetch");
  }
}

function scrollActiveThumbIntoView() {
  const item = activeItem.value;
  if (!item || viewMode.value !== "stage") return;
  void nextTick(() => {
    const el = thumbElements.get(item.id);
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({
        behavior: "smooth",
        inline: "center",
        block: "nearest",
      });
    }
  });
}

function selectIndex(idx: number, switchToStage = false) {
  const total = safeItems.value.length;
  if (total === 0) return;
  const normalized = ((idx % total) + total) % total;
  if (normalized !== activeIndex.value) {
    pauseStageMedia();
    deleteConfirmOpen.value = false;
    activeIndex.value = normalized;
    naturalWidth.value = 0;
    naturalHeight.value = 0;
    stageImageError.value = false;
    resetZoom();
  }
  if (switchToStage) {
    viewMode.value = "stage";
  }
  const nextActive = safeItems.value[normalized];
  if (nextActive?.id) {
    emitActiveChangeIfNeeded(nextActive.id);
  }
  requestActiveAndNeighborMedia();
  scrollActiveThumbIntoView();
}

function prevItem() {
  if (safeItems.value.length <= 1) return;
  selectIndex(activeIndex.value - 1);
}

function nextItem() {
  if (safeItems.value.length <= 1) return;
  selectIndex(activeIndex.value + 1);
}

function onViewModeChange(val: string | number) {
  if (val === "stage" || val === "grid") {
    viewMode.value = val;
  }
}

function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  const tag = target.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    const type = ((target as HTMLInputElement).type || "").toLowerCase();
    return (
      type !== "radio" &&
      type !== "checkbox" &&
      type !== "button" &&
      type !== "submit" &&
      type !== "reset"
    );
  }
  return false;
}

function hasHigherVisibleOverlay(): boolean {
  if (typeof document === "undefined") return false;
  const popconfirms = document.querySelectorAll<HTMLElement>(
    ".ant-popconfirm:not(.ant-popover-hidden)",
  );
  for (const el of popconfirms) {
    if (/\bant-[\w-]+-leave(?:-active|-to)?\b/.test(el.className)) continue;
    const style = window.getComputedStyle(el);
    if (
      style.display !== "none" &&
      style.visibility !== "hidden" &&
      style.pointerEvents !== "none" &&
      el.getBoundingClientRect().height > 0
    ) {
      return true;
    }
  }
  const modals = document.querySelectorAll<HTMLElement>(
    ".ant-modal-wrap:not(.co-gallery-modal-wrap)",
  );
  for (const el of modals) {
    if (/\bant-[\w-]+-leave(?:-active|-to)?\b/.test(el.className)) continue;
    const style = window.getComputedStyle(el);
    if (
      style.display !== "none" &&
      style.visibility !== "hidden" &&
      style.pointerEvents !== "none" &&
      el.getBoundingClientRect().height > 0
    ) {
      const z = Number.parseInt(style.zIndex || "0", 10);
      if (Number.isFinite(z) && z >= 1060) {
        return true;
      }
    }
  }
  return false;
}

function isArrowKeyControlTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "VIDEO" || tag === "AUDIO") return true;
  if (tag === "INPUT" && ((target as HTMLInputElement).type || "").toLowerCase() === "range") {
    return true;
  }
  return Boolean(
    target.closest(
      "video, audio, .ant-segmented, [role='radiogroup'], [role='radio'], [role='slider'], input[type='range']",
    ),
  );
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open) return;
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  if (event.key === "Escape" && deleteConfirmOpen.value) {
    if (!isActiveItemDeleting.value) {
      event.preventDefault();
      deleteConfirmOpen.value = false;
    }
    return;
  }
  if (
    deleteConfirmOpen.value ||
    isActiveItemDeleting.value ||
    hasHigherVisibleOverlay()
  ) {
    return;
  }

  if (event.key === "Escape") {
    event.preventDefault();
    emit("close");
    return;
  }

  if (isEditableTarget(event.target)) return;
  if (viewMode.value !== "stage") return;

  if (
    (event.key === "ArrowLeft" || event.key === "ArrowRight") &&
    isArrowKeyControlTarget(event.target)
  ) {
    return;
  }

  if (event.key === "ArrowLeft" && safeItems.value.length > 1) {
    event.preventDefault();
    prevItem();
  } else if (event.key === "ArrowRight" && safeItems.value.length > 1) {
    event.preventDefault();
    nextItem();
  } else if ((event.key === "+" || event.key === "=") && canZoomActiveImage.value) {
    event.preventDefault();
    zoomIn();
  } else if ((event.key === "-" || event.key === "_") && canZoomActiveImage.value) {
    event.preventDefault();
    zoomOut();
  } else if (event.key === "0" && canZoomActiveImage.value) {
    event.preventDefault();
    resetZoom();
  }
}

function bindKeyboard() {
  if (keyboardBound || typeof window === "undefined") return;
  keyboardBound = true;
  window.addEventListener("keydown", onKeydown);
}

function unbindKeyboard() {
  if (!keyboardBound || typeof window === "undefined") return;
  keyboardBound = false;
  window.removeEventListener("keydown", onKeydown);
}

function syncInitialSelection() {
  const list = safeItems.value;
  if (list.length === 0) {
    activeIndex.value = 0;
    return;
  }
  if (props.initialItemId) {
    const found = list.findIndex((item) => item.id === props.initialItemId);
    activeIndex.value = found >= 0 ? found : 0;
  } else {
    activeIndex.value = 0;
  }
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      viewMode.value = props.initialMode || "stage";
      naturalWidth.value = 0;
      naturalHeight.value = 0;
      stageImageLoaded.value = false;
      stageImageError.value = false;
      deleteConfirmOpen.value = false;
      resetZoom();
      syncInitialSelection();
      if (activeItem.value?.id) {
        emitActiveChangeIfNeeded(activeItem.value.id);
      }
      bindKeyboard();
      requestActiveAndNeighborMedia();
      scrollActiveThumbIntoView();
      if (viewMode.value === "stage") {
        void nextTick(syncStageImageNaturalSize);
      }
    } else {
      lastEmittedActiveId = "";
      stageImageError.value = false;
      pauseStageMedia();
      deleteConfirmOpen.value = false;
      unbindKeyboard();
      resetZoom();
    }
  },
  { immediate: true },
);

watch(
  [
    () => activeItem.value?.id ?? "",
    activeItemSrc,
    viewMode,
    () => props.open,
  ],
  () => {
    stageImageLoaded.value = false;
    stageImageError.value = false;
    if (props.open && viewMode.value === "stage") {
      void nextTick(syncStageImageNaturalSize);
    }
  },
);

watch(
  safeItems,
  (nextList, prevList) => {
    if (!props.open) return;
    if (nextList.length === 0) {
      emit("close");
      return;
    }
    if (!prevList || prevList.length === 0) {
      syncInitialSelection();
      if (activeItem.value?.id) {
        emitActiveChangeIfNeeded(activeItem.value.id);
      }
      stageImageLoaded.value = false;
      stageImageError.value = false;
      requestActiveAndNeighborMedia();
      scrollActiveThumbIntoView();
      if (viewMode.value === "stage") {
        void nextTick(syncStageImageNaturalSize);
      }
      return;
    }
    const prevId = prevList[activeIndex.value]?.id;
    if (prevId) {
      const sameIdx = nextList.findIndex((item) => item.id === prevId);
      if (sameIdx >= 0) {
        activeIndex.value = sameIdx;
        const listChanged =
          nextList.length !== prevList.length ||
          nextList.some((item, i) => item.id !== prevList[i]?.id);
        if (listChanged) {
          requestActiveAndNeighborMedia();
        }
        return;
      }
    }
    if (activeIndex.value >= nextList.length) {
      activeIndex.value = nextList.length - 1;
    }
    deleteConfirmOpen.value = false;
    naturalWidth.value = 0;
    naturalHeight.value = 0;
    stageImageLoaded.value = false;
    stageImageError.value = false;
    resetZoom();
    if (activeItem.value?.id) {
      emitActiveChangeIfNeeded(activeItem.value.id);
    }
    requestActiveAndNeighborMedia();
    scrollActiveThumbIntoView();
    if (viewMode.value === "stage") {
      void nextTick(syncStageImageNaturalSize);
    }
  },
);

watch(
  () => [props.open, activeItem.value?.id ?? ""] as const,
  ([isOpen, itemId]) => {
    if (!isOpen) {
      lastEmittedActiveId = "";
      return;
    }
    if (itemId) {
      emitActiveChangeIfNeeded(itemId);
    }
  },
  { immediate: true },
);

watch([canDeleteActiveItem, isActiveItemDeleting], ([canDel, deleting]) => {
  if (!canDel || deleting) {
    deleteConfirmOpen.value = false;
  }
});

watch(viewMode, (mode) => {
  if (!props.open) return;
  if (mode === "grid") {
    pauseStageMedia();
    deleteConfirmOpen.value = false;
    resetZoom();
    requestActiveAndNeighborMedia();
  } else {
    requestActiveAndNeighborMedia();
    scrollActiveThumbIntoView();
  }
});

onBeforeUnmount(() => {
  pauseStageMedia();
  unbindKeyboard();
  thumbElements.clear();
});
</script>

<template>
  <AModal
    :open="open"
    :footer="null"
    :closable="false"
    :z-index="1060"
    :keyboard="false"
    :mask-closable="!isActiveItemDeleting"
    centered
    width="94vw"
    wrap-class-name="co-gallery-modal-wrap"
    @cancel="!isActiveItemDeleting && emit('close')"
  >
    <div class="co-gal-modal" data-testid="output-gallery-modal">
      <!-- Studio Header Toolbar -->
      <header class="co-gal-modal-header">
        <div class="co-gal-modal-title-group">
          <div class="co-gal-modal-title-line">
            <h2 class="co-gal-modal-title" :title="displayTitle">
              {{ displayTitle }}
            </h2>
            <span
              v-if="safeItems.length > 0"
              class="co-gal-modal-counter"
              data-testid="output-gallery-counter"
            >
              {{ counterLabel }}
            </span>
            <ATag
              v-if="viewMode === 'stage' && activeItemIsInspect"
              color="orange"
              :bordered="false"
              class="co-gal-inspect-tag"
            >
              {{ t("outputGallery.inspectBadge") }}
            </ATag>
          </div>
          <div
            v-if="viewMode === 'stage' && activeItem"
            class="co-gal-modal-meta-line"
          >
            <span class="co-gal-modal-filename" :title="activeItem.filename">
              {{ activeItem.filename }}
            </span>
            <span v-if="activeItem.size > 0" class="co-gal-modal-dot" aria-hidden="true">·</span>
            <span v-if="activeItem.size > 0" class="co-gal-modal-size">
              {{ formatOutputSize(activeItem.size) }}
            </span>
          </div>
        </div>

        <div class="co-gal-modal-controls">
          <!-- Stage / Grid Mode Switch -->
          <ASegmented
            v-if="safeItems.length > 1"
            :value="viewMode"
            :options="modeOptions"
            size="small"
            class="co-gal-mode-switch"
            data-testid="output-gallery-mode-switch"
            @change="onViewModeChange"
          />

          <!-- Zoom Controls (Stage Image Mode) -->
          <div
            v-if="canZoomActiveImage"
            class="co-gal-zoom-group"
            data-testid="output-gallery-zoom-controls"
          >
            <ColabOneTooltip
              :title="t('outputGallery.zoomOut')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <AButton
                size="small"
                type="text"
                class="co-gal-tool-btn"
                :disabled="zoom <= MIN_ZOOM"
                :aria-label="t('outputGallery.zoomOut')"
                data-testid="output-gallery-zoom-out"
                @click="zoomOut"
              >
                <template #icon><PhMagnifyingGlassMinus :size="16" /></template>
              </AButton>
            </ColabOneTooltip>

            <ColabOneTooltip
              :title="t('outputGallery.zoomReset')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <button
                type="button"
                class="co-gal-zoom-pct"
                :aria-label="t('outputGallery.zoomReset')"
                data-testid="output-gallery-zoom-pct"
                @click="resetZoom"
              >
                {{ zoomPercentLabel }}
              </button>
            </ColabOneTooltip>

            <ColabOneTooltip
              :title="t('outputGallery.zoomIn')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <AButton
                size="small"
                type="text"
                class="co-gal-tool-btn"
                :disabled="zoom >= MAX_ZOOM"
                :aria-label="t('outputGallery.zoomIn')"
                data-testid="output-gallery-zoom-in"
                @click="zoomIn"
              >
                <template #icon><PhMagnifyingGlassPlus :size="16" /></template>
              </AButton>
            </ColabOneTooltip>

            <ColabOneTooltip
              :title="t('outputGallery.zoomReset')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <AButton
                size="small"
                type="text"
                class="co-gal-tool-btn"
                :aria-label="t('outputGallery.zoomReset')"
                data-testid="output-gallery-zoom-fit"
                @click="resetZoom"
              >
                <template #icon><PhArrowsIn :size="15" /></template>
              </AButton>
            </ColabOneTooltip>

            <ColabOneTooltip
              :title="t('outputGallery.zoomActual')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <AButton
                size="small"
                type="text"
                class="co-gal-tool-btn"
                :aria-label="t('outputGallery.zoomActual')"
                data-testid="output-gallery-zoom-actual"
                @click="setActualZoom"
              >
                <template #icon><PhCornersOut :size="15" /></template>
              </AButton>
            </ColabOneTooltip>
          </div>

          <!-- Item & Album Actions -->
          <div class="co-gal-action-group">
            <AButton
              v-if="allowDownload && activeItem && activeItemAvailable && viewMode === 'stage'"
              size="small"
              :loading="isActiveItemDownloading"
              :disabled="isActiveItemDeleting"
              data-testid="output-gallery-download-current"
              @click="emit('download-item', activeItem)"
            >
              <template #icon><PhDownloadSimple :size="15" /></template>
              <span class="co-gal-btn-label">{{ t("outputGallery.downloadCurrent") }}</span>
            </AButton>

            <AButton
              v-if="allowDownload && allowZipDownload && safeItems.length > 1"
              size="small"
              type="primary"
              ghost
              :loading="downloadingZip"
              :disabled="isActiveItemDeleting"
              data-testid="output-gallery-download-zip"
              @click="emit('download-zip')"
            >
              <template #icon><PhFileArchive :size="15" /></template>
              <span class="co-gal-btn-label">{{ t("outputGallery.downloadAlbumZip") }}</span>
            </AButton>

            <APopconfirm
              v-if="canDeleteActiveItem && activeItem && viewMode === 'stage'"
              v-model:open="deleteConfirmOpen"
              :title="
                t('outputGallery.deleteItemConfirm', {
                  name: activeItem.filename,
                })
              "
              :ok-text="t('common.delete')"
              :cancel-text="t('common.cancel')"
              :ok-button-props="{ danger: true }"
              :overlay-style="{ zIndex: 1080 }"
              :disabled="isActiveItemDeleting"
              @confirm="emit('delete-item', activeItem)"
            >
              <ColabOneTooltip
                :title="t('common.delete')"
                :disabled="deleteConfirmOpen"
                :overlay-style="{ zIndex: 1090 }"
              >
                <AButton
                  size="small"
                  danger
                  :loading="isActiveItemDeleting"
                  :aria-label="t('common.delete')"
                  data-testid="output-gallery-delete-current"
                >
                  <template #icon><PhTrash :size="15" /></template>
                </AButton>
              </ColabOneTooltip>
            </APopconfirm>

            <ColabOneTooltip
              :title="t('common.close')"
              :overlay-style="{ zIndex: 1090 }"
            >
              <AButton
                size="small"
                type="text"
                class="co-gal-close-btn"
                :disabled="isActiveItemDeleting"
                :aria-label="t('common.close')"
                data-testid="output-gallery-close"
                @click="emit('close')"
              >
                <template #icon><PhX :size="18" /></template>
              </AButton>
            </ColabOneTooltip>
          </div>
        </div>
      </header>

      <!-- Main Stage Mode -->
      <div
        v-if="viewMode === 'stage'"
        ref="stageRef"
        class="co-gal-stage"
        data-testid="output-gallery-stage"
        @wheel.prevent="onStageWheel"
      >
        <button
          v-if="safeItems.length > 1"
          type="button"
          class="co-gal-nav-btn is-prev"
          :aria-label="t('outputGallery.prevItem')"
          data-testid="output-gallery-prev"
          @click="prevItem"
        >
          <PhCaretLeft :size="22" weight="bold" />
        </button>

        <div class="co-gal-stage-viewport">
          <template v-if="activeItem">
            <!-- Unavailable / Offline State -->
            <div
              v-if="!activeItemAvailable"
              class="co-gal-stage-state"
              data-testid="output-gallery-offline"
            >
              <PhWifiSlash :size="36" weight="duotone" class="co-gal-state-icon" />
              <p>{{ t("outputGallery.itemOfflineNotice") }}</p>
            </div>

            <!-- Load Error State with Retry -->
            <div
              v-else-if="stageImageError || activeItemError"
              class="co-gal-stage-state"
              role="alert"
              data-testid="output-gallery-error"
            >
              <PhWarningCircle
                :size="36"
                weight="duotone"
                class="co-gal-state-icon"
              />
              <p>{{ activeItemError || t("outputGallery.loadFailedRetry") }}</p>
              <AButton
                size="small"
                type="primary"
                ghost
                data-testid="output-gallery-retry-btn"
                @click="handleStageRetry(activeItem)"
              >
                <template #icon><PhArrowsClockwise :size="14" /></template>
                {{ t("outputGallery.retryLoadBtn") }}
              </AButton>
            </div>

            <!-- Loading State -->
            <div
              v-else-if="!activeItemSrc || activeItemLoading"
              class="co-gal-stage-state"
              role="status"
              aria-busy="true"
            >
              <ASpin size="large" />
              <p>{{ t("comfy.loadingOutput") }}</p>
            </div>

            <!-- Interactive Zoomable Image -->
            <div
              v-else-if="activeItem.kind === 'image'"
              class="co-gal-image-wrap"
              :class="{
                'is-zoomed': zoom > 1,
                'is-dragging': isDragging,
                'is-loading': !stageImageLoaded,
              }"
              @dblclick="onImageDblClick"
              @pointerdown="onImagePointerDown"
              @pointermove="onImagePointerMove"
              @pointerup="endImageDrag"
              @pointercancel="endImageDrag"
              @lostpointercapture="endImageDrag"
            >
              <div
                v-if="!stageImageLoaded"
                class="co-gal-stage-state co-gal-stage-loading-overlay"
                role="status"
                aria-busy="true"
              >
                <ASpin size="large" />
                <p>{{ t("comfy.loadingOutput") }}</p>
              </div>
              <img
                ref="stageImageRef"
                :key="activeItem.id"
                :src="activeItemSrc"
                :alt="activeItem.filename"
                class="co-gal-stage-image"
                :class="{ 'is-pending': !stageImageLoaded }"
                :style="stageImageStyle"
                draggable="false"
                @load="onStageImageLoad"
                @error="onStageImageError(activeItem)"
              />
            </div>

            <!-- Video Player -->
            <video
              v-else-if="activeItem.kind === 'video'"
              :key="activeItem.id"
              :src="activeItemSrc"
              controls
              :controlslist="!allowDownload ? 'nodownload' : undefined"
              playsinline
              class="co-gal-stage-video"
              @error="emit('media-error', activeItem)"
            />

            <!-- Audio Player -->
            <div v-else class="co-gal-stage-audio">
              <div class="co-gal-audio-emblem">
                <PhWaveform :size="36" weight="duotone" />
              </div>
              <strong class="co-gal-audio-name">{{ activeItem.filename }}</strong>
              <audio
                :key="activeItem.id"
                :src="activeItemSrc"
                controls
                :controlslist="!allowDownload ? 'nodownload' : undefined"
                @error="emit('media-error', activeItem)"
              />
            </div>
          </template>
        </div>

        <button
          v-if="safeItems.length > 1"
          type="button"
          class="co-gal-nav-btn is-next"
          :aria-label="t('outputGallery.nextItem')"
          data-testid="output-gallery-next"
          @click="nextItem"
        >
          <PhCaretRight :size="22" weight="bold" />
        </button>

        <div class="co-gal-keyboard-hint" aria-hidden="true">
          {{ t("outputGallery.keyboardHint") }}
        </div>
      </div>

      <!-- Grid Overview Mode -->
      <div
        v-else
        class="co-gal-grid-overview"
        data-testid="output-gallery-grid"
      >
        <div
          v-for="(item, idx) in safeItems"
          :key="item.id"
          class="co-gal-grid-tile"
          :class="{ 'is-active': idx === activeIndex }"
          :data-testid="`output-gallery-grid-item-${item.id}`"
          @click="selectIndex(idx, true)"
        >
          <div class="co-gal-grid-thumb">
            <span class="co-gal-grid-idx">#{{ idx + 1 }}</span>
            <span
              v-if="isColabOneInspectOutput(item)"
              class="co-gal-grid-inspect-pill"
            >
              {{ t("outputGallery.inspectBadge") }}
            </span>

            <div
              v-if="!checkItemAvailable(item)"
              class="co-gal-grid-placeholder"
            >
              <PhWifiSlash :size="24" weight="duotone" />
              <span>{{ t("outputGallery.itemOfflineNotice") }}</span>
            </div>
            <div
              v-else-if="props.getItemError && props.getItemError(item)"
              class="co-gal-grid-placeholder is-error"
              role="alert"
            >
              <PhWarningCircle :size="22" weight="duotone" />
              <span class="co-gal-grid-error-text">{{
                props.getItemError(item) || t("outputGallery.loadFailedRetry")
              }}</span>
              <AButton
                size="small"
                type="primary"
                ghost
                :data-testid="`output-gallery-grid-retry-${item.id}`"
                @click.stop="emit('retry-load', item)"
              >
                <template #icon><PhArrowsClockwise :size="13" /></template>
                {{ t("outputGallery.retryLoadBtn") }}
              </AButton>
            </div>
            <img
              v-else-if="item.kind === 'image' && getMediaSrc(item)"
              :src="getMediaSrc(item)"
              :alt="item.filename"
              loading="lazy"
              decoding="async"
              @error="emit('media-error', item)"
            />
            <div
              v-else-if="item.kind === 'video'"
              class="co-gal-grid-placeholder"
            >
              <PhVideoCamera :size="26" weight="duotone" />
            </div>
            <div
              v-else-if="item.kind === 'audio'"
              class="co-gal-grid-placeholder"
            >
              <PhWaveform :size="26" weight="duotone" />
            </div>
            <ColabOneSkeleton
              v-else
              width="100%"
              height="100%"
              :radius="0"
              variant="rect"
              tone="subtle"
            />
          </div>
          <button
            type="button"
            class="co-gal-grid-meta"
            @click.stop="selectIndex(idx, true)"
          >
            <span class="co-gal-grid-name" :title="item.filename">
              {{ item.filename }}
            </span>
            <span class="co-gal-grid-size">{{ formatOutputSize(item.size) }}</span>
          </button>
        </div>
      </div>

      <!-- Bottom Filmstrip (Stage Mode, Multi-Item Albums) -->
      <footer
        v-if="viewMode === 'stage' && safeItems.length > 1"
        ref="filmstripRef"
        class="co-gal-filmstrip"
        data-testid="output-gallery-filmstrip"
      >
        <div class="co-gal-filmstrip-track">
          <button
            v-for="(item, idx) in safeItems"
            :key="item.id"
            :ref="(el) => setThumbRef(item.id, el)"
            type="button"
            class="co-gal-thumb"
            :class="{
              'is-active': idx === activeIndex,
              'is-inspect': isColabOneInspectOutput(item),
            }"
            :title="item.filename"
            :aria-label="`${idx + 1}: ${item.filename}`"
            :aria-current="idx === activeIndex ? 'true' : undefined"
            @click="selectIndex(idx)"
          >
            <img
              v-if="
                item.kind === 'image' &&
                checkItemAvailable(item) &&
                !(props.getItemError && props.getItemError(item)) &&
                getMediaSrc(item)
              "
              :src="getMediaSrc(item)"
              :alt="item.filename"
              loading="lazy"
              decoding="async"
              @error="emit('media-error', item)"
            />
            <span v-else-if="!checkItemAvailable(item)" class="co-gal-thumb-icon">
              <PhWifiSlash :size="18" />
            </span>
            <span
              v-else-if="props.getItemError && props.getItemError(item)"
              class="co-gal-thumb-icon"
            >
              <PhWarningCircle :size="18" />
            </span>
            <span v-else-if="item.kind === 'video'" class="co-gal-thumb-icon">
              <PhVideoCamera :size="18" />
            </span>
            <span v-else-if="item.kind === 'audio'" class="co-gal-thumb-icon">
              <PhWaveform :size="18" />
            </span>
            <span v-else class="co-gal-thumb-icon">
              <PhImage :size="18" />
            </span>

            <span class="co-gal-thumb-index">{{ idx + 1 }}</span>
            <span
              v-if="isColabOneInspectOutput(item)"
              class="co-gal-thumb-inspect-dot"
              :title="t('outputGallery.inspectBadge')"
            />
          </button>
        </div>
      </footer>
    </div>
  </AModal>
</template>

<style>
.co-gallery-modal-wrap .ant-modal {
  max-width: 1480px;
  padding-bottom: 0;
}

.co-gallery-modal-wrap .ant-modal-content {
  padding: 0 !important;
  border-radius: 16px;
  overflow: hidden;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.36);
}

.co-gallery-modal-wrap .ant-modal-body {
  padding: 0 !important;
}
</style>

<style scoped>
.co-gal-modal {
  display: flex;
  flex-direction: column;
  height: min(90vh, 920px);
  min-height: 460px;
  background: var(--co-surface);
  color: var(--co-text);
  overflow: hidden;
}

/* Header Toolbar */
.co-gal-modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  flex-shrink: 0;
  flex-wrap: wrap;
}

.co-gal-modal-title-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1 1 220px;
}

.co-gal-modal-title-line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex-wrap: wrap;
}

.co-gal-modal-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 340px;
}

.co-gal-modal-counter {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 9px;
  border-radius: 9999px;
  background: var(--co-track);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-secondary-text);
  font-family: var(--co-font-mono);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.co-gal-inspect-tag {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
}

.co-gal-modal-meta-line {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: 12px;
  color: var(--co-muted);
}

.co-gal-modal-filename {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 340px;
  font-family: var(--co-font-mono);
}

.co-gal-modal-dot {
  opacity: 0.6;
}

.co-gal-modal-size {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.co-gal-modal-controls {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.co-gal-mode-switch {
  flex-shrink: 0;
}

.co-gal-zoom-group {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px 4px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
}

.co-gal-tool-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  color: var(--co-secondary-text);
}

.co-gal-tool-btn:hover:not(:disabled) {
  color: var(--co-text);
  background: var(--co-soft-hover);
}

.co-gal-zoom-pct {
  min-width: 48px;
  height: 26px;
  padding: 0 6px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--co-text);
  font-family: var(--co-font-mono);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  cursor: pointer;
  transition: background-color 0.14s ease;
}

.co-gal-zoom-pct:hover {
  background: var(--co-soft-hover);
}

.co-gal-action-group {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.co-gal-close-btn {
  width: 30px;
  height: 30px;
  padding: 0;
  color: var(--co-secondary-text);
}

.co-gal-close-btn:hover {
  color: var(--co-text);
  background: var(--co-soft-hover);
}

/* Stage Viewport */
.co-gal-stage {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--co-subtle);
  overflow: hidden;
  user-select: none;
}

:root[data-theme="dark"] .co-gal-stage {
  background: #0c0e13;
}

.co-gal-stage-viewport {
  width: 100%;
  height: 100%;
  padding: 20px 64px 36px;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

.co-gal-image-wrap {
  position: relative;
  width: 100%;
  height: 100%;
  max-width: 100%;
  max-height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: zoom-in;
  touch-action: none;
}

.co-gal-stage-loading-overlay {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
}

.co-gal-stage-image.is-pending {
  opacity: 0;
}

.co-gal-image-wrap.is-zoomed {
  cursor: grab;
}

.co-gal-image-wrap.is-dragging {
  cursor: grabbing;
}

.co-gal-stage-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  display: block;
  border-radius: 6px;
  box-shadow: 0 8px 30px rgba(15, 23, 42, 0.14);
  background-color: #ffffff;
  background-image:
    linear-gradient(45deg, rgba(148, 163, 184, 0.16) 25%, transparent 25%),
    linear-gradient(-45deg, rgba(148, 163, 184, 0.16) 25%, transparent 25%),
    linear-gradient(45deg, transparent 75%, rgba(148, 163, 184, 0.16) 75%),
    linear-gradient(-45deg, transparent 75%, rgba(148, 163, 184, 0.16) 75%);
  background-size: 16px 16px;
  background-position:
    0 0,
    0 8px,
    8px -8px,
    -8px 0;
  will-change: transform;
}

:root[data-theme="dark"] .co-gal-stage-image {
  background-color: #161820;
  box-shadow: 0 12px 42px rgba(0, 0, 0, 0.52);
  background-image:
    linear-gradient(45deg, rgba(255, 255, 255, 0.06) 25%, transparent 25%),
    linear-gradient(-45deg, rgba(255, 255, 255, 0.06) 25%, transparent 25%),
    linear-gradient(45deg, transparent 75%, rgba(255, 255, 255, 0.06) 75%),
    linear-gradient(-45deg, transparent 75%, rgba(255, 255, 255, 0.06) 75%);
}

.co-gal-stage-video {
  max-width: 100%;
  max-height: 100%;
  border-radius: 10px;
  background: #000000;
  box-shadow: 0 10px 32px rgba(0, 0, 0, 0.28);
}

.co-gal-stage-audio {
  width: min(460px, 100%);
  padding: 28px 24px;
  border-radius: 16px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
}

.co-gal-audio-emblem {
  width: 64px;
  height: 64px;
  border-radius: 18px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
}

.co-gal-audio-name {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--co-text);
  word-break: break-all;
  text-align: center;
}

.co-gal-stage-audio audio {
  width: 100%;
}

.co-gal-stage-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--co-secondary-text);
  text-align: center;
  padding: 24px;
}

.co-gal-stage-state p {
  margin: 0;
  font-size: 13.5px;
}

.co-gal-state-icon {
  color: var(--co-muted);
}

/* Floating Prev / Next Buttons */
.co-gal-nav-btn {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  z-index: 4;
  width: 42px;
  height: 42px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: color-mix(in srgb, var(--co-surface) 88%, transparent);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  color: var(--co-text);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.14);
  transition:
    background-color 0.15s ease,
    border-color 0.15s ease,
    transform 0.12s ease;
}

.co-gal-nav-btn.is-prev {
  left: 14px;
}

.co-gal-nav-btn.is-next {
  right: 14px;
}

.co-gal-nav-btn:hover {
  background: var(--co-surface);
  border-color: var(--co-accent);
  color: var(--co-accent);
}

.co-gal-nav-btn:active {
  transform: translateY(-50%) scale(0.94);
}

.co-gal-nav-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-gal-keyboard-hint {
  position: absolute;
  bottom: 10px;
  right: 14px;
  padding: 3px 10px;
  border-radius: 9999px;
  background: color-mix(in srgb, var(--co-surface) 78%, transparent);
  backdrop-filter: blur(8px);
  border: 1px solid var(--co-border-secondary);
  color: var(--co-muted);
  font-size: 11.5px;
  pointer-events: none;
}

/* Grid Overview Mode */
.co-gal-grid-overview {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 18px 20px 24px;
  background: var(--co-subtle);
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(186px, 1fr));
  gap: 14px;
  align-content: start;
}

.co-gal-grid-tile {
  display: flex;
  flex-direction: column;
  padding: 0;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow: hidden;
  cursor: pointer;
  text-align: left;
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease,
    transform 0.12s ease;
}

.co-gal-grid-tile:hover {
  border-color: var(--co-accent);
  box-shadow: var(--co-card-shadow-hover);
  transform: translateY(-1px);
}

.co-gal-grid-tile.is-active {
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-gal-grid-thumb {
  position: relative;
  width: 100%;
  aspect-ratio: 4 / 3;
  background: var(--co-track);
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
}

.co-gal-grid-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-gal-grid-idx {
  position: absolute;
  top: 7px;
  left: 7px;
  z-index: 2;
  padding: 1px 7px;
  border-radius: 9999px;
  background: rgba(15, 23, 42, 0.72);
  color: #ffffff;
  font-family: var(--co-font-mono);
  font-size: 11px;
  font-weight: 600;
}

.co-gal-grid-inspect-pill {
  position: absolute;
  top: 7px;
  right: 7px;
  z-index: 2;
  padding: 1px 7px;
  border-radius: 9999px;
  background: #b45309;
  color: #ffffff;
  font-size: 10.5px;
  font-weight: 600;
}

.co-gal-grid-placeholder {
  width: 100%;
  height: 100%;
  padding: 12px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--co-muted);
  font-size: 11.5px;
  text-align: center;
}

.co-gal-grid-placeholder.is-error {
  color: var(--co-secondary-text);
}

.co-gal-grid-error-text {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  max-width: 100%;
  word-break: break-word;
}

.co-gal-grid-meta {
  width: 100%;
  border: 0;
  background: transparent;
  cursor: pointer;
  text-align: left;
  padding: 9px 10px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.co-gal-grid-meta:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: -2px;
}

.co-gal-grid-name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 500;
  color: var(--co-text);
}

.co-gal-grid-size {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--co-muted);
  font-variant-numeric: tabular-nums;
}

/* Bottom Thumbnail Filmstrip */
.co-gal-filmstrip {
  flex-shrink: 0;
  padding: 10px 16px;
  border-top: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  overflow-x: auto;
  overflow-y: hidden;
}

.co-gal-filmstrip-track {
  display: flex;
  align-items: center;
  justify-content: safe center;
  gap: 8px;
  min-width: max-content;
}

.co-gal-thumb {
  position: relative;
  width: 62px;
  height: 62px;
  padding: 0;
  border-radius: 10px;
  border: 2px solid transparent;
  background: var(--co-track);
  overflow: hidden;
  cursor: pointer;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.74;
  transition:
    opacity 0.14s ease,
    border-color 0.14s ease,
    transform 0.12s ease;
}

.co-gal-thumb:hover {
  opacity: 0.96;
}

.co-gal-thumb.is-active {
  opacity: 1;
  border-color: var(--co-accent);
  box-shadow: 0 0 0 2px var(--co-focus-ring);
}

.co-gal-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.co-gal-thumb-icon {
  color: var(--co-muted);
  display: grid;
  place-items: center;
}

.co-gal-thumb-index {
  position: absolute;
  bottom: 3px;
  left: 4px;
  padding: 0 4px;
  border-radius: 4px;
  background: rgba(15, 23, 42, 0.72);
  color: #ffffff;
  font-family: var(--co-font-mono);
  font-size: 10px;
  font-weight: 600;
  line-height: 14px;
}

.co-gal-thumb-inspect-dot {
  position: absolute;
  top: 4px;
  right: 4px;
  width: 7px;
  height: 7px;
  border-radius: 9999px;
  background: #f59e0b;
  box-shadow: 0 0 0 1.5px rgba(255, 255, 255, 0.9);
}

@media (max-width: 720px) {
  .co-gal-modal {
    height: 92vh;
  }

  .co-gal-modal-header {
    padding: 10px 12px;
    gap: 8px;
  }

  .co-gal-btn-label,
  .co-gal-keyboard-hint {
    display: none;
  }

  .co-gal-stage-viewport {
    padding: 12px 46px 16px;
  }

  .co-gal-nav-btn {
    width: 34px;
    height: 34px;
  }

  .co-gal-nav-btn.is-prev {
    left: 6px;
  }

  .co-gal-nav-btn.is-next {
    right: 6px;
  }
}
</style>
