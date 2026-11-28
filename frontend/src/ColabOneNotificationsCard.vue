<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import {
  Alert as AAlert,
  Button as AButton,
  Input as AInput,
  InputNumber as AInputNumber,
  Popconfirm as APopconfirm,
  Select as ASelect,
  SelectOption as ASelectOption,
  Space as ASpace,
  Switch as ASwitch,
  Tag as ATag,
} from "ant-design-vue";
import {
  PhBellRinging,
  PhCheckCircle,
  PhDeviceMobile,
  PhEnvelopeSimple,
  PhLightning,
  PhPaperPlaneTilt,
  PhSlidersHorizontal,
  PhWarningCircle,
} from "@phosphor-icons/vue";
import ColabOneSecretInput from "./ColabOneSecretInput.vue";
import ColabOneSkeleton from "./ColabOneSkeleton.vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { colabOneCommand } from "./api";
import {
  COLABONE_BARK_LEVEL_VALUES,
  COLABONE_BARK_SOUND_VALUES,
  COLABONE_DEFAULT_BARK_GROUP,
  COLABONE_DEFAULT_BARK_SERVER_URL,
  COLABONE_DEFAULT_EMAIL_FROM_NAME,
  COLABONE_MAX_SMTP_PORT,
  COLABONE_MIN_SMTP_PORT,
  COLABONE_SMTP_PRESETS,
  buildBarkSavePayload,
  buildEmailSavePayload,
  buildTelegramSavePayload,
  canSaveBarkDraft,
  canSaveEmailDraft,
  canSaveTelegramDraft,
  colabOneCountEnabledNotificationChannels,
  colabOneDefaultPortForSmtpSecurity,
  colabOneMatchSmtpPreset,
  colabOneParseBarkEndpoint,
  createBarkDraftFromView,
  createEmailDraftFromView,
  createTelegramDraftFromView,
  getBarkValidationErrorKey,
  getEmailValidationErrorKey,
  getTelegramValidationErrorKey,
  isBarkDraftDirty,
  isColabOneDefaultSmtpPort,
  isEmailDraftDirty,
  isTelegramDraftDirty,
  normalizeColabOneNotificationSettings,
  normalizeColabOneSmtpSecurity,
  type ColabOneBarkDraft,
  type ColabOneEmailDraft,
  type ColabOneNotificationChannel,
  type ColabOneNotificationSecretField,
  type ColabOneParsedBarkEndpoint,
  type ColabOneSmtpPreset,
  type ColabOneSmtpSecurity,
  type ColabOneTelegramDraft,
} from "./colabOneNotifications";
import { colabOnePrivacyActive } from "./colabOnePrivacy";
import type { ColabOneToastKind } from "./colabOneToast";
import { t, tb } from "./i18n";
import type { ColabOneNotificationSettings } from "./types";

defineOptions({
  name: "ColabOneNotificationsCard",
});

const props = withDefaults(
  defineProps<{
    settings: ColabOneNotificationSettings;
    loading: boolean;
    canRevealSecrets: boolean;
    disabled?: boolean;
  }>(),
  {
    disabled: false,
  },
);

const emit = defineEmits<{
  updated: [settings: ColabOneNotificationSettings];
  notify: [message: string, kind?: ColabOneToastKind, dedupeKey?: string];
  error: [err: unknown];
}>();

type SecretInputHandle = { hide?: () => void; focus?: () => void };
type FocusableHandle = { focus?: () => void };

const telegramExpanded = ref(false);
const barkExpanded = ref(false);
const emailExpanded = ref(false);

const telegramBotTokenRef = ref<SecretInputHandle | null>(null);
const telegramProxyRef = ref<SecretInputHandle | null>(null);
const barkServerRef = ref<FocusableHandle | null>(null);
const barkDeviceKeyRef = ref<SecretInputHandle | null>(null);
const emailHostRef = ref<FocusableHandle | null>(null);
const emailPasswordRef = ref<SecretInputHandle | null>(null);

const telegramDraft = ref<ColabOneTelegramDraft>(
  createTelegramDraftFromView(props.settings.telegram),
);
const barkDraft = ref<ColabOneBarkDraft>(
  createBarkDraftFromView(props.settings.bark),
);
const emailDraft = ref<ColabOneEmailDraft>(
  createEmailDraftFromView(props.settings.email),
);

const telegramTouched = ref(false);
const barkTouched = ref(false);
const emailTouched = ref(false);

let lastSyncedTelegramJson = JSON.stringify(props.settings.telegram);
let lastSyncedBarkJson = JSON.stringify(props.settings.bark);
let lastSyncedEmailJson = JSON.stringify(props.settings.email);

const savingPrefKey = ref<string>("");
const savingChannel = ref<ColabOneNotificationChannel | "">("");
const clearingChannel = ref<ColabOneNotificationChannel | "">("");
const testingChannel = ref<ColabOneNotificationChannel | "">("");

const isWebConsoleRestricted = computed(() => !props.canRevealSecrets);

function isChannelBusy(channel: ColabOneNotificationChannel): boolean {
  return (
    savingChannel.value === channel ||
    clearingChannel.value === channel ||
    testingChannel.value === channel
  );
}

const telegramInputsDisabled = computed(
  () =>
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("telegram") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value),
);

const barkInputsDisabled = computed(
  () =>
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("bark") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value),
);

const emailInputsDisabled = computed(
  () =>
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("email") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value),
);

const enabledChannelsCount = computed(() =>
  colabOneCountEnabledNotificationChannels(props.settings),
);

const telegramDirty = computed(() =>
  isTelegramDraftDirty(telegramDraft.value, props.settings.telegram),
);
const barkDirty = computed(() =>
  isBarkDraftDirty(barkDraft.value, props.settings.bark),
);
const emailDirty = computed(() =>
  isEmailDraftDirty(emailDraft.value, props.settings.email),
);

const telegramValidationHint = computed(() => {
  const key = getTelegramValidationErrorKey(
    telegramDraft.value,
    props.settings.telegram,
  );
  return key ? t(key) : "";
});
const barkValidationHint = computed(() => {
  const key = getBarkValidationErrorKey(barkDraft.value, props.settings.bark);
  return key ? t(key) : "";
});
const emailValidationHint = computed(() => {
  const key = getEmailValidationErrorKey(emailDraft.value, props.settings.email);
  return key ? t(key) : "";
});

const canSaveTelegram = computed(() =>
  canSaveTelegramDraft(telegramDraft.value, props.settings.telegram),
);
const canSaveBark = computed(() =>
  canSaveBarkDraft(barkDraft.value, props.settings.bark),
);
const canSaveEmail = computed(() =>
  canSaveEmailDraft(emailDraft.value, props.settings.email),
);

const telegramSaveTooltip = computed(() => {
  if (isWebConsoleRestricted.value) {
    return t("notifications.webDesktopOnlyEditNotice");
  }
  if (!canSaveTelegram.value && telegramValidationHint.value) {
    return telegramValidationHint.value;
  }
  return "";
});

const barkSaveTooltip = computed(() => {
  if (isWebConsoleRestricted.value) {
    return t("notifications.webDesktopOnlyEditNotice");
  }
  if (!canSaveBark.value && barkValidationHint.value) {
    return barkValidationHint.value;
  }
  return "";
});

const emailSaveTooltip = computed(() => {
  if (isWebConsoleRestricted.value) {
    return t("notifications.webDesktopOnlyEditNotice");
  }
  if (!canSaveEmail.value && emailValidationHint.value) {
    return emailValidationHint.value;
  }
  return "";
});

const telegramTestTooltip = computed(() => {
  if (!props.settings.telegram.configured) {
    return t("notifications.testNeedConfigHint");
  }
  if (telegramDirty.value) {
    return t("notifications.testSaveFirstHint");
  }
  return "";
});

const barkTestTooltip = computed(() => {
  if (!props.settings.bark.configured) {
    return t("notifications.testNeedConfigHint");
  }
  if (barkDirty.value) {
    return t("notifications.testSaveFirstHint");
  }
  return "";
});

const emailTestTooltip = computed(() => {
  if (!props.settings.email.configured) {
    return t("notifications.testNeedConfigHint");
  }
  if (emailDirty.value) {
    return t("notifications.testSaveFirstHint");
  }
  return "";
});

const telegramBotTokenSavedPlaceholder = computed(() =>
  props.settings.telegram.masked_bot_token && !colabOnePrivacyActive.value
    ? t("notifications.telegramBotTokenSavedWithMask", {
        mask: props.settings.telegram.masked_bot_token,
      })
    : t("notifications.telegramBotTokenSavedPlaceholder"),
);

const telegramProxySavedPlaceholder = computed(() =>
  props.settings.telegram.masked_proxy_url && !colabOnePrivacyActive.value
    ? t("notifications.telegramProxySavedWithMask", {
        mask: props.settings.telegram.masked_proxy_url,
      })
    : t("notifications.telegramProxySavedPlaceholder"),
);

