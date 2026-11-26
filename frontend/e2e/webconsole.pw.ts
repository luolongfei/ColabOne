import { test, expect } from "@playwright/test";

function makeBaseSnapshot(ready = true) {
  return {
    runtime: {
      state: ready ? "ready" : "stopped",
      protocol: 7,
      capabilities: ["public_api", "comfyui"],
      active_engine: null,
      gpu: ready
        ? { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 }
        : undefined,
      id: ready ? "test-runtime" : undefined,
      disk_free: 90 * 1024 ** 3,
      tasks: [],
      drive: {
        connected: ready,
        mode: ready ? "mount" : "disabled",
        remote_folder: "ColabOne_Drive",
        pending: 0,
        error: "",
      },
      comfyui: {
        installed: ready,
        running: ready,
        state: ready ? "ready" : "stopped",
        port: 8188,
        tag: "v0.46.0",
        queue_running: 0,
        queue_pending: 0,
        error: "",
        canvas: {
          state: ready ? "ready" : "stopped",
          enabled: ready,
          mode: "named",
          url: ready ? "https://canvas.example.com" : "",
          error: "",
          checked_at: Date.now() / 1000,
          active_connections: 0,
        },
      },
    },
    models: [],
    workflows: [],
    tts_engines: [],
    comfy_outputs: [],
    settings: {
      gpu: "T4",
      idle_minutes: 30,
      download_threads: 16,
      auto_drive: false,
      assign_max_attempts: 6,
      locale: "zh-CN",
    },
    api_settings: {
      mode: "named",
      domain: "https://api.example.com",
      has_token: true,
    },
    comfy_canvas_settings: {
      mode: "named",
      domain: "https://canvas.example.com",
      has_token: true,
    },
    assign_proxy_settings: {
      enabled: false,
      has_url: true,
      redacted: "socks5://127.0.0.1:1080",
    },
    account: {
      logged_in: ready,
      email: ready ? "user@example.com" : "",
      balance: 98.5,
      rate: 1.96,
      assignments: ready ? 1 : 0,
      checked_at: Date.now() / 1000,
    },
    setup: {
      colab: ready,
      persistent_login: ready,
      drive: ready,
      drive_authorized: ready,
      drive_headless: ready,
      hf_token: true,
      start_menu_shortcut: false,
      desktop_shortcut: false,
    },
    instances: [],
    active_endpoint: "",
    monitor_endpoint: "",
    operation: null,
    error: "",
    auth_url: "",
    drive_auth_hint: "",
    activity: [],
  };
}

test("desktop Settings Remote Web Console card toggles on default port 3232, generates login link, and revokes sessions", async ({
  page,
}) => {
  const snapshot = makeBaseSnapshot(true);

  await page.addInitScript(({ snap }) => {
    let webState = {
      enabled: false,
      running: false,
      bind_mode: "loopback",
      listen_addr: "127.0.0.1:3232",
      port: 3232,
      local_url: "http://127.0.0.1:3232",
      lan_urls: [] as string[],
      public_origin: "",
      has_passcode: true,
      passcode_preview: "co_w••••99ab",
      active_sessions: 0,
      last_error: "",
    };
    const calls: Array<{ method: string; args: unknown[] }> = [];
    const copied: string[] = [];
    (window as any).__webConsoleCalls = calls;
    (window as any).__webConsoleCopied = copied;
    (window as any).mygo = {
      on: () => () => {},
      once: () => () => {},
      callWith: async (method: string, args: any[] = []) => {
        calls.push({ method, args });
        switch (method) {
          case "ColabOneDesktop.Snapshot":
            return JSON.stringify(snap);
          case "ColabOneDesktop.Command":
            return JSON.stringify({ ok: true });
          case "ColabOneDesktop.SystemAppearance":
            return "light";
          case "ColabOneDesktop.SetAppearance":
          case "ColabOneDesktop.SetLocale":
            return null;
          case "ColabOneDesktop.GetLogDrawerState":
            return { open: false, standalone: true, expanded: false, delta: 0 };
          case "ColabOneDesktop.GetCloseBehavior":
            return { behavior: "ask", default_choice: "quit", can_background: true };
          case "ColabOneDesktop.GetUpdateState":
            return {
              status: "idle",
              current_version: "0.1.20",
              available_version: "",
              is_prerelease: false,
              is_rollback: false,
              release_date: "",
              release_url: "https://github.com/luolongfei/ColabOne/releases",
              notes_raw: "",
              notes_zh: "",
              notes_en: "",
              downloaded_bytes: 0,
              total_bytes: 0,
              progress_percent: 0,
              speed_bps: 0,
              can_self_update: true,
              disabled_reason: "",
              auto_check: true,
              include_prerelease: false,
              skipped_version: "",
              last_checked_at: 0,
              error_code: "",
              error_message: "",
              rollback_versions: [],
            };
          case "ColabOneDesktop.GetWebConsoleState":
            return { ...webState };
          case "ColabOneDesktop.UpdateWebConsole": {
            const req = args[0] || {};
            const nextEnabled =
              typeof req.enabled === "boolean" ? req.enabled : webState.enabled;
            const nextBind = req.bind_mode || webState.bind_mode;
            const nextPort = req.port || webState.port;
            const nextOrigin =
              typeof req.public_origin === "string"
                ? req.public_origin
                : webState.public_origin;
            webState = {
              ...webState,
              enabled: nextEnabled,
              running: nextEnabled,
              bind_mode: nextBind,
              port: nextPort,
              listen_addr:
                nextBind === "lan"
                  ? `0.0.0.0:${nextPort}`
                  : `127.0.0.1:${nextPort}`,
              local_url: `http://127.0.0.1:${nextPort}`,
              lan_urls: nextBind === "lan" ? [`http://192.168.1.5:${nextPort}`] : [],
              public_origin: nextOrigin,
              active_sessions: nextEnabled ? 2 : 0,
              last_error: "",
            };
            return { ...webState };
          }
          case "ColabOneDesktop.GetWebConsolePasscode":
            return "co_web_test_passcode_123456";
          case "ColabOneDesktop.SetWebConsolePasscode":
            webState = {
              ...webState,
              passcode_preview: "co_w••••z999",
              active_sessions: 0,
            };
            return { ...webState };
          case "ColabOneDesktop.CreateWebConsoleLoginTicket":
            return {
              url: "http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123",
              local_url: "http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123",
              expires_at: Math.floor(Date.now() / 1000) + 60,
              ttl_seconds: 60,
              uses_public_host: false,
            };
          case "ColabOneDesktop.RevokeWebConsoleSessions":
            webState = { ...webState, active_sessions: 0 };
            return { ...webState };
          case "ColabOneDesktop.WriteClipboard":
            copied.push(String(args[0] ?? ""));
            return null;
          default:
            return null;
        }
      },
    };
  }, { snap: snapshot });

  await page.goto("/");
  await page.getByRole("button", { name: "设置" }).click();

  const card = page.getByTestId("settings-web-console-row");
  await expect(card).toBeVisible();
  await expect(card).toContainText("已关闭");

  // Toggle web console ON
  await page.getByTestId("settings-web-console-switch").click();
  await expect(card).toContainText("运行中");
  await expect(card).toContainText("http://127.0.0.1:3232");
  await expect(card).toContainText("2 个在线会话");

  // Verify ticket button and hint text have comfortable vertical separation (no ASpace negative-margin collapse)
  const ticketBtnBox = await page
    .getByTestId("settings-web-console-ticket-btn")
    .boundingBox();
  const ticketHintBox = await page
    .getByTestId("settings-web-console-ticket-hint")
    .boundingBox();
  expect(ticketBtnBox).not.toBeNull();
  expect(ticketHintBox).not.toBeNull();
  expect(
    ticketHintBox!.y - (ticketBtnBox!.y + ticketBtnBox!.height),
  ).toBeGreaterThanOrEqual(8);

  // Copy one-time login link
  await page.getByTestId("settings-web-console-ticket-btn").click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).__webConsoleCopied.slice()),
    )
    .toContain("http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123");

  // Revoke sessions
  await page.getByTestId("settings-web-console-revoke-btn").click();
  await expect(card).not.toContainText("2 个在线会话");

  // Toggle web console OFF
  await page.getByTestId("settings-web-console-switch").click();
  await expect(card).toContainText("已关闭");
});

