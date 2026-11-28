import {
  createColabOneDefaultNotificationSettings,
  type ColabOneNotificationBarkView,
  type ColabOneNotificationEmailView,
  type ColabOneNotificationSettings,
  type ColabOneNotificationTelegramView,
} from "./types";

export const COLABONE_DEFAULT_BARK_SERVER_URL = "https://api.day.app";
export const COLABONE_DEFAULT_BARK_GROUP = "ColabOne";
export const COLABONE_DEFAULT_EMAIL_FROM_NAME = "ColabOne";
export const COLABONE_DEFAULT_SMTP_PORT_TLS = 465;
export const COLABONE_DEFAULT_SMTP_PORT_STARTTLS = 587;
export const COLABONE_DEFAULT_SMTP_PORT_NONE = 25;
export const COLABONE_MIN_SMTP_PORT = 1;
export const COLABONE_MAX_SMTP_PORT = 65535;

export type ColabOneNotificationChannel =
  | "system"
  | "telegram"
  | "bark"
  | "email";

export type ColabOneNotificationSecretField =
  | "telegram_bot_token"
  | "telegram_proxy_url"
  | "bark_device_key"
  | "email_password";

export type ColabOneSmtpSecurity = "tls" | "starttls" | "none";

export interface ColabOneSmtpPreset {
  id: "qq" | "163" | "gmail" | "outlook" | "icloud";
  labelKey:
    | "notifications.emailPresetQQ"
    | "notifications.emailPreset163"
    | "notifications.emailPresetGmail"
    | "notifications.emailPresetOutlook"
    | "notifications.emailPresetICloud";
  host: string;
  port: number;
  security: ColabOneSmtpSecurity;
}

export const COLABONE_SMTP_PRESETS: readonly ColabOneSmtpPreset[] = [
  {
    id: "qq",
    labelKey: "notifications.emailPresetQQ",
    host: "smtp.qq.com",
    port: 465,
    security: "tls",
  },
  {
    id: "163",
    labelKey: "notifications.emailPreset163",
    host: "smtp.163.com",
    port: 465,
    security: "tls",
  },
  {
    id: "gmail",
    labelKey: "notifications.emailPresetGmail",
    host: "smtp.gmail.com",
    port: 465,
    security: "tls",
  },
  {
    id: "outlook",
    labelKey: "notifications.emailPresetOutlook",
    host: "smtp-mail.outlook.com",
    port: 587,
    security: "starttls",
  },
  {
    id: "icloud",
    labelKey: "notifications.emailPresetICloud",
    host: "smtp.mail.me.com",
    port: 587,
    security: "starttls",
  },
] as const;

export const COLABONE_BARK_SOUND_VALUES = [
  "",
  "bell",
  "glass",
  "minuet",
  "alarm",
  "telegraph",
  "anticipate",
] as const;

export const COLABONE_BARK_LEVEL_VALUES = [
  "",
  "active",
  "timeSensitive",
  "passive",
] as const;

export function normalizeColabOneSmtpSecurity(
  raw: unknown,
): ColabOneSmtpSecurity {
  const clean = String(raw ?? "")
    .trim()
    .toLowerCase();
  if (clean === "starttls" || clean === "none" || clean === "tls") {
    return clean;
  }
  return "tls";
}

export function colabOneDefaultPortForSmtpSecurity(
  security: ColabOneSmtpSecurity,
): number {
  switch (security) {
    case "starttls":
      return COLABONE_DEFAULT_SMTP_PORT_STARTTLS;
    case "none":
      return COLABONE_DEFAULT_SMTP_PORT_NONE;
    default:
      return COLABONE_DEFAULT_SMTP_PORT_TLS;
  }
}

export function isColabOneDefaultSmtpPort(port: number | null | undefined): boolean {
  return (
    port === COLABONE_DEFAULT_SMTP_PORT_TLS ||
    port === COLABONE_DEFAULT_SMTP_PORT_STARTTLS ||
    port === COLABONE_DEFAULT_SMTP_PORT_NONE
  );
}