const barkKeySavedPlaceholder = computed(() =>
  props.settings.bark.masked_device_key && !colabOnePrivacyActive.value
    ? t("notifications.barkKeySavedWithMask", {
        mask: props.settings.bark.masked_device_key,
      })
    : t("notifications.barkKeySavedPlaceholder"),
);

const activeSmtpPresetId = computed(() =>
  colabOneMatchSmtpPreset(
    emailDraft.value.smtp_host,
    emailDraft.value.smtp_port,
    emailDraft.value.security,
  ),
);

const barkSoundOptions = computed(() =>
  COLABONE_BARK_SOUND_VALUES.map((val) => ({
    value: val,
    label: val ? val : t("notifications.barkSoundDefault"),
  })),
);

const barkLevelOptions = computed(() =>
  COLABONE_BARK_LEVEL_VALUES.map((val) => {
    let label = t("notifications.barkLevelAuto");
    if (val === "active") label = t("notifications.barkLevelActive");
    else if (val === "timeSensitive") {
      label = t("notifications.barkLevelTimeSensitive");
    } else if (val === "passive") {
      label = t("notifications.barkLevelPassive");
    }
    return { value: val, label };
  }),
);

watch(
  () => props.settings.telegram,
  (next) => {
    const nextJson = JSON.stringify(next);
    if (nextJson === lastSyncedTelegramJson) return;
    lastSyncedTelegramJson = nextJson;
    if (
      telegramDirty.value ||
      (telegramExpanded.value && telegramTouched.value)
    ) {
      return;
    }
    telegramDraft.value = createTelegramDraftFromView(next);
    telegramTouched.value = false;
  },
  { deep: true },
);

watch(
  () => props.settings.bark,
  (next) => {
    const nextJson = JSON.stringify(next);
    if (nextJson === lastSyncedBarkJson) return;
    lastSyncedBarkJson = nextJson;
    if (barkDirty.value || (barkExpanded.value && barkTouched.value)) {
      return;
    }
    barkDraft.value = createBarkDraftFromView(next);
    barkTouched.value = false;
  },
  { deep: true },
);

watch(
  () => props.settings.email,
  (next) => {
    const nextJson = JSON.stringify(next);
    if (nextJson === lastSyncedEmailJson) return;
    lastSyncedEmailJson = nextJson;
    if (emailDirty.value || (emailExpanded.value && emailTouched.value)) {
      return;
    }
    emailDraft.value = createEmailDraftFromView(next);
    emailTouched.value = false;
  },
  { deep: true },
);

function channelDisplayName(channel: ColabOneNotificationChannel): string {
  switch (channel) {
    case "system":
      return t("notifications.systemTitle");
    case "telegram":
      return t("notifications.telegramTitle");
    case "bark":
      return t("notifications.barkTitle");
    case "email":
      return t("notifications.emailTitle");
  }
}

async function saveNotificationPayload(
  payload: Record<string, unknown>,
): Promise<ColabOneNotificationSettings> {
  const res = await colabOneCommand<{
    ok?: boolean;
    notification_settings?: Partial<ColabOneNotificationSettings>;
  }>("notifications.save", payload);
  const normalized = normalizeColabOneNotificationSettings(
    res?.notification_settings ?? props.settings,
  );
  emit("updated", normalized);
  return normalized;
}

async function loadNotificationSecret(
  field: ColabOneNotificationSecretField,
): Promise<string> {
  const res = await colabOneCommand<{ value?: string }>(
    "notifications.secret",
    { field },
  );
  return typeof res?.value === "string" ? res.value : "";
}

const loadTelegramBotToken = () => loadNotificationSecret("telegram_bot_token");
const loadTelegramProxy = () => loadNotificationSecret("telegram_proxy_url");
const loadBarkDeviceKey = () => loadNotificationSecret("bark_device_key");
const loadEmailPassword = () => loadNotificationSecret("email_password");

type TopLevelBoolPrefKey =
  | "notify_gpu"
  | "notify_task"
  | "notify_alert"
  | "system_only_when_unfocused"
  | "remote_only_when_unfocused";

async function toggleTopLevelPref(key: TopLevelBoolPrefKey, checked: unknown) {
  if (props.disabled || savingPrefKey.value) return;
  const next = Boolean(checked);
  savingPrefKey.value = key;
  try {
    await saveNotificationPayload({ [key]: next });
    emit("notify", t("notifications.toastSaved"), "success", `notif-pref-${key}`);
  } catch (err) {
    emit("error", err);
  } finally {
    savingPrefKey.value = "";
  }
}

function onScenarioCardClick(key: "notify_gpu" | "notify_task" | "notify_alert", event: MouseEvent) {
  const target = event.target as HTMLElement | null;
  if (target?.closest(".ant-switch, button, a, input")) return;
  void toggleTopLevelPref(key, !props.settings[key]);
}

async function onSystemEnabledChange(checked: unknown) {
  if (
    props.disabled ||
    isChannelBusy("system") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value) ||
    !props.settings.system.supported
  ) {
    return;
  }
  const next = Boolean(checked);
  savingChannel.value = "system";
  try {
    await saveNotificationPayload({ system: { enabled: next } });
    emit(
      "notify",
      next
        ? t("notifications.toastChannelEnabled", {
            channel: channelDisplayName("system"),
          })
        : t("notifications.toastChannelDisabled", {
            channel: channelDisplayName("system"),
          }),
      "success",
      "notif-toggle-system",
    );
  } catch (err) {
    emit("error", err);
  } finally {
    savingChannel.value = "";
  }
}

async function onRemoteChannelSwitch(
  channel: "telegram" | "bark" | "email",
  checked: unknown,
) {
  if (
    props.disabled ||
    isChannelBusy(channel) ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value)
  ) {
    return;
  }
  const next = Boolean(checked);
  const configured = props.settings[channel].configured;
  if (next && !configured) {
    if (channel === "telegram") {
      telegramExpanded.value = true;
      await nextTick();
      if (!isWebConsoleRestricted.value) {
        telegramBotTokenRef.value?.focus?.();
      }
    } else if (channel === "bark") {
      barkExpanded.value = true;
      await nextTick();
      if (!isWebConsoleRestricted.value) {
        barkServerRef.value?.focus?.();
      }
    } else {
      emailExpanded.value = true;
      await nextTick();
      if (!isWebConsoleRestricted.value) {
        emailHostRef.value?.focus?.();
      }
    }
    emit(
      "notify",
      isWebConsoleRestricted.value
        ? t("notifications.webDesktopOnlyEditNotice")
        : t("notifications.toastConfigureFirst", {
            channel: channelDisplayName(channel),
          }),
      "info",
      `notif-need-config-${channel}`,
    );
    return;
  }
  savingChannel.value = channel;
  try {
    await saveNotificationPayload({ [channel]: { enabled: next } });
    emit(
      "notify",
      next
        ? t("notifications.toastChannelEnabled", {
            channel: channelDisplayName(channel),
          })
        : t("notifications.toastChannelDisabled", {
            channel: channelDisplayName(channel),
          }),
      "success",
      `notif-toggle-${channel}`,
    );
  } catch (err) {
    emit("error", err);
  } finally {
    savingChannel.value = "";
  }
}

function toggleChannelExpanded(channel: "telegram" | "bark" | "email") {
  if (channel === "telegram") {
    const next = !telegramExpanded.value;
    telegramExpanded.value = next;
    if (next) {
      if (!telegramDirty.value && !telegramTouched.value) {
        telegramDraft.value = createTelegramDraftFromView(props.settings.telegram);
      }
    } else {
      telegramBotTokenRef.value?.hide?.();
      telegramProxyRef.value?.hide?.();
    }
  } else if (channel === "bark") {
    const next = !barkExpanded.value;
    barkExpanded.value = next;
    if (next) {
      if (!barkDirty.value && !barkTouched.value) {
        barkDraft.value = createBarkDraftFromView(props.settings.bark);
      }
    } else {
      barkDeviceKeyRef.value?.hide?.();
    }
  } else {
    const next = !emailExpanded.value;
    emailExpanded.value = next;
    if (next) {
      if (!emailDirty.value && !emailTouched.value) {
        emailDraft.value = createEmailDraftFromView(props.settings.email);
      }
    } else {
      emailPasswordRef.value?.hide?.();
    }
  }
}

function applyParsedBarkEndpoint(parsed: ColabOneParsedBarkEndpoint) {
  barkDraft.value.server_url =
    parsed.serverUrl || COLABONE_DEFAULT_BARK_SERVER_URL;
  barkDraft.value.device_key = parsed.deviceKey;
  if (parsed.group) barkDraft.value.group = parsed.group;
  if (
    parsed.sound &&
    (COLABONE_BARK_SOUND_VALUES as readonly string[]).includes(parsed.sound)
  ) {
    barkDraft.value.sound = parsed.sound;
  }
  if (
    parsed.level &&
    (COLABONE_BARK_LEVEL_VALUES as readonly string[]).includes(parsed.level)
  ) {
    barkDraft.value.level = parsed.level;
  }
  barkTouched.value = true;
  emit("notify", t("notifications.barkSplitToast"), "info", "notif-bark-split");
}

