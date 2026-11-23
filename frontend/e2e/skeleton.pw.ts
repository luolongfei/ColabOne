import { expect, test, type Page } from "@playwright/test";

function baseSnapshot(overrides: Record<string, any> = {}) {
  return {
    runtime: {
      state: "stopped",
      protocol: 7,
      capabilities: ["public_api", "comfyui"],
      active_engine: null,
      tasks: [],
      drive: {
        connected: false,
        mode: "disabled",
        remote_folder: "ColabOne_Drive",
        pending: 0,
        error: "",
      },
      comfyui: {
        installed: false,
        running: false,
        state: "stopped",
        port: 8188,
        tag: "v0.46.0",
        queue_running: 0,
        queue_pending: 0,
        error: "",
        canvas: {
          state: "stopped",
          enabled: false,
          mode: "named",
          url: "",
          error: "",
          checked_at: 0,
          active_connections: 0,
        },
      },
    },
    models: [
      {
        id: "qwen",
        name: "Qwen2.5 · 7B",
        description: "主力对话模型 · Q4_K_M",
        repo: "Qwen/Qwen2.5-7B-Instruct-GGUF",
        common: true,
        custom: false,
        size: 4_700_000_000,
        min_vram_mib: 6144,
        downloaded: true,
        loaded: true,
        files: [],
      },
    ],
    common_repos: [],
    workflows: [],
    comfy_outputs: [],
    comfy_canvas_settings: { mode: "named", domain: "", has_token: false },
    logs: [
      {
        id: 1,
        time: Date.now() / 1000,
        source: "客户端",
        level: "info",
        message: "已连接本地桌面服务",
      },
    ],
    account: {
      logged_in: false,
    },
    operation: null,
    error: "",
    auth_url: "",
    setup: {
      colab: true,
      drive: false,
      drive_authorized: true,
      hf: false,
    },
    settings: {
      gpu: "T4",
      idle_minutes: 30,
      idle_countdown_seconds: 60,
      auto_drive: true,
      download_threads: 16,
    },
    ...overrides,
  };
}

async function setupControlledMock(
  page: Page,
  options: {
    holdInitialSnapshot?: boolean;
    initialTheme?: "light" | "dark";
    snapshot?: Record<string, any>;
    path?: string;
  } = {},
) {
  const {
    holdInitialSnapshot = false,
    initialTheme = "light",
    snapshot = baseSnapshot(),
    path = "/",
  } = options;

  await page.addInitScript(
    ({ holdInitialSnapshot, initialTheme, snapshot }) => {
      window.localStorage.setItem("colabone.appearance", initialTheme);
      const state = structuredClone(snapshot);
      const gates: Record<string, (() => void) | null> = {};
      const holdFlags: Record<string, boolean> = {
        snapshot: holdInitialSnapshot,
      };
      const gatePromises: Record<string, Promise<void>> = {};

      function armGate(name: string) {
        holdFlags[name] = true;
        gatePromises[name] = new Promise<void>((resolve) => {
          gates[name] = () => {
            holdFlags[name] = false;
            gates[name] = null;
            resolve();
          };
        });
      }

      if (holdInitialSnapshot) {
        armGate("snapshot");
      }

      async function waitIfHeld(name: string) {
        if (holdFlags[name] && gatePromises[name]) {
          await gatePromises[name];
        }
      }

      (window as any).__state = state;
      (window as any).__armGate = armGate;
      (window as any).__releaseGate = (name: string) => {
        gates[name]?.();
      };

      (window as any).mygo = {
        call: async (method: string, ...args: any[]) => {
          const request = args[0];
          if (method === "ColabOneDesktop.Snapshot") {
            await waitIfHeld("snapshot");
            return JSON.stringify(state);
          }
          if (method === "ColabOneDesktop.GetLogDrawerState") {
            return { open: false, standalone: false, expanded: false, delta: 0 };
          }
          if (method === "ColabOneDesktop.SetLogDrawer") {
            return { open: !!args[0], standalone: false, expanded: false, delta: 0 };
          }
          if (method === "ColabOneDesktop.Command") {
            const action = request?.action;
            await waitIfHeld(action);
            if (action === "account.refresh") {
              return JSON.stringify({ ok: true });
            }
            if (action === "monitor.select") {
              state.monitor = {
                ...(state.monitor || {}),
                endpoint: request?.payload?.endpoint || "",
              };
              return JSON.stringify({ ok: true });
            }
            if (action === "api.key") {
              return JSON.stringify({ key: "co_sk_live_test_1234567890abcdef" });
            }
            if (action === "runtime.release.preview") {
              return JSON.stringify({
                id: "plan-1",
                instances: [
                  {
                    id: "gpu-t4-1",
                    managed: true,
                    active: true,
                    accelerator: "T4",
                    shape: "STANDARD",
                    loaded_model: "Qwen2.5 · 7B",
                  },
                ],
              });
            }
            if (action === "hub.search") {
              return JSON.stringify({
                models: [
                  {
                    repo: "Qwen/Qwen2.5-7B-Instruct-GGUF",
                    downloads: 128400,
                    likes: 2340,
                    gated: false,
                    modality: "text",
                    params: "7B",
                  },
                ],
                next_cursor: "",
              });
            }
            if (action === "hub.files") {
              return JSON.stringify({
                revision: "main",
                modality: "text",
                comfy_only: false,
                params: "7B",
                files: [
                  {
                    filename: "Qwen2.5-7B-Instruct-Q4_K_M.gguf",
                    size: 4_680_000_000,
                    shards: 1,
                    supported: true,
                    quant: "Q4_K_M",
                  },
                ],
              });
            }
            if (action === "comfyui.output.asset") {
              const pngB64 =
                "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";
              return JSON.stringify({
                mime: "image/png",
                size: 68,
                data: pngB64,
              });
            }
            return JSON.stringify({ ok: true });
          }
          return {};
        },
      };
    },
    { holdInitialSnapshot, initialTheme, snapshot },
  );

  await page.goto(path);
}