export function isColabOneValidSmtpPort(port: unknown): port is number {
  return (
    typeof port === "number" &&
    Number.isInteger(port) &&
    port >= COLABONE_MIN_SMTP_PORT &&
    port <= COLABONE_MAX_SMTP_PORT
  );
}

export function normalizeColabOneNotificationSettings(
  raw?: Partial<ColabOneNotificationSettings> | null,
): ColabOneNotificationSettings {
  const defaults = createColabOneDefaultNotificationSettings();
  if (!raw || typeof raw !== "object") {
    return defaults;
  }
  const sys = raw.system;
  const tg = raw.telegram;
  const bark = raw.bark;
  const email = raw.email;
  const security = normalizeColabOneSmtpSecurity(email?.security);
  const rawPort = typeof email?.smtp_port === "number" ? email.smtp_port : 0;
  const smtpPort = isColabOneValidSmtpPort(rawPort)
    ? rawPort
    : colabOneDefaultPortForSmtpSecurity(security);

  return {
    notify_gpu:
      typeof raw.notify_gpu === "boolean"
        ? raw.notify_gpu
        : defaults.notify_gpu,
    notify_task:
      typeof raw.notify_task === "boolean"
        ? raw.notify_task
        : defaults.notify_task,
    notify_alert:
      typeof raw.notify_alert === "boolean"
        ? raw.notify_alert
        : defaults.notify_alert,
    system_only_when_unfocused:
      typeof raw.system_only_when_unfocused === "boolean"
        ? raw.system_only_when_unfocused
        : defaults.system_only_when_unfocused,
    remote_only_when_unfocused:
      typeof raw.remote_only_when_unfocused === "boolean"
        ? raw.remote_only_when_unfocused
        : defaults.remote_only_when_unfocused,
    system: {
      enabled:
        typeof sys?.enabled === "boolean"
          ? sys.enabled
          : defaults.system.enabled,
      supported:
        typeof sys?.supported === "boolean"
          ? sys.supported
          : defaults.system.supported,
    },
    telegram: {
      enabled: Boolean(tg?.enabled),
      configured: Boolean(tg?.configured),
      has_bot_token: Boolean(tg?.has_bot_token),
      masked_bot_token:
        typeof tg?.masked_bot_token === "string" ? tg.masked_bot_token : "",
      chat_id: typeof tg?.chat_id === "string" ? tg.chat_id : "",
      api_base_url: typeof tg?.api_base_url === "string" ? tg.api_base_url : "",
      has_proxy: Boolean(tg?.has_proxy),
      masked_proxy_url:
        typeof tg?.masked_proxy_url === "string" ? tg.masked_proxy_url : "",
      send_preview_image:
        typeof tg?.send_preview_image === "boolean"
          ? tg.send_preview_image
          : defaults.telegram.send_preview_image,
    },
    bark: {
      enabled: Boolean(bark?.enabled),
      configured: Boolean(bark?.configured),
      server_url:
        typeof bark?.server_url === "string" && bark.server_url.trim()
          ? bark.server_url.trim()
          : defaults.bark.server_url,
      has_device_key: Boolean(bark?.has_device_key),
      masked_device_key:
        typeof bark?.masked_device_key === "string"
          ? bark.masked_device_key
          : "",
      group:
        typeof bark?.group === "string" && bark.group.trim()
          ? bark.group.trim()
          : defaults.bark.group,
      sound: typeof bark?.sound === "string" ? bark.sound.trim() : "",
      level: typeof bark?.level === "string" ? bark.level.trim() : "",
    },
    email: {
      enabled: Boolean(email?.enabled),
      configured: Boolean(email?.configured),
      smtp_host: typeof email?.smtp_host === "string" ? email.smtp_host : "",
      smtp_port: smtpPort,
      security,
      username: typeof email?.username === "string" ? email.username : "",
      has_password: Boolean(email?.has_password),
      from_address:
        typeof email?.from_address === "string" ? email.from_address : "",
      from_name:
        typeof email?.from_name === "string" && email.from_name.trim()
          ? email.from_name.trim()
          : defaults.email.from_name,
      to_addresses:
        typeof email?.to_addresses === "string" ? email.to_addresses : "",
      embed_preview_image:
        typeof email?.embed_preview_image === "boolean"
          ? email.embed_preview_image
          : defaults.email.embed_preview_image,
    },
  };
}