function tryAutoSplitBarkEndpoint() {
  if (barkDraft.value.device_key.trim()) return;
  const rawServer = barkDraft.value.server_url.trim();
  const savedServer = (
    props.settings.bark.server_url || COLABONE_DEFAULT_BARK_SERVER_URL
  )
    .trim()
    .replace(/\/+$/, "");
  if (
    props.settings.bark.has_device_key &&
    !rawServer.includes("?") &&
    rawServer.replace(/\/+$/, "") === savedServer
  ) {
    return;
  }
  const parsed = colabOneParseBarkEndpoint(rawServer);
  if (!parsed.hasEmbeddedKey || !parsed.deviceKey) return;
  applyParsedBarkEndpoint(parsed);
}

function onBarkServerPaste(event: ClipboardEvent) {
  if (barkInputsDisabled.value) return;
  const pasted = event.clipboardData?.getData("text")?.trim() ?? "";
  if (/^https?:\/\//i.test(pasted)) {
    const parsed = colabOneParseBarkEndpoint(pasted);
    if (parsed.hasEmbeddedKey && parsed.deviceKey) {
      event.preventDefault();
      applyParsedBarkEndpoint(parsed);
      return;
    }
  }
  barkTouched.value = true;
  void nextTick(() => {
    tryAutoSplitBarkEndpoint();
  });
}

function onBarkKeyPaste(event: ClipboardEvent) {
  if (barkInputsDisabled.value) return;
  const pasted = event.clipboardData?.getData("text")?.trim() ?? "";
  if (!/^https?:\/\//i.test(pasted)) return;
  const parsed = colabOneParseBarkEndpoint(pasted);
  if (!parsed.hasEmbeddedKey || !parsed.deviceKey) return;
  event.preventDefault();
  event.stopPropagation();
  applyParsedBarkEndpoint(parsed);
}

function tryAutoSplitBarkInputsOnSave() {
  const keyRaw = barkDraft.value.device_key.trim();
  if (/^https?:\/\//i.test(keyRaw)) {
    const parsed = colabOneParseBarkEndpoint(keyRaw);
    if (parsed.hasEmbeddedKey && parsed.deviceKey) {
      applyParsedBarkEndpoint(parsed);
      return;
    }
  }
  tryAutoSplitBarkEndpoint();
}

function toggleClearTelegramProxy() {
  telegramDraft.value.clear_proxy = !telegramDraft.value.clear_proxy;
  telegramTouched.value = true;
}

function toggleClearEmailPassword() {
  emailDraft.value.clear_password = !emailDraft.value.clear_password;
  emailTouched.value = true;
}

function applySmtpPreset(preset: ColabOneSmtpPreset) {
  emailDraft.value.smtp_host = preset.host;
  emailDraft.value.smtp_port = preset.port;
  emailDraft.value.security = preset.security;
  emailTouched.value = true;
}

function onEmailSecurityChange(raw: unknown) {
  const nextSec: ColabOneSmtpSecurity = normalizeColabOneSmtpSecurity(raw);
  const prevPort = emailDraft.value.smtp_port;
  emailDraft.value.security = nextSec;
  if (prevPort === null || isColabOneDefaultSmtpPort(prevPort)) {
    emailDraft.value.smtp_port = colabOneDefaultPortForSmtpSecurity(nextSec);
  }
  emailTouched.value = true;
}

function onEmailPortUpdate(raw: string | number | null) {
  if (raw === null || raw === "") {
    emailDraft.value.smtp_port = null;
    emailTouched.value = true;
    return;
  }
  const num = typeof raw === "number" ? raw : Number(raw);
  emailDraft.value.smtp_port = Number.isFinite(num) ? Math.trunc(num) : null;
  emailTouched.value = true;
}

async function saveTelegram() {
  if (
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("telegram") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value) ||
    !canSaveTelegram.value
  ) {
    return;
  }
  savingChannel.value = "telegram";
  try {
    const payload = buildTelegramSavePayload(
      telegramDraft.value,
      props.settings.telegram,
    );
    const updated = await saveNotificationPayload({ telegram: payload });
    lastSyncedTelegramJson = JSON.stringify(updated.telegram);
    telegramDraft.value = createTelegramDraftFromView(updated.telegram);
    telegramTouched.value = false;
    telegramBotTokenRef.value?.hide?.();
    telegramProxyRef.value?.hide?.();
    emit(
      "notify",
      t("notifications.toastChannelSaved", {
        channel: channelDisplayName("telegram"),
      }),
      "success",
      "notif-save-telegram",
    );
  } catch (err) {
    emit("error", err);
  } finally {
    savingChannel.value = "";
  }
}

async function saveBark() {
  if (
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("bark") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value)
  ) {
    return;
  }
  tryAutoSplitBarkInputsOnSave();
  if (!canSaveBark.value) return;
  savingChannel.value = "bark";
  try {
    const payload = buildBarkSavePayload(barkDraft.value, props.settings.bark);
    const updated = await saveNotificationPayload({ bark: payload });
    lastSyncedBarkJson = JSON.stringify(updated.bark);
    barkDraft.value = createBarkDraftFromView(updated.bark);
    barkTouched.value = false;
    barkDeviceKeyRef.value?.hide?.();
    emit(
      "notify",
      t("notifications.toastChannelSaved", {
        channel: channelDisplayName("bark"),
      }),
      "success",
      "notif-save-bark",
    );
  } catch (err) {
    emit("error", err);
  } finally {
    savingChannel.value = "";
  }
}

async function saveEmail() {
  if (
    props.disabled ||
    isWebConsoleRestricted.value ||
    isChannelBusy("email") ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value) ||
    !canSaveEmail.value
  ) {
    return;
  }
  savingChannel.value = "email";
  try {
    const payload = buildEmailSavePayload(emailDraft.value, props.settings.email);
    const updated = await saveNotificationPayload({ email: payload });
    lastSyncedEmailJson = JSON.stringify(updated.email);
    emailDraft.value = createEmailDraftFromView(updated.email);
    emailTouched.value = false;
    emailPasswordRef.value?.hide?.();
    emit(
      "notify",
      t("notifications.toastChannelSaved", {
        channel: channelDisplayName("email"),
      }),
      "success",
      "notif-save-email",
    );
  } catch (err) {
    emit("error", err);
  } finally {
    savingChannel.value = "";
  }
}

async function clearRemoteChannel(channel: "telegram" | "bark" | "email") {
  if (
    props.disabled ||
    isChannelBusy(channel) ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value)
  ) {
    return;
  }
  clearingChannel.value = channel;
  try {
    const updated = await saveNotificationPayload({ [channel]: { clear: true } });
    if (channel === "telegram") {
      lastSyncedTelegramJson = JSON.stringify(updated.telegram);
      telegramDraft.value = createTelegramDraftFromView(updated.telegram);
      telegramTouched.value = false;
      telegramBotTokenRef.value?.hide?.();
      telegramProxyRef.value?.hide?.();
      telegramExpanded.value = false;
    } else if (channel === "bark") {
      lastSyncedBarkJson = JSON.stringify(updated.bark);
      barkDraft.value = createBarkDraftFromView(updated.bark);
      barkTouched.value = false;
      barkDeviceKeyRef.value?.hide?.();
      barkExpanded.value = false;
    } else {
      lastSyncedEmailJson = JSON.stringify(updated.email);
      emailDraft.value = createEmailDraftFromView(updated.email);
      emailTouched.value = false;
      emailPasswordRef.value?.hide?.();
      emailExpanded.value = false;
    }
    emit(
      "notify",
      t("notifications.toastChannelCleared", {
        channel: channelDisplayName(channel),
      }),
      "success",
      `notif-clear-${channel}`,
    );
  } catch (err) {
    emit("error", err);
  } finally {
    clearingChannel.value = "";
  }
}

async function testChannel(channel: ColabOneNotificationChannel) {
  if (
    props.disabled ||
    isChannelBusy(channel) ||
    Boolean(testingChannel.value) ||
    Boolean(savingChannel.value) ||
    Boolean(clearingChannel.value)
  ) {
    return;
  }
  if (channel === "system" && !props.settings.system.supported) return;
  if (channel === "telegram" && (!props.settings.telegram.configured || telegramDirty.value)) {
    return;
  }
  if (channel === "bark" && (!props.settings.bark.configured || barkDirty.value)) {
    return;
  }
  if (channel === "email" && (!props.settings.email.configured || emailDirty.value)) {
    return;
  }
  testingChannel.value = channel;
  try {
    const res = await colabOneCommand<{
      ok?: boolean;
      channel?: string;
      message?: string;
    }>("notifications.test", { channel });
    const rawMsg = typeof res?.message === "string" ? res.message.trim() : "";
    const msg =
      !rawMsg || rawMsg === "测试通知已发送"
        ? t("notifications.toastTestSent", {
            channel: channelDisplayName(channel),
          })
        : tb(rawMsg);
    emit("notify", msg, "success", `notif-test-${channel}`);
  } catch (err) {
    emit("error", err);
  } finally {
    testingChannel.value = "";
  }
}
</script>

