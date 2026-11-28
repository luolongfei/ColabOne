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
      has_proxy: true,
      masked_url: "socks5://127.0.0.1:1080",
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
      hf: true,
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

test("web console browser mode runs over HTTP without window.mygo, hides host-only sections, allows workflow shares / tunnel tokens / saved assign proxy test, and handles 401 session expiry", async ({
  page,
}) => {
  const baseSnapshot = makeBaseSnapshot(true);
  const snapshot = {
    ...baseSnapshot,
    workflows: [
      {
        id: "wf-demo",
        name: "Demo Studio Workflow",
        description: "Test workflow for web console sharing",
        source: "builtin" as const,
        min_vram_mib: 8192,
        node_count: 4,
        fields: [],
      },
    ],
  };
  let expireSession = false;
  const commandCalls: Array<{
    action: string;
    payload?: Record<string, unknown>;
  }> = [];
  let workflowShares: Array<Record<string, unknown>> = [];

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
        can_manage_workflow_shares: true,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_reveal_tunnel_tokens: true,
        can_test_saved_assign_proxy: true,
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
    const reqBody = route.request().postDataJSON() as {
      action?: string;
      payload?: Record<string, unknown>;
    };
    if (reqBody?.action) {
      commandCalls.push({
        action: reqBody.action,
        payload: reqBody.payload,
      });
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
    if (reqBody?.action === "comfyui.canvas.tunnel_token") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ token: "eyJ-canvas-tunnel-token-web" }),
      });
      return;
    }
    if (reqBody?.action === "api.tunnel_token") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ token: "eyJ-api-tunnel-token-web" }),
      });
      return;
    }
    if (reqBody?.action === "settings.assign_proxy.test") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ok: true,
          latency_ms: 42,
          masked_url: "socks5://127.0.0.1:1080",
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

  await page.route("**/api/output-shares", async (route) => {
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
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
        items: [],
      }),
    });
  });

  await page.route("**/api/workflow-shares**", async (route) => {
    if (expireSession) {
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "Unauthorized" }),
      });
      return;
    }
    const req = route.request();
    const url = new URL(req.url());
    const method = req.method();
    const buildState = () => ({
      active_count: workflowShares.filter((s) => s.status === "active").length,
      total_active_sessions: 0,
      shares: workflowShares,
    });
    if (url.pathname === "/api/workflow-shares" && method === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(buildState()),
      });
      return;
    }
    if (url.pathname === "/api/workflow-shares" && method === "POST") {
      const body = (req.postDataJSON() || {}) as {
        label?: string;
        title?: string;
        workflow_ids?: string[];
        ttl_mode?: string;
      };
      const nowSec = Math.floor(Date.now() / 1000);
      const created = {
        id: "wfshareweb01",
        label: body.label || "Mobile Share",
        title: body.title || "",
        workflow_ids: body.workflow_ids || ["wf-demo"],
        ttl_mode: body.ttl_mode || "24h",
        single_use: body.ttl_mode === "once",
        status: "active",
        created_at: nowSec,
        expires_at: nowSec + 86400,
        active_sessions: 0,
        output_count: 0,
        uses_public_host: false,
        url: "http://192.168.1.50:3232/share/s_web_token_1",
        local_url: "http://127.0.0.1:3232/share/s_web_token_1",
        can_copy_link: true,
      };
      workflowShares = [created, ...workflowShares];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(created),
      });
      return;
    }
    const revokeMatch = url.pathname.match(/^\/api\/workflow-shares\/([^/]+)\/revoke$/);
    if (revokeMatch && method === "POST") {
      const targetId = revokeMatch[1];
      workflowShares = workflowShares.map((item) =>
        item.id === targetId
          ? { ...item, status: "revoked", can_copy_link: false }
          : item,
      );
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(buildState()),
      });
      return;
    }
    const deleteMatch = url.pathname.match(/^\/api\/workflow-shares\/([^/]+)$/);
    if (deleteMatch && method === "DELETE") {
      const targetId = deleteMatch[1];
      workflowShares = workflowShares.filter((item) => item.id !== targetId);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(buildState()),
      });
      return;
    }
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "Not found" }),
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

  // Settings host secrets (HF Token + Assign Proxy) remain non-revealable in web mode,
  // while testing the saved Assign Proxy is allowed (saved-only, disabled when unsaved draft exists)
  const hfToggle = page
    .locator(".co-secret-field", { has: page.locator("#settings-hf-token") })
    .locator(".co-secret-toggle");
  await expect(hfToggle).toHaveAttribute("aria-label", "显示密钥");
  await hfToggle.click();
  await expect(page.locator("#settings-hf-token")).toHaveValue("");
  await page.getByRole("button", { name: /^编\s*辑$/ }).click();
  const proxyToggle = page
    .locator(".co-secret-field", {
      has: page.locator("#settings-assign-proxy"),
    })
    .locator(".co-secret-toggle");
  await expect(proxyToggle).toHaveAttribute("aria-label", "显示密钥");
  await proxyToggle.click();
  await expect(page.locator("#settings-assign-proxy")).toHaveValue("");
  const proxyTestBtn = page.getByTestId("settings-assign-proxy-test-btn");
  await expect(proxyTestBtn).toBeVisible();
  await expect(proxyTestBtn).toBeEnabled();
  await proxyTestBtn.click();
  await expect
    .poll(() =>
      commandCalls.filter((c) => c.action === "settings.assign_proxy.test").length,
    )
    .toBe(1);
  const proxyTestCall = commandCalls.find(
    (c) => c.action === "settings.assign_proxy.test",
  );
  expect(proxyTestCall?.payload || {}).toEqual({});
  await page.locator("#settings-assign-proxy").fill("socks5://10.0.0.2:1080");
  await expect(proxyTestBtn).toBeDisabled();
  await page.getByTestId("settings-assign-proxy-test-wrap").hover();
  await expect(
    page.getByRole("tooltip", { name: /网页模式下请先保存代理地址/ }),
  ).toBeVisible();

  // ComfyUI Studio exposes Workflow Sharing in Web Mode (hiding desktop-only Web Console settings buttons)
  await page.getByRole("button", { name: "ComfyUI" }).click();
  const shareBtn = page.getByTestId("comfy-share-workflows-btn");
  await expect(shareBtn).toBeVisible();
  await shareBtn.click();
  await expect(page.getByTestId("workflow-share-modal")).toBeVisible();
  await expect(page.getByTestId("workflow-share-open-settings-btn")).toHaveCount(0);
  await expect(page.getByTestId("workflow-share-local-origin-hint")).toContainText(
    "请在宿主机客户端设置中配置公网域名",
  );
  await page.getByTestId("workflow-share-label-input").fill("Mobile Client Link");
  await page.getByTestId("workflow-share-create-btn").click();
  await expect(page.getByTestId("workflow-share-created-card")).toBeVisible();
  await expect(page.getByTestId("workflow-share-created-card")).toContainText(
    "http://192.168.1.50:3232/share/s_web_token_1",
  );
  await page.getByRole("tab", { name: /管理分享/ }).click();
  await expect(page.getByTestId("workflow-share-card-wfshareweb01")).toBeVisible();
  await page.getByTestId("workflow-share-revoke-wfshareweb01").click();
  await page.locator(".ant-popconfirm .ant-btn-primary, .ant-popover .ant-btn-primary").last().click();
  await expect(page.getByTestId("workflow-share-delete-wfshareweb01")).toBeVisible();
  await page.getByTestId("workflow-share-delete-wfshareweb01").click();
  await expect(page.getByTestId("workflow-share-empty-state")).toBeVisible();
  await page
    .getByRole("dialog", { name: "分享工作流" })
    .getByRole("button", { name: "Close" })
    .click();
  await expect(page.getByTestId("workflow-share-modal")).toBeHidden();

  // ComfyUI canvas shows "Open Canvas in New Tab" instead of desktop native window button
  await page.getByRole("tab", { name: /原生画布/ }).click();
  await expect(page.getByRole("button", { name: "在新标签页打开画布" })).toBeVisible();
  await expect(page.getByRole("button", { name: "在应用窗口中打开" })).toHaveCount(0);
  // Web mode CAN reveal/copy business Tunnel Tokens (can_reveal_tunnel_tokens: true)
  const comfyTokenCopyBtn = page.locator(".co-comfy-canvas-grid .co-secret-copy-btn");
  await expect(comfyTokenCopyBtn).toBeEnabled();
  await comfyTokenCopyBtn.click();
  await expect
    .poll(() =>
      commandCalls.some((c) => c.action === "comfyui.canvas.tunnel_token"),
    )
    .toBe(true);
  await page.getByRole("button", { name: "对外 API" }).click();
  const apiTokenCopyBtn = page.locator(".co-api-connection .co-secret-copy-btn");
  await expect(apiTokenCopyBtn).toBeEnabled();
  await apiTokenCopyBtn.click();
  await expect
    .poll(() => commandCalls.some((c) => c.action === "api.tunnel_token"))
    .toBe(true);

  // Trigger 401 session expiration and confirm ONLY the session expired banner appears (no duplicate error banner)
  expireSession = true;
  await expect(page.getByTestId("web-session-expired-banner")).toBeVisible({
    timeout: 5000,
  });
  await page.waitForTimeout(2600);
  await expect(page.locator(".co-banners .ant-alert-error")).toHaveCount(0);
  const commandActions = commandCalls.map((c) => c.action);
  expect(commandActions).not.toContain("auth.colab.auto");
  expect(commandActions).not.toContain("settings.hf_token");
  expect(commandActions).not.toContain("settings.assign_proxy");
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
        can_manage_workflow_shares: true,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_reveal_tunnel_tokens: true,
        can_test_saved_assign_proxy: true,
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

  await page.route("**/api/output-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
        items: [],
      }),
    });
  });

  await page.route("**/api/workflow-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
      }),
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
        can_manage_workflow_shares: true,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_reveal_tunnel_tokens: true,
        can_test_saved_assign_proxy: true,
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

  await page.route("**/api/output-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
        items: [],
      }),
    });
  });

  await page.route("**/api/workflow-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
      }),
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

    // 1. Workflow Param Drawer (idle): bottom-left Close button visible, desktop pill hidden, compact icon trigger + full-width Run stay single-row
    await page.locator(".co-comfy-wf-card-trigger").first().click();
    const paramFooter = page.locator(
      ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
    );
    await expect(paramFooter).toBeVisible();
    const paramCloseBtn = paramFooter.getByTestId("comfy-param-drawer-close-btn");
    await expect(paramCloseBtn).toBeVisible();
    await expect(paramFooter.getByTestId("comfy-drawer-auto-release")).toBeHidden();
    const arTrigger = paramFooter.getByTestId(
      "comfy-drawer-auto-release-mobile-trigger",
    );
    await expect(arTrigger).toBeVisible();

    const idleMetrics = await assertSingleRowButtons(
      ".co-comfy-param-drawer-root .co-comfy-param-drawer-footer",
    );
    expect(idleMetrics.count).toBe(3);
    expect(idleMetrics.rects[0].text).toMatch(/关\s*闭|Close/);

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

    // 2. Workflow Param Drawer with active task (Cancel Task visible): 4 controls (Close + Auto-Release + Cancel + Run) remain single-row without wrapping
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
    expect(activeTaskMetrics.count).toBe(4);
    expect(activeTaskMetrics.rects[0].text).toMatch(/关\s*闭|Close/);

    // Close drawer via bottom-left Close button
    await paramCloseBtn.click();
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
    const outputsCloseBtn = page.getByTestId("comfy-outputs-drawer-close-btn");
    await expect(outputsCloseBtn).toBeVisible();
    await outputsCloseBtn.click();
    await expect(outputsCloseBtn).toBeHidden();

    // 5. LogDrawer footer: single-row with bottom-left Close button, metadata, and Follow button
    await page.locator(".co-logs-trigger").click();
    const logFooter = page.locator(".co-log-drawer-footer");
    await expect(logFooter).toBeVisible();
    const logCloseBtn = logFooter.getByTestId("log-drawer-footer-close");
    await expect(logCloseBtn).toBeVisible();
    const logFooterMetrics = await page.evaluate(() => {
      const footer = document.querySelector(".co-log-drawer-footer");
      const closeBtn = footer?.querySelector(".co-log-drawer-close-btn");
      const meta = footer?.querySelector(".co-log-drawer-footer-meta");
      const btn = footer?.querySelector(".co-log-drawer-follow-btn");
      if (!footer || !closeBtn || !meta || !btn) return null;
      const fr = footer.getBoundingClientRect();
      const cr = closeBtn.getBoundingClientRect();
      const mr = meta.getBoundingClientRect();
      const br = btn.getBoundingClientRect();
      return {
        metaHeight: mr.height,
        closeOnLeft: cr.left >= fr.left - 1 && cr.right <= mr.left + 1,
        sameRow:
          Math.abs(cr.top + cr.height / 2 - (br.top + br.height / 2)) <= 4 &&
          Math.abs(mr.top + mr.height / 2 - (br.top + br.height / 2)) <= 4,
        noOverflow: br.right <= fr.right + 1 && mr.right <= br.left + 1,
      };
    });
    expect(logFooterMetrics).not.toBeNull();
    expect(logFooterMetrics!.metaHeight).toBeLessThanOrEqual(24);
    expect(logFooterMetrics!.closeOnLeft).toBe(true);
    expect(logFooterMetrics!.sameRow).toBe(true);
    expect(logFooterMetrics!.noOverflow).toBe(true);
    await logCloseBtn.click();
    await expect(logFooter).toBeHidden();

    // 5b. ModelDrawer footer: bottom-left Close button visible and closes drawer cleanly
    await bottomNav.getByRole("button", { name: "对外 API" }).click();
    await page
      .locator(".co-api-model-slot")
      .getByRole("button", { name: /选择模型|切换模型/ })
      .click();
    const modelCloseBtn = page.getByTestId("model-drawer-close-btn");
    await expect(modelCloseBtn).toBeVisible();
    await modelCloseBtn.click();
    await expect(modelCloseBtn).toBeHidden();
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