export function colabOneCountEnabledNotificationChannels(
  settings: ColabOneNotificationSettings,
): number {
  let count = 0;
  if (settings.system.supported && settings.system.enabled) count += 1;
  if (settings.telegram.configured && settings.telegram.enabled) count += 1;
  if (settings.bark.configured && settings.bark.enabled) count += 1;
  if (settings.email.configured && settings.email.enabled) count += 1;
  return count;
}

export interface ColabOneParsedBarkEndpoint {
  serverUrl: string;
  deviceKey: string;
  hasEmbeddedKey: boolean;
  group?: string;
  sound?: string;
  level?: string;
}

function normalizeBareBarkServerUrl(raw: string): string {
  const trimmed = String(raw ?? "").trim();
  if (!trimmed) return "";
  if (/^https?:\/\//i.test(trimmed)) {
    try {
      const parsed = new URL(trimmed);
      if (parsed.protocol === "http:" || parsed.protocol === "https:") {
        const segments = parsed.pathname
          .replace(/\/+$/, "")
          .split("/")
          .map((s) => s.trim())
          .filter(Boolean);
        if (
          segments.length > 0 &&
          segments[segments.length - 1].toLowerCase() === "push"
        ) {
          segments.pop();
        }
        return segments.length > 0
          ? `${parsed.origin}/${segments.join("/")}`
          : parsed.origin;
      }
    } catch {
      // Fall through to trailing-slash normalization.
    }
  }
  return trimmed.replace(/\/+$/, "");
}

export function colabOneParseBarkEndpoint(
  raw: string,
): ColabOneParsedBarkEndpoint {
  const trimmed = String(raw ?? "").trim();
  if (!trimmed) {
    return { serverUrl: "", deviceKey: "", hasEmbeddedKey: false };
  }
  if (!/^https?:\/\//i.test(trimmed)) {
    return {
      serverUrl: trimmed.replace(/\/+$/, ""),
      deviceKey: "",
      hasEmbeddedKey: false,
    };
  }
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return {
      serverUrl: trimmed.replace(/\/+$/, ""),
      deviceKey: "",
      hasEmbeddedKey: false,
    };
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return {
      serverUrl: trimmed.replace(/\/+$/, ""),
      deviceKey: "",
      hasEmbeddedKey: false,
    };
  }

  const queryGroup = parsed.searchParams.get("group")?.trim() || undefined;
  const querySound = parsed.searchParams.get("sound")?.trim() || undefined;
  const queryLevel = parsed.searchParams.get("level")?.trim() || undefined;

  const segments = parsed.pathname
    .replace(/\/+$/, "")
    .split("/")
    .map((s) => s.trim())
    .filter(Boolean);

  if (segments.length === 0) {
    return {
      serverUrl: parsed.origin,
      deviceKey: "",
      hasEmbeddedKey: false,
      group: queryGroup,
      sound: querySound,
      level: queryLevel,
    };
  }

  if (segments[segments.length - 1].toLowerCase() === "push") {
    const prefix = segments.slice(0, -1).join("/");
    return {
      serverUrl: prefix ? `${parsed.origin}/${prefix}` : parsed.origin,
      deviceKey: "",
      hasEmbeddedKey: false,
      group: queryGroup,
      sound: querySound,
      level: queryLevel,
    };
  }

  if (segments.length === 1) {
    const deviceKey = decodeURIComponentSafe(segments[0]);
    return {
      serverUrl: parsed.origin,
      deviceKey,
      hasEmbeddedKey: Boolean(deviceKey),
      group: queryGroup,
      sound: querySound,
      level: queryLevel,
    };
  }

  const prefix = segments.slice(0, -1).join("/");
  const deviceKey = decodeURIComponentSafe(segments[segments.length - 1]);
  return {
    serverUrl: `${parsed.origin}/${prefix}`,
    deviceKey,
    hasEmbeddedKey: Boolean(deviceKey),
    group: queryGroup,
    sound: querySound,
    level: queryLevel,
  };
}

