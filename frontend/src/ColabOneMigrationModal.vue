<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Checkbox as ACheckbox,
  Modal as AModal,
  Spin as ASpin,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhChartLineUp,
  PhCube,
  PhDownloadSimple,
  PhFileArrowDown,
  PhFileArrowUp,
  PhKey,
  PhLockKey,
  PhNotePencil,
  PhShieldCheck,
  PhSlidersHorizontal,
  PhUploadSimple,
  PhUserCircle,
} from "@phosphor-icons/vue";
import ColabOnePrivacyText from "./ColabOnePrivacyText.vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import type {
  ColabOneMigrationExportSummary,
  ColabOneMigrationModuleSummary,
  ColabOneMigrationPickResult,
  ColabOneMigrationPreview,
} from "./api";
import {
  COLABONE_MIGRATION_MIN_PASSWORD_LEN,
  COLABONE_MIGRATION_MODULES,
  defaultImportModuleSelection,
  formatMigrationFileSize,
  formatMigrationSourceOS,
  validateColabOneExportPassword,
  type ColabOneMigrationModuleId,
} from "./colabOneMigration";
import { buildColabOnePrivacyContext } from "./colabOnePrivacy";
import { colabOneLocaleCode, t, tb } from "./i18n";
import type { ColabOneSnapshot } from "./types";

const props = withDefaults(
  defineProps<{
    open: boolean;
    mode: "export" | "import";
    state: ColabOneSnapshot;
    exportSummary?: ColabOneMigrationExportSummary | null;
    exportLoading?: boolean;
    exportSubmitting?: boolean;
    exportError?: string;
    importStep?: "decrypt" | "preview";
    pickedFile?: ColabOneMigrationPickResult | null;
    importPreview?: ColabOneMigrationPreview | null;
    importDecrypting?: boolean;
    importApplying?: boolean;
    importError?: string;
  }>(),
  {
    exportSummary: null,
    exportLoading: false,
    exportSubmitting: false,
    exportError: "",
    importStep: "decrypt",
    pickedFile: null,
    importPreview: null,
    importDecrypting: false,
    importApplying: false,
    importError: "",
  },
);

const emit = defineEmits<{
  "update:open": [boolean];
  export: [payload: { modules: ColabOneMigrationModuleId[]; password: string }];
  repick: [];
  decrypt: [payload: { fileToken: string; password: string }];
  apply: [payload: { previewId: string; modules: ColabOneMigrationModuleId[] }];
}>();

const exportSelected = ref<ColabOneMigrationModuleId[]>([
  ...COLABONE_MIGRATION_MODULES,
]);
const exportPassword = ref("");
const exportConfirm = ref("");
const exportPasswordTouched = ref(false);
const exportConfirmTouched = ref(false);

const importPassword = ref("");
const importSelected = ref<ColabOneMigrationModuleId[]>([]);
type SecretInputHandle = InstanceType<typeof ColabOneSecretInput>;
const exportPasswordInputRef = ref<SecretInputHandle | null>(null);
const exportConfirmInputRef = ref<SecretInputHandle | null>(null);
const importPasswordInputRef = ref<SecretInputHandle | null>(null);

function hideAllPasswordInputs() {
  exportPasswordInputRef.value?.hide?.();
  exportConfirmInputRef.value?.hide?.();
  importPasswordInputRef.value?.hide?.();
}

const privacyContext = computed(() =>
  buildColabOnePrivacyContext(props.state),
);

const isBusy = computed(
  () =>
    props.exportSubmitting || props.importDecrypting || props.importApplying,
);

const canClose = computed(() => !isBusy.value);

const exportPasswordError = computed(() =>
  validateColabOneExportPassword(exportPassword.value, exportConfirm.value),
);

const exportPasswordHintText = computed(() => {
  switch (exportPasswordError.value) {
    case "tooShort":
      if (!exportPasswordTouched.value && !exportConfirmTouched.value) {
        return "";
      }
      return t("migration.passwordTooShort", {
        min: COLABONE_MIGRATION_MIN_PASSWORD_LEN,
      });
    case "tooLong":
      return t("migration.passwordTooLong");
    case "mismatch":
      if (!exportConfirmTouched.value && !exportConfirm.value) {
        return "";
      }
      return t("migration.passwordMismatch");
    default:
      return "";
  }
});

const canSubmitExport = computed(
  () =>
    !props.exportLoading &&
    !props.exportSubmitting &&
    exportSelected.value.length > 0 &&
    exportPasswordError.value === null,
);

const allExportSelected = computed(
  () => exportSelected.value.length === COLABONE_MIGRATION_MODULES.length,
);

const configOnlyModules: ColabOneMigrationModuleId[] = [
  "settings",
  "library_workflows",
  "workflow_drafts",
];

const hasRunningInstances = computed(() => {
  const rtState = props.state.runtime.state;
  if (rtState === "ready" || rtState === "starting") {
    return true;
  }
  const instances = props.state.account?.instances;
  if (Array.isArray(instances)) {
    return instances.some((inst) => inst?.managed);
  }
  const assignments = props.state.account?.assignments;
  if (
    typeof assignments === "number" &&
    Number.isFinite(assignments) &&
    assignments > 0
  ) {
    return true;
  }
  if (rtState === "stopped") {
    return false;
  }
  return Boolean(props.importPreview?.has_running_instances);
});

