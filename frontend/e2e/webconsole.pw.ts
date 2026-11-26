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
          case "ColabOneDesktop.CreateWebConsoleLoginTicket": {
            const singleUse = Boolean(args[0]);
            return {
              url: singleUse
                ? "http://127.0.0.1:3232/_colabone_web/redeem?ticket=once999"
                : "http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123",
              local_url: singleUse
                ? "http://127.0.0.1:3232/_colabone_web/redeem?ticket=once999"
                : "http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123",
              expires_at: Math.floor(Date.now() / 1000) + 60,
              ttl_seconds: 60,
              uses_public_host: false,
              single_use: singleUse,
            };
          }
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

  // Copy default 60s reusable login link (singleUse = false)
  await expect(page.getByTestId("settings-web-console-ticket-hint")).toContainText(
    "60 秒内可多次使用",
  );
  await page.getByTestId("settings-web-console-ticket-btn").click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).__webConsoleCopied.slice()),
    )
    .toContain("http://127.0.0.1:3232/_colabone_web/redeem?ticket=abc123");

  // Quick copy passcode button in the top live block copies the raw passcode directly
  const quickCopyPasscodeBtn = page.getByTestId(
    "settings-web-console-quick-copy-passcode-btn",
  );
  await expect(quickCopyPasscodeBtn).toBeVisible();
  await quickCopyPasscodeBtn.click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).__webConsoleCopied.slice()),
    )
    .toContain("co_web_test_passcode_123456");

  // Optional single-use toggle switches to single-use mode (singleUse = true)
  await page.getByTestId("settings-web-console-ticket-single-use").click();
  await expect(page.getByTestId("settings-web-console-ticket-hint")).toContainText(
    "一次性登录链接",
  );
  await page.getByTestId("settings-web-console-ticket-btn").click();
  await expect
    .poll(() =>
      page.evaluate(() => (window as any).__webConsoleCopied.slice()),
    )
    .toContain("http://127.0.0.1:3232/_colabone_web/redeem?ticket=once999");

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

