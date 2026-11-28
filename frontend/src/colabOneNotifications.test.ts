import { describe, expect, it } from "vitest";
import {
  COLABONE_DEFAULT_BARK_GROUP,
  COLABONE_DEFAULT_BARK_SERVER_URL,
  COLABONE_DEFAULT_EMAIL_FROM_NAME,
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
  colabOneNormalizeNavigatePayload,
  colabOneParseBarkEndpoint,
  colabOneSplitEmailRecipients,
  createBarkDraftFromView,
  createEmailDraftFromView,
  createTelegramDraftFromView,
  getBarkValidationErrorKey,
  getEmailValidationErrorKey,
  getTelegramValidationErrorKey,
  isBarkDraftDirty,
  isColabOneDefaultSmtpPort,
  isColabOneValidEmailAddress,
  isColabOneValidSmtpPort,
  isEmailDraftDirty,
  isTelegramDraftDirty,
  normalizeColabOneNotificationSettings,
  normalizeColabOneSmtpSecurity,
} from "./colabOneNotifications";
import { createColabOneDefaultNotificationSettings } from "./types";

describe("colabOneNotifications defaults and normalization", () => {
  it("provides safe defaults and normalizes partial backend payloads", () => {
    const defaults = createColabOneDefaultNotificationSettings();
    expect(defaults.notify_gpu).toBe(true);
    expect(defaults.notify_task).toBe(true);
    expect(defaults.notify_alert).toBe(true);
    expect(defaults.system_only_when_unfocused).toBe(true);
    expect(defaults.remote_only_when_unfocused).toBe(false);
    expect(defaults.system).toEqual({ enabled: true, supported: true });
    expect(defaults.bark.server_url).toBe(COLABONE_DEFAULT_BARK_SERVER_URL);
    expect(defaults.bark.group).toBe(COLABONE_DEFAULT_BARK_GROUP);
    expect(defaults.email.from_name).toBe(COLABONE_DEFAULT_EMAIL_FROM_NAME);
    expect(defaults.email.smtp_port).toBe(465);
    expect(defaults.email.security).toBe("tls");

    const normalized = normalizeColabOneNotificationSettings({
      notify_gpu: false,
      remote_only_when_unfocused: true,
      system: { enabled: true, supported: false },
      email: {
        enabled: true,
        configured: true,
        smtp_host: "smtp.example.com",
        smtp_port: 0,
        security: "starttls",
        username: "bot@example.com",
        has_password: true,
        from_address: "",
        from_name: "",
        to_addresses: "user@example.com",
        embed_preview_image: false,
      },
    });
    expect(normalized.notify_gpu).toBe(false);
    expect(normalized.notify_task).toBe(true);
    expect(normalized.remote_only_when_unfocused).toBe(true);
    expect(normalized.system).toEqual({ enabled: true, supported: false });
    expect(normalized.email.smtp_port).toBe(587);
    expect(normalized.email.from_name).toBe(COLABONE_DEFAULT_EMAIL_FROM_NAME);
    expect(normalized.email.embed_preview_image).toBe(false);
  });

  it("counts only supported/configured enabled notification channels", () => {
    const settings = createColabOneDefaultNotificationSettings();
    expect(colabOneCountEnabledNotificationChannels(settings)).toBe(1);

    settings.system.supported = false;
    settings.telegram.enabled = true;
    settings.telegram.configured = false;
    expect(colabOneCountEnabledNotificationChannels(settings)).toBe(0);

    settings.telegram.configured = true;
    settings.bark.enabled = true;
    settings.bark.configured = true;
    settings.email.enabled = false;
    settings.email.configured = true;
    expect(colabOneCountEnabledNotificationChannels(settings)).toBe(2);
  });
});