for (const theme of ["light", "dark"] as const) {
  test(`cold-start, account hydration, and manual refresh show refined skeletons without layout shift (${theme})`, async ({
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (err) => errors.push(err.message));

    await setupControlledMock(page, {
      holdInitialSnapshot: true,
      initialTheme: theme,
    });

    // 1. Cold-start before first snapshot arrives: all primary regions show skeletons, no auth callout flash
    await expect(page.locator(".co-topbar-sk")).toBeVisible();
    await expect(page.locator(".co-billing-strip.is-loading")).toBeVisible();
    await expect(page.locator(".co-device-sk-title")).toBeVisible();
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(4);
    await expect(page.locator(".co-monitor.is-skeleton")).toBeVisible();
    await expect(
      page.locator(".co-monitor.is-skeleton .co-rm-plot-frame"),
    ).toHaveCount(4);
    await expect(page.locator(".co-setup-callout")).toHaveCount(0);

    const overflowDuringSkeleton = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflowDuringSkeleton).toBe(0);

    // Also verify stacked row layout renders clean skeletons during cold start
    await page
      .locator(".co-rm-layout-segmented .ant-segmented-item")
      .nth(1)
      .click();
    await expect(
      page.locator(".co-monitor.is-skeleton .co-rm-row-plot"),
    ).toHaveCount(4);
    await page
      .locator(".co-rm-layout-segmented .ant-segmented-item")
      .nth(0)
      .click();

    // 2. First snapshot arrives with setup.colab=true, account not yet hydrated (checked_at missing)
    await page.evaluate(() => (window as any).__releaseGate("snapshot"));

    await expect(page.locator(".co-topbar-sk")).toHaveCount(0);
    await expect(page.locator(".co-device-sk-title")).toHaveCount(0);
    await expect(page.locator(".co-setup-callout")).toHaveCount(0);
    // Billing bar & card remain in hydrating skeleton state until account profile returns
    await expect(page.locator(".co-billing-strip.is-loading")).toBeVisible();
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(4);

    // Settings email shows inline skeleton while hydrating
    await page.getByRole("menuitem", { name: "设置" }).click();
    await expect(page.locator(".co-settings-email-sk")).toBeVisible();

    // 3. Background account fetch resolves with real email & billing metrics
    await page.evaluate(() => {
      const s = (window as any).__state;
      s.account = {
        logged_in: true,
        email: "alice@colabone.dev",
        balance: 1842.5,
        rate: 1.96,
        assignments: 1,
        checked_at: Date.now() / 1000,
        instances: [
          {
            id: "gpu-t4-main",
            managed: true,
            active: true,
            accelerator: "T4",
            state: "ready",
            gpu_name: "NVIDIA T4",
            vram_used_mib: 1024,
            vram_total_mib: 15360,
          },
        ],
      };
    });

    await expect(page.locator(".co-settings-email-sk")).toHaveCount(0);
    await expect(page.locator(".co-settings-page")).toContainText(
      "alice@colabone.dev",
    );

    await page.getByRole("menuitem", { name: "远端设备" }).click();
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(0);
    await expect(page.locator(".co-billing-card")).toContainText("1842.50");
    await expect(page.locator(".co-billing-card")).toContainText("1.96");

    // 4. Manual refresh triggers refresh skeleton on BillingBar, BillingCard, and Instances
    await page.evaluate(() => (window as any).__armGate("account.refresh"));
    await page.getByRole("button", { name: "刷新账户实例" }).click();
    await expect(page.locator(".co-billing-strip.is-loading")).toBeVisible();
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(4);
    await expect(page.locator(".co-instance-skeleton")).toHaveCount(1);

    await page.evaluate(() => (window as any).__releaseGate("account.refresh"));
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(0);
    await expect(page.locator(".co-instance-skeleton")).toHaveCount(0);

    // 5. Offline / connection error during account hydration must exit skeleton cleanly (never infinite skeleton)
    await page.evaluate(() => {
      const s = (window as any).__state;
      s.account = { logged_in: false };
      s.error = "连接检查失败：TimeoutError。客户端会自动重试，请检查网络连接。";
    });
    await expect(page.locator(".co-billing-strip.is-loading")).toHaveCount(0);
    await expect(page.locator(".co-billing-card .co-bc-sk-val")).toHaveCount(0);
    await page.getByRole("menuitem", { name: "设置" }).click();
    await expect(page.locator(".co-settings-email-sk")).toHaveCount(0);

    expect(errors).toEqual([]);
  });
}