const hasActiveOperation = computed(() => Boolean(props.state.operation));

const activeInstancesBlockAccount = computed(
  () =>
    hasRunningInstances.value &&
    Boolean(
      props.importPreview?.modules?.some(
        (m) => m.id === "account" && m.included,
      ),
    ),
);

const hasCloseBehaviorFallbackWarning = computed(() =>
  Boolean(
    props.importPreview?.warnings?.includes("close_behavior_fallback_quit"),
  ),
);

const canSubmitDecrypt = computed(
  () =>
    !props.importDecrypting &&
    Boolean(props.pickedFile?.file_token) &&
    importPassword.value.length > 0,
);

const canSubmitApply = computed(
  () =>
    !props.importApplying &&
    Boolean(props.importPreview?.preview_id) &&
    !hasActiveOperation.value &&
    importSelected.value.length > 0,
);

const formattedExportedAt = computed(() => {
  const raw = props.importPreview?.exported_at;
  if (!raw) return "";
  const dt = new Date(raw);
  if (Number.isNaN(dt.getTime())) return raw;
  try {
    return new Intl.DateTimeFormat(colabOneLocaleCode.value, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }).format(dt);
  } catch {
    return raw;
  }
});

const exportModulesList = computed<ColabOneMigrationModuleSummary[]>(() => {
  if (props.exportSummary?.modules?.length) {
    return props.exportSummary.modules;
  }
  return COLABONE_MIGRATION_MODULES.map((id) => ({
    id,
    included: true,
    has_data: false,
  }));
});

const importModulesList = computed<ColabOneMigrationModuleSummary[]>(() => {
  return props.importPreview?.modules ?? [];
});

watch(
  () => [props.open, props.mode] as const,
  ([isOpen, nextMode]) => {
    hideAllPasswordInputs();
    if (!isOpen) {
      exportPassword.value = "";
      exportConfirm.value = "";
      exportPasswordTouched.value = false;
      exportConfirmTouched.value = false;
      importPassword.value = "";
      return;
    }
    if (nextMode === "export") {
      exportSelected.value = [...COLABONE_MIGRATION_MODULES];
      exportPassword.value = "";
      exportConfirm.value = "";
      exportPasswordTouched.value = false;
      exportConfirmTouched.value = false;
    } else {
      importPassword.value = "";
      if (props.importStep === "decrypt") {
        void nextTick(() => importPasswordInputRef.value?.focus?.());
      }
    }
  },
  { immediate: true },
);

watch(
  () => [props.importStep, props.pickedFile?.file_token] as const,
  ([step]) => {
    if (!props.open || props.mode !== "import") return;
    if (step === "decrypt") {
      importPassword.value = "";
      importPasswordInputRef.value?.hide?.();
      void nextTick(() => importPasswordInputRef.value?.focus?.());
    }
  },
);

watch(
  () => props.importPreview,
  (preview) => {
    const base = defaultImportModuleSelection(preview);
    importSelected.value = hasRunningInstances.value
      ? base.filter((id) => id !== "account")
      : base;
  },
  { immediate: true },
);

watch(hasRunningInstances, (running) => {
  if (running && importSelected.value.includes("account")) {
    importSelected.value = importSelected.value.filter(
      (id) => id !== "account",
    );
  }
});

function requestClose() {
  if (!canClose.value) return;
  emit("update:open", false);
}

function isModuleSelected(
  list: ColabOneMigrationModuleId[],
  id: string,
): boolean {
  return list.includes(id as ColabOneMigrationModuleId);
}

function toggleExportModule(id: string, checked: boolean) {
  const modId = id as ColabOneMigrationModuleId;
  const set = new Set(exportSelected.value);
  if (checked) {
    set.add(modId);
  } else {
    set.delete(modId);
  }
  exportSelected.value = COLABONE_MIGRATION_MODULES.filter((m) => set.has(m));
}

function selectAllExportModules() {
  exportSelected.value = [...COLABONE_MIGRATION_MODULES];
}

function selectConfigOnlyExportModules() {
  exportSelected.value = [...configOnlyModules];
}

function isImportModuleDisabled(mod: ColabOneMigrationModuleSummary): boolean {
  if (!mod.included) return true;
  if (mod.id === "account" && hasRunningInstances.value) {
    return true;
  }
  return false;
}

function toggleImportModule(mod: ColabOneMigrationModuleSummary, checked: boolean) {
  if (isImportModuleDisabled(mod)) return;
  const modId = mod.id as ColabOneMigrationModuleId;
  const set = new Set(importSelected.value);
  if (checked) {
    set.add(modId);
  } else {
    set.delete(modId);
  }
  importSelected.value = COLABONE_MIGRATION_MODULES.filter((m) => set.has(m));
}

function shouldIgnoreModuleRowClick(event: MouseEvent): boolean {
  const target = event.target as HTMLElement | null;
  return Boolean(
    target?.closest(
      ".ant-checkbox-wrapper, button, a, input, .co-privacy-mask",
    ),
  );
}

function onExportModuleRowClick(
  modId: string,
  event: MouseEvent,
) {
  if (props.exportSubmitting || shouldIgnoreModuleRowClick(event)) return;
  toggleExportModule(modId, !isModuleSelected(exportSelected.value, modId));
}