describe("Bark endpoint URL parsing and splitting", () => {
  it("parses official api.day.app server URLs and embedded push keys", () => {
    expect(colabOneParseBarkEndpoint("https://api.day.app")).toEqual({
      serverUrl: "https://api.day.app",
      deviceKey: "",
      hasEmbeddedKey: false,
      group: undefined,
      sound: undefined,
      level: undefined,
    });
    expect(colabOneParseBarkEndpoint("https://api.day.app/push")).toEqual({
      serverUrl: "https://api.day.app",
      deviceKey: "",
      hasEmbeddedKey: false,
      group: undefined,
      sound: undefined,
      level: undefined,
    });
    expect(colabOneParseBarkEndpoint("https://api.day.app/AbCdEf123456/")).toEqual({
      serverUrl: "https://api.day.app",
      deviceKey: "AbCdEf123456",
      hasEmbeddedKey: true,
      group: undefined,
      sound: undefined,
      level: undefined,
    });
    expect(
      colabOneParseBarkEndpoint(
        "https://api.day.app/KeyXYZ99?group=GPU&sound=bell&level=timeSensitive",
      ),
    ).toEqual({
      serverUrl: "https://api.day.app",
      deviceKey: "KeyXYZ99",
      hasEmbeddedKey: true,
      group: "GPU",
      sound: "bell",
      level: "timeSensitive",
    });
  });

  it("parses self-hosted Bark endpoints preserving arbitrary subpaths and trailing /push", () => {
    expect(
      colabOneParseBarkEndpoint("https://bark.example.com:8443/MyDeviceKey123"),
    ).toMatchObject({
      serverUrl: "https://bark.example.com:8443",
      deviceKey: "MyDeviceKey123",
      hasEmbeddedKey: true,
    });

    expect(
      colabOneParseBarkEndpoint("https://example.com/bark/SelfHostedKey99"),
    ).toMatchObject({
      serverUrl: "https://example.com/bark",
      deviceKey: "SelfHostedKey99",
      hasEmbeddedKey: true,
    });

    expect(
      colabOneParseBarkEndpoint(
        "https://example.com/sub/path/MyKey?sound=bell&group=Render",
      ),
    ).toEqual({
      serverUrl: "https://example.com/sub/path",
      deviceKey: "MyKey",
      hasEmbeddedKey: true,
      group: "Render",
      sound: "bell",
      level: undefined,
    });

    expect(colabOneParseBarkEndpoint("https://example.com/bark/push")).toMatchObject({
      serverUrl: "https://example.com/bark",
      deviceKey: "",
      hasEmbeddedKey: false,
    });

    expect(
      colabOneParseBarkEndpoint("https://example.com/sub/path/push/"),
    ).toMatchObject({
      serverUrl: "https://example.com/sub/path",
      deviceKey: "",
      hasEmbeddedKey: false,
    });
  });
});