test("web console browser mode runs over HTTP without window.mygo, hides host-only sections, and handles 401 session expiry", async ({
  page,
}) => {
  const snapshot = makeBaseSnapshot(true);
  let expireSession = false;
  const commandActions: string[] = [];

  await page.route("**/api/web/capabilities", async (route) => {
    if (expireSession) {
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "Unauthorized" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        runtime: "web",
        version: "0.1.20",
        can_open_native_canvas_window: false,
        can_standalone_log_window: false,
        can_manage_web_console: false,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_loopback_oauth: false,
        can_download_output: true,
      }),
    });
  });

  await page.route("**/api/snapshot", async (route) => {
    if (expireSession) {
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "Unauthorized" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { ETag: '"sha256-web-test-1"' },
      body: JSON.stringify(snapshot),
    });
  });

  await page.route("**/api/command", async (route) => {
    const reqBody = route.request().postDataJSON() as { action?: string };
    if (reqBody?.action) {
      commandActions.push(reqBody.action);
    }
    if (reqBody?.action === "auth.colab.begin") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          url: "https://accounts.google.com/o/oauth2/auth?response_type=code",
        }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    });
  });

  await page.goto("/");

  // Navbar shows Web Mode badge and Sign Out button
  await expect(page.getByTestId("nav-web-mode-badge")).toBeVisible();
  await expect(page.getByTestId("nav-web-signout-btn")).toBeVisible();

  // Settings page hides host-only sections & shows web mode notice
  await page.getByRole("button", { name: "设置" }).click();
  await expect(page.getByTestId("settings-web-mode-notice")).toBeVisible();
  await expect(page.getByTestId("settings-web-console-row")).toHaveCount(0);
  await expect(page.getByTestId("settings-migration-row")).toHaveCount(0);
  await expect(page.getByTestId("settings-close-behavior-row")).toHaveCount(0);
  await expect(page.getByTestId("settings-update-row")).toHaveCount(0);

  // ComfyUI canvas shows "Open Canvas in New Tab" instead of desktop native window button
  await page.getByRole("button", { name: "ComfyUI" }).click();
  await page.getByRole("tab", { name: /原生画布/ }).click();
  await expect(page.getByRole("button", { name: "在新标签页打开画布" })).toBeVisible();
  await expect(page.getByRole("button", { name: "在应用窗口中打开" })).toHaveCount(0);

  // Trigger 401 session expiration and confirm ONLY the session expired banner appears (no duplicate error banner)
  expireSession = true;
  await expect(page.getByTestId("web-session-expired-banner")).toBeVisible({
    timeout: 5000,
  });
  await page.waitForTimeout(2600);
  await expect(page.locator(".co-banners .ant-alert-error")).toHaveCount(0);
  expect(commandActions).not.toContain("auth.colab.auto");
});