function onImportModuleRowClick(
  mod: ColabOneMigrationModuleSummary,
  event: MouseEvent,
) {
  if (
    props.importApplying ||
    isImportModuleDisabled(mod) ||
    shouldIgnoreModuleRowClick(event)
  ) {
    return;
  }
  toggleImportModule(mod, !isModuleSelected(importSelected.value, mod.id));
}

function handleSubmitExport() {
  exportPasswordTouched.value = true;
  exportConfirmTouched.value = true;
  if (!canSubmitExport.value) return;
  emit("export", {
    modules: [...exportSelected.value],
    password: exportPassword.value,
  });
}

function handleSubmitDecrypt() {
  if (!canSubmitDecrypt.value || !props.pickedFile?.file_token) return;
  emit("decrypt", {
    fileToken: props.pickedFile.file_token,
    password: importPassword.value,
  });
}

function handleSubmitApply() {
  if (!canSubmitApply.value || !props.importPreview?.preview_id) return;
  emit("apply", {
    previewId: props.importPreview.preview_id,
    modules: [...importSelected.value],
  });
}

function moduleTitle(id: string): string {
  switch (id) {
    case "account":
      return t("migration.moduleAccountTitle");
    case "credentials":
      return t("migration.moduleCredentialsTitle");
    case "settings":
      return t("migration.moduleSettingsTitle");
    case "library_workflows":
      return t("migration.moduleLibraryTitle");
    case "cu_usage":
      return t("migration.moduleCuUsageTitle");
    case "workflow_drafts":
      return t("migration.moduleDraftsTitle");
    default:
      return id;
  }
}

function moduleDesc(id: string): string {
  switch (id) {
    case "account":
      return t("migration.moduleAccountDesc");
    case "credentials":
      return t("migration.moduleCredentialsDesc");
    case "settings":
      return t("migration.moduleSettingsDesc");
    case "library_workflows":
      return t("migration.moduleLibraryDesc");
    case "cu_usage":
      return t("migration.moduleCuUsageDesc");
    case "workflow_drafts":
      return t("migration.moduleDraftsDesc");
    default:
      return "";
  }
}

function moduleIconClass(id: string): string {
  switch (id) {
    case "account":
      return "is-blue";
    case "credentials":
      return "is-violet";
    case "settings":
      return "is-amber";
    case "library_workflows":
      return "is-emerald";
    case "cu_usage":
      return "is-blue";
    default:
      return "is-slate";
  }
}

function settingsCompactText(mod: ColabOneMigrationModuleSummary): string {
  const s = mod.settings;
  if (!s) return "";
  const parts: string[] = [];
  if (s.gpu) parts.push(s.gpu);
  if (s.idle_minutes > 0) {
    parts.push(t("migration.summaryIdleMinutes", { minutes: s.idle_minutes }));
  }
  if (s.download_threads > 0) {
    parts.push(t("migration.summaryThreads", { count: s.download_threads }));
  }
  if (s.appearance === "dark") {
    parts.push(t("appearance.dark"));
  } else if (s.appearance === "light") {
    parts.push(t("appearance.light"));
  } else if (s.appearance === "system") {
    parts.push(t("appearance.system"));
  }
  return parts.join(" · ");
}

function libraryCompactText(mod: ColabOneMigrationModuleSummary): string {
  const l = mod.library_workflows;
  if (!l) return t("migration.summaryEmpty");
  const parts: string[] = [];
  if (l.custom_model_count > 0) {
    parts.push(
      t("migration.summaryCustomModels", { count: l.custom_model_count }),
    );
  }
  if (l.common_repo_count > 0) {
    parts.push(
      t("migration.summaryCommonRepos", { count: l.common_repo_count }),
    );
  }
  if (l.custom_workflow_count > 0) {
    parts.push(
      t("migration.summaryCustomWorkflows", { count: l.custom_workflow_count }),
    );
  }
  if (parts.length === 0) {
    return t("migration.summaryEmpty");
  }
  return parts.join(" · ");
}
</script>