function decodeURIComponentSafe(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

// ---- Draft Types, Dirty Checks, and Save Payloads ----

export interface ColabOneTelegramDraft {
  bot_token: string;
  chat_id: string;
  api_base_url: string;
  proxy_url: string;
  clear_proxy: boolean;
  send_preview_image: boolean;
}

export function createTelegramDraftFromView(
  view: ColabOneNotificationTelegramView,
): ColabOneTelegramDraft {
  return {
    bot_token: "",
    chat_id: view.chat_id || "",
    api_base_url: view.api_base_url || "",
    proxy_url: "",
    clear_proxy: false,
    send_preview_image: Boolean(view.send_preview_image),
  };
}

export function isTelegramDraftDirty(
  draft: ColabOneTelegramDraft,
  view: ColabOneNotificationTelegramView,
): boolean {
  if (draft.bot_token.trim() !== "") return true;
  if (draft.proxy_url.trim() !== "") return true;
  if (draft.clear_proxy && view.has_proxy) return true;
  if (draft.chat_id.trim() !== (view.chat_id || "").trim()) return true;
  if (
    draft.api_base_url.trim().replace(/\/+$/, "") !==
    (view.api_base_url || "").trim().replace(/\/+$/, "")
  ) {
    return true;
  }
  if (Boolean(draft.send_preview_image) !== Boolean(view.send_preview_image)) {
    return true;
  }
  return false;
}

export type ColabOneNotificationValidationKey =
  | "notifications.valTelegramNeedToken"
  | "notifications.valTelegramNeedChatId"
  | "notifications.valBarkNeedServer"
  | "notifications.valBarkNeedKey"
  | "notifications.valEmailNeedHost"
  | "notifications.valEmailInvalidPort"
  | "notifications.valEmailNeedFromForNonEmailUser"
  | "notifications.valEmailInvalidFrom"
  | "notifications.valEmailNeedRecipients"
  | "notifications.valEmailInvalidRecipients";

export function getTelegramValidationErrorKey(
  draft: ColabOneTelegramDraft,
  view: ColabOneNotificationTelegramView,
): ColabOneNotificationValidationKey | null {
  if (draft.bot_token.trim().length === 0 && !view.has_bot_token) {
    return "notifications.valTelegramNeedToken";
  }
  if (draft.chat_id.trim().length === 0) {
    return "notifications.valTelegramNeedChatId";
  }
  return null;
}

export function canSaveTelegramDraft(
  draft: ColabOneTelegramDraft,
  view: ColabOneNotificationTelegramView,
): boolean {
  if (getTelegramValidationErrorKey(draft, view) !== null) return false;
  return !view.configured || isTelegramDraftDirty(draft, view);
}

export function buildTelegramSavePayload(
  draft: ColabOneTelegramDraft,
  view: ColabOneNotificationTelegramView,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {
    enabled: view.configured ? view.enabled : true,
    chat_id: draft.chat_id.trim(),
    api_base_url: draft.api_base_url.trim().replace(/\/+$/, ""),
    send_preview_image: Boolean(draft.send_preview_image),
  };
  if (draft.bot_token.trim()) {
    payload.bot_token = draft.bot_token.trim();
  }
  if (draft.proxy_url.trim()) {
    payload.proxy_url = draft.proxy_url.trim();
  } else if (draft.clear_proxy && view.has_proxy) {
    payload.clear_proxy = true;
  }
  return payload;
}

export interface ColabOneBarkDraft {
  server_url: string;
  device_key: string;
  group: string;
  sound: string;
  level: string;
}

export function createBarkDraftFromView(
  view: ColabOneNotificationBarkView,
): ColabOneBarkDraft {
  return {
    server_url: view.server_url || COLABONE_DEFAULT_BARK_SERVER_URL,
    device_key: "",
    group: view.group || COLABONE_DEFAULT_BARK_GROUP,
    sound: view.sound || "",
    level: view.level || "",
  };
}

export function isBarkDraftDirty(
  draft: ColabOneBarkDraft,
  view: ColabOneNotificationBarkView,
): boolean {
  if (draft.device_key.trim() !== "") return true;
  const viewServer =
    normalizeBareBarkServerUrl(
      view.server_url || COLABONE_DEFAULT_BARK_SERVER_URL,
    ) || COLABONE_DEFAULT_BARK_SERVER_URL;
  const rawDraftServer = draft.server_url.trim();
  const bareDraftServer =
    normalizeBareBarkServerUrl(rawDraftServer) ||
    COLABONE_DEFAULT_BARK_SERVER_URL;
  const unchangedSavedServer =
    view.has_device_key &&
    bareDraftServer === viewServer &&
    !rawDraftServer.includes("?");
  if (!unchangedSavedServer) {
    const parsed = colabOneParseBarkEndpoint(rawDraftServer);
    if (parsed.hasEmbeddedKey) return true;
    const cleanServer =
      (parsed.serverUrl || rawDraftServer).replace(/\/+$/, "") ||
      COLABONE_DEFAULT_BARK_SERVER_URL;
    if (cleanServer !== viewServer) return true;
  }
  const cleanGroup = draft.group.trim() || COLABONE_DEFAULT_BARK_GROUP;
  const viewGroup = (view.group || COLABONE_DEFAULT_BARK_GROUP).trim();
  if (cleanGroup !== viewGroup) return true;
  if (draft.sound.trim() !== (view.sound || "").trim()) return true;
  if (draft.level.trim() !== (view.level || "").trim()) return true;
  return false;
}

export function getBarkValidationErrorKey(
  draft: ColabOneBarkDraft,
  view: ColabOneNotificationBarkView,
): ColabOneNotificationValidationKey | null {
  const rawDraftServer = draft.server_url.trim();
  const keyDraft = draft.device_key.trim();
  const parsed = colabOneParseBarkEndpoint(rawDraftServer);
  const parsedFromKey = /^https?:\/\//i.test(keyDraft)
    ? colabOneParseBarkEndpoint(keyDraft)
    : null;
  const effectiveServer = (
    (keyDraft
      ? normalizeBareBarkServerUrl(rawDraftServer)
      : parsed.serverUrl) ||
    parsedFromKey?.serverUrl ||
    rawDraftServer ||
    COLABONE_DEFAULT_BARK_SERVER_URL
  ).trim();
  if (!effectiveServer || !/^https?:\/\/[^/\s]+/i.test(effectiveServer)) {
    return "notifications.valBarkNeedServer";
  }
  const hasKey =
    (parsedFromKey ? parsedFromKey.hasEmbeddedKey : keyDraft.length > 0) ||
    parsed.hasEmbeddedKey ||
    view.has_device_key;
  if (!hasKey) {
    return "notifications.valBarkNeedKey";
  }
  return null;
}

export function canSaveBarkDraft(
  draft: ColabOneBarkDraft,
  view: ColabOneNotificationBarkView,
): boolean {
  if (getBarkValidationErrorKey(draft, view) !== null) return false;
  return !view.configured || isBarkDraftDirty(draft, view);
}

export function buildBarkSavePayload(
  draft: ColabOneBarkDraft,
  view: ColabOneNotificationBarkView,
): Record<string, unknown> {
  const rawDraftServer = draft.server_url.trim();
  const keyDraft = draft.device_key.trim();
  const viewServer =
    normalizeBareBarkServerUrl(
      view.server_url || COLABONE_DEFAULT_BARK_SERVER_URL,
    ) || COLABONE_DEFAULT_BARK_SERVER_URL;
  const bareDraftServer =
    normalizeBareBarkServerUrl(rawDraftServer) ||
    COLABONE_DEFAULT_BARK_SERVER_URL;
  const parsed = colabOneParseBarkEndpoint(rawDraftServer);
  const unchangedSavedServer =
    !keyDraft &&
    view.has_device_key &&
    bareDraftServer === viewServer &&
    !rawDraftServer.includes("?");

  const payload: Record<string, unknown> = {
    enabled: view.configured ? view.enabled : true,
    group: draft.group.trim() || COLABONE_DEFAULT_BARK_GROUP,
    sound: draft.sound.trim(),
    level: draft.level.trim(),
  };
  if (keyDraft) {
    if (/^https?:\/\//i.test(keyDraft)) {
      const parsedKey = colabOneParseBarkEndpoint(keyDraft);
      if (parsedKey.hasEmbeddedKey && parsedKey.deviceKey) {
        payload.server_url =
          parsedKey.serverUrl || COLABONE_DEFAULT_BARK_SERVER_URL;
        payload.device_key = parsedKey.deviceKey;
        return payload;
      }
    }
    payload.server_url = bareDraftServer || COLABONE_DEFAULT_BARK_SERVER_URL;
    payload.device_key = keyDraft;
  } else if (unchangedSavedServer) {
    payload.server_url = viewServer;
  } else if (parsed.hasEmbeddedKey) {
    payload.endpoint_url = rawDraftServer;
  } else {
    payload.server_url =
      (parsed.serverUrl || rawDraftServer).replace(/\/+$/, "") ||
      COLABONE_DEFAULT_BARK_SERVER_URL;
  }
  return payload;
}

export interface ColabOneEmailDraft {
  smtp_host: string;
  smtp_port: number | null;
  security: ColabOneSmtpSecurity;
  username: string;
  password: string;
  clear_password: boolean;
  from_address: string;
  from_name: string;
  to_addresses: string;
  embed_preview_image: boolean;
}

export function createEmailDraftFromView(
  view: ColabOneNotificationEmailView,
): ColabOneEmailDraft {
  const security = normalizeColabOneSmtpSecurity(view.security);
  return {
    smtp_host: view.smtp_host || "",
    smtp_port: isColabOneValidSmtpPort(view.smtp_port)
      ? view.smtp_port
      : colabOneDefaultPortForSmtpSecurity(security),
    security,
    username: view.username || "",
    password: "",
    clear_password: false,
    from_address: view.from_address || "",
    from_name: view.from_name || COLABONE_DEFAULT_EMAIL_FROM_NAME,
    to_addresses: view.to_addresses || "",
    embed_preview_image: Boolean(view.embed_preview_image),
  };
}

export function colabOneSplitEmailRecipients(raw: string): string[] {
  return String(raw ?? "")
    .split(/[,;，；\n\r]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

const SIMPLE_EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function isColabOneValidEmailAddress(value: string): boolean {
  return SIMPLE_EMAIL_RE.test(String(value ?? "").trim());
}

export function isEmailDraftDirty(
  draft: ColabOneEmailDraft,
  view: ColabOneNotificationEmailView,
): boolean {
  if (draft.password.length > 0) return true;
  if (draft.clear_password && view.has_password) return true;
  if (draft.smtp_host.trim() !== (view.smtp_host || "").trim()) return true;
  const viewSecurity = normalizeColabOneSmtpSecurity(view.security);
  if (draft.security !== viewSecurity) return true;
  const viewPort = isColabOneValidSmtpPort(view.smtp_port)
    ? view.smtp_port
    : colabOneDefaultPortForSmtpSecurity(viewSecurity);
  if (draft.smtp_port !== viewPort) return true;
  if (draft.username.trim() !== (view.username || "").trim()) return true;
  if (draft.from_address.trim() !== (view.from_address || "").trim()) return true;
  const cleanFromName = draft.from_name.trim() || COLABONE_DEFAULT_EMAIL_FROM_NAME;
  const viewFromName = (view.from_name || COLABONE_DEFAULT_EMAIL_FROM_NAME).trim();
  if (cleanFromName !== viewFromName) return true;
  if (draft.to_addresses.trim() !== (view.to_addresses || "").trim()) return true;
  if (Boolean(draft.embed_preview_image) !== Boolean(view.embed_preview_image)) {
    return true;
  }
  return false;
}

export function getEmailValidationErrorKey(
  draft: ColabOneEmailDraft,
  _view: ColabOneNotificationEmailView,
): ColabOneNotificationValidationKey | null {
  if (!draft.smtp_host.trim()) {
    return "notifications.valEmailNeedHost";
  }
  if (!isColabOneValidSmtpPort(draft.smtp_port)) {
    return "notifications.valEmailInvalidPort";
  }
  const trimmedFrom = draft.from_address.trim();
  const trimmedUser = draft.username.trim();
  const effectiveFrom = trimmedFrom || trimmedUser;
  if (!isColabOneValidEmailAddress(effectiveFrom)) {
    if (!trimmedFrom && trimmedUser) {
      return "notifications.valEmailNeedFromForNonEmailUser";
    }
    return "notifications.valEmailInvalidFrom";
  }
  const recipients = colabOneSplitEmailRecipients(draft.to_addresses);
  if (recipients.length === 0) {
    return "notifications.valEmailNeedRecipients";
  }
  if (!recipients.every(isColabOneValidEmailAddress)) {
    return "notifications.valEmailInvalidRecipients";
  }
  return null;
}

export function canSaveEmailDraft(
  draft: ColabOneEmailDraft,
  view: ColabOneNotificationEmailView,
): boolean {
  if (getEmailValidationErrorKey(draft, view) !== null) return false;
  return !view.configured || isEmailDraftDirty(draft, view);
}

export function buildEmailSavePayload(
  draft: ColabOneEmailDraft,
  view: ColabOneNotificationEmailView,
): Record<string, unknown> {
  const effectiveFrom = draft.from_address.trim() || draft.username.trim();
  const payload: Record<string, unknown> = {
    enabled: view.configured ? view.enabled : true,
    smtp_host: draft.smtp_host.trim(),
    smtp_port: isColabOneValidSmtpPort(draft.smtp_port)
      ? draft.smtp_port
      : colabOneDefaultPortForSmtpSecurity(draft.security),
    security: draft.security,
    username: draft.username.trim(),
    from_address: effectiveFrom,
    from_name: draft.from_name.trim() || COLABONE_DEFAULT_EMAIL_FROM_NAME,
    to_addresses: colabOneSplitEmailRecipients(draft.to_addresses).join(", "),
    embed_preview_image: Boolean(draft.embed_preview_image),
  };
  if (draft.password.length > 0) {
    payload.password = draft.password;
  } else if (draft.clear_password && view.has_password) {
    payload.clear_password = true;
  }
  return payload;
}

export function colabOneMatchSmtpPreset(
  host: string,
  port: number | null,
  security: ColabOneSmtpSecurity,
): ColabOneSmtpPreset["id"] | null {
  const cleanHost = host.trim().toLowerCase();
  if (!cleanHost) return null;
  for (const preset of COLABONE_SMTP_PRESETS) {
    if (
      preset.host === cleanHost &&
      preset.port === port &&
      preset.security === security
    ) {
      return preset.id;
    }
  }
  return null;
}

// ---- Desktop Notification Click Navigation Normalization ----

export interface ColabOneNormalizedNavigateTarget {
  tab: "device" | "api" | "comfyui" | "settings" | null;
  openModels: boolean;
  openOutputs: boolean;
}

export function colabOneNormalizeNavigatePayload(
  raw: unknown,
): ColabOneNormalizedNavigateTarget {
  if (!raw || typeof raw !== "object") {
    return { tab: null, openModels: false, openOutputs: false };
  }
  const rec = raw as Record<string, unknown>;
  const tabRaw = typeof rec.tab === "string" ? rec.tab.trim().toLowerCase() : "";
  const openOutputs = Boolean(rec.open_outputs);
  switch (tabRaw) {
    case "device":
    case "devices":
      return { tab: "device", openModels: false, openOutputs: false };
    case "api":
      return { tab: "api", openModels: false, openOutputs: false };
    case "models":
      return { tab: "api", openModels: true, openOutputs: false };
    case "comfy":
    case "comfyui":
      return { tab: "comfyui", openModels: false, openOutputs };
    case "settings":
      return { tab: "settings", openModels: false, openOutputs: false };
    default:
      if (openOutputs) {
        return { tab: "comfyui", openModels: false, openOutputs: true };
      }
      return { tab: null, openModels: false, openOutputs: false };
  }
}