test("async feature surfaces show targeted skeletons during Hub search, Version Picker, API key reveal, Release preview, ComfyUI loading, and Logs boot", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));

  const readySnap = baseSnapshot({
    runtime: {
      state: "ready",
      id: "gpu-t4-main",
      protocol: 7,
      capabilities: ["public_api", "comfyui"],
      gpu: { name: "NVIDIA T4", total_mib: 15360, used_mib: 1200 },
      resources: {
        gpu: {
          name: "NVIDIA T4",
          total_mib: 15360,
          used_mib: 1200,
          util_percent: 24,
        },
        memory: {
          total_bytes: 12 * 1024 ** 3,
          used_bytes: 4 * 1024 ** 3,
          available_bytes: 8 * 1024 ** 3,
          percent: 33.3,
        },
        disk: {
          total_bytes: 100 * 1024 ** 3,
          used_bytes: 20 * 1024 ** 3,
          free_bytes: 80 * 1024 ** 3,
          percent: 20,
        },
        cpu: { cores: 4, percent: 18 },
        history: [],
      },
      tasks: [
        {
          id: "run-1",
          kind: "comfyui_run",
          state: "running",
          progress: 0.42,
          message: "生成图像中",
        },
      ],
      public_api: {
        enabled: true,
        state: "starting",
        url: "",
        requests: 1,
        active: 1,
        completed: 0,
        failed: 0,
        recent_requests: [
          {
            id: "req-1",
            seq: 1,
            kind: "chat",
            state: "running",
            stream: true,
            started_at: Date.now() / 1000,
            duration_ms: 420,
            tokens_per_second: 0,
          },
        ],
      },
      comfyui: {
        installed: true,
        running: true,
        state: "ready",
        port: 8188,
        tag: "v0.46.0",
        queue_running: 1,
        queue_pending: 0,
        error: "",
        canvas: {
          state: "starting",
          enabled: true,
          mode: "named",
          url: "",
          error: "",
          checked_at: 0,
          active_connections: 0,
        },
      },
    },
    comfy_outputs: [
      {
        id: "out-1",
        workflow_id: "wf-1",
        workflow_name: "Flux Demo",
        filename: "demo_0001.png",
        kind: "image",
        size: 512_000,
        elapsed: 3.4,
        created_at: Date.now() / 1000,
      },
    ],
    account: {
      logged_in: true,
      email: "alice@colabone.dev",
      balance: 200,
      rate: 1.5,
      assignments: 1,
      checked_at: Date.now() / 1000,
      instances: [
        {
          id: "gpu-t4-main",
          managed: true,
          active: true,
          accelerator: "T4",
          state: "ready",
          gpu_name: "NVIDIA T4",
          vram_used_mib: 1200,
          vram_total_mib: 15360,
        },
      ],
    },
  });

  await setupControlledMock(page, {
    holdInitialSnapshot: false,
    snapshot: readySnap,
  });

  // 1. Release Modal preview skeleton
  await page.evaluate(() => (window as any).__armGate("runtime.release.preview"));
  await page.getByRole("button", { name: "停止实例", exact: true }).click();
  await expect(page.locator(".co-release-skeleton")).toBeVisible();
  await expect(page.locator(".co-release-item-skeleton")).toHaveCount(2);
  await page.evaluate(() =>
    (window as any).__releaseGate("runtime.release.preview"),
  );
  await expect(page.locator(".co-release-skeleton")).toHaveCount(0);
  await expect(page.locator(".co-release-item")).toContainText("gpu-t4-1");
  await page.getByRole("dialog").getByRole("button", { name: /取\s*消/ }).click();

  // 2. API page: Base URL connecting shimmer, pending TTFT shimmer, API Key reveal shimmer
  await page.getByRole("menuitem", { name: "对外 API" }).click();
  await expect(page.locator(".co-api-code-sk")).toHaveCount(1);
  await expect(page.locator(".co-api-tps-pending")).toBeVisible();

  await page.evaluate(() => (window as any).__armGate("api.key"));
  await page.getByRole("button", { name: "显示 API 密钥" }).click();
  await expect(page.locator(".co-api-code-sk")).toHaveCount(2);
  await page.evaluate(() => (window as any).__releaseGate("api.key"));
  await expect(page.locator(".co-api-access")).toContainText(
    "co_sk_live_test_1234567890abcdef",
  );

  // 3. Model Drawer -> Community Hub search skeleton + repo size skeleton + Version Picker skeleton
  await page
    .locator(".co-api-model-slot")
    .getByRole("button", { name: /选择模型|切换模型/ })
    .click();
  await expect(page.locator(".co-models-drawer")).toBeVisible();

  await page.evaluate(() => {
    (window as any).__armGate("hub.search");
    (window as any).__armGate("hub.files");
  });
  await page
    .locator(".co-model-tabs")
    .getByRole("tab", { name: /^社\s*区$/ })
    .click();
  await expect(page.locator(".co-hub-skeleton-list")).toBeVisible();
  await expect(page.locator(".co-hub-repo-skeleton")).toHaveCount(5);

  await page.evaluate(() => (window as any).__releaseGate("hub.search"));
  await expect(page.locator(".co-hub-skeleton-list")).toHaveCount(0);
  await expect(page.locator(".co-hub-size-skeleton")).toBeVisible();
  await expect(page.locator(".co-hub-size")).toContainText("读取中");

  await page.getByRole("button", { name: "选择版本" }).first().click();
  await expect(page.locator(".co-vp-skeleton")).toBeVisible();
  await expect(page.locator(".co-vp-skeleton .co-version-card")).toHaveCount(3);

  await page.evaluate(() => (window as any).__releaseGate("hub.files"));
  await expect(page.locator(".co-vp-skeleton")).toHaveCount(0);
  await expect(page.locator(".co-version-card").first()).toBeVisible();
  await page.keyboard.press("Escape");

  // 4. ComfyUI page: live generating artwork skeleton + preview loading skeleton + Canvas connecting skeleton
  await page.evaluate(() => (window as any).__armGate("comfyui.output.asset"));
  await page.getByRole("menuitem", { name: "ComfyUI" }).click();
  await expect(page.locator(".co-comfy-output-generating")).toBeVisible();
  await expect(page.locator(".co-comfy-preview-skeleton")).toBeVisible();
  await page.evaluate(() =>
    (window as any).__releaseGate("comfyui.output.asset"),
  );
  await expect(page.locator(".co-comfy-preview-skeleton")).toHaveCount(0);
  await expect(
    page.locator('.co-comfy-output-frame img[alt="demo_0001.png"]'),
  ).toBeVisible();

  await page
    .locator(".co-comfy-tabs")
    .getByRole("tab", { name: /原生画布/ })
    .click();
  await expect(page.locator(".co-comfy-host-sk")).toBeVisible();
  await expect(page.locator(".co-comfy-canvas-connecting")).toBeVisible();
  await expect(page.locator(".co-comfy-canvas-launch-skeleton")).toBeVisible();

  // 5. Standalone Log Window cold-start skeleton
  const logPage = await page.context().newPage();
  await setupControlledMock(logPage, {
    holdInitialSnapshot: true,
    snapshot: readySnap,
    path: "/?view=logs",
  });
  await expect(logPage.locator(".co-log-skeleton-list")).toBeVisible();
  await expect(logPage.locator(".co-log-entry-skeleton")).toHaveCount(5);
  await logPage.evaluate(() => (window as any).__releaseGate("snapshot"));
  await expect(logPage.locator(".co-log-skeleton-list")).toHaveCount(0);
  await expect(logPage.locator(".co-log-entry")).toContainText(
    "已连接本地桌面服务",
  );
  await logPage.close();

  expect(errors).toEqual([]);
});