<template>
  <AModal
    :open="open"
    :width="580"
    :centered="true"
    :closable="canClose"
    :mask-closable="canClose"
    :keyboard="canClose"
    :z-index="1060"
    destroy-on-close
    wrap-class-name="co-migration-modal-wrap"
    @cancel="requestClose"
  >
    <template #title>
      <div class="co-mig-title">
        <span class="co-mig-title-main">
          <span
            class="co-mig-title-icon"
            :class="mode === 'export' ? 'is-export' : 'is-import'"
            aria-hidden="true"
          >
            <PhFileArrowUp v-if="mode === 'export'" :size="18" weight="duotone" />
            <PhFileArrowDown v-else :size="18" weight="duotone" />
          </span>
          <span class="co-mig-title-text">
            {{
              mode === "export"
                ? t("migration.exportModalTitle")
                : importStep === "decrypt"
                  ? t("migration.importDecryptModalTitle")
                  : t("migration.importPreviewModalTitle")
            }}
          </span>
        </span>
        <ATag color="blue" :bordered="false" class="co-mig-cipher-tag">
          <PhLockKey :size="12" aria-hidden="true" />
          <span>AES-256-GCM</span>
        </ATag>
      </div>
    </template>

    <!-- =============================================================== -->
    <!-- MODE 1: EXPORT                                                  -->
    <!-- =============================================================== -->
    <div
      v-if="mode === 'export'"
      class="co-mig-body"
      data-testid="migration-export-modal"
    >
      <p class="co-mig-lead">{{ t("migration.exportModalDesc") }}</p>

      <div class="co-mig-section-head">
        <span class="co-mig-section-title">
          {{ t("migration.selectModulesLabel") }}
        </span>
        <div class="co-mig-quick-actions">
          <button
            type="button"
            class="co-mig-link-btn"
            :class="{ 'is-active': allExportSelected }"
            :disabled="exportSubmitting"
            data-testid="migration-export-select-all"
            @click="selectAllExportModules"
          >
            {{ t("migration.selectAllBtn") }}
          </button>
          <span class="co-mig-dot" aria-hidden="true">·</span>
          <button
            type="button"
            class="co-mig-link-btn"
            :disabled="exportSubmitting"
            data-testid="migration-export-config-only"
            @click="selectConfigOnlyExportModules"
          >
            {{ t("migration.selectConfigOnlyBtn") }}
          </button>
        </div>
      </div>

      <div v-if="exportLoading" class="co-mig-loading" role="status" aria-busy="true">
        <ASpin size="small" />
        <span>{{ t("common.loading") }}</span>
      </div>

      <div v-else class="co-mig-module-list">
        <div
          v-for="mod in exportModulesList"
          :key="mod.id"
          class="co-mig-module-item"
          :class="{ 'is-selected': isModuleSelected(exportSelected, mod.id) }"
          :data-testid="`migration-export-mod-${mod.id}`"
          @click="onExportModuleRowClick(mod.id, $event)"
        >
          <ACheckbox
            :checked="isModuleSelected(exportSelected, mod.id)"
            :disabled="exportSubmitting"
            :aria-label="moduleTitle(mod.id)"
            @update:checked="(v) => toggleExportModule(mod.id, Boolean(v))"
          />
          <span class="co-mig-mod-icon" :class="moduleIconClass(mod.id)" aria-hidden="true">
            <PhUserCircle v-if="mod.id === 'account'" :size="15" weight="duotone" />
            <PhKey v-else-if="mod.id === 'credentials'" :size="15" weight="duotone" />
            <PhSlidersHorizontal v-else-if="mod.id === 'settings'" :size="15" weight="duotone" />
            <PhCube v-else-if="mod.id === 'library_workflows'" :size="15" weight="duotone" />
            <PhChartLineUp v-else-if="mod.id === 'cu_usage'" :size="15" weight="duotone" />
            <PhNotePencil v-else :size="15" weight="duotone" />
          </span>
          <div class="co-mig-mod-copy">
            <div class="co-mig-mod-head">
              <span class="co-mig-mod-title">{{ moduleTitle(mod.id) }}</span>
              <span class="co-mig-mod-meta">
                <!-- Account summary -->
                <template v-if="mod.id === 'account'">
                  <ATag v-if="mod.account?.has_login" color="success" :bordered="false">
                    <ColabOnePrivacyText
                      v-if="mod.account?.email"
                      :text="mod.account.email"
                      kind="email"
                      :context="privacyContext"
                    />
                    <template v-else>{{ t("migration.summarySignedIn") }}</template>
                  </ATag>
                  <ATag v-else :bordered="false">{{ t("migration.summaryNotSignedIn") }}</ATag>
                </template>
                <!-- Credentials summary -->
                <template v-else-if="mod.id === 'credentials'">
                  <ATag
                    v-if="(mod.credentials?.configured_count || 0) > 0"
                    color="blue"
                    :bordered="false"
                  >
                    {{
                      t("migration.summaryConfiguredCount", {
                        count: mod.credentials?.configured_count || 0,
                      })
                    }}
                  </ATag>
                  <ATag
                    v-if="mod.credentials?.has_notifications"
                    color="purple"
                    :bordered="false"
                    data-testid="migration-export-credentials-notifications-tag"
                  >
                    {{ t("migration.summaryNotifications") }}
                  </ATag>
                  <ATag
                    v-if="
                      (mod.credentials?.configured_count || 0) === 0 &&
                      !mod.credentials?.has_notifications
                    "
                    :bordered="false"
                  >
                    {{ t("migration.summaryEmpty") }}
                  </ATag>
                </template>
                <!-- Settings summary -->
                <template v-else-if="mod.id === 'settings'">
                  <span class="co-mig-meta-text">{{ settingsCompactText(mod) }}</span>
                </template>
                <!-- Library & workflows summary -->
                <template v-else-if="mod.id === 'library_workflows'">
                  <span class="co-mig-meta-text">{{ libraryCompactText(mod) }}</span>
                </template>
                <!-- CU Usage summary -->
                <template v-else-if="mod.id === 'cu_usage'">
                  <span class="co-mig-meta-text">
                    {{
                      (mod.cu_usage?.day_count || 0) > 0
                        ? t("migration.summaryCuDays", {
                            days: mod.cu_usage?.day_count || 0,
                          })
                        : t("migration.summaryEmpty")
                    }}
                  </span>
                </template>
                <!-- Workflow drafts summary -->
                <template v-else-if="mod.id === 'workflow_drafts'">
                  <span class="co-mig-meta-text">
                    {{
                      (mod.workflow_drafts?.draft_count || 0) > 0
                        ? t("migration.summaryDraftsCount", {
                            count: mod.workflow_drafts?.draft_count || 0,
                          })
                        : t("migration.summaryEmpty")
                    }}
                  </span>
                </template>
              </span>
            </div>
            <p class="co-mig-mod-desc">{{ moduleDesc(mod.id) }}</p>
          </div>
        </div>
      </div>

      <!-- Password fields (mandatory encryption) -->
      <div class="co-mig-password-card">
        <div class="co-mig-password-head">
          <PhShieldCheck :size="15" weight="duotone" class="co-mig-shield-icon" aria-hidden="true" />
          <strong>{{ t("migration.exportPasswordTitle") }}</strong>
        </div>
        <p class="co-mig-password-desc">
          {{ t("migration.exportPasswordDesc", { min: COLABONE_MIGRATION_MIN_PASSWORD_LEN }) }}
        </p>
        <div class="co-mig-password-grid">
          <ColabOneSecretInput
            ref="exportPasswordInputRef"
            v-model:value="exportPassword"
            :disabled="exportSubmitting"
            autocomplete="new-password"
            :placeholder="t('migration.exportPasswordPlaceholder')"
            :aria-label="t('migration.exportPasswordPlaceholder')"
            input-test-id="migration-export-password"
            @blur="exportPasswordTouched = true"
            @press-enter="handleSubmitExport"
          />
          <ColabOneSecretInput
            ref="exportConfirmInputRef"
            v-model:value="exportConfirm"
            :disabled="exportSubmitting"
            autocomplete="new-password"
            :placeholder="t('migration.exportPasswordConfirmPlaceholder')"
            :aria-label="t('migration.exportPasswordConfirmPlaceholder')"
            input-test-id="migration-export-confirm"
            @blur="exportConfirmTouched = true"
            @press-enter="handleSubmitExport"
          />
        </div>
        <p
          v-if="exportPasswordHintText"
          class="co-mig-field-error"
          role="alert"
          data-testid="migration-export-password-error"
        >
          {{ exportPasswordHintText }}
        </p>
      </div>

      <AAlert v-if="exportError" type="error" show-icon>
        <template #message>
          <ColabOnePrivacyText
            :text="tb(exportError)"
            :context="privacyContext"
          />
        </template>
      </AAlert>
    </div>

    <!-- =============================================================== -->
    <!-- MODE 2A: IMPORT — STEP 1 (ENTER PASSWORD TO DECRYPT)            -->
    <!-- =============================================================== -->
    <div
      v-else-if="importStep === 'decrypt'"
      class="co-mig-body"
      data-testid="migration-import-decrypt-modal"
    >
      <div class="co-mig-file-banner">
        <div class="co-mig-file-copy">
          <strong class="co-mig-file-name" data-testid="migration-picked-filename">
            {{ pickedFile?.filename }}
          </strong>
          <span class="co-mig-file-meta">
            {{ formatMigrationFileSize(pickedFile?.file_size) }} ·
            {{ t("migration.encryptedArchiveBadge") }}
          </span>
        </div>
        <AButton
          size="small"
          :disabled="importDecrypting"
          @click="emit('repick')"
        >
          {{ t("migration.changeFileBtn") }}
        </AButton>
      </div>

      <div class="co-mig-password-card">
        <div class="co-mig-password-head">
          <PhLockKey :size="15" weight="duotone" class="co-mig-shield-icon" aria-hidden="true" />
          <strong>{{ t("migration.importPasswordTitle") }}</strong>
        </div>
        <p class="co-mig-password-desc">
          {{ t("migration.importPasswordDesc") }}
        </p>
        <ColabOneSecretInput
          ref="importPasswordInputRef"
          v-model:value="importPassword"
          :disabled="importDecrypting"
          autocomplete="current-password"
          :placeholder="t('migration.importPasswordPlaceholder')"
          :aria-label="t('migration.importPasswordPlaceholder')"
          input-test-id="migration-import-password"
          @press-enter="handleSubmitDecrypt"
        />
      </div>

      <AAlert
        v-if="importError"
        type="error"
        show-icon
        data-testid="migration-import-error"
      >
        <template #message>
          <ColabOnePrivacyText
            :text="tb(importError)"
            :context="privacyContext"
          />
        </template>
      </AAlert>
    </div>

    <!-- =============================================================== -->
    <!-- MODE 2B: IMPORT — STEP 2 (REDACTED SUMMARY & MODULE FILTER)     -->
    <!-- =============================================================== -->
    <div
      v-else
      class="co-mig-body"
      data-testid="migration-import-preview-modal"
    >
      <div class="co-mig-meta-strip">
        <span class="co-mig-meta-chip co-mig-meta-filename">
          <strong>{{ importPreview?.filename }}</strong>
        </span>
        <span v-if="importPreview?.source_os" class="co-mig-meta-chip">
          {{ t("migration.metaSourceOS") }}: {{ formatMigrationSourceOS(importPreview.source_os) }}
        </span>
        <span v-if="importPreview?.app_version" class="co-mig-meta-chip">
          {{ importPreview.app_version }}
        </span>
        <span v-if="formattedExportedAt" class="co-mig-meta-chip">
          {{ formattedExportedAt }}
        </span>
      </div>

      <AAlert
        v-if="hasActiveOperation"
        type="warning"
        show-icon
        :message="tb('请等待当前操作完成后再导入迁移文件')"
      />

      <AAlert
        v-if="activeInstancesBlockAccount"
        type="warning"
        show-icon
        :message="t('migration.activeInstancesAccountBlocked')"
        data-testid="migration-active-instances-alert"
      />

      <AAlert
        v-if="hasCloseBehaviorFallbackWarning"
        type="info"
        show-icon
        :message="t('migration.closeBehaviorFallbackNotice')"
      />

      <div class="co-mig-section-head">
        <span class="co-mig-section-title">
          {{ t("migration.importModulesTitle") }}
        </span>
        <span class="co-mig-section-hint">
          {{ t("migration.importModulesHint") }}
        </span>
      </div>

      <div class="co-mig-module-list">
        <div
          v-for="mod in importModulesList"
          :key="mod.id"
          class="co-mig-module-item"
          :class="{
            'is-selected': isModuleSelected(importSelected, mod.id),
            'is-disabled': isImportModuleDisabled(mod),
          }"
          :data-testid="`migration-import-mod-${mod.id}`"
          @click="onImportModuleRowClick(mod, $event)"
        >
          <ACheckbox
            :checked="isModuleSelected(importSelected, mod.id)"
            :disabled="importApplying || isImportModuleDisabled(mod)"
            :aria-label="moduleTitle(mod.id)"
            @update:checked="(v) => toggleImportModule(mod, Boolean(v))"
          />
          <span class="co-mig-mod-icon" :class="moduleIconClass(mod.id)" aria-hidden="true">
            <PhUserCircle v-if="mod.id === 'account'" :size="15" weight="duotone" />
            <PhKey v-else-if="mod.id === 'credentials'" :size="15" weight="duotone" />
            <PhSlidersHorizontal v-else-if="mod.id === 'settings'" :size="15" weight="duotone" />
            <PhCube v-else-if="mod.id === 'library_workflows'" :size="15" weight="duotone" />
            <PhChartLineUp v-else-if="mod.id === 'cu_usage'" :size="15" weight="duotone" />
            <PhNotePencil v-else :size="15" weight="duotone" />
          </span>
          <div class="co-mig-mod-copy">
            <div class="co-mig-mod-head">
              <span class="co-mig-mod-title">{{ moduleTitle(mod.id) }}</span>
              <span class="co-mig-mod-meta">
                <ATag v-if="!mod.included" :bordered="false">
                  {{ t("migration.moduleNotIncluded") }}
                </ATag>
                <template v-else-if="mod.id === 'account'">
                  <ATag v-if="mod.account?.has_login" color="success" :bordered="false">
                    <ColabOnePrivacyText
                      v-if="mod.account?.email"
                      :text="mod.account.email"
                      kind="email"
                      :context="privacyContext"
                    />
                    <template v-else>{{ t("migration.summarySignedIn") }}</template>
                  </ATag>
                  <ATag v-if="mod.account?.has_cloud_project" color="blue" :bordered="false">
                    {{ t("migration.summaryDriveReady") }}
                  </ATag>
                  <ATag v-if="!mod.account?.has_login" :bordered="false">
                    {{ t("migration.summaryNotSignedIn") }}
                  </ATag>
                </template>
                <template v-else-if="mod.id === 'credentials'">
                  <ATag
                    v-if="(mod.credentials?.configured_count || 0) > 0"
                    color="blue"
                    :bordered="false"
                  >
                    {{
                      t("migration.summaryConfiguredCount", {
                        count: mod.credentials?.configured_count || 0,
                      })
                    }}
                  </ATag>
                  <ATag
                    v-if="mod.credentials?.has_notifications"
                    color="purple"
                    :bordered="false"
                    data-testid="migration-import-credentials-notifications-tag"
                  >
                    {{ t("migration.summaryNotifications") }}
                  </ATag>
                  <ATag
                    v-if="
                      (mod.credentials?.configured_count || 0) === 0 &&
                      !mod.credentials?.has_notifications
                    "
                    :bordered="false"
                  >
                    {{ t("migration.summaryEmpty") }}
                  </ATag>
                </template>
                <template v-else-if="mod.id === 'settings'">
                  <span class="co-mig-meta-text">{{ settingsCompactText(mod) }}</span>
                </template>
                <template v-else-if="mod.id === 'library_workflows'">
                  <span class="co-mig-meta-text">{{ libraryCompactText(mod) }}</span>
                </template>
                <template v-else-if="mod.id === 'cu_usage'">
                  <span class="co-mig-meta-text">
                    {{
                      (mod.cu_usage?.day_count || 0) > 0
                        ? t("migration.summaryCuDays", {
                            days: mod.cu_usage?.day_count || 0,
                          })
                        : t("migration.summaryEmpty")
                    }}
                  </span>
                </template>
                <template v-else-if="mod.id === 'workflow_drafts'">
                  <span class="co-mig-meta-text">
                    {{
                      (mod.workflow_drafts?.draft_count || 0) > 0
                        ? t("migration.summaryDraftsCount", {
                            count: mod.workflow_drafts?.draft_count || 0,
                          })
                        : t("migration.summaryEmpty")
                    }}
                  </span>
                </template>
              </span>
            </div>
            <p class="co-mig-mod-desc">
              <template
                v-if="
                  mod.included &&
                  mod.id === 'credentials' &&
                  (mod.credentials?.public_api_domain || mod.credentials?.comfy_canvas_domain)
                "
              >
                <span v-if="mod.credentials?.public_api_domain" class="co-mig-domain-inline">
                  API:
                  <ColabOnePrivacyText
                    :text="mod.credentials.public_api_domain"
                    kind="host"
                    :context="privacyContext"
                  />
                </span>
                <span
                  v-if="mod.credentials?.public_api_domain && mod.credentials?.comfy_canvas_domain"
                  class="co-mig-dot"
                >
                  ·
                </span>
                <span v-if="mod.credentials?.comfy_canvas_domain" class="co-mig-domain-inline">
                  ComfyUI:
                  <ColabOnePrivacyText
                    :text="mod.credentials.comfy_canvas_domain"
                    kind="host"
                    :context="privacyContext"
                  />
                </span>
              </template>
              <template v-else>{{ moduleDesc(mod.id) }}</template>
            </p>
          </div>
        </div>
      </div>

      <AAlert
        type="warning"
        show-icon
        :message="t('migration.overwriteWarningTitle')"
        :description="
          isModuleSelected(importSelected, 'account')
            ? t('migration.overwriteWarningDescWithAccount')
            : t('migration.overwriteWarningDesc')
        "
      />

      <AAlert v-if="importError" type="error" show-icon>
        <template #message>
          <ColabOnePrivacyText
            :text="tb(importError)"
            :context="privacyContext"
          />
        </template>
      </AAlert>
    </div>

    <!-- =============================================================== -->
    <!-- FOOTER                                                          -->
    <!-- =============================================================== -->
    <template #footer>
      <div class="co-mig-footer">
        <AButton :disabled="!canClose" @click="requestClose">
          {{ t("common.cancel") }}
        </AButton>

        <AButton
          v-if="mode === 'export'"
          type="primary"
          :loading="exportSubmitting"
          :disabled="!canSubmitExport"
          data-testid="migration-export-submit"
          @click="handleSubmitExport"
        >
          <template #icon><PhUploadSimple :size="15" /></template>
          {{
            exportSubmitting
              ? t("migration.exportingBtn")
              : t("migration.exportConfirmBtn")
          }}
        </AButton>

        <AButton
          v-else-if="importStep === 'decrypt'"
          type="primary"
          :loading="importDecrypting"
          :disabled="!canSubmitDecrypt"
          data-testid="migration-decrypt-submit"
          @click="handleSubmitDecrypt"
        >
          <template #icon><PhLockKey :size="15" /></template>
          {{
            importDecrypting
              ? t("migration.decryptingBtn")
              : t("migration.decryptContinueBtn")
          }}
        </AButton>

        <AButton
          v-else
          type="primary"
          :loading="importApplying"
          :disabled="!canSubmitApply"
          data-testid="migration-apply-submit"
          @click="handleSubmitApply"
        >
          <template #icon><PhDownloadSimple :size="15" /></template>
          {{
            importApplying
              ? t("migration.importingBtn")
              : t("migration.applyConfirmBtn", { count: importSelected.length })
          }}
        </AButton>
      </div>
    </template>
  </AModal>