test("mobile web mode (440x956 and 375x812) renders bottom tab bar, keeps header/status unclipped, prevents API hero squish, and opens full-width drawers", async ({
  page,
}) => {
  const readySnapshot = {
    ...makeBaseSnapshot(true),
    account: {
      logged_in: true,
      email: "mobile@example.com",
      balance: 1918.27,
      rate: 4.82,
      assignments: 1,
      checked_at: Date.now() / 1000,
    },
    runtime: {
      ...makeBaseSnapshot(true).runtime,
      idle: {
        phase: "counting",
        remaining_seconds: 1420,
        timeout_seconds: 1800,
        countdown_seconds: 60,
        server_time: Math.floor(Date.now() / 1000),
      },
    },
  };
  const startingSnapshot = {
    ...makeBaseSnapshot(false),
    settings: {
      ...makeBaseSnapshot(false).settings,
      gpu: "G4",
    },
    account: {
      logged_in: true,
      email: "mobile@example.com",
      balance: 1918.27,
      rate: 0,
      assignments: 0,
      checked_at: Date.now() / 1000,
    },
    runtime: {
      ...makeBaseSnapshot(false).runtime,
      state: "starting",
    },
    operation: {
      kind: "runtime_start",
      title: "正在分配 G4...",
      gpu: "G4",
      waiting_retry: true,
      retry_kind: "capacity",
      attempt: 6,
      next_attempt: 7,
      total_attempts: 20,
      retry_at: Math.floor(Date.now() / 1000) + 23,
      cancellable: true,
      cancelling: false,
    },
  };

  let activeSnapshot: Record<string, unknown> = startingSnapshot;

  await page.route("**/api/web/capabilities", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        runtime: "web",
        version: "0.1.21",
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
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { ETag: `"mobile-${activeSnapshot === startingSnapshot ? "s" : "r"}"` },
      body: JSON.stringify(activeSnapshot),
    });
  });

  await page.route("**/api/command", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    });
  });

  await page.route("**/api/telemetry**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ samples: [] }),
    });
  });

  for (const vp of [
    { width: 440, height: 956 },
    { width: 375, height: 812 },
  ]) {
    activeSnapshot = startingSnapshot;
    await page.setViewportSize(vp);
    await page.goto("/");

    // Desktop center nav track hides; mobile bottom tab bar is visible with all 4 tabs
    await expect(page.locator(".co-nav-center")).toBeHidden();
    const bottomNav = page.getByTestId("mobile-bottom-nav");
    await expect(bottomNav).toBeVisible();
    await expect(bottomNav.getByRole("button")).toHaveCount(4);

    // Brand remains crisp & does not collide with right utility controls
    const navGeometry = await page.evaluate(() => {
      const brand = document.querySelector(".co-brand")?.getBoundingClientRect();
      const right = document.querySelector(".co-nav-right")?.getBoundingClientRect();
      return brand && right
        ? {
            brandLeft: brand.left,
            brandRight: brand.right,
            brandWidth: brand.width,
            rightLeft: right.left,
            rightRight: right.right,
            vw: window.innerWidth,
          }
        : null;
    });
    expect(navGeometry).not.toBeNull();
    expect(navGeometry!.brandWidth).toBeGreaterThanOrEqual(80);
    expect(navGeometry!.brandRight).toBeLessThanOrEqual(navGeometry!.rightLeft);
    expect(navGeometry!.rightRight).toBeLessThanOrEqual(navGeometry!.vw + 1);

    // Switch to Public API in starting state (2 action buttons) and verify hero & model card stack cleanly without text squish
    await bottomNav.getByRole("button", { name: "对外 API" }).click();
    await expect(page.getByRole("heading", { name: "对外 API", exact: true })).toBeVisible();

    const apiLayout = await page.evaluate(() => {
      const headerMain = document
        .querySelector(".co-api-header-main")
        ?.getBoundingClientRect();
      const headerAction = document
        .querySelector(".co-api-header-action")
        ?.getBoundingClientRect();
      const modelMain = document
        .querySelector(".co-api-model-main")
        ?.getBoundingClientRect();
      const modelActions = document
        .querySelector(".co-api-model-actions")
        ?.getBoundingClientRect();
      return headerMain && headerAction && modelMain && modelActions
        ? {
            headerMainW: headerMain.width,
            headerStacked: headerAction.top >= headerMain.bottom - 2,
            modelMainW: modelMain.width,
            modelStacked: modelActions.top >= modelMain.bottom - 2,
          }
        : null;
    });
    expect(apiLayout).not.toBeNull();
    expect(apiLayout!.headerMainW).toBeGreaterThanOrEqual(vp.width - 40);
    expect(apiLayout!.headerStacked).toBe(true);
    expect(apiLayout!.modelMainW).toBeGreaterThanOrEqual(vp.width - 70);
    expect(apiLayout!.modelStacked).toBe(true);

    // Switch to ready state with active billing & test all 4 tabs for zero overflow + unclipped billing bar
    activeSnapshot = readySnapshot;
    await page.reload();
    await expect(bottomNav).toBeVisible();

    const billingClip = await page.evaluate(() => {
      const strip = document.querySelector(".co-billing-strip")?.getBoundingClientRect();
      return strip ? { right: strip.right, vw: window.innerWidth } : null;
    });
    expect(billingClip).not.toBeNull();
    expect(billingClip!.right).toBeLessThanOrEqual(billingClip!.vw + 1);

    for (const tabLabel of ["远端设备", "对外 API", "ComfyUI", "设置"]) {
      await bottomNav.getByRole("button", { name: tabLabel }).click();
      await page.waitForTimeout(150);
      const overflow = await page.evaluate(() => {
        const content = document.querySelector(".co-content");
        return {
          doc: document.documentElement.scrollWidth - window.innerWidth,
          content: content ? content.scrollWidth - content.clientWidth : 0,
        };
      });
      expect(overflow.doc).toBeLessThanOrEqual(0);
      expect(overflow.content).toBeLessThanOrEqual(0);
    }

    // Open inline LogDrawer on mobile: full-screen overlay with explicit close button
    await page.locator(".co-logs-trigger").click();
    const logPanel = page.getByRole("complementary", { name: "运行日志" });
    await expect(logPanel).toBeVisible();
    const logBox = await logPanel.boundingBox();
    expect(logBox).not.toBeNull();
    expect(logBox!.width).toBeGreaterThanOrEqual(vp.width - 2);
    await page.getByTestId("log-drawer-mobile-close").click();
    await expect(logPanel).toBeHidden();

    // Open ModelDrawer from API page: full-width sheet on phones
    await bottomNav.getByRole("button", { name: "对外 API" }).click();
    await page
      .locator(".co-api-model-slot")
      .getByRole("button", { name: /选择模型|切换模型/ })
      .click();
    const drawerWrapper = page.locator(
      ".co-model-drawer-root .ant-drawer-content-wrapper",
    );
    await expect(drawerWrapper).toBeVisible();
    const drawerBox = await drawerWrapper.boundingBox();
    expect(drawerBox).not.toBeNull();
    expect(drawerBox!.width).toBeGreaterThanOrEqual(vp.width - 2);
    await page.locator(".co-model-drawer-root .ant-drawer-close").click();
    await expect(drawerWrapper).toBeHidden();
  }

  // Tablet boundary (681px): desktop navbar stays uncrushed and uncollided
  activeSnapshot = readySnapshot;
  await page.setViewportSize({ width: 681, height: 800 });
  await page.reload();
  await expect(page.locator(".co-nav-center")).toBeVisible();
  const tabletNav = await page.evaluate(() => {
    const left = document.querySelector(".co-nav-left")?.getBoundingClientRect();
    const center = document.querySelector(".co-nav-center")?.getBoundingClientRect();
    const right = document.querySelector(".co-nav-right")?.getBoundingClientRect();
    const brandText = document.querySelector(".co-brand-text");
    return left && center && right && brandText
      ? {
          leftRight: left.right,
          centerLeft: center.left,
          centerRight: center.right,
          rightLeft: right.left,
          rightRight: right.right,
          brandTruncated: brandText.scrollWidth > brandText.clientWidth + 1,
          vw: window.innerWidth,
        }
      : null;
  });
  expect(tabletNav).not.toBeNull();
  expect(tabletNav!.brandTruncated).toBe(false);
  expect(tabletNav!.leftRight).toBeLessThanOrEqual(tabletNav!.centerLeft + 1);
  expect(tabletNav!.centerRight).toBeLessThanOrEqual(tabletNav!.rightLeft + 1);
  expect(tabletNav!.rightRight).toBeLessThanOrEqual(tabletNav!.vw + 1);

  // Small tablet (600px): ModelDrawer width and toast horizontal bounds stay aligned
  await page.setViewportSize({ width: 600, height: 800 });
  await page.reload();
  await page.getByTestId("mobile-bottom-nav").getByRole("button", { name: "对外 API" }).click();
  await page
    .locator(".co-api-model-slot")
    .getByRole("button", { name: /选择模型|切换模型/ })
    .click();
  const drawer600 = page.locator(
    ".co-model-drawer-root .ant-drawer-content-wrapper",
  );
  await expect(drawer600).toBeVisible();
  await expect
    .poll(async () =>
      page.evaluate(() => {
        const wrap = document
          .querySelector(".co-model-drawer-root .ant-drawer-content-wrapper")
          ?.getBoundingClientRect();
        const toast = document
          .querySelector(".co-toast-host")
          ?.getBoundingClientRect();
        if (!wrap || !toast) return null;
        return {
          drawerWidth: Math.round(wrap.width),
          aligned: Math.abs(wrap.left - toast.left) <= 1,
        };
      }),
    )
    .toEqual({ drawerWidth: 552, aligned: true });
});