describe("SMTP presets, validation, and channel drafts", () => {
  it("matches all 5 built-in SMTP presets and port/security helpers", () => {
    expect(COLABONE_SMTP_PRESETS.map((p) => p.id)).toEqual([
      "qq",
      "163",
      "gmail",
      "outlook",
      "icloud",
    ]);
    for (const preset of COLABONE_SMTP_PRESETS) {
      expect(
        colabOneMatchSmtpPreset(preset.host, preset.port, preset.security),
      ).toBe(preset.id);
    }
    expect(colabOneMatchSmtpPreset("smtp.custom.org", 465, "tls")).toBeNull();
    expect(normalizeColabOneSmtpSecurity("STARTTLS")).toBe("starttls");
    expect(normalizeColabOneSmtpSecurity("unknown")).toBe("tls");
    expect(colabOneDefaultPortForSmtpSecurity("tls")).toBe(465);
    expect(colabOneDefaultPortForSmtpSecurity("starttls")).toBe(587);
    expect(colabOneDefaultPortForSmtpSecurity("none")).toBe(25);
    expect(isColabOneDefaultSmtpPort(465)).toBe(true);
    expect(isColabOneDefaultSmtpPort(2525)).toBe(false);
    expect(isColabOneValidSmtpPort(587)).toBe(true);
    expect(isColabOneValidSmtpPort(0)).toBe(false);
    expect(isColabOneValidSmtpPort(70000)).toBe(false);
  });

  it("tracks Telegram draft dirty state, validation, and clear_proxy payloads", () => {
    const emptyView = createColabOneDefaultNotificationSettings().telegram;
    const draft = createTelegramDraftFromView(emptyView);
    expect(isTelegramDraftDirty(draft, emptyView)).toBe(false);
    expect(canSaveTelegramDraft(draft, emptyView)).toBe(false);

    draft.bot_token = "123456:AAHtoken";
    draft.chat_id = "987654321";
    expect(isTelegramDraftDirty(draft, emptyView)).toBe(true);
    expect(canSaveTelegramDraft(draft, emptyView)).toBe(true);
    expect(buildTelegramSavePayload(draft, emptyView)).toEqual({
      enabled: true,
      bot_token: "123456:AAHtoken",
      chat_id: "987654321",
      api_base_url: "",
      send_preview_image: true,
    });

    const configuredView = {
      ...emptyView,
      enabled: false,
      configured: true,
      has_bot_token: true,
      chat_id: "987654321",
      has_proxy: true,
      masked_proxy_url: "http://127.0.0.1:7890",
    };
    const editDraft = createTelegramDraftFromView(configuredView);
    expect(isTelegramDraftDirty(editDraft, configuredView)).toBe(false);
    expect(canSaveTelegramDraft(editDraft, configuredView)).toBe(false);

    editDraft.clear_proxy = true;
    expect(isTelegramDraftDirty(editDraft, configuredView)).toBe(true);
    expect(canSaveTelegramDraft(editDraft, configuredView)).toBe(true);
    expect(buildTelegramSavePayload(editDraft, configuredView)).toEqual({
      enabled: false,
      chat_id: "987654321",
      api_base_url: "",
      send_preview_image: true,
      clear_proxy: true,
    });
  });

  it("tracks Bark draft dirty state and sends endpoint_url or server_url+device_key", () => {
    const emptyView = createColabOneDefaultNotificationSettings().bark;
    const draft = createBarkDraftFromView(emptyView);
    expect(canSaveBarkDraft(draft, emptyView)).toBe(false);

    draft.server_url = "https://api.day.app/MyPushKey999";
    expect(isBarkDraftDirty(draft, emptyView)).toBe(true);
    expect(canSaveBarkDraft(draft, emptyView)).toBe(true);
    expect(buildBarkSavePayload(draft, emptyView)).toEqual({
      enabled: true,
      endpoint_url: "https://api.day.app/MyPushKey999",
      group: "ColabOne",
      sound: "",
      level: "",
    });

    draft.server_url = "https://api.day.app";
    draft.device_key = "ExplicitKey123";
    draft.sound = "bell";
    draft.level = "timeSensitive";
    expect(buildBarkSavePayload(draft, emptyView)).toEqual({
      enabled: true,
      server_url: "https://api.day.app",
      device_key: "ExplicitKey123",
      group: "ColabOne",
      sound: "bell",
      level: "timeSensitive",
    });

    // Preserves arbitrary subpath server_url when explicit device_key is set
    draft.server_url = "https://example.com/sub/path";
    draft.device_key = "SubpathKey123";
    expect(buildBarkSavePayload(draft, emptyView)).toEqual({
      enabled: true,
      server_url: "https://example.com/sub/path",
      device_key: "SubpathKey123",
      group: "ColabOne",
      sound: "bell",
      level: "timeSensitive",
    });

    // Untouched saved subpath server_url stays clean (not dirty) and preserves subpath on sound edit
    const savedSubpathView = {
      ...emptyView,
      enabled: true,
      configured: true,
      server_url: "https://example.com/sub/path",
      has_device_key: true,
      masked_device_key: "Subp****123",
    };
    const savedSubpathDraft = createBarkDraftFromView(savedSubpathView);
    expect(isBarkDraftDirty(savedSubpathDraft, savedSubpathView)).toBe(false);
    expect(canSaveBarkDraft(savedSubpathDraft, savedSubpathView)).toBe(false);
    savedSubpathDraft.sound = "glass";
    expect(isBarkDraftDirty(savedSubpathDraft, savedSubpathView)).toBe(true);
    expect(buildBarkSavePayload(savedSubpathDraft, savedSubpathView)).toEqual({
      enabled: true,
      server_url: "https://example.com/sub/path",
      group: "ColabOne",
      sound: "glass",
      level: "",
    });
  });

  it("validates email recipients (including fullwidth Chinese delimiters), preserves raw SMTP password whitespace, supports clear_password, and returns accurate validation hints", () => {
    expect(colabOneSplitEmailRecipients("a@x.com, b@y.com; c@z.com\nd@w.com")).toEqual([
      "a@x.com",
      "b@y.com",
      "c@z.com",
      "d@w.com",
    ]);
    expect(
      colabOneSplitEmailRecipients("a@qq.com，b@163.com；c@gmail.com"),
    ).toEqual(["a@qq.com", "b@163.com", "c@gmail.com"]);
    expect(isColabOneValidEmailAddress("user@example.com")).toBe(true);
    expect(isColabOneValidEmailAddress("not-an-email")).toBe(false);

    const emptyView = createColabOneDefaultNotificationSettings().email;
    const draft = createEmailDraftFromView(emptyView);
    expect(canSaveEmailDraft(draft, emptyView)).toBe(false);
    expect(getEmailValidationErrorKey(draft, emptyView)).toBe(
      "notifications.valEmailNeedHost",
    );

    draft.smtp_host = "smtp.qq.com";
    draft.smtp_port = 465;
    draft.security = "tls";
    draft.username = "smtp-user-login";
    expect(getEmailValidationErrorKey(draft, emptyView)).toBe(
      "notifications.valEmailNeedFromForNonEmailUser",
    );

    draft.username = "sender@qq.com";
    expect(getEmailValidationErrorKey(draft, emptyView)).toBe(
      "notifications.valEmailNeedRecipients",
    );

    draft.password = "  raw pass with spaces  ";
    draft.to_addresses = "one@example.com，two@example.com；three@example.com";
    expect(getEmailValidationErrorKey(draft, emptyView)).toBeNull();
    expect(isEmailDraftDirty(draft, emptyView)).toBe(true);
    expect(canSaveEmailDraft(draft, emptyView)).toBe(true);
    expect(buildEmailSavePayload(draft, emptyView)).toEqual({
      enabled: true,
      smtp_host: "smtp.qq.com",
      smtp_port: 465,
      security: "tls",
      username: "sender@qq.com",
      password: "  raw pass with spaces  ",
      from_address: "sender@qq.com",
      from_name: "ColabOne",
      to_addresses: "one@example.com, two@example.com, three@example.com",
      embed_preview_image: true,
    });

    // Whitespace-only password is also preserved & treated as dirty
    const configuredEmailView = {
      ...emptyView,
      enabled: true,
      configured: true,
      smtp_host: "smtp.qq.com",
      smtp_port: 465,
      security: "tls",
      username: "sender@qq.com",
      has_password: true,
      from_address: "sender@qq.com",
      to_addresses: "one@example.com",
    };
    const editEmailDraft = createEmailDraftFromView(configuredEmailView);
    expect(isEmailDraftDirty(editEmailDraft, configuredEmailView)).toBe(false);
    editEmailDraft.clear_password = true;
    expect(isEmailDraftDirty(editEmailDraft, configuredEmailView)).toBe(true);
    expect(canSaveEmailDraft(editEmailDraft, configuredEmailView)).toBe(true);
    expect(buildEmailSavePayload(editEmailDraft, configuredEmailView)).toMatchObject({
      clear_password: true,
    });
    expect(
      "password" in buildEmailSavePayload(editEmailDraft, configuredEmailView),
    ).toBe(false);

    draft.to_addresses = "bad-email";
    expect(canSaveEmailDraft(draft, emptyView)).toBe(false);
    expect(getEmailValidationErrorKey(draft, emptyView)).toBe(
      "notifications.valEmailInvalidRecipients",
    );

    // Telegram & Bark validation hints
    const emptyTg = createColabOneDefaultNotificationSettings().telegram;
    const tgDraft = createTelegramDraftFromView(emptyTg);
    expect(getTelegramValidationErrorKey(tgDraft, emptyTg)).toBe(
      "notifications.valTelegramNeedToken",
    );
    tgDraft.bot_token = "123456:AAH";
    expect(getTelegramValidationErrorKey(tgDraft, emptyTg)).toBe(
      "notifications.valTelegramNeedChatId",
    );
    tgDraft.chat_id = "12345";
    expect(getTelegramValidationErrorKey(tgDraft, emptyTg)).toBeNull();

    const emptyBark = createColabOneDefaultNotificationSettings().bark;
    const barkDraft = createBarkDraftFromView(emptyBark);
    expect(getBarkValidationErrorKey(barkDraft, emptyBark)).toBe(
      "notifications.valBarkNeedKey",
    );
    barkDraft.server_url = "not-a-url";
    expect(getBarkValidationErrorKey(barkDraft, emptyBark)).toBe(
      "notifications.valBarkNeedServer",
    );
    barkDraft.server_url = "https://example.com/sub/MyKey";
    expect(getBarkValidationErrorKey(barkDraft, emptyBark)).toBeNull();
  });
});

describe("desktop notification click navigation normalization", () => {
  it("maps tab aliases and open_outputs / models flags cleanly", () => {
    expect(colabOneNormalizeNavigatePayload({ tab: "device" })).toEqual({
      tab: "device",
      openModels: false,
      openOutputs: false,
    });
    expect(colabOneNormalizeNavigatePayload({ tab: "models" })).toEqual({
      tab: "api",
      openModels: true,
      openOutputs: false,
    });
    expect(
      colabOneNormalizeNavigatePayload({ tab: "comfy", open_outputs: true }),
    ).toEqual({
      tab: "comfyui",
      openModels: false,
      openOutputs: true,
    });
    expect(colabOneNormalizeNavigatePayload({ tab: "settings" })).toEqual({
      tab: "settings",
      openModels: false,
      openOutputs: false,
    });
    expect(colabOneNormalizeNavigatePayload({ open_outputs: true })).toEqual({
      tab: "comfyui",
      openModels: false,
      openOutputs: true,
    });
    expect(colabOneNormalizeNavigatePayload(null)).toEqual({
      tab: null,
      openModels: false,
      openOutputs: false,
    });
  });
});