</template>

<style scoped>
.co-mig-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
  flex-wrap: wrap;
  padding-right: 20px;
}

.co-mig-title-main {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  max-width: 100%;
}

.co-mig-title-icon {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--co-selected);
  color: var(--co-accent);
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.co-mig-title-icon.is-import {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

:root[data-theme="dark"] .co-mig-title-icon.is-export {
  color: var(--co-accent-strong);
}

.co-mig-title-text {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
  min-width: 0;
}

.co-mig-cipher-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-inline-end: 0;
  font-family: var(--co-font-mono);
  font-size: 11px;
}

.co-mig-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding-top: 4px;
}

.co-mig-lead {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--co-secondary-text);
}

.co-mig-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.co-mig-section-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--co-text);
}

.co-mig-section-hint {
  font-size: 12px;
  color: var(--co-muted);
}

.co-mig-quick-actions {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 10px;
}

.co-mig-link-btn {
  background: transparent;
  border: none;
  padding: 0;
  font-size: 12px;
  color: var(--co-accent);
  cursor: pointer;
  white-space: nowrap;
  text-align: left;
  transition: opacity 0.15s ease;
}

.co-mig-link-btn:hover:not(:disabled) {
  opacity: 0.8;
  text-decoration: underline;
}

.co-mig-link-btn.is-active {
  font-weight: 600;
}