<template>
  <div class="co-settings-list co-notif-card" data-testid="settings-notifications-card">
    <!-- Row 1: Trigger Scenarios & Delivery Timing -->
    <div class="co-settings-row" data-testid="settings-notifications-prefs-row">
      <div class="co-sr-main">
        <div class="co-sr-info">
          <div class="co-sr-title-line">
            <span class="co-sr-title-group">
              <span class="co-sr-icon is-blue" aria-hidden="true">
                <PhSlidersHorizontal :size="14" weight="duotone" />
              </span>
              <h3 class="co-sr-title">{{ t("notifications.prefsTitle") }}</h3>
            </span>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="72"
              :height="20"
              variant="tag"
              tone="subtle"
              :delay="100"
              inline
            />
            <ATag v-else-if="enabledChannelsCount > 0" color="blue" :bordered="false">
              {{ t("notifications.activeChannelsTag", { count: enabledChannelsCount }) }}
            </ATag>
            <ATag v-else :bordered="false">
              {{ t("notifications.noActiveChannelsTag") }}
            </ATag>
          </div>
          <p class="co-sr-desc">{{ t("notifications.prefsDesc") }}</p>
        </div>
      </div>

      <div v-if="props.loading" class="co-notif-skeleton-grid" aria-hidden="true">
        <ColabOneSkeleton width="100%" :height="58" :radius="10" variant="rect" tone="subtle" />
        <ColabOneSkeleton width="100%" :height="58" :radius="10" variant="rect" tone="subtle" :delay="60" />
        <ColabOneSkeleton width="100%" :height="58" :radius="10" variant="rect" tone="subtle" :delay="120" />
      </div>

      <template v-else>
        <!-- 3 Event Category Pills -->
        <div class="co-notif-scenario-grid co-sk-reveal">
          <div
            class="co-notif-scenario-pill"
            :class="{ 'is-active': props.settings.notify_gpu }"
            data-testid="notif-scenario-gpu"
            @click="onScenarioCardClick('notify_gpu', $event)"
          >
            <span class="co-sr-icon is-blue" aria-hidden="true">
              <PhLightning :size="14" weight="duotone" />
            </span>
            <div class="co-notif-scenario-copy">
              <span class="co-notif-scenario-title">{{ t("notifications.eventGpuTitle") }}</span>
              <span class="co-notif-scenario-desc">{{ t("notifications.eventGpuDesc") }}</span>
            </div>
            <ASwitch
              size="small"
              :checked="props.settings.notify_gpu"
              :loading="savingPrefKey === 'notify_gpu'"
              :disabled="props.disabled || (!!savingPrefKey && savingPrefKey !== 'notify_gpu')"
              :aria-label="t('notifications.eventGpuTitle')"
              @update:checked="(v) => toggleTopLevelPref('notify_gpu', v)"
            />
          </div>

          <div
            class="co-notif-scenario-pill"
            :class="{ 'is-active': props.settings.notify_task }"
            data-testid="notif-scenario-task"
            @click="onScenarioCardClick('notify_task', $event)"
          >
            <span class="co-sr-icon is-emerald" aria-hidden="true">
              <PhCheckCircle :size="14" weight="duotone" />
            </span>
            <div class="co-notif-scenario-copy">
              <span class="co-notif-scenario-title">{{ t("notifications.eventTaskTitle") }}</span>
              <span class="co-notif-scenario-desc">{{ t("notifications.eventTaskDesc") }}</span>
            </div>
            <ASwitch
              size="small"
              :checked="props.settings.notify_task"
              :loading="savingPrefKey === 'notify_task'"
              :disabled="props.disabled || (!!savingPrefKey && savingPrefKey !== 'notify_task')"
              :aria-label="t('notifications.eventTaskTitle')"
              @update:checked="(v) => toggleTopLevelPref('notify_task', v)"
            />
          </div>

          <div
            class="co-notif-scenario-pill"
            :class="{ 'is-active': props.settings.notify_alert }"
            data-testid="notif-scenario-alert"
            @click="onScenarioCardClick('notify_alert', $event)"
          >
            <span class="co-sr-icon is-amber" aria-hidden="true">
              <PhWarningCircle :size="14" weight="duotone" />
            </span>
            <div class="co-notif-scenario-copy">
              <span class="co-notif-scenario-title">{{ t("notifications.eventAlertTitle") }}</span>
              <span class="co-notif-scenario-desc">{{ t("notifications.eventAlertDesc") }}</span>
            </div>
            <ASwitch
              size="small"
              :checked="props.settings.notify_alert"
              :loading="savingPrefKey === 'notify_alert'"
              :disabled="props.disabled || (!!savingPrefKey && savingPrefKey !== 'notify_alert')"
              :aria-label="t('notifications.eventAlertTitle')"
              @update:checked="(v) => toggleTopLevelPref('notify_alert', v)"
            />
          </div>
        </div>

        <!-- 2 Delivery Timing Toggles -->
        <div class="co-sr-subpanel co-notif-timing-box co-sk-reveal">
          <div class="co-sr-toggle-row">
            <div class="co-sr-toggle-copy">
              <span class="co-sr-toggle-title">
                {{ t("notifications.systemUnfocusedTitle") }}
              </span>
              <span class="co-sr-toggle-desc">
                {{ t("notifications.systemUnfocusedDesc") }}
              </span>
            </div>
            <ASwitch
              :checked="props.settings.system_only_when_unfocused"
              :loading="savingPrefKey === 'system_only_when_unfocused'"
              :disabled="
                props.disabled ||
                (!!savingPrefKey && savingPrefKey !== 'system_only_when_unfocused')
              "
              :aria-label="t('notifications.systemUnfocusedTitle')"
              data-testid="notif-system-unfocused-switch"
              @update:checked="(v) => toggleTopLevelPref('system_only_when_unfocused', v)"
            />
          </div>
          <div class="co-sr-toggle-row">
            <div class="co-sr-toggle-copy">
              <span class="co-sr-toggle-title">
                {{ t("notifications.remoteUnfocusedTitle") }}
              </span>
              <span class="co-sr-toggle-desc">
                {{ t("notifications.remoteUnfocusedDesc") }}
              </span>
            </div>
            <ASwitch
              :checked="props.settings.remote_only_when_unfocused"
              :loading="savingPrefKey === 'remote_only_when_unfocused'"
              :disabled="
                props.disabled ||
                (!!savingPrefKey && savingPrefKey !== 'remote_only_when_unfocused')
              "
              :aria-label="t('notifications.remoteUnfocusedTitle')"
              data-testid="notif-remote-unfocused-switch"
              @update:checked="(v) => toggleTopLevelPref('remote_only_when_unfocused', v)"
            />
          </div>
        </div>
      </template>
    </div>

    <!-- Row 2: System Notifications -->
    <div class="co-settings-row" data-testid="settings-notifications-system-row">
      <div class="co-sr-main">
        <div class="co-sr-info">
          <div class="co-sr-title-line">
            <span class="co-sr-title-group">
              <span class="co-sr-icon is-blue" aria-hidden="true">
                <PhBellRinging :size="14" weight="duotone" />
              </span>
              <h3 class="co-sr-title">{{ t("notifications.systemTitle") }}</h3>
            </span>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="56"
              :height="20"
              variant="tag"
              tone="subtle"
              :delay="100"
              inline
            />
            <template v-else>
              <ATag v-if="!props.settings.system.supported">
                {{ t("notifications.statusUnsupported") }}
              </ATag>
              <ATag v-else-if="props.settings.system.enabled" color="success">
                {{ t("notifications.statusEnabled") }}
              </ATag>
              <ATag v-else>
                {{ t("notifications.statusDisabled") }}
              </ATag>
            </template>
          </div>
          <p class="co-sr-desc">{{ t("notifications.systemDesc") }}</p>
          <p v-if="isWebConsoleRestricted" class="co-sr-desc co-notif-web-note">
            {{ t("notifications.systemWebHostNote") }}
          </p>
        </div>
        <div class="co-sr-control">
          <ColabOneSkeleton
            v-if="props.loading"
            :width="116"
            :height="28"
            variant="button"
            tone="subtle"
          />
          <template v-else>
            <AButton
              size="small"
              :loading="testingChannel === 'system'"
              :disabled="
                props.disabled ||
                !props.settings.system.supported ||
                isChannelBusy('system') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel) ||
                (Boolean(testingChannel) && testingChannel !== 'system')
              "
              data-testid="notif-system-test-btn"
              @click="testChannel('system')"
            >
              {{ t("notifications.testBtn") }}
            </AButton>
            <ASwitch
              :checked="props.settings.system.supported && props.settings.system.enabled"
              :loading="savingChannel === 'system'"
              :disabled="
                props.disabled ||
                !props.settings.system.supported ||
                isChannelBusy('system') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              :aria-label="t('notifications.systemSwitchAria')"
              data-testid="notif-system-switch"
              @update:checked="onSystemEnabledChange"
            />
          </template>
        </div>
      </div>
    </div>

    <!-- Row 3: Telegram Bot -->
    <div class="co-settings-row" data-testid="settings-notifications-telegram-row">
      <div class="co-sr-main">
        <div class="co-sr-info">
          <div class="co-sr-title-line">
            <span class="co-sr-title-group">
              <span class="co-sr-icon is-blue" aria-hidden="true">
                <PhPaperPlaneTilt :size="14" weight="duotone" />
              </span>
              <h3 class="co-sr-title">{{ t("notifications.telegramTitle") }}</h3>
            </span>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="56"
              :height="20"
              variant="tag"
              tone="subtle"
              :delay="100"
              inline
            />
            <template v-else>
              <ATag
                v-if="props.settings.telegram.configured && props.settings.telegram.enabled"
                color="success"
              >
                {{ t("notifications.statusEnabled") }}
              </ATag>
              <ATag v-else-if="props.settings.telegram.configured">
                {{ t("notifications.statusConfigured") }}
              </ATag>
              <ATag v-else>{{ t("common.optional") }}</ATag>
              <ATag
                v-if="!telegramExpanded && telegramDirty"
                color="warning"
                :bordered="false"
                data-testid="notif-telegram-unsaved-tag"
              >
                {{ t("notifications.unsavedDraftTag") }}
              </ATag>
            </template>
          </div>
          <p class="co-sr-desc">{{ t("notifications.telegramDesc") }}</p>
        </div>
        <div class="co-sr-control">
          <ColabOneSkeleton
            v-if="props.loading"
            :width="102"
            :height="28"
            variant="button"
            tone="subtle"
          />
          <template v-else>
            <ASwitch
              :checked="props.settings.telegram.enabled"
              :loading="savingChannel === 'telegram'"
              :disabled="
                props.disabled ||
                isChannelBusy('telegram') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              :aria-label="t('notifications.telegramSwitchAria')"
              data-testid="notif-telegram-switch"
              @update:checked="(v) => onRemoteChannelSwitch('telegram', v)"
            />
            <AButton
              size="small"
              :disabled="props.disabled"
              data-testid="notif-telegram-expand-btn"
              @click="toggleChannelExpanded('telegram')"
            >
              {{
                telegramExpanded
                  ? t("common.collapse")
                  : props.settings.telegram.configured
                    ? t("notifications.editBtn")
                    : t("notifications.configureBtn")
              }}
            </AButton>
          </template>
        </div>
      </div>

      <div
        v-if="!props.loading && telegramExpanded"
        class="co-sr-subpanel co-notif-subpanel co-sk-reveal"
        data-testid="notif-telegram-panel"
      >
        <AAlert
          v-if="isWebConsoleRestricted"
          type="info"
          show-icon
          :message="t('notifications.webDesktopOnlyEditNotice')"
          class="co-notif-web-alert"
          data-testid="notif-telegram-web-notice"
        />

        <div class="co-notif-grid">
          <div class="co-notif-field">
            <label for="notif-tg-token" class="co-notif-label">
              {{ t("notifications.telegramBotTokenLabel") }}
            </label>
            <ColabOneSecretInput
              ref="telegramBotTokenRef"
              id="notif-tg-token"
              v-model:value="telegramDraft.bot_token"
              :has-saved="props.settings.telegram.has_bot_token"
              :disabled="telegramInputsDisabled"
              autocomplete="new-password"
              :aria-label="t('notifications.telegramBotTokenLabel')"
              :placeholder="t('notifications.telegramBotTokenPlaceholder')"
              :saved-placeholder="telegramBotTokenSavedPlaceholder"
              :load-saved="props.canRevealSecrets ? loadTelegramBotToken : undefined"
              @update:value="telegramTouched = true"
              @press-enter="saveTelegram"
              @error="emit('error', $event)"
            />
          </div>

          <div class="co-notif-field">
            <label for="notif-tg-chat-id" class="co-notif-label">
              {{ t("notifications.telegramChatIdLabel") }}
            </label>
            <AInput
              id="notif-tg-chat-id"
              v-model:value="telegramDraft.chat_id"
              :disabled="telegramInputsDisabled"
              :placeholder="t('notifications.telegramChatIdPlaceholder')"
              autocomplete="off"
              @update:value="telegramTouched = true"
              @press-enter="saveTelegram"
            />
            <span class="co-notif-hint">{{ t("notifications.telegramChatIdHint") }}</span>
          </div>

          <div class="co-notif-field">
            <label for="notif-tg-api-base" class="co-notif-label">
              {{ t("notifications.telegramApiBaseLabel") }}
            </label>
            <AInput
              id="notif-tg-api-base"
              v-model:value="telegramDraft.api_base_url"
              :disabled="telegramInputsDisabled"
              :placeholder="t('notifications.telegramApiBasePlaceholder')"
              autocomplete="off"
              @update:value="telegramTouched = true"
              @press-enter="saveTelegram"
            />
          </div>

          <div class="co-notif-field">
            <div class="co-notif-label-row">
              <label for="notif-tg-proxy" class="co-notif-label">
                {{ t("notifications.telegramProxyLabel") }}
              </label>
              <button
                v-if="props.settings.telegram.has_proxy && !telegramDraft.proxy_url.trim()"
                type="button"
                class="co-notif-inline-link"
                :class="{ 'is-danger': !telegramDraft.clear_proxy }"
                :disabled="telegramInputsDisabled"
                @click="toggleClearTelegramProxy"
              >
                {{
                  telegramDraft.clear_proxy
                    ? t("notifications.telegramUndoClearProxyBtn")
                    : t("notifications.telegramClearProxyBtn")
                }}
              </button>
            </div>
            <ColabOneSecretInput
              ref="telegramProxyRef"
              id="notif-tg-proxy"
              v-model:value="telegramDraft.proxy_url"
              :has-saved="props.settings.telegram.has_proxy && !telegramDraft.clear_proxy"
              :disabled="telegramInputsDisabled"
              autocomplete="new-password"
              :aria-label="t('notifications.telegramProxyLabel')"
              :placeholder="t('notifications.telegramProxyPlaceholder')"
              :saved-placeholder="telegramProxySavedPlaceholder"
              :load-saved="props.canRevealSecrets ? loadTelegramProxy : undefined"
              @update:value="
                (v) => {
                  telegramTouched = true;
                  if (v.trim()) telegramDraft.clear_proxy = false;
                }
              "
              @press-enter="saveTelegram"
              @error="emit('error', $event)"
            />
            <span v-if="telegramDraft.clear_proxy" class="co-notif-hint is-warn">
              {{ t("notifications.telegramProxyWillClearHint") }}
            </span>
          </div>
        </div>

        <div class="co-notif-inline-toggle">
          <div class="co-sr-toggle-copy">
            <span class="co-sr-toggle-title">{{ t("notifications.telegramPreviewTitle") }}</span>
            <span class="co-sr-toggle-desc">{{ t("notifications.telegramPreviewDesc") }}</span>
          </div>
          <ASwitch
            size="small"
            :checked="telegramDraft.send_preview_image"
            :disabled="telegramInputsDisabled"
            :aria-label="t('notifications.telegramPreviewTitle')"
            @update:checked="
              (v) => {
                telegramDraft.send_preview_image = Boolean(v);
                telegramTouched = true;
              }
            "
          />
        </div>

        <p
          v-if="!isWebConsoleRestricted && telegramTouched && telegramValidationHint"
          class="co-notif-validation-hint"
          role="status"
          data-testid="notif-telegram-validation-hint"
        >
          {{ telegramValidationHint }}
        </p>

        <ASpace :size="8" wrap class="co-notif-actions">
          <ColabOneTooltip
            :title="telegramSaveTooltip"
            :disabled="!telegramSaveTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                type="primary"
                size="small"
                :loading="savingChannel === 'telegram'"
                :disabled="telegramInputsDisabled || !canSaveTelegram"
                data-testid="notif-telegram-save-btn"
                @click="saveTelegram"
              >
                {{ t("notifications.saveBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <ColabOneTooltip
            :title="telegramTestTooltip"
            :disabled="!telegramTestTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                size="small"
                :loading="testingChannel === 'telegram'"
                :disabled="
                  props.disabled ||
                  !props.settings.telegram.configured ||
                  telegramDirty ||
                  isChannelBusy('telegram') ||
                  Boolean(savingChannel) ||
                  Boolean(clearingChannel) ||
                  (Boolean(testingChannel) && testingChannel !== 'telegram')
                "
                data-testid="notif-telegram-test-btn"
                @click="testChannel('telegram')"
              >
                {{ t("notifications.testBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <APopconfirm
            v-if="props.settings.telegram.configured"
            :title="
              t('notifications.clearConfirmTitle', {
                channel: channelDisplayName('telegram'),
              })
            "
            :description="t('notifications.clearConfirmDesc')"
            :ok-text="t('notifications.clearBtn')"
            :cancel-text="t('common.cancel')"
            :ok-button-props="{ danger: true }"
            :disabled="
              props.disabled ||
              isChannelBusy('telegram') ||
              Boolean(savingChannel) ||
              Boolean(clearingChannel)
            "
            @confirm="clearRemoteChannel('telegram')"
          >
            <AButton
              size="small"
              type="link"
              danger
              :loading="clearingChannel === 'telegram'"
              :disabled="
                props.disabled ||
                isChannelBusy('telegram') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              data-testid="notif-telegram-clear-btn"
            >
              {{ t("notifications.clearBtn") }}
            </AButton>
          </APopconfirm>
        </ASpace>
      </div>
    </div>

    <!-- Row 4: Bark Push -->
    <div class="co-settings-row" data-testid="settings-notifications-bark-row">
      <div class="co-sr-main">
        <div class="co-sr-info">
          <div class="co-sr-title-line">
            <span class="co-sr-title-group">
              <span class="co-sr-icon is-amber" aria-hidden="true">
                <PhDeviceMobile :size="14" weight="duotone" />
              </span>
              <h3 class="co-sr-title">{{ t("notifications.barkTitle") }}</h3>
            </span>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="56"
              :height="20"
              variant="tag"
              tone="subtle"
              :delay="100"
              inline
            />
            <template v-else>
              <ATag
                v-if="props.settings.bark.configured && props.settings.bark.enabled"
                color="success"
              >
                {{ t("notifications.statusEnabled") }}
              </ATag>
              <ATag v-else-if="props.settings.bark.configured">
                {{ t("notifications.statusConfigured") }}
              </ATag>
              <ATag v-else>{{ t("common.optional") }}</ATag>
              <ATag
                v-if="!barkExpanded && barkDirty"
                color="warning"
                :bordered="false"
                data-testid="notif-bark-unsaved-tag"
              >
                {{ t("notifications.unsavedDraftTag") }}
              </ATag>
            </template>
          </div>
          <p class="co-sr-desc">{{ t("notifications.barkDesc") }}</p>
        </div>
        <div class="co-sr-control">
          <ColabOneSkeleton
            v-if="props.loading"
            :width="102"
            :height="28"
            variant="button"
            tone="subtle"
          />
          <template v-else>
            <ASwitch
              :checked="props.settings.bark.enabled"
              :loading="savingChannel === 'bark'"
              :disabled="
                props.disabled ||
                isChannelBusy('bark') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              :aria-label="t('notifications.barkSwitchAria')"
              data-testid="notif-bark-switch"
              @update:checked="(v) => onRemoteChannelSwitch('bark', v)"
            />
            <AButton
              size="small"
              :disabled="props.disabled"
              data-testid="notif-bark-expand-btn"
              @click="toggleChannelExpanded('bark')"
            >
              {{
                barkExpanded
                  ? t("common.collapse")
                  : props.settings.bark.configured
                    ? t("notifications.editBtn")
                    : t("notifications.configureBtn")
              }}
            </AButton>
          </template>
        </div>
      </div>

      <div
        v-if="!props.loading && barkExpanded"
        class="co-sr-subpanel co-notif-subpanel co-sk-reveal"
        data-testid="notif-bark-panel"
      >
        <AAlert
          v-if="isWebConsoleRestricted"
          type="info"
          show-icon
          :message="t('notifications.webDesktopOnlyEditNotice')"
          class="co-notif-web-alert"
          data-testid="notif-bark-web-notice"
        />

        <div class="co-notif-grid">
          <div class="co-notif-field">
            <label for="notif-bark-server" class="co-notif-label">
              {{ t("notifications.barkServerLabel") }}
            </label>
            <AInput
              ref="barkServerRef"
              id="notif-bark-server"
              v-model:value="barkDraft.server_url"
              :disabled="barkInputsDisabled"
              :placeholder="t('notifications.barkServerPlaceholder')"
              autocomplete="off"
              @update:value="barkTouched = true"
              @paste="onBarkServerPaste"
              @blur="tryAutoSplitBarkEndpoint"
              @press-enter="saveBark"
            />
            <span class="co-notif-hint">{{ t("notifications.barkServerHint") }}</span>
          </div>

          <div class="co-notif-field">
            <label for="notif-bark-key" class="co-notif-label">
              {{ t("notifications.barkKeyLabel") }}
            </label>
            <ColabOneSecretInput
              ref="barkDeviceKeyRef"
              id="notif-bark-key"
              v-model:value="barkDraft.device_key"
              :has-saved="props.settings.bark.has_device_key"
              :disabled="barkInputsDisabled"
              autocomplete="new-password"
              :aria-label="t('notifications.barkKeyLabel')"
              :placeholder="t('notifications.barkKeyPlaceholder')"
              :saved-placeholder="barkKeySavedPlaceholder"
              :load-saved="props.canRevealSecrets ? loadBarkDeviceKey : undefined"
              @paste.capture="onBarkKeyPaste"
              @update:value="barkTouched = true"
              @press-enter="saveBark"
              @error="emit('error', $event)"
            />
          </div>
        </div>

        <div class="co-notif-grid-3">
          <div class="co-notif-field">
            <label for="notif-bark-group" class="co-notif-label">
              {{ t("notifications.barkGroupLabel") }}
            </label>
            <AInput
              id="notif-bark-group"
              v-model:value="barkDraft.group"
              :disabled="barkInputsDisabled"
              :placeholder="COLABONE_DEFAULT_BARK_GROUP"
              autocomplete="off"
              @update:value="barkTouched = true"
              @press-enter="saveBark"
            />
          </div>

          <div class="co-notif-field">
            <label for="notif-bark-sound" class="co-notif-label">
              {{ t("notifications.barkSoundLabel") }}
            </label>
            <ASelect
              id="notif-bark-sound"
              :value="barkDraft.sound"
              :disabled="barkInputsDisabled"
              :aria-label="t('notifications.barkSoundLabel')"
              @change="
                (v) => {
                  barkDraft.sound = String(v ?? '');
                  barkTouched = true;
                }
              "
            >
              <ASelectOption
                v-for="opt in barkSoundOptions"
                :key="opt.value || 'default'"
                :value="opt.value"
              >
                {{ opt.label }}
              </ASelectOption>
            </ASelect>
          </div>

          <div class="co-notif-field">
            <label for="notif-bark-level" class="co-notif-label">
              {{ t("notifications.barkLevelLabel") }}
            </label>
            <ASelect
              id="notif-bark-level"
              :value="barkDraft.level"
              :disabled="barkInputsDisabled"
              :aria-label="t('notifications.barkLevelLabel')"
              @change="
                (v) => {
                  barkDraft.level = String(v ?? '');
                  barkTouched = true;
                }
              "
            >
              <ASelectOption
                v-for="opt in barkLevelOptions"
                :key="opt.value || 'auto'"
                :value="opt.value"
              >
                {{ opt.label }}
              </ASelectOption>
            </ASelect>
          </div>
        </div>

        <p
          v-if="!isWebConsoleRestricted && barkTouched && barkValidationHint"
          class="co-notif-validation-hint"
          role="status"
          data-testid="notif-bark-validation-hint"
        >
          {{ barkValidationHint }}
        </p>

        <ASpace :size="8" wrap class="co-notif-actions">
          <ColabOneTooltip
            :title="barkSaveTooltip"
            :disabled="!barkSaveTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                type="primary"
                size="small"
                :loading="savingChannel === 'bark'"
                :disabled="barkInputsDisabled || !canSaveBark"
                data-testid="notif-bark-save-btn"
                @click="saveBark"
              >
                {{ t("notifications.saveBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <ColabOneTooltip
            :title="barkTestTooltip"
            :disabled="!barkTestTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                size="small"
                :loading="testingChannel === 'bark'"
                :disabled="
                  props.disabled ||
                  !props.settings.bark.configured ||
                  barkDirty ||
                  isChannelBusy('bark') ||
                  Boolean(savingChannel) ||
                  Boolean(clearingChannel) ||
                  (Boolean(testingChannel) && testingChannel !== 'bark')
                "
                data-testid="notif-bark-test-btn"
                @click="testChannel('bark')"
              >
                {{ t("notifications.testBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <APopconfirm
            v-if="props.settings.bark.configured"
            :title="
              t('notifications.clearConfirmTitle', {
                channel: channelDisplayName('bark'),
              })
            "
            :description="t('notifications.clearConfirmDesc')"
            :ok-text="t('notifications.clearBtn')"
            :cancel-text="t('common.cancel')"
            :ok-button-props="{ danger: true }"
            :disabled="
              props.disabled ||
              isChannelBusy('bark') ||
              Boolean(savingChannel) ||
              Boolean(clearingChannel)
            "
            @confirm="clearRemoteChannel('bark')"
          >
            <AButton
              size="small"
              type="link"
              danger
              :loading="clearingChannel === 'bark'"
              :disabled="
                props.disabled ||
                isChannelBusy('bark') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              data-testid="notif-bark-clear-btn"
            >
              {{ t("notifications.clearBtn") }}
            </AButton>
          </APopconfirm>
        </ASpace>
      </div>
    </div>

    <!-- Row 5: Email Notifications (SMTP) -->
    <div class="co-settings-row" data-testid="settings-notifications-email-row">
      <div class="co-sr-main">
        <div class="co-sr-info">
          <div class="co-sr-title-line">
            <span class="co-sr-title-group">
              <span class="co-sr-icon is-violet" aria-hidden="true">
                <PhEnvelopeSimple :size="14" weight="duotone" />
              </span>
              <h3 class="co-sr-title">{{ t("notifications.emailTitle") }}</h3>
            </span>
            <ColabOneSkeleton
              v-if="props.loading"
              :width="56"
              :height="20"
              variant="tag"
              tone="subtle"
              :delay="100"
              inline
            />
            <template v-else>
              <ATag
                v-if="props.settings.email.configured && props.settings.email.enabled"
                color="success"
              >
                {{ t("notifications.statusEnabled") }}
              </ATag>
              <ATag v-else-if="props.settings.email.configured">
                {{ t("notifications.statusConfigured") }}
              </ATag>
              <ATag v-else>{{ t("common.optional") }}</ATag>
              <ATag
                v-if="!emailExpanded && emailDirty"
                color="warning"
                :bordered="false"
                data-testid="notif-email-unsaved-tag"
              >
                {{ t("notifications.unsavedDraftTag") }}
              </ATag>
            </template>
          </div>
          <p class="co-sr-desc">{{ t("notifications.emailDesc") }}</p>
        </div>
        <div class="co-sr-control">
          <ColabOneSkeleton
            v-if="props.loading"
            :width="102"
            :height="28"
            variant="button"
            tone="subtle"
          />
          <template v-else>
            <ASwitch
              :checked="props.settings.email.enabled"
              :loading="savingChannel === 'email'"
              :disabled="
                props.disabled ||
                isChannelBusy('email') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              :aria-label="t('notifications.emailSwitchAria')"
              data-testid="notif-email-switch"
              @update:checked="(v) => onRemoteChannelSwitch('email', v)"
            />
            <AButton
              size="small"
              :disabled="props.disabled"
              data-testid="notif-email-expand-btn"
              @click="toggleChannelExpanded('email')"
            >
              {{
                emailExpanded
                  ? t("common.collapse")
                  : props.settings.email.configured
                    ? t("notifications.editBtn")
                    : t("notifications.configureBtn")
              }}
            </AButton>
          </template>
        </div>
      </div>

      <div
        v-if="!props.loading && emailExpanded"
        class="co-sr-subpanel co-notif-subpanel co-sk-reveal"
        data-testid="notif-email-panel"
      >
        <AAlert
          v-if="isWebConsoleRestricted"
          type="info"
          show-icon
          :message="t('notifications.webDesktopOnlyEditNotice')"
          class="co-notif-web-alert"
          data-testid="notif-email-web-notice"
        />

        <!-- Quick SMTP Presets -->
        <div class="co-notif-presets">
          <span class="co-notif-presets-label">{{ t("notifications.emailPresetsLabel") }}</span>
          <div class="co-notif-preset-pills">
            <button
              v-for="preset in COLABONE_SMTP_PRESETS"
              :key="preset.id"
              type="button"
              class="co-notif-preset-pill"
              :class="{ 'is-active': activeSmtpPresetId === preset.id }"
              :disabled="emailInputsDisabled"
              :data-testid="`notif-email-preset-${preset.id}`"
              @click="applySmtpPreset(preset)"
            >
              {{ t(preset.labelKey) }}
            </button>
          </div>
        </div>

        <div class="co-notif-grid">
          <div class="co-notif-field">
            <label for="notif-email-host" class="co-notif-label">
              {{ t("notifications.emailHostLabel") }}
            </label>
            <AInput
              ref="emailHostRef"
              id="notif-email-host"
              v-model:value="emailDraft.smtp_host"
              :disabled="emailInputsDisabled"
              :placeholder="t('notifications.emailHostPlaceholder')"
              autocomplete="off"
              @update:value="emailTouched = true"
              @press-enter="saveEmail"
            />
          </div>

          <div class="co-notif-port-sec-row">
            <div class="co-notif-field co-notif-port-field">
              <label for="notif-email-port" class="co-notif-label">
                {{ t("notifications.emailPortLabel") }}
              </label>
              <AInputNumber
                id="notif-email-port"
                :value="emailDraft.smtp_port ?? undefined"
                :min="COLABONE_MIN_SMTP_PORT"
                :max="COLABONE_MAX_SMTP_PORT"
                :step="1"
                :precision="0"
                :disabled="emailInputsDisabled"
                class="co-notif-port-input"
                @update:value="onEmailPortUpdate"
                @press-enter="saveEmail"
              />
            </div>

            <div class="co-notif-field co-notif-sec-field">
              <label for="notif-email-security" class="co-notif-label">
                {{ t("notifications.emailSecurityLabel") }}
              </label>
              <ASelect
                id="notif-email-security"
                :value="emailDraft.security"
                :disabled="emailInputsDisabled"
                :aria-label="t('notifications.emailSecurityLabel')"
                @change="onEmailSecurityChange"
              >
                <ASelectOption value="tls">
                  {{ t("notifications.emailSecurityTls") }}
                </ASelectOption>
                <ASelectOption value="starttls">
                  {{ t("notifications.emailSecurityStarttls") }}
                </ASelectOption>
                <ASelectOption value="none">
                  {{ t("notifications.emailSecurityNone") }}
                </ASelectOption>
              </ASelect>
            </div>
          </div>

          <div class="co-notif-field">
            <label for="notif-email-user" class="co-notif-label">
              {{ t("notifications.emailUsernameLabel") }}
            </label>
            <AInput
              id="notif-email-user"
              v-model:value="emailDraft.username"
              :disabled="emailInputsDisabled"
              :placeholder="t('notifications.emailUsernamePlaceholder')"
              autocomplete="off"
              @update:value="emailTouched = true"
              @press-enter="saveEmail"
            />
          </div>

          <div class="co-notif-field">
            <div class="co-notif-label-row">
              <label for="notif-email-pass" class="co-notif-label">
                {{ t("notifications.emailPasswordLabel") }}
              </label>
              <button
                v-if="props.settings.email.has_password && emailDraft.password.length === 0"
                type="button"
                class="co-notif-inline-link"
                :class="{ 'is-danger': !emailDraft.clear_password }"
                :disabled="emailInputsDisabled"
                data-testid="notif-email-clear-password-btn"
                @click="toggleClearEmailPassword"
              >
                {{
                  emailDraft.clear_password
                    ? t("notifications.emailUndoClearPasswordBtn")
                    : t("notifications.emailClearPasswordBtn")
                }}
              </button>
            </div>
            <ColabOneSecretInput
              ref="emailPasswordRef"
              id="notif-email-pass"
              v-model:value="emailDraft.password"
              :has-saved="props.settings.email.has_password && !emailDraft.clear_password"
              :disabled="emailInputsDisabled"
              autocomplete="new-password"
              :aria-label="t('notifications.emailPasswordLabel')"
              :placeholder="t('notifications.emailPasswordPlaceholder')"
              :saved-placeholder="t('notifications.emailPasswordSavedPlaceholder')"
              :load-saved="props.canRevealSecrets ? loadEmailPassword : undefined"
              @update:value="
                (v) => {
                  emailTouched = true;
                  if (v.length > 0) emailDraft.clear_password = false;
                }
              "
              @press-enter="saveEmail"
              @error="emit('error', $event)"
            />
            <span v-if="emailDraft.clear_password" class="co-notif-hint is-warn">
              {{ t("notifications.emailPasswordWillClearHint") }}
            </span>
          </div>

          <div class="co-notif-field">
            <label for="notif-email-from-addr" class="co-notif-label">
              {{ t("notifications.emailFromAddressLabel") }}
            </label>
            <AInput
              id="notif-email-from-addr"
              v-model:value="emailDraft.from_address"
              :disabled="emailInputsDisabled"
              :placeholder="t('notifications.emailFromAddressPlaceholder')"
              autocomplete="off"
              @update:value="emailTouched = true"
              @press-enter="saveEmail"
            />
          </div>

          <div class="co-notif-field">
            <label for="notif-email-from-name" class="co-notif-label">
              {{ t("notifications.emailFromNameLabel") }}
            </label>
            <AInput
              id="notif-email-from-name"
              v-model:value="emailDraft.from_name"
              :disabled="emailInputsDisabled"
              :placeholder="COLABONE_DEFAULT_EMAIL_FROM_NAME"
              autocomplete="off"
              @update:value="emailTouched = true"
              @press-enter="saveEmail"
            />
          </div>

          <div class="co-notif-field co-notif-field-full">
            <label for="notif-email-to" class="co-notif-label">
              {{ t("notifications.emailToLabel") }}
            </label>
            <AInput
              id="notif-email-to"
              v-model:value="emailDraft.to_addresses"
              :disabled="emailInputsDisabled"
              :placeholder="t('notifications.emailToPlaceholder')"
              autocomplete="off"
              @update:value="emailTouched = true"
              @press-enter="saveEmail"
            />
            <span class="co-notif-hint">{{ t("notifications.emailToHint") }}</span>
          </div>
        </div>

        <div class="co-notif-inline-toggle">
          <div class="co-sr-toggle-copy">
            <span class="co-sr-toggle-title">{{ t("notifications.emailEmbedPreviewTitle") }}</span>
            <span class="co-sr-toggle-desc">{{ t("notifications.emailEmbedPreviewDesc") }}</span>
          </div>
          <ASwitch
            size="small"
            :checked="emailDraft.embed_preview_image"
            :disabled="emailInputsDisabled"
            :aria-label="t('notifications.emailEmbedPreviewTitle')"
            @update:checked="
              (v) => {
                emailDraft.embed_preview_image = Boolean(v);
                emailTouched = true;
              }
            "
          />
        </div>

        <p
          v-if="!isWebConsoleRestricted && emailTouched && emailValidationHint"
          class="co-notif-validation-hint"
          role="status"
          data-testid="notif-email-validation-hint"
        >
          {{ emailValidationHint }}
        </p>

        <ASpace :size="8" wrap class="co-notif-actions">
          <ColabOneTooltip
            :title="emailSaveTooltip"
            :disabled="!emailSaveTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                type="primary"
                size="small"
                :loading="savingChannel === 'email'"
                :disabled="emailInputsDisabled || !canSaveEmail"
                data-testid="notif-email-save-btn"
                @click="saveEmail"
              >
                {{ t("notifications.saveBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <ColabOneTooltip
            :title="emailTestTooltip"
            :disabled="!emailTestTooltip"
            placement="top"
          >
            <span class="co-notif-tooltip-wrap">
              <AButton
                size="small"
                :loading="testingChannel === 'email'"
                :disabled="
                  props.disabled ||
                  !props.settings.email.configured ||
                  emailDirty ||
                  isChannelBusy('email') ||
                  Boolean(savingChannel) ||
                  Boolean(clearingChannel) ||
                  (Boolean(testingChannel) && testingChannel !== 'email')
                "
                data-testid="notif-email-test-btn"
                @click="testChannel('email')"
              >
                {{ t("notifications.testBtn") }}
              </AButton>
            </span>
          </ColabOneTooltip>
          <APopconfirm
            v-if="props.settings.email.configured"
            :title="
              t('notifications.clearConfirmTitle', {
                channel: channelDisplayName('email'),
              })
            "
            :description="t('notifications.clearConfirmDesc')"
            :ok-text="t('notifications.clearBtn')"
            :cancel-text="t('common.cancel')"
            :ok-button-props="{ danger: true }"
            :disabled="
              props.disabled ||
              isChannelBusy('email') ||
              Boolean(savingChannel) ||
              Boolean(clearingChannel)
            "
            @confirm="clearRemoteChannel('email')"
          >
            <AButton
              size="small"
              type="link"
              danger
              :loading="clearingChannel === 'email'"
              :disabled="
                props.disabled ||
                isChannelBusy('email') ||
                Boolean(savingChannel) ||
                Boolean(clearingChannel)
              "
              data-testid="notif-email-clear-btn"
            >
              {{ t("notifications.clearBtn") }}
            </AButton>
          </APopconfirm>
        </ASpace>
      </div>
    </div>
  </div>
</template>

<style scoped>
.co-notif-card {
  border-radius: 14px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
  overflow: hidden;
}

.co-settings-row {
  padding: 15px 18px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.co-settings-row + .co-settings-row {
  border-top: 1px solid var(--co-border-secondary);
}

.co-sr-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 18px;
}

.co-sr-main > .co-sr-info {
  flex: 1 1 240px;
}

.co-sr-info {
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

.co-sr-title-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
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

.co-sr-icon.is-emerald {
  background: var(--co-success-bg);
  color: var(--co-success-text);
}

.co-sr-icon.is-amber {
  background: var(--co-warning-bg);
  color: var(--co-warning-text);
}

.co-sr-icon.is-violet {
  background: rgba(139, 92, 246, 0.12);
  color: #7c3aed;
}

:global(:root[data-theme="dark"]) .co-sr-icon.is-violet {
  background: rgba(139, 92, 246, 0.18);
  color: #a78bfa;
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

.co-notif-skeleton-grid,
.co-notif-scenario-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.co-notif-scenario-pill {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--co-subtle);
  border: 1px solid var(--co-border-secondary);
  cursor: pointer;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease,
    box-shadow 0.16s ease;
}

.co-notif-scenario-pill:hover {
  border-color: var(--co-border);
  background: var(--co-soft-hover);
}

.co-notif-scenario-pill.is-active {
  border-color: rgba(59, 130, 246, 0.28);
  background: var(--co-surface);
  box-shadow: var(--co-card-shadow);
}

.co-notif-scenario-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-notif-scenario-title {
  font-size: 13px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--co-text);
}

.co-notif-scenario-desc {
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--co-muted);
}

.co-notif-timing-box {
  padding: 4px 14px;
  gap: 0;
}

.co-sr-toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 0;
}

.co-sr-toggle-row + .co-sr-toggle-row {
  border-top: 1px solid var(--co-border-secondary);
}

.co-sr-toggle-copy {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.co-sr-toggle-title {
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--co-text);
}

.co-sr-toggle-desc {
  font-size: 12px;
  line-height: 1.5;
  color: var(--co-muted);
}

.co-notif-subpanel {
  gap: 12px;
}

.co-notif-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  align-items: start;
}

.co-notif-grid-3 {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  align-items: start;
}

.co-notif-port-sec-row {
  display: grid;
  grid-template-columns: 104px minmax(0, 1fr);
  gap: 10px;
  align-items: start;
}

.co-notif-field {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.co-notif-field-full {
  grid-column: 1 / -1;
}

.co-notif-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.co-notif-label {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--co-text);
}

.co-notif-hint {
  font-size: 11.5px;
  line-height: 1.45;
  color: var(--co-muted);
}

.co-notif-hint.is-warn {
  color: var(--co-warning-text);
}

.co-notif-validation-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--co-warning-text);
}

.co-notif-web-note {
  margin-top: 2px;
  font-size: 12px;
  color: var(--co-secondary-text);
}

.co-notif-web-alert {
  border-radius: 8px;
}

.co-notif-inline-link {
  border: 0;
  padding: 0;
  border-radius: 4px;
  background: transparent;
  font-size: 12px;
  color: var(--co-accent);
  cursor: pointer;
}

.co-notif-inline-link.is-danger {
  color: var(--co-danger-text);
}

.co-notif-inline-link:hover:not(:disabled) {
  text-decoration: underline;
}

.co-notif-inline-link:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-notif-inline-link:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.co-notif-port-input {
  width: 100%;
}

.co-notif-port-input :deep(.ant-input-number-input) {
  font-variant-numeric: tabular-nums;
}

.co-notif-presets {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--co-border-secondary);
}

.co-notif-presets-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--co-muted);
}

.co-notif-preset-pills {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.co-notif-preset-pill {
  padding: 4px 10px;
  border-radius: 9999px;
  border: 1px solid var(--co-border);
  background: var(--co-surface);
  color: var(--co-secondary-text);
  font-size: 12px;
  font-weight: 500;
  line-height: 1.3;
  cursor: pointer;
  transition:
    color 0.15s ease,
    border-color 0.15s ease,
    background-color 0.15s ease;
}

.co-notif-preset-pill:hover:not(:disabled) {
  color: var(--co-text);
  border-color: var(--co-accent);
}

.co-notif-preset-pill:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 1px;
}

.co-notif-preset-pill.is-active {
  background: var(--co-selected);
  border-color: var(--co-accent);
  color: var(--co-accent);
  font-weight: 600;
}

.co-notif-preset-pill:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.co-notif-inline-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding-top: 10px;
  border-top: 1px solid var(--co-border-secondary);
}

.co-notif-actions {
  padding-top: 2px;
}

.co-notif-tooltip-wrap {
  display: inline-flex;
}

@media (max-width: 780px) {
  .co-notif-skeleton-grid,
  .co-notif-scenario-grid,
  .co-notif-grid,
  .co-notif-grid-3 {
    grid-template-columns: 1fr;
  }
}

@container co-settings (max-width: 720px) {
  .co-notif-skeleton-grid,
  .co-notif-scenario-grid,
  .co-notif-grid,
  .co-notif-grid-3 {
    grid-template-columns: 1fr;
  }
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
}

@container co-settings (max-width: 680px) {
  .co-sr-main {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .co-sr-control {
    justify-content: flex-start;
    flex-wrap: wrap;
  }
}

@container co-settings (max-width: 360px) {
  .co-notif-port-sec-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .co-settings-row {
    padding: 14px;
  }
}
</style>