test("mobile drawer and modal footers stay single-row across 375px and 440px while desktop preserves inline layout", async ({
  page,
}) => {
  const sampleWorkflow = {
    id: "builtin_digital_human",
    name: "口播数字人",
    description: "内置口播数字人工作流",
    source: "builtin",
    fields: [
      {
        key: "prompt",
        label: "口播文案",
        type: "textarea",
        default: "你好，欢迎体验 ColabOne。",
        required: true,
      },
    ],
  };

  let snapshot = {
    ...makeBaseSnapshot(true),
    workflows: [sampleWorkflow],
    runtime: {
      ...makeBaseSnapshot(true).runtime,
      comfyui: {
        ...makeBaseSnapshot(true).runtime.comfyui,
        auto_release: { enabled: false, state: "idle" },
      },
      tasks: [] as Array<Record<string, unknown>>,
    },
  };
  let etagCounter = 1;

  await page.route("**/api/web/capabilities", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        runtime: "web",
        version: "0.1.22",
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
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { ETag: `"footer-test-${etagCounter}"` },
      body: JSON.stringify(snapshot),
    });
  });

  await page.route("**/api/command", async (route) => {
    const req = route.request().postDataJSON() as {
      action?: string;
      payload?: Record<string, unknown>;
    };
    if (req?.action === "comfyui.auto_release") {
      const enabled = Boolean(req?.payload?.enabled);
      snapshot = {
        ...snapshot,
        runtime: {
          ...snapshot.runtime,
          comfyui: {
            ...snapshot.runtime.comfyui,
            auto_release: { enabled, state: enabled ? "armed" : "idle" },
          },
        },
      };
      etagCounter += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(snapshot.runtime.comfyui.auto_release),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    });
  });

  await page.route("**/api/telemetry**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ samples: [] }),
    });
  });

  async function assertSingleRowButtons(containerSelector: string) {
    let lastMetrics: {
      containerLeft: number;
      containerRight: number;
      vw: number;
      count: number;
      rects: Array<{
        text: string;
        top: number;
        bottom: number;
        left: number;
        right: number;
        height: number;
      }>;
    } | null = null;

    await expect
      .poll(async () => {
        lastMetrics = await page.evaluate((sel) => {
          const container = document.querySelector(sel);
          if (!container) return null;
          const cRect = container.getBoundingClientRect();
          const buttons = Array.from(container.querySelectorAll("button")).filter(
            (el) => {
              const style = window.getComputedStyle(el);
              const r = el.getBoundingClientRect();
              return (
                style.display !== "none" &&
                style.visibility !== "hidden" &&
                r.width > 0 &&
                r.height > 0
              );
            },
          );
          const rects = buttons.map((el) => {
            const r = el.getBoundingClientRect();
            return {
              text: (el.textContent || el.getAttribute("aria-label") || "").trim(),
              top: r.top,
              bottom: r.bottom,
              left: r.left,
              right: r.right,
              height: r.height,
            };
          });
          return {
            containerLeft: cRect.left,
            containerRight: cRect.right,
            vw: window.innerWidth,
            count: rects.length,
            rects,
          };
        }, containerSelector);
        if (!lastMetrics || lastMetrics.count < 2) return false;
        return (
          lastMetrics.containerLeft >= -1 &&
          lastMetrics.containerRight <= lastMetrics.vw + 1
        );
      })
      .toBe(true);

    const metrics = lastMetrics!;
    expect(metrics.count).toBeGreaterThanOrEqual(2);
    const firstTop = metrics.rects[0].top;
    const maxBottom = Math.max(...metrics.rects.map((r) => r.bottom));
    const minTop = Math.min(...metrics.rects.map((r) => r.top));
    for (const r of metrics.rects) {
      expect(Math.abs(r.top - firstTop)).toBeLessThanOrEqual(2.5);
      expect(r.left).toBeGreaterThanOrEqual(metrics.containerLeft - 1.5);
      expect(r.right).toBeLessThanOrEqual(metrics.containerRight + 1.5);
      expect(r.right).toBeLessThanOrEqual(metrics.vw + 1);
    }
    expect(maxBottom - minTop).toBeLessThanOrEqual(44);
    return metrics;
  }

  for (const vp of [
    { width: 440, height: 956 },
    { width: 375, height: 812 },
  ]) {
    snapshot = {
      ...makeBaseSnapshot(true),
      workflows: [sampleWorkflow],
      runtime: {
        ...makeBaseSnapshot(true).runtime,
        comfyui: {
          ...makeBaseSnapshot(true).runtime.comfyui,
          auto_release: { enabled: false, state: "idle" },
        },
        tasks: [],
      },
    };
    etagCounter += 1;

    await page.setViewportSize(vp);
    await page.goto("/");

    const bottomNav = page.getByTestId("mobile-bottom-nav");
    await expect(bottomNav).toBeVisible();
    await bottomNav.getByRole("button", { name: "ComfyUI" }).click();

    // 1. Workflow Param Drawer (idle): bottom Close & desktop pill hidden, compact icon trigger + full-width Run stay single-row
    await page.locator(".co-comfy-wf-card-trigger").first().click();
    const paramFooter = page.locator(
      ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
    );
    await expect(paramFooter).toBeVisible();
    await expect(paramFooter.locator(".co-comfy-drawer-close-btn")).toBeHidden();
    await expect(paramFooter.getByTestId("comfy-drawer-auto-release")).toBeHidden();
    const arTrigger = paramFooter.getByTestId(
      "comfy-drawer-auto-release-mobile-trigger",
    );
    await expect(arTrigger).toBeVisible();

    const idleMetrics = await assertSingleRowButtons(
      ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
    );
    expect(idleMetrics.count).toBe(2);

    // Tap icon button: opens popover sheet and arms auto-release with status indicator dot
    await arTrigger.click();
    const arSheet = page.getByTestId("comfy-drawer-auto-release-mobile-sheet");
    await expect(arSheet).toBeVisible();
    const mobileSwitch = arSheet.getByTestId(
      "comfy-drawer-auto-release-mobile-switch",
    );
    await expect(mobileSwitch).toHaveAttribute("aria-checked", "false");
    await mobileSwitch.click();
    await expect(mobileSwitch).toHaveAttribute("aria-checked", "true");
    await expect(arTrigger).toHaveClass(/is-armed/);
    await expect(arTrigger.locator(".co-comfy-mobile-ar-dot")).toBeVisible();
    // Pressing Escape dismisses ONLY the mobile popover without closing the drawer
    await page.keyboard.press("Escape");
    await expect(arSheet).toBeHidden();
    await expect(paramFooter).toBeVisible();

    // 2. Workflow Param Drawer with active task (Cancel Task visible): 3 controls remain single-row without wrapping
    snapshot = {
      ...snapshot,
      runtime: {
        ...snapshot.runtime,
        tasks: [
          {
            id: "task-comfy-1",
            kind: "comfyui_run",
            state: "running",
            progress: 0.45,
            message: "正在生成视频…",
          },
        ],
      },
    };
    etagCounter += 1;
    await expect(paramFooter.locator(".co-comfy-drawer-cancel-btn")).toBeVisible();
    const activeTaskMetrics = await assertSingleRowButtons(
      ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
    );
    expect(activeTaskMetrics.count).toBe(3);

    // Close drawer via header X
    await page.locator(".co-comfy-param-drawer-root .ant-drawer-close").click();
    await expect(paramFooter).toBeHidden();

    // 3. Import Workflow Modal footer: single-row on mobile
    await page.getByRole("button", { name: "导入 API 工作流 (JSON)" }).click();
    const importFooter = page.locator(".co-comfy-import-modal-footer");
    await expect(importFooter).toBeVisible();
    await assertSingleRowButtons(".co-comfy-import-modal-footer");
    await importFooter.getByRole("button", { name: /^取\s*消$/ }).click();
    await expect(importFooter).toBeHidden();

    // 4. Drive Export Modal in 3-button waitingBrowserAuth state: collapses secondary actions into single More button
    snapshot = {
      ...snapshot,
      setup: { ...snapshot.setup, drive: false },
      runtime: {
        ...snapshot.runtime,
        drive: { connected: false, mode: "disabled", remote_folder: "ColabOne_Drive", pending: 0, error: "" },
        tasks: [],
      },
      auth_url: "https://accounts.google.com/o/oauth2/auth?mock=1",
      operation: {
        kind: "drive_connect",
        stage: "waiting_auth",
        title: "等待 Google 云盘授权",
        cancellable: true,
        cancelling: false,
      },
    };
    etagCounter += 1;
    await page.locator(".co-comfy-outputs-trigger").click();
    const drivePill = page.getByTestId("comfy-outputs-drive-pill");
    await expect(drivePill).toHaveClass(/is-warning/);
    await drivePill.click();
    const driveFooter = page.locator(".co-drive-modal-footer");
    await expect(driveFooter).toBeVisible();
    const moreBtn = driveFooter.getByTestId("drive-modal-mobile-more-trigger");
    await expect(moreBtn).toBeVisible();
    const driveMetrics = await assertSingleRowButtons(".co-drive-modal-footer");
    expect(driveMetrics.count).toBe(2);
    await moreBtn.click();
    await expect(page.getByRole("button", { name: "重新打开授权页" })).toBeVisible();
    await expect(page.getByRole("button", { name: "取消挂载" })).toBeVisible();
    // Pressing Escape dismisses ONLY the Drive modal More popover without closing the modal
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "重新打开授权页" })).toBeHidden();
    await expect(driveFooter).toBeVisible();
    await page.locator(".co-drive-export-modal-wrap .ant-modal-close").click();
    await expect(driveFooter).toBeHidden();
    await page.locator(".co-comfy-outputs-drawer-root .ant-drawer-close").click();

    // 5. LogDrawer footer: single-row without wrapping metadata text
    await page.locator(".co-logs-trigger").click();
    const logFooter = page.locator(".co-log-drawer-footer");
    await expect(logFooter).toBeVisible();
    const logFooterMetrics = await page.evaluate(() => {
      const footer = document.querySelector(".co-log-drawer-footer");
      const meta = footer?.querySelector(".co-log-drawer-footer-meta");
      const btn = footer?.querySelector("button");
      if (!footer || !meta || !btn) return null;
      const fr = footer.getBoundingClientRect();
      const mr = meta.getBoundingClientRect();
      const br = btn.getBoundingClientRect();
      return {
        metaHeight: mr.height,
        sameRow: Math.abs(mr.top + mr.height / 2 - (br.top + br.height / 2)) <= 4,
        noOverflow: br.right <= fr.right + 1 && mr.right <= br.left + 1,
      };
    });
    expect(logFooterMetrics).not.toBeNull();
    expect(logFooterMetrics!.metaHeight).toBeLessThanOrEqual(24);
    expect(logFooterMetrics!.sameRow).toBe(true);
    expect(logFooterMetrics!.noOverflow).toBe(true);
    await page.getByTestId("log-drawer-mobile-close").click();
  }

  // 6. Desktop (1280x800): keeps bottom Close button + inline Auto-Release pill and hides mobile icon button
  snapshot = {
    ...makeBaseSnapshot(true),
    workflows: [sampleWorkflow],
    runtime: {
      ...makeBaseSnapshot(true).runtime,
      comfyui: {
        ...makeBaseSnapshot(true).runtime.comfyui,
        auto_release: { enabled: false, state: "idle" },
      },
      tasks: [],
    },
  };
  etagCounter += 1;
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/");
  await page.locator(".co-nav-center").getByRole("button", { name: "ComfyUI" }).click();
  await page.locator(".co-comfy-wf-card-trigger").first().click();
  const desktopFooter = page.locator(
    ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
  );
  await expect(desktopFooter).toBeVisible();
  await expect(desktopFooter.locator(".co-comfy-drawer-close-btn")).toBeVisible();
  await expect(desktopFooter.getByTestId("comfy-drawer-auto-release")).toBeVisible();
  await expect(
    desktopFooter.getByTestId("comfy-drawer-auto-release-mobile-trigger"),
  ).toBeHidden();

  // 7. Regression guards: dark-mode armed styling, resize-closes-modal-popover, and 360px en-US non-truncating Cancel
  snapshot = {
    ...makeBaseSnapshot(true),
    workflows: [sampleWorkflow],
    settings: { ...makeBaseSnapshot(true).settings, locale: "en-US" },
    setup: { ...makeBaseSnapshot(true).setup, drive: false, drive_headless: false },
    runtime: {
      ...makeBaseSnapshot(true).runtime,
      drive: { connected: false, mode: "disabled", remote_folder: "ColabOne_Drive", pending: 0, error: "" },
      comfyui: {
        ...makeBaseSnapshot(true).runtime.comfyui,
        auto_release: { enabled: true, state: "armed" },
      },
      tasks: [],
    },
  };
  etagCounter += 1;
  await page.setViewportSize({ width: 360, height: 780 });
  await page.evaluate(() => {
    window.localStorage.setItem("colabone.locale", "en-US");
  });
  await page.goto("/");
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "dark";
  });
  await page.getByTestId("mobile-bottom-nav").getByRole("button", { name: "ComfyUI" }).click();
  await page.locator(".co-comfy-wf-card-trigger").first().click();

  const darkArmedStyle = await page.evaluate(() => {
    const btn = document.querySelector(
      '[data-testid="comfy-drawer-auto-release-mobile-trigger"]',
    );
    if (!btn) return null;
    const cs = window.getComputedStyle(btn);
    return { borderColor: cs.borderColor, backgroundColor: cs.backgroundColor };
  });
  expect(darkArmedStyle).not.toBeNull();
  // Must NOT fall back to default dark neutral #1d1f27 (rgb(29, 31, 39))
  expect(darkArmedStyle!.backgroundColor).not.toBe("rgb(29, 31, 39)");

  // Open Drive onboarding modal from armed switch click on 360px en-US: Cancel must stay untruncated & single-row
  await page.locator(".co-comfy-param-drawer-root .ant-drawer-close").click();
  await page.locator(".co-comfy-outputs-trigger").click();
  await page.getByTestId("comfy-outputs-drive-pill").click();
  const enDriveFooter = page.locator(".co-drive-modal-footer");
  await expect(enDriveFooter).toBeVisible();
  await assertSingleRowButtons(".co-drive-modal-footer");
  const cancelTruncated = await page.evaluate(() => {
    const cancelSpan = document.querySelector(
      ".co-drive-modal-footer > button:first-child > span:last-child",
    );
    if (!cancelSpan) return true;
    return cancelSpan.scrollWidth > cancelSpan.clientWidth + 1;
  });
  expect(cancelTruncated).toBe(false);

  // Open 3-button More popover on mobile then widen viewport to desktop: popover must auto-close (no orphan)
  snapshot = {
    ...snapshot,
    auth_url: "https://accounts.google.com/o/oauth2/auth?mock=2",
    operation: {
      kind: "drive_connect",
      stage: "waiting_auth",
      title: "Waiting for Drive authorization",
      cancellable: true,
      cancelling: false,
    },
  };
  etagCounter += 1;
  const moreBtn360 = enDriveFooter.getByTestId("drive-modal-mobile-more-trigger");
  await expect(moreBtn360).toBeVisible();
  await moreBtn360.click();
  const reopenBtn = page.getByRole("button", { name: "Reopen Consent Page" });
  await expect(reopenBtn).toBeVisible();
  await page.setViewportSize({ width: 1024, height: 780 });
  await expect(page.locator(".co-drive-mobile-more-popover")).toBeHidden();
});