.co-mig-link-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-mig-dot {
  color: var(--co-muted);
  font-size: 12px;
}

.co-mig-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px 0;
  color: var(--co-muted);
  font-size: 13px;
}

.co-mig-module-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 310px;
  overflow-y: auto;
  padding-right: 2px;
}

.co-mig-module-item {
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

.co-mig-module-item:hover:not(.is-disabled) {
  border-color: var(--co-border);
}

.co-mig-module-item.is-selected {
  border-color: rgba(59, 130, 246, 0.35);
  background: var(--co-selected);
}

.co-mig-module-item.is-selected .co-mig-mod-meta :deep(.ant-tag-blue) {
  background: var(--co-surface);
  border-color: rgba(59, 130, 246, 0.28);
}

:root[data-theme="dark"] .co-mig-module-item.is-selected .co-mig-mod-meta :deep(.ant-tag-blue) {
  background: rgba(59, 130, 246, 0.24);
  border-color: rgba(96, 165, 250, 0.42);
}

.co-mig-module-item.is-disabled {
  opacity: 0.58;
  cursor: not-allowed;
}

.co-mig-mod-icon {
  width: 24px;
  height: 24px;
  border-radius: 7px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  margin-top: 1px;
  background: var(--co-surface);
  color: var(--co-secondary-text);
  border: 1px solid var(--co-border-secondary);
}

.co-mig-mod-icon.is-blue {
  color: var(--co-accent);
}

.co-mig-mod-icon.is-violet {
  color: #7c3aed;
}

.co-mig-mod-icon.is-amber {
  color: #d97706;
}

.co-mig-mod-icon.is-emerald {
  color: #059669;
}

:root[data-theme="dark"] .co-mig-mod-icon.is-blue {
  color: var(--co-accent-strong);
}

:root[data-theme="dark"] .co-mig-mod-icon.is-violet {
  color: #a78bfa;
}

:root[data-theme="dark"] .co-mig-mod-icon.is-amber {
  color: #fbbf24;
}

:root[data-theme="dark"] .co-mig-mod-icon.is-emerald {
  color: #34d399;
}

.co-mig-mod-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-mig-mod-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
  max-width: 100%;
}