test("mobile iPhone viewports keep running status bar in 2 compact rows and prevent long drawer badges from causing horizontal overflow", async ({
  page,
}) => {
  const longSpecsWorkflow = {
    id: "digital-human-infinitetalk",
    name: "口播数字人 · 长视频推荐版",
    description: "内置口播数字人工作流",
    source: "builtin",
    min_vram_mib: 24576,
    recommended_gpu: "G4 96 GB / H100 80 GB / A100 80 GB",
    license: "Apache-2.0 / CC-BY-4.0 / GPL-3.0",
    non_commercial: true,
    models: [
      { name: "m1.safetensors", size_bytes: 18 * 1024 ** 3 },
      { name: "m2.safetensors", size_bytes: 10 * 1024 ** 3 },
    ],
    custom_nodes: ["node-a", "node-b"],
    fields: [
      {
        key: "pipeline.audio_source",
        label: "音频来源",
        type: "select",
        default: "tts",
        options: [
          { value: "tts", label: "文字转语音 (TTS)" },
          { value: "upload", label: "上传音频" },
        ],
      },
      {
        key: "pipeline.tts_engine",
        label: "语音引擎",
        type: "select",
        default: "breeze_tts2",
        options: [
          {
            value: "breeze_tts2",
            label: "Breeze TTS 2（3B+ · 中英双语情绪导演 · 仅非商用）",
          },
        ],
      },
      {
        key: "prompt",
        label: "口播文案",
        type: "textarea",
        default: "你好，欢迎体验 ColabOne。",
        required: true,
      },
    ],
  };

  const ttsEngineLongLicense = {
    id: "breeze_tts2",
    name: "Fish Audio Drama 3（35B MoE · 44.1kHz · 96G 旗舰 · 仅非商用）",
    description: "超长引擎徽标与协议测试",
    sample_rate: 44100,
    min_vram_mib: 24576,
    min_disk_mib: 32768,
    license: "BreezeBlue Research & Non-Commercial License",
    non_commercial: true,
    supports_voice_clone: true,
    supports_voice_design: true,
    supports_emotion_ref: true,
  };

  const oversizedModel = {
    id: "qwen3-super-long-model",
    name: "Qwen3-235B-A22B-Instruct-2507-UD-Q4_K_XL-Super-Long-Display-Name",
    repo: "unsloth/Qwen3-235B-A22B-Instruct-2507-GGUF",
    filename:
      "Qwen3-235B-A22B-Instruct-2507-UD-Q4_K_XL-00001-of-00004.gguf",
    size: 135 * 1024 ** 3,
    downloaded: false,
    loaded: false,
    common: true,
    description: "用于验证超长徽标与模型名称在手机端抽屉内不溢出容器",
  };

  let idlePhase: "counting" | "active" = "counting";
  const buildSnapshot = () => ({
    ...makeBaseSnapshot(true),
    workflows: [longSpecsWorkflow],
    tts_engines: [ttsEngineLongLicense],
    models: [oversizedModel],
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
      gpu: {
        name: "NVIDIA RTX PRO 6000 Blackwell",
        total_mib: 98304,
        used_mib: 4200,
      },
      location: {
        country: "US",
        country_name: "United States",
        region: "Ohio",
        zone: "us-east4-a",
      },
      idle: {
        phase: idlePhase,
        remaining_seconds: 1452,
        timeout_seconds: 1800,
        countdown_seconds: 60,
        server_time: Math.floor(Date.now() / 1000),
      },
    },
  });

  await page.route("**/api/web/capabilities", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        runtime: "web",
        version: "0.1.23",
        can_open_native_canvas_window: false,
        can_standalone_log_window: false,
        can_manage_web_console: false,
        can_manage_workflow_shares: true,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_reveal_tunnel_tokens: true,
        can_test_saved_assign_proxy: true,
        can_loopback_oauth: false,
        can_download_output: true,
      }),
    });
  });

  await page.route("**/api/snapshot", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { ETag: `"iphone-polish-${idlePhase}"` },
      body: JSON.stringify(buildSnapshot()),
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

  await page.route("**/api/output-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
        items: [],
      }),
    });
  });

  await page.route("**/api/workflow-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
      }),
    });
  });

  for (const vp of [
    { width: 375, height: 812 },
    { width: 440, height: 956 },
  ]) {
    idlePhase = "counting";
    await page.setViewportSize(vp);
    await page.goto("/");

    // 1. Running-instance status bar stays strictly 2 compact rows (Row 1: status/GPU/region + idle pill; Row 2: single-line billing capsule)
    await expect(page.locator(".co-idle-timer")).toBeVisible();
    await expect(page.locator(".co-billing-strip")).toBeVisible();
    await expect(page.locator(".co-location-chip")).toBeVisible();

    const measureStatusBar = () =>
      page.evaluate(() => {
        const bar = document.querySelector(".co-status-bar")?.getBoundingClientRect();
        const rt = document.querySelector(".co-runtime-tag")?.getBoundingClientRect();
        const gpu = document.querySelector(".co-gpu-name")?.getBoundingClientRect();
        const loc = document.querySelector(".co-location-chip")?.getBoundingClientRect();
        const idle = document.querySelector(".co-idle-timer")?.getBoundingClientRect();
        const strip = document.querySelector(".co-billing-strip")?.getBoundingClientRect();
        const pill = document.querySelector(".co-billing-pill")?.getBoundingClientRect();
        const rate = document.querySelector(".co-billing-rate")?.getBoundingClientRect();
        const bal = document.querySelector(".co-billing-balance")?.getBoundingClientRect();
        const end = document.querySelector(".co-endurance-tag")?.getBoundingClientRect();
        if (!bar || !rt || !gpu || !loc || !idle || !strip || !pill || !rate || !bal || !end) {
          return null;
        }
        return {
          barHeight: Math.round(bar.height),
          row1Aligned:
            Math.abs(rt.top - idle.top) <= 6 &&
            Math.abs(rt.top - loc.top) <= 6 &&
            loc.left - gpu.right <= 12 &&
            loc.right <= idle.left + 1 &&
            idle.right <= window.innerWidth + 1,
          stripBelowRow1: strip.top >= rt.bottom - 1,
          stripHeight: Math.round(strip.height),
          stripRightOk: strip.right <= window.innerWidth + 1,
          row2SingleLine:
            Math.abs(pill.top - rate.top) <= 4 &&
            Math.abs(pill.top - bal.top) <= 4 &&
            Math.abs(pill.top - end.top) <= 4,
          docOverflow: document.documentElement.scrollWidth - window.innerWidth,
        };
      });

    const countingBar = await measureStatusBar();
    expect(countingBar).not.toBeNull();
    expect(countingBar!.barHeight).toBeLessThanOrEqual(74);
    expect(countingBar!.row1Aligned).toBe(true);
    expect(countingBar!.stripBelowRow1).toBe(true);
    expect(countingBar!.stripHeight).toBeLessThanOrEqual(30);
    expect(countingBar!.stripRightOk).toBe(true);
    expect(countingBar!.row2SingleLine).toBe(true);
    expect(countingBar!.docOverflow).toBeLessThanOrEqual(0);

    // Also verify active/busy paused idle state stays 2 compact rows without line explosion
    idlePhase = "active";
    await page.reload();
    await expect(page.locator(".co-idle-timer")).toContainText("暂停释放");
    const busyBar = await measureStatusBar();
    expect(busyBar).not.toBeNull();
    expect(busyBar!.barHeight).toBeLessThanOrEqual(74);
    expect(busyBar!.row1Aligned).toBe(true);
    expect(busyBar!.row2SingleLine).toBe(true);
    expect(busyBar!.docOverflow).toBeLessThanOrEqual(0);

    // 2. ComfyUI Param Drawer with ultra-long TTS license & workflow GPU/license tags: zero horizontal overflow
    const bottomNav = page.getByTestId("mobile-bottom-nav");
    await bottomNav.getByRole("button", { name: "ComfyUI" }).click();
    await page.locator(".co-comfy-wf-card-trigger").first().click();
    await expect(page.locator(".co-comfy-param-drawer-body")).toBeVisible();
    await page.locator(".co-comfy-advanced-toggle").click();
    await expect(page.locator(".co-comfy-specs-box")).toBeVisible();

    const comfyDrawerCheck = await page.evaluate(() => {
      const body = document.querySelector(
        ".co-comfy-param-drawer-root .ant-drawer-body",
      ) as HTMLElement | null;
      if (!body) return null;
      const bodyRect = body.getBoundingClientRect();
      const sourceTag = document.querySelector(
        ".co-comfy-param-drawer-root .co-comfy-source-tag",
      );
      const sourceTagHeight = sourceTag
        ? Math.round(sourceTag.getBoundingClientRect().height)
        : 0;
      const tags = Array.from(body.querySelectorAll(".ant-tag"));
      const overflowingTags = tags
        .map((el) => ({
          text: (el.textContent || "").trim(),
          rightDelta: Math.round(el.getBoundingClientRect().right - bodyRect.right),
        }))
        .filter((item) => item.rightDelta > 1);
      return {
        docOverflow: document.documentElement.scrollWidth - window.innerWidth,
        bodyOverflow: body.scrollWidth - body.clientWidth,
        sourceTagHeight,
        tagCount: tags.length,
        overflowingTags,
      };
    });
    expect(comfyDrawerCheck).not.toBeNull();
    expect(comfyDrawerCheck!.sourceTagHeight).toBeLessThanOrEqual(26);
    expect(comfyDrawerCheck!.tagCount).toBeGreaterThanOrEqual(4);
    expect(comfyDrawerCheck!.docOverflow).toBeLessThanOrEqual(0);
    expect(comfyDrawerCheck!.bodyOverflow).toBeLessThanOrEqual(0);
    expect(comfyDrawerCheck!.overflowingTags).toEqual([]);
    await page.locator(".co-comfy-param-drawer-root .ant-drawer-close").click();

    // 3. ModelDrawer with ultra-long model name & fit/disk badges: zero horizontal overflow
    await bottomNav.getByRole("button", { name: "对外 API" }).click();
    await page
      .locator(".co-api-model-slot")
      .getByRole("button", { name: /选择模型|切换模型/ })
      .click();
    await expect(
      page.locator(".co-model-drawer-root .ant-drawer-body"),
    ).toBeVisible();

    const modelDrawerCheck = await page.evaluate(() => {
      const body = document.querySelector(
        ".co-model-drawer-root .ant-drawer-body",
      ) as HTMLElement | null;
      if (!body) return null;
      const bodyRect = body.getBoundingClientRect();
      const tags = Array.from(body.querySelectorAll(".ant-tag"));
      const overflowingTags = tags
        .map((el) => ({
          text: (el.textContent || "").trim(),
          rightDelta: Math.round(el.getBoundingClientRect().right - bodyRect.right),
        }))
        .filter((item) => item.rightDelta > 1);
      return {
        docOverflow: document.documentElement.scrollWidth - window.innerWidth,
        bodyOverflow: body.scrollWidth - body.clientWidth,
        tagCount: tags.length,
        overflowingTags,
      };
    });
    expect(modelDrawerCheck).not.toBeNull();
    expect(modelDrawerCheck!.tagCount).toBeGreaterThanOrEqual(1);
    expect(modelDrawerCheck!.docOverflow).toBeLessThanOrEqual(0);
    expect(modelDrawerCheck!.bodyOverflow).toBeLessThanOrEqual(0);
    expect(modelDrawerCheck!.overflowingTags).toEqual([]);
    await page.locator(".co-model-drawer-root .ant-drawer-close").click();
  }
});