test("starting instance detail skeleton, multi-instance cards, and monitor row skeletons stay vertically aligned", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));

  await page.addInitScript(() => {
    window.localStorage.setItem("colabone.monitor.layout", "rows");
  });

  const startingSnap = baseSnapshot({
    runtime: {
      state: "starting",
      id: "gpu-t4-starting",
      protocol: 7,
      capabilities: ["public_api", "comfyui"],
      location: { country: "IN", city: "Mumbai", region: "asia-south1" },
      tasks: [],
    },
    operation: { kind: "start", title: "部署远程管理服务" },
    account: {
      logged_in: true,
      email: "alice@colabone.dev",
      balance: 2060.86,
      rate: 11.62,
      assignments: 3,
      checked_at: Date.now() / 1000,
      instances: [
        {
          id: "gpu-g4-tall",
          managed: true,
          active: false,
          accelerator: "G4",
          shape: "STANDARD",
          state: "ready",
          gpu_name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
          vram_used_mib: 512,
          vram_total_mib: 98304,
          disk_free: 59.2 * 1024 ** 3,
          active_task: "load",
          location: { country: "NL", city: "Groningen", region: "europe-west4" },
        },
        {
          id: "gpu-t4-compact",
          managed: true,
          active: false,
          accelerator: "T4",
          shape: "HIGH_RAM",
          state: "ready",
          gpu_name: "Tesla T4",
          vram_used_mib: 0,
          vram_total_mib: 15360,
          disk_free: 235.5 * 1024 ** 3,
          location: { country: "SG", city: "Singapore", region: "asia-southeast1" },
        },
        {
          id: "gpu-t4-starting",
          managed: true,
          active: true,
          accelerator: "T4",
          shape: "HIGH_RAM",
          state: "starting",
          location: { country: "IN", city: "Mumbai", region: "asia-south1" },
        },
      ],
    },
  });

  await setupControlledMock(page, {
    holdInitialSnapshot: false,
    snapshot: startingSnap,
  });

  await expect(page.locator(".co-instance-row.active .co-instance-detail-sk")).toBeVisible();

  const offsets = await page.evaluate(() => {
    const midY = (el: Element | null) => {
      if (!el) return NaN;
      const r = el.getBoundingClientRect();
      return r.top + r.height / 2;
    };

    const detail = document.querySelector(".co-instance-row.active .co-instance-detail");
    const detailFirst = detail?.firstElementChild ?? null;
    const detailSk = detail?.querySelector(".co-instance-detail-sk .co-skeleton") ?? null;
    const detailLast = detail?.lastElementChild ?? null;

    const cards = [...document.querySelectorAll(".co-instance-row")];
    const row1MainTopDiff =
      cards.length >= 2
        ? Math.abs(
            cards[0].querySelector(".co-instance-main")!.getBoundingClientRect().top -
              cards[1].querySelector(".co-instance-main")!.getBoundingClientRect().top,
          )
        : NaN;

    const cpuRow = document.querySelector(".co-rm-row.co-metric-cpu");
    const railTitleDiff = Math.abs(
      midY(cpuRow?.querySelector(".co-rm-row-title") ?? null) -
        midY(cpuRow?.querySelector(".co-rm-row-rail-line .co-skeleton") ?? null),
    );
    const railSubDiff = Math.abs(
      midY(cpuRow?.querySelector(".co-rm-row-subtitle") ?? null) -
        midY(cpuRow?.querySelector(".co-rm-row-rail-subline .co-skeleton") ?? null),
    );
    const statDiffs = [...(cpuRow?.querySelectorAll(".co-rm-row-stat") ?? [])].map(
      (stat) =>
        Math.abs(
          midY(stat.querySelector("em")) -
            midY(stat.querySelector(".co-skeleton")),
        ),
    );

    return {
      detailSkVsFirst: Math.abs(midY(detailSk) - midY(detailFirst)),
      detailSkVsLast: Math.abs(midY(detailSk) - midY(detailLast)),
      row1MainTopDiff,
      railTitleDiff,
      railSubDiff,
      maxStatDiff: Math.max(...statDiffs),
    };
  });

  expect(offsets.detailSkVsFirst).toBeLessThanOrEqual(1);
  expect(offsets.detailSkVsLast).toBeLessThanOrEqual(1);
  expect(offsets.row1MainTopDiff).toBeLessThanOrEqual(1);
  expect(offsets.railTitleDiff).toBeLessThanOrEqual(1);
  expect(offsets.railSubDiff).toBeLessThanOrEqual(1);
  expect(offsets.maxStatDiff).toBeLessThanOrEqual(1);
  expect(errors).toEqual([]);
});