.co-mig-mod-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--co-text);
}

.co-mig-mod-meta {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  min-width: 0;
  max-width: 100%;
}

.co-mig-mod-meta :deep(.ant-tag) {
  margin-inline-end: 0;
  font-size: 11px;
  line-height: 18px;
}

.co-mig-meta-text {
  font-size: 11.5px;
  color: var(--co-secondary-text);
  font-variant-numeric: tabular-nums;
}

.co-mig-mod-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-mig-domain-inline {
  font-family: var(--co-font-mono);
  font-size: 11.5px;
  color: var(--co-secondary-text);
}

.co-mig-password-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-mig-password-head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--co-text);
}

.co-mig-shield-icon {
  color: var(--co-accent);
  flex-shrink: 0;
}

:root[data-theme="dark"] .co-mig-shield-icon {
  color: var(--co-accent-strong);
}

.co-mig-password-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-secondary-text);
}

.co-mig-password-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

@media (max-width: 520px) {
  .co-mig-password-grid {
    grid-template-columns: 1fr;
  }
}

.co-mig-field-error {
  margin: 0;
  font-size: 12px;
  color: var(--co-danger, #dc2626);
}

.co-mig-file-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 14px;
  border-radius: 12px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
}

.co-mig-file-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.co-mig-file-name {
  font-family: var(--co-font-mono);
  font-size: 13px;
  color: var(--co-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.co-mig-file-meta {
  font-size: 12px;
  color: var(--co-muted);
}

.co-mig-meta-strip {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-mig-meta-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.co-mig-meta-filename {
  flex: 1 1 100%;
  min-width: 0;
  overflow-wrap: anywhere;
}

.co-mig-meta-chip + .co-mig-meta-chip::before {
  content: "·";
  margin-right: 4px;
  color: var(--co-muted);
}

.co-mig-meta-filename + .co-mig-meta-chip::before {
  content: none;
}

.co-mig-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

:global(.co-migration-modal-wrap .ant-modal-content) {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 24px);
  max-height: calc(100dvh - 24px);
}

:global(.co-migration-modal-wrap .ant-modal-header),
:global(.co-migration-modal-wrap .ant-modal-footer) {
  flex-shrink: 0;
}

:global(.co-migration-modal-wrap .ant-modal-body) {
  flex: 1 1 auto;
  min-height: 0;
  max-height: none;
  overflow-y: auto;
  overscroll-behavior: contain;
}

@media (max-width: 680px), (max-height: 768px) {
  .co-mig-module-list {
    max-height: none;
    overflow-y: visible;
    padding-right: 0;
  }
}

@media (max-width: 680px) {
  .co-mig-footer {
    width: 100%;
    flex-wrap: nowrap;
  }

  .co-mig-footer > :first-child {
    flex-shrink: 0;
  }

  .co-mig-footer > :last-child {
    flex: 1 1 0;
    min-width: 0;
  }
}

@media (max-width: 520px) {
  .co-mig-meta-strip {
    gap: 4px 12px;
  }

  .co-mig-meta-chip + .co-mig-meta-chip::before {
    display: none;
  }
}

@media (max-width: 480px) {
  .co-mig-quick-actions .co-mig-dot {
    display: none;
  }
}

@media (max-width: 440px) {
  .co-mig-footer {
    flex-direction: column-reverse;
    align-items: stretch;
    gap: 8px;
  }

  .co-mig-footer > :first-child,
  .co-mig-footer > :last-child,
  .co-mig-footer :deep(.ant-btn) {
    width: 100%;
    flex: none;
  }
}
</style>