test("mobile task dock defaults collapsed with thin progress bar and billing ticker rotates rate, run cost, and duration", async ({
  page,
}) => {
  let activeTaskId = "task-wf-1";
  let activeTaskKind = "comfyui_run";
  let activeTaskProgress = 0.13;
  let activeTaskMessage =
    "下载模型 2/6 · qwen3vl_32b_minimax_h3_int8_convrot.safetensors";
  let activeTaskHasDownload = true;

  const buildSnapshot = () => {
    const nowSec = Date.now() / 1000;
    const base = makeBaseSnapshot(true);
    return {
      ...base,
      account: {
        ...base.account,
        logged_in: true,
        balance: 1858.97,
        rate: 11.2,
        assignments: 1,
        checked_at: nowSec,
        current_run: {
          active: true,
          started_at: nowSec - 252,
          start_balance: 1859.75,
          end_balance: 1858.97,
          consumed: 0.78,
          unsettled: 0,
          topup: 0,
          last_sample_at: nowSec,
          last_rate: 11.2,
          peak_rate: 11.2,
          max_instances: 1,
          samples: 12,
        },
      },
      runtime: {
        ...base.runtime,
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
          total_mib: 98304,
          used_mib: 4200,
        },
        location: {
          country: "IN",
          country_name: "India",
          region: "Mumbai",
          zone: "asia-south1-a",
        },
        idle: {
          phase: "active",
          remaining_seconds: 1800,
          timeout_seconds: 1800,
          countdown_seconds: 60,
          server_time: Math.floor(nowSec),
        },
        tasks: activeTaskId
          ? [
              {
                id: activeTaskId,
                kind: activeTaskKind,
                state: "running",
                progress: activeTaskProgress,
                message: activeTaskMessage,
                created_at: Math.floor(nowSec - 252),
                ...(activeTaskHasDownload
                  ? {
                      download: {
                        phase: "downloading",
                        speed_bps: 222.3 * 1024 ** 2,
                        downloaded_bytes: 42.4 * 1024 ** 3,
                        total_bytes: 87.7 * 1024 ** 3,
                        eta_seconds: 209,
                        connections: 16,
                        threads: 16,
                        file_index: 2,
                        file_count: 6,
                      },
                    }
                  : {}),
              },
            ]
          : [],
      },
    };
  };

  await page.route("**/api/web/capabilities", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        runtime: "web",
        version: "0.1.24",
        can_open_native_canvas_window: false,
        can_standalone_log_window: false,
        can_manage_web_console: false,
        can_manage_workflow_shares: true,
        can_manage_close_behavior: false,
        can_manage_shortcuts: false,
        can_self_update: false,
        can_migrate: false,
        can_reveal_host_secrets: false,
        can_reveal_tunnel_tokens: true,
        can_test_saved_assign_proxy: true,
        can_loopback_oauth: false,
        can_download_output: true,
      }),
    });
  });

  await page.route("**/api/snapshot", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { ETag: `"mobile-dock-${activeTaskId}-${activeTaskProgress}"` },
      body: JSON.stringify(buildSnapshot()),
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

  await page.route("**/api/output-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
        items: [],
      }),
    });
  });

  await page.route("**/api/workflow-shares", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        active_count: 0,
        total_active_sessions: 0,
        shares: [],
      }),
    });
  });

  // 1. Mobile viewport (375x812): dock defaults collapsed, stays ultra-compact, and expands/collapses cleanly
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto("/");

  const dock = page.getByTestId("dock-task");
  await expect(dock).toBeVisible();
  await expect(dock).toHaveAttribute("data-collapsed", "true");
  await expect(page.getByTestId("dock-task-expand-btn")).toBeVisible();
  await expect(page.getByTestId("dock-task-mini-percent")).toHaveText("13%");
  await expect(page.getByTestId("dock-task-mini-stat")).toContainText("222.3 MiB/s");

  const collapsedMetrics = await page.evaluate(() => {
    const dockRect = document
      .querySelector('[data-testid="dock-task"]')
      ?.getBoundingClientRect();
    const fill = document.querySelector(
      ".co-task-mini-progress-fill",
    ) as HTMLElement | null;
    return {
      height: dockRect ? Math.round(dockRect.height) : 0,
      fillWidth: fill?.style.width ?? "",
      docOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  expect(collapsedMetrics.height).toBeGreaterThan(28);
  expect(collapsedMetrics.height).toBeLessThanOrEqual(56);
  expect(collapsedMetrics.fillWidth).toBe("13%");
  expect(collapsedMetrics.docOverflow).toBeLessThanOrEqual(0);

  // Expanding reveals full progress stats, cancel button, and collapse button
  await page.getByTestId("dock-task-expand-btn").click();
  await expect(dock).toHaveAttribute("data-collapsed", "false");
  await expect(page.getByTestId("dock-task-collapse-btn")).toBeVisible();
  await expect(page.getByTestId("dock-task-elapsed")).toBeVisible();
  await expect(dock).toContainText("42.4 GB / 87.7 GB");
  await expect(dock).toContainText("16/16 个连接");
  await expect(dock.getByRole("button", { name: /取\s*消/ })).toBeVisible();

  const expandedBox = await dock.boundingBox();
  expect(expandedBox).not.toBeNull();
  expect(expandedBox!.height).toBeGreaterThan(collapsedMetrics.height + 50);

  // Collapsing back restores the compact single-row bar
  await page.getByTestId("dock-task-collapse-btn").click();
  await expect(dock).toHaveAttribute("data-collapsed", "true");

  // Re-expanding and starting a brand-new task resets back to default collapsed on mobile
  await page.getByTestId("dock-task-expand-btn").click();
  await expect(dock).toHaveAttribute("data-collapsed", "false");
  activeTaskId = "task-wf-2";
  activeTaskProgress = 0.42;
  activeTaskMessage = "采样渲染 8/20";
  await page.locator(".co-nav-circle-btn").first().click();
  await expect(dock).toHaveAttribute("data-collapsed", "true");
  await expect(page.getByTestId("dock-task-mini-percent")).toHaveText("42%");

  // 2. Mobile billing strip keeps 2-row compact header and rotates rate, session consumption, and duration every 3s
  const statusBarMetrics = await page.evaluate(() => {
    const bar = document.querySelector(".co-status-bar")?.getBoundingClientRect();
    const loc = document.querySelector(".co-location-chip")?.getBoundingClientRect();
    const idle = document.querySelector(".co-idle-timer")?.getBoundingClientRect();
    return {
      barHeight: bar ? Math.round(bar.height) : 0,
      sameRow: Boolean(loc && idle && Math.abs(loc.top - idle.top) <= 6),
      docOverflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  expect(statusBarMetrics.barHeight).toBeLessThanOrEqual(74);
  expect(statusBarMetrics.sameRow).toBe(true);
  expect(statusBarMetrics.docOverflow).toBeLessThanOrEqual(0);

  const ticker = page.getByTestId("billing-bar-rate-ticker");
  const consumedSlide = page.getByTestId("billing-bar-consumed-slide");
  await expect(ticker).toBeVisible();
  await expect(ticker).toHaveAttribute("data-slide-count", "3");
  await expect(page.getByTestId("billing-bar-run")).toBeHidden();
  await expect(consumedSlide).toContainText("本次");
  await expect(consumedSlide).toContainText(/0\.(?:7[89]|8\d) CU/);
  await expect(ticker).toHaveAttribute("data-active-slide", "consumed", {
    timeout: 9500,
  });
  await expect(consumedSlide).toHaveAttribute("data-active", "true");

  // 3. Indeterminate non-percentage task with whitespace-only message falls back cleanly to task title & compact elapsed
  activeTaskId = "task-load-1";
  activeTaskKind = "load";
  activeTaskProgress = 0;
  activeTaskMessage = "   ";
  activeTaskHasDownload = false;
  await page.locator(".co-nav-circle-btn").first().click();
  await expect(dock).toHaveAttribute("data-collapsed", "true");
  await expect(page.getByTestId("dock-task-expand-btn")).toContainText(
    "正在装载模型到远端显存",
  );
  await expect(page.getByTestId("dock-task-mini-percent")).toHaveCount(0);
  await expect(page.getByTestId("dock-task-mini-stat")).toContainText("4 分");
  await expect(
    dock.locator(".co-task-mini-progress-fill.is-indeterminate"),
  ).toBeVisible();

  // Resizing across the 680px breakpoint without manual override follows viewport default automatically
  await page.setViewportSize({ width: 1280, height: 860 });
  await expect(dock).toHaveAttribute("data-collapsed", "false");
  await page.setViewportSize({ width: 375, height: 812 });
  await expect(dock).toHaveAttribute("data-collapsed", "true");

  // 4. Desktop viewport (1280x860): new task defaults expanded and still offers collapse/expand toggle
  activeTaskId = "task-wf-3";
  activeTaskKind = "comfyui_run";
  activeTaskProgress = 0.64;
  activeTaskMessage = "下载模型 4/6 · model.safetensors";
  activeTaskHasDownload = true;
  await page.setViewportSize({ width: 1280, height: 860 });
  await page.goto("/");
  await expect(dock).toHaveAttribute("data-collapsed", "false");
  await expect(page.getByTestId("dock-task-collapse-btn")).toBeVisible();
  await page.getByTestId("dock-task-collapse-btn").click();
  await expect(dock).toHaveAttribute("data-collapsed", "true");
  await expect(page.getByTestId("dock-task-mini-percent")).toHaveText("64%");
  await page.getByTestId("dock-task-expand-btn").click();
  await expect(dock).toHaveAttribute("data-collapsed", "false");
});

test("mobile touch taps suppress hover tooltips while long-press reveals title without triggering click", async ({
  browser,
}) => {
  const mobileCtx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    hasTouch: true,
    isMobile: true,
    locale: "zh-CN",
  });
  try {
    const page = await mobileCtx.newPage();
    const cdp = await mobileCtx.newCDPSession(page);

    await page.route("**/api/web/capabilities", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          runtime: "web",
          version: "0.1.27",
          can_open_native_canvas_window: false,
          can_standalone_log_window: false,
          can_manage_web_console: false,
          can_manage_workflow_shares: true,
          can_manage_close_behavior: false,
          can_manage_shortcuts: false,
          can_self_update: false,
          can_migrate: false,
          can_reveal_host_secrets: false,
          can_reveal_tunnel_tokens: true,
          can_test_saved_assign_proxy: true,
          can_loopback_oauth: false,
          can_download_output: true,
        }),
      });
    });

    const buildSnap = () => {
      const base = makeBaseSnapshot(true);
      return {
        ...base,
        account: {
          ...base.account,
          logged_in: true,
          balance: 120.5,
          rate: 2.0,
          assignments: 1,
          checked_at: Date.now() / 1000,
        },
        runtime: {
          ...base.runtime,
          state: "stopped",
          gpu: undefined,
        },
        api: {
          ...base.api,
          running: false,
        },
      };
    };

    await page.route("**/api/snapshot", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(buildSnap()),
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

    await page.route("**/api/output-shares", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          active_count: 0,
          total_active_sessions: 0,
          shares: [],
          items: [],
        }),
      });
    });

    await page.route("**/api/workflow-shares", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          active_count: 0,
          total_active_sessions: 0,
          shares: [],
        }),
      });
    });

    await page.goto("/", { waitUntil: "networkidle" });
    await expect(page.getByTestId("nav-privacy-toggle")).toBeVisible();

    const dispatchTap = async (x: number, y: number, holdMs = 70) => {
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchStart",
        touchPoints: [{ x, y, id: 1 }],
      });
      await page.waitForTimeout(holdMs);
      await cdp.send("Input.dispatchTouchEvent", {
        type: "touchEnd",
        touchPoints: [],
      });
    };

    const centerOf = async (locator: ReturnType<typeof page.locator>) => {
      await locator.scrollIntoViewIfNeeded();
      const box = await locator.boundingBox();
      expect(box).not.toBeNull();
      return {
        x: Math.round(box!.x + box!.width / 2),
        y: Math.round(box!.y + box!.height / 2),
      };
    };

    // 1. Quick tap on Privacy toggle executes action without popping any tooltip
    const privacyBtn = page.getByTestId("nav-privacy-toggle");
    const privacyBefore = await privacyBtn.getAttribute("aria-pressed");
    const pCenter = await centerOf(privacyBtn);
    await dispatchTap(pCenter.x, pCenter.y, 70);
    await page.waitForTimeout(350);
    await expect(privacyBtn).toHaveAttribute(
      "aria-pressed",
      privacyBefore === "true" ? "false" : "true",
    );
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 2. Quick tap on BillingCard trend button pops zero tooltips
    const trendBtn = page.locator(".co-bc-trend-btn").first();
    const trendCenter = await centerOf(trendBtn);
    await dispatchTap(trendCenter.x, trendCenter.y, 70);
    await page.waitForTimeout(350);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 3. Quick tap on disabled-compatible-wrapper button on Public API pops zero tooltips
    await page
      .locator(".co-mobile-tab-item")
      .filter({ hasText: /对外 API|Public API/ })
      .first()
      .tap();
    const disabledApiWrap = page
      .locator(".co-api-header-action .ant-tooltip-disabled-compatible-wrapper")
      .first();
    await expect(disabledApiWrap).toBeVisible();
    const apiWrapCenter = await centerOf(disabledApiWrap);
    await dispatchTap(apiWrapCenter.x, apiWrapCenter.y, 80);
    await page.waitForTimeout(350);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 4. Long-press on disabled-compatible-wrapper reveals its disabled reason tooltip cleanly
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x: apiWrapCenter.x, y: apiWrapCenter.y, id: 2 }],
    });
    await page.waitForTimeout(560);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
    await expect(page.locator(".ant-tooltip:visible")).toContainText(
      /启动远端 GPU|Start a remote GPU/,
    );
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await page.waitForTimeout(250);
    // Remains readable right after lift, and dismisses immediately on tapping blank space
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
    await dispatchTap(10, 10, 40);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 5. Long-press on Privacy toggle shows tooltip and suppresses trailing synthetic click
    const pressedBeforeLongPress = await privacyBtn.getAttribute("aria-pressed");
    const pCenter2 = await centerOf(privacyBtn);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x: pCenter2.x, y: pCenter2.y, id: 3 }],
    });
    await page.waitForTimeout(560);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
    await expect(page.locator(".ant-tooltip:visible")).toContainText(
      /隐私打码|privacy/i,
    );
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await page.waitForTimeout(250);
    await expect(privacyBtn).toHaveAttribute(
      "aria-pressed",
      pressedBeforeLongPress!,
    );
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);

    // Immediate fresh quick tap on the same button dismisses tooltip and executes toggle on first try
    await dispatchTap(pCenter2.x, pCenter2.y, 70);
    await page.waitForTimeout(350);
    await expect(privacyBtn).toHaveAttribute(
      "aria-pressed",
      pressedBeforeLongPress === "true" ? "false" : "true",
    );
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 6. Popconfirm-wrapped Sign Out button: quick tap opens Popconfirm cleanly (zero tooltip),
    //    while long-press shows tooltip without opening Popconfirm
    const signOutBtn = page.getByTestId("nav-web-signout-btn");
    const sCenter = await centerOf(signOutBtn);
    await dispatchTap(sCenter.x, sCenter.y, 70);
    await page.waitForTimeout(250);
    await expect(page.locator(".ant-popconfirm:visible")).toHaveCount(1);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
    await dispatchTap(10, 10, 40);
    await expect(page.locator(".ant-popconfirm:visible")).toHaveCount(0);

    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x: sCenter.x, y: sCenter.y, id: 4 }],
    });
    await page.waitForTimeout(560);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await page.waitForTimeout(250);
    await expect(page.locator(".ant-popconfirm:visible")).toHaveCount(0);
    await dispatchTap(10, 10, 40);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

    // 7. LanguageSwitcher dropdown trigger: quick tap opens menu (zero tooltip),
    //    long-press shows current language tooltip without opening menu
    const localeBtn = page.getByTestId("topbar-locale-trigger");
    const lCenter = await centerOf(localeBtn);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x: lCenter.x, y: lCenter.y, id: 6 }],
    });
    await page.waitForTimeout(560);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
    await expect(page.locator(".ant-tooltip:visible")).toContainText("简体中文");
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await page.waitForTimeout(250);
    await expect(page.locator(".co-locale-dropdown:visible")).toHaveCount(0);
    await dispatchTap(lCenter.x, lCenter.y, 70);
    await page.waitForTimeout(250);
    await expect(page.locator(".co-locale-dropdown:visible")).toHaveCount(1);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
    await dispatchTap(10, 10, 40);
    await expect(page.locator(".co-locale-dropdown:visible")).toHaveCount(0);

    // 8. Dragging finger (> 10px) during press cancels long-press so scrolling never triggers tooltips
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchStart",
      touchPoints: [{ x: pCenter2.x, y: pCenter2.y, id: 5 }],
    });
    await page.waitForTimeout(200);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x: pCenter2.x, y: pCenter2.y + 24, id: 5 }],
    });
    await page.waitForTimeout(400);
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchEnd",
      touchPoints: [],
    });
    await page.waitForTimeout(150);
    await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
  } finally {
    await mobileCtx.close();
  }
});
