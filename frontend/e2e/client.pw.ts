import { test, expect } from "@playwright/test";
import { fileURLToPath } from "node:url";

async function mock(page: any, ready = false, path = "/") {
  await page.addInitScript(
    ({ ready }: any) => {
      const state: any = {
        runtime: {
          state: ready ? "ready" : "stopped",
          protocol: 7,
          capabilities: ["public_api", "comfyui"],
          active_engine: null,
          gpu: ready
            ? { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 }
            : undefined,
          location: ready
            ? {
                country: "US",
                country_name: "美国",
                region: "俄勒冈",
                zone: "us-west1-b",
              }
            : undefined,
          id: ready ? "test-runtime" : undefined,
          disk_free: 90 * 1024 ** 3,
          resources: ready
            ? {
                gpu: {
                  name: "NVIDIA T4",
                  total_mib: 15360,
                  used_mib: 700,
                  util_percent: 34,
                  temp_c: 56,
                  power_w: 42,
                  power_limit_w: 70,
                },
                memory: {
                  total_bytes: 12.7 * 1024 ** 3,
                  used_bytes: 4.2 * 1024 ** 3,
                  available_bytes: 8.5 * 1024 ** 3,
                  percent: 33.1,
                },
                disk: {
                  total_bytes: 112 * 1024 ** 3,
                  used_bytes: 22 * 1024 ** 3,
                  free_bytes: 90 * 1024 ** 3,
                  percent: 19.6,
                  read_bps: 12 * 1024 ** 2,
                  write_bps: 4 * 1024 ** 2,
                },
                cpu: {
                  cores: 2,
                  percent: 24.5,
                },
                history: [
                  {
                    t: Date.now() / 1000 - 2,
                    vram_pct: 4.6,
                    gpu_util: 20,
                    ram_pct: 31.5,
                    cpu_pct: 19,
                    disk_pct: 19.6,
                    disk_read_bps: 8 * 1024 ** 2,
                    disk_write_bps: 2 * 1024 ** 2,
                  },
                  {
                    t: Date.now() / 1000 - 1,
                    vram_pct: 4.6,
                    gpu_util: 34,
                    ram_pct: 33.1,
                    cpu_pct: 24.5,
                    disk_pct: 19.6,
                    disk_read_bps: 12 * 1024 ** 2,
                    disk_write_bps: 4 * 1024 ** 2,
                  },
                ],
              }
            : undefined,
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
            name: "Qwen2.5 · 0.5B",
            description: "轻量验收模型 · Q4_K_M",
            repo: "Qwen/Qwen2.5-0.5B-Instruct-GGUF",
            common: true,
            custom: false,
            size: 491000000,
            min_vram_mib: 2048,
            downloaded: false,
            loaded: false,
            files: [],
          },
        ],
        common_repos: [],
        workflows: [],
        comfy_outputs: [],
        comfy_canvas_settings: {
          mode: "named",
          domain: "",
          has_token: false,
        },
        logs: [
          {
            id: 1,
            time: Date.now() / 1000,
            source: "客户端",
            level: "info",
            message: "已恢复本机保存的 Google 登录；到期自动续期",
          },
        ],
        account: {
          logged_in: ready,
          balance: 100,
          rate: ready ? 1.5 : 0,
          assignments: ready ? 1 : 0,
          checked_at: Date.now() / 1000,
        },
        operation: null,
        error: "",
        auth_url: "",
        setup: {
          colab: ready,
          drive: ready,
          drive_authorized: ready,
          hf: false,
        },
        settings: {
          gpu: "T4",
          idle_minutes: 30,
          idle_countdown_seconds: 60,
          auto_drive: true,
          download_threads: 16,
        },
      };
      const calls: any[] = [];
      const canvasOpenCalls: any[] = [];
      const exportCalls: any[] = [];
      const logDrawerCalls: any[] = [];
      const nativeLocales: string[] = [];
      const localeCommands: any[] = [];
      (window as any).__state = state;
      (window as any).__calls = calls;
      (window as any).__canvasOpenCalls = canvasOpenCalls;
      (window as any).__exportCalls = exportCalls;
      (window as any).__logDrawerCalls = logDrawerCalls;
      (window as any).__nativeLocales = nativeLocales;
      (window as any).__localeCommands = localeCommands;
      (window as any).mygo = {
        call: async (method: string, ...args: any[]) => {
          const request = args[0];
          if (method === "ColabOneDesktop.SetAppearance") {
            (window as any).__nativeAppearance = request;
            return;
          }
          if (method === "ColabOneDesktop.SystemAppearance") {
            return (window as any).__mockSystemAppearance ?? "";
          }
          if (method === "ColabOneDesktop.SetLocale") {
            nativeLocales.push(String(request ?? ""));
            return;
          }
          if (method === "ColabOneDesktop.GetLogDrawerState") {
            if (typeof (window as any).__onGetLogDrawerState === "function") {
              return await (window as any).__onGetLogDrawerState();
            }
            return { open: false, standalone: false, expanded: false, delta: 0 };
          }
          if (method === "ColabOneDesktop.SetLogDrawer") {
            const [open, width] = args;
            logDrawerCalls.push({ open, width });
            if (typeof (window as any).__onSetLogDrawer === "function") {
              return await (window as any).__onSetLogDrawer(!!open, width);
            }
            return { open: !!open, standalone: false, expanded: false, delta: 0 };
          }
          if (method === "ColabOneDesktop.OpenComfyCanvas") {
            canvasOpenCalls.push(request);
            return;
          }
          if (method === "ColabOneDesktop.ExportWorkflowOutput") {
            exportCalls.push(request);
            return "/tmp/exported-output.png";
          }
          if (method === "ColabOneDesktop.WriteClipboard") {
            (window as any).__clipboard = request;
            return;
          }
          if (method === "ColabOneDesktop.Snapshot") return JSON.stringify(state);
          if (method === "ColabOneDesktop.Command") {
            if (request.action === "runtime.activity") {
              (window as any).__activity =
                ((window as any).__activity || 0) + 1;
              return JSON.stringify({ ok: true });
            }
            if (request.action === "locale.set") {
              localeCommands.push(request.payload);
              if (request.payload?.persist && request.payload?.locale) {
                state.settings.locale = request.payload.locale;
              }
              return JSON.stringify({
                locale: request.payload?.locale || "zh-CN",
                persisted: !!request.payload?.persist,
              });
            }
            calls.push(request);
            const { action, payload } = request;
            if (action === "settings.save") {
              for (const k of [
                "gpu",
                "idle_minutes",
                "auto_drive",
                "download_threads",
              ]) {
                if (payload && k in payload) state.settings[k] = payload[k];
              }
            }
            if (action === "runtime.release.preview")
              return JSON.stringify({
                id: "reviewed-plan",
                instances: state.account.instances || [
                  {
                    id: "test-runtime",
                    managed: true,
                    active: true,
                    accelerator: "T4",
                    shape: "HIGH_RAM",
                  },
                  {
                    id: "other-runtime",
                    managed: false,
                    active: false,
                    accelerator: "A100",
                    shape: "STANDARD",
                  },
                ],
              });
            if (action === "runtime.release.all") {
              if (payload.endpoints?.length) {
                const removed = new Set(payload.endpoints);
                state.account.instances = (
                  state.account.instances || []
                ).filter((i: any) => !removed.has(i.id));
                state.account.assignments = state.account.instances.length;
              } else {
                state.runtime.state = "stopped";
                state.account.assignments = 0;
                state.account.rate = 0;
                state.account.instances = [];
              }
            }
            if (action === "runtime.select") {
              state.runtime.id = payload.endpoint;
              state.active_endpoint = payload.endpoint;
              for (const inst of state.account.instances || []) {
                inst.active = inst.id === payload.endpoint;
                if (inst.active) state.runtime.location = inst.location;
              }
            }
            if (action === "hub.search")
              return JSON.stringify({
                models: [
                  {
                    repo: "org/Qwen-GGUF",
                    downloads: 2000,
                    likes: 120,
                    gated: false,
                  },
                ],
                next_cursor: "",
              });
            if (action === "hub.files")
              return JSON.stringify({
                revision: "a".repeat(40),
                files: [
                  {
                    filename: "Qwen-Q4_K_M-00001-of-00002.gguf",
                    size: 4096000000,
                    shards: 2,
                    quantization: "Q4_K_M",
                    supported: true,
                    reason: "",
                  },
                ],
              });
            if (action === "logs.clear") state.logs = [];
            if (action === "model.download") {
              const target =
                state.models.find(
                  (m: any) => m.id === (payload?.id || state.models[0]?.id),
                ) || state.models[0];
              if (target) {
                target.downloaded = true;
                if (payload?.load) {
                  for (const m of state.models) m.loaded = false;
                  target.loaded = true;
                  state.runtime.loaded_model = target.id;
                  state.runtime.active_engine = "llama";
                  state.runtime.active_options = {
                    context_length:
                      payload.context_length || target.context_length || 8192,
                    temperature:
                      payload.temperature ?? target.temperature ?? 0.7,
                  };
                }
              }
            }
            if (action === "model.load") {
              const target =
                state.models.find(
                  (m: any) => m.id === (payload?.id || state.models[0]?.id),
                ) || state.models[0];
              if (target) {
                for (const m of state.models) m.loaded = false;
                target.loaded = true;
                state.runtime.loaded_model = target.id;
                state.runtime.active_engine = "llama";
                if (state.runtime.comfyui) {
                  state.runtime.comfyui.running = false;
                  state.runtime.comfyui.state = "stopped";
                }
                state.runtime.active_options = {
                  context_length:
                    payload?.context_length || target.context_length || 8192,
                  temperature:
                    payload?.temperature ?? target.temperature ?? 0.7,
                };
              }
            }
            if (action === "model.unload") {
              for (const m of state.models) m.loaded = false;
              state.runtime.loaded_model = undefined;
              state.runtime.active_engine = null;
            }
            if (action === "model.delete") {
              const target =
                state.models.find(
                  (m: any) => m.id === (payload?.id || state.models[0]?.id),
                ) || state.models[0];
              if (target) target.downloaded = false;
            }
            if (action === "model.common") {
              const target = state.models.find(
                (m: any) => m.id === (payload?.id || state.models[0]?.id),
              );
              if (target) {
                target.common = payload?.common ?? !target.common;
                return JSON.stringify({ ok: true, common: target.common });
              }
            }
            if (action === "model.remove_local") {
              state.models = state.models.filter(
                (m: any) => m.id !== payload?.id,
              );
            }
            if (action === "common_repo.save") {
              const idx = state.common_repos.findIndex(
                (b: any) =>
                  b.repo.toLowerCase() === String(payload.repo).toLowerCase(),
              );
              const exists = idx >= 0;
              if (exists && payload.toggle !== false) {
                state.common_repos.splice(idx, 1);
              } else {
                const entry = {
                  repo: payload.repo,
                  name: payload.name || payload.repo.split("/").pop(),
                  downloads: payload.downloads || 0,
                  likes: payload.likes || 0,
                };
                if (exists) state.common_repos[idx] = entry;
                else state.common_repos.unshift(entry);
              }
              return JSON.stringify({
                ok: true,
                common: exists && payload.toggle !== false ? false : true,
                repo: payload.repo,
              });
            }
            if (action === "common_repo.remove") {
              state.common_repos = state.common_repos.filter(
                (b: any) =>
                  b.repo.toLowerCase() !==
                  String(payload?.repo || "").toLowerCase(),
              );
            }
            if (action === "desktop.shortcuts") {
              const remove = !!payload?.remove;
              if (
                payload?.target === "start_menu" ||
                payload?.target === "both"
              )
                state.setup.start_menu_shortcut = !remove;
              if (payload?.target === "desktop" || payload?.target === "both")
                state.setup.desktop_shortcut = !remove;
              return JSON.stringify({
                ok: true,
                start_menu: !!state.setup.start_menu_shortcut,
                desktop: !!state.setup.desktop_shortcut,
              });
            }
            if (action === "hub.install") {
              if (payload?.load) {
                for (const m of state.models) m.loaded = false;
                state.runtime.loaded_model = "hf-installed";
              }
              state.models = state.models.filter(
                (m: any) => m.id !== "hf-installed",
              );
              state.models.push({
                id: "hf-installed",
                name: payload.name || payload.repo.split("/").pop(),
                description: payload.filename,
                repo: payload.repo,
                common: true,
                custom: true,
                downloaded: !!payload.download,
                loaded: !!payload.load,
                size: 4096000000,
                min_vram_mib: 4096,
                files: [{ repo: payload.repo, path: payload.filename }],
              });
            }
            if (action === "comfyui.start") {
              for (const m of state.models) m.loaded = false;
              state.runtime.loaded_model = undefined;
              state.runtime.active_engine = "comfyui";
              state.runtime.comfyui = {
                ...state.runtime.comfyui,
                installed: true,
                running: true,
                state: "ready",
                error: "",
              };
              return JSON.stringify({ id: "task-comfy-start", state: "completed" });
            }
            if (action === "comfyui.stop") {
              state.runtime.active_engine = null;
              state.runtime.comfyui = {
                ...state.runtime.comfyui,
                running: false,
                state: "stopped",
                queue_running: 0,
                queue_pending: 0,
                canvas: {
                  state: "stopped",
                  enabled: false,
                  mode: "named",
                  url: "",
                  error: "",
                  checked_at: 0,
                  active_connections: 0,
                },
              };
              return JSON.stringify({ ok: true });
            }
            if (action === "comfyui.free") {
              return JSON.stringify({ ok: true, running: true });
            }
            if (action === "comfyui.canvas.save") {
              const rawDomain = (payload?.domain || "").trim();
              const normalized = rawDomain
                ? rawDomain.startsWith("https://")
                  ? rawDomain
                  : `https://${rawDomain}`
                : "";
              state.comfy_canvas_settings = {
                mode: "named",
                domain: normalized,
                has_token: !!payload?.tunnel_token || !!state.comfy_canvas_settings?.has_token,
              };
              return JSON.stringify({ ok: true });
            }
            if (action === "comfyui.canvas.start") {
              const rawDomain = (
                payload?.domain ||
                state.comfy_canvas_settings?.domain ||
                "comfy.example.com"
              ).trim();
              const url = rawDomain.startsWith("https://")
                ? rawDomain
                : `https://${rawDomain}`;
              state.runtime.comfyui.canvas = {
                state: "ready",
                enabled: true,
                mode: "named",
                url,
                error: "",
                checked_at: Date.now() / 1000,
                active_connections: 1,
              };
              return JSON.stringify(state.runtime.comfyui.canvas);
            }
            if (action === "comfyui.canvas.stop") {
              state.runtime.comfyui.canvas = {
                state: "stopped",
                enabled: false,
                mode: "named",
                url: "",
                error: "",
                checked_at: 0,
                active_connections: 0,
              };
              return JSON.stringify(state.runtime.comfyui.canvas);
            }
            if (action === "comfyui.canvas.url") {
              const base =
                state.runtime.comfyui?.canvas?.url ||
                "https://comfy.example.com";
              return JSON.stringify({
                url: `${base}/_colabone_auth?token=mock-secret-canvas-token`,
                base_url: base,
              });
            }
            if (action === "comfyui.workflow.import") {
              const wfObj = payload?.workflow || {};
              const fields: any[] = [];
              for (const [nid, node] of Object.entries<any>(wfObj)) {
                const inputs = node?.inputs || {};
                for (const [iname, val] of Object.entries<any>(inputs)) {
                  if (Array.isArray(val)) continue;
                  if (typeof val === "string" && iname === "text") {
                    fields.push({
                      key: `${nid}.${iname}`,
                      label: `${node?._meta?.title || node?.class_type} · 正向提示词`,
                      node_id: nid,
                      input_name: iname,
                      field_type: "textarea",
                      default: val,
                    });
                  } else if (typeof val === "number" && (iname === "seed" || iname === "steps")) {
                    fields.push({
                      key: `${nid}.${iname}`,
                      label: `${node?._meta?.title || node?.class_type} · ${iname === "seed" ? "随机种子" : "采样步数"}`,
                      node_id: nid,
                      input_name: iname,
                      field_type: "integer",
                      default: val,
                    });
                  }
                }
              }
              const item = {
                id: payload?.id || `wf-mock-${state.workflows.length + 1}`,
                name: payload?.name || "未命名工作流",
                description: payload?.description || "",
                source: "custom",
                min_vram_mib: 0,
                node_count: Object.keys(wfObj).length,
                fields,
                workflow: wfObj,
              };
              state.workflows = [item, ...state.workflows];
              return JSON.stringify(item);
            }
            if (action === "comfyui.workflow.delete") {
              state.workflows = state.workflows.filter(
                (w: any) => w.id !== payload?.id,
              );
              return JSON.stringify({ ok: true });
            }
            if (action === "comfyui.run") {
              return JSON.stringify({ id: "task-comfy-run", state: "running" });
            }
            if (action === "comfyui.output.delete") {
              state.comfy_outputs = state.comfy_outputs.filter(
                (o: any) => o.id !== payload?.id,
              );
              return JSON.stringify({ ok: true });
            }
            return JSON.stringify({ ok: true });
          }
          return "";
        },
      };
    },
    { ready },
  );
  await page.goto(path);
}

function navItem(page: any, name: string) {
  return page
    .locator(".co-navbar nav")
    .getByRole("menuitem", { name, exact: true });
}

function antSelect(scope: any, label: string) {
  return scope
    .locator(
      `.ant-select[aria-label="${label}"], .ant-select:has([aria-label="${label}"])`,
    )
    .first();
}

async function chooseAntOption(
  page: any,
  select: any,
  option: string | RegExp,
) {
  await select.click();
  const dropdown = page
    .locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden)")
    .last();
  await expect(dropdown).toBeVisible();
  await dropdown
    .locator(".ant-select-item-option", { hasText: option })
    .first()
    .click();
}

function segmentedItem(scope: any, text: string | RegExp) {
  return scope.locator(".ant-segmented-item", { hasText: text });
}

async function openApiModelDrawer(page: any) {
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await page
    .locator(".co-api-model-slot")
    .getByRole("button", { name: /选择模型|切换模型|查看模型/ })
    .click();
  const drawer = page.locator(".co-models-drawer");
  await expect(drawer).toBeVisible();
  return drawer;
}

test("disconnected client exposes 4-item navigation, opens model drawer from API page, and does not allocate on navigation", async ({
  page,
}) => {
  await mock(page);
  const nav = page.locator(".co-navbar nav");
  await expect(nav.getByRole("menuitem")).toHaveText([
    "远端设备",
    "对外 API",
    "ComfyUI",
    "设置",
  ]);
  await expect(
    nav.getByRole("menuitem", { name: "模型", exact: true }),
  ).toHaveCount(0);
  await expect(
    nav.getByRole("menuitem", { name: "使用", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "启动 GPU", exact: true }),
  ).toBeDisabled();
  const drawer = await openApiModelDrawer(page);
  await expect(
    drawer.getByRole("button", { name: "下载并装载" }),
  ).toBeDisabled();
  await expect(
    drawer.getByRole("button", { name: "去启动 GPU" }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(
    page.getByRole("button", { name: "开启 API 服务", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "去启动 GPU" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "复制 API 地址", exact: true }),
  ).toBeDisabled();
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "启动 ComfyUI", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "去启动 GPU" }),
  ).toBeVisible();
  await expect(page.getByText("还没有可用的工作流")).toBeVisible();
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "Google 云盘", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /查看 GPU|去启动 GPU|前往远端设备/ }),
  ).toHaveCount(0);
  await expect(page.getByText("ColabOne_Drive").first()).toBeVisible();
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);
});

test("model deletion requires explicit confirmation", async ({ page }) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.models[0].downloaded = true;
  });
  const drawer = await openApiModelDrawer(page);
  await drawer.getByRole("button", { name: "删除远端模型文件" }).click();
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);
  await page
    .locator(".ant-modal")
    .getByRole("button", { name: /^取\s*消$/ })
    .click();
  await expect(drawer).toBeVisible();
});

test("compact desktop layout stays within viewport across all 4 sections", async ({
  page,
}) => {
  await page.setViewportSize({ width: 950, height: 700 });
  await mock(page, true);
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <= window.innerWidth &&
        document.documentElement.scrollHeight <= window.innerHeight,
    ),
  ).toBe(true);
  await page.screenshot({
    path: fileURLToPath(new URL("../../work/device-ui.png", import.meta.url)),
    fullPage: true,
  });
  for (const vp of [
    { width: 1024, height: 640 },
    { width: 900, height: 580 },
  ]) {
    await page.setViewportSize(vp);
    await navItem(page, "ComfyUI").click();
    await page.mouse.move(700, 400);
    await page.mouse.wheel(0, 600);
    const metrics = await page.evaluate(() => {
      const shell = document
        .querySelector(".co-app-shell")!
        .getBoundingClientRect();
      return {
        scrollY: window.scrollY,
        docH: document.documentElement.scrollHeight,
        innerH: window.innerHeight,
        shellTop: Math.round(shell.top),
        shellBottom: Math.round(shell.bottom),
      };
    });
    expect(metrics).toEqual({
      scrollY: 0,
      docH: vp.height,
      innerH: vp.height,
      shellTop: 0,
      shellBottom: vp.height,
    });
  }
});

test("setup offers Google login and whole-page paste without API credentials", async ({
  page,
}) => {
  await mock(page);
  await navItem(page, "设置").click();
  await expect(page.getByText("OAuth Client ID")).toHaveCount(0);
  await expect(page.getByText("私有网络", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "登录 Google", exact: true }).click();
  expect((await page.evaluate(() => (window as any).__calls))[0].action).toBe(
    "auth.colab.auto",
  );
  await page
    .getByText("授权页返回了一段内容？点此手动粘贴", { exact: true })
    .click();
  await page
    .getByRole("textbox", { name: "Google 授权内容" })
    .fill(
      "Authorization complete\n4/test-code-long-enough-for-a-valid-parser-input",
    );
  await page
    .getByRole("button", { name: "自动识别并登录", exact: true })
    .click();
  expect(
    (await page.evaluate(() => (window as any).__calls))[1].payload.code,
  ).toContain("Authorization complete");
  await expect(
    page.getByRole("textbox", { name: "Google 授权内容" }),
  ).toHaveValue("");
  await expect(
    page.getByRole("button", { name: "保存设置", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "保存偏好", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({
    path: fileURLToPath(new URL("../../work/setup-ui.png", import.meta.url)),
    fullPage: true,
  });
});

test("Google login alone enables GPU startup without any Drive authorization", async ({
  page,
}) => {
  await mock(page);
  await page.evaluate(() => {
    (window as any).__state.setup.colab = true;
  });
  await expect(
    page.getByRole("button", { name: "启动 GPU", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "启动 GPU", exact: true }).click();
  expect(
    (await page.evaluate(() => (window as any).__calls)).map(
      (c: any) => c.action,
    ),
  ).toEqual(["settings.save", "runtime.start"]);
});

test("ColabOne_Drive status bar syncs immediately and settings handles hot mount and scope hint", async ({
  page,
}) => {
  await mock(page, true);
  await expect(page.locator(".co-sync-note")).toContainText(
    "My Drive / ColabOne_Drive",
  );
  await page.getByRole("button", { name: "立即同步", exact: true }).click();
  await navItem(page, "设置").click();
  await expect(page.getByText("已挂载 · ColabOne_Drive")).toBeVisible();
  await page.getByRole("button", { name: "立即同步云盘", exact: true }).click();
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.setup.drive = false;
    s.runtime.drive.connected = false;
  });
  await page.getByRole("button", { name: "立即挂载云盘", exact: true }).click();
  await page.evaluate(() => {
    (window as any).__state.setup.drive_authorized = false;
  });
  await expect(page.getByText("需重新登录一次授权")).toBeVisible();
  const actions = (await page.evaluate(() => (window as any).__calls)).map(
    (c: any) => c.action,
  );
  expect(actions).toEqual(["drive.sync", "drive.sync", "drive.connect"]);
});

test("multi-instance cards display separate specs, switch workspace, start new GPU and stop selected instance", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.account.assignments = 3;
    state.account.instances = [
      {
        id: "m-s-t4",
        managed: true,
        active: true,
        accelerator: "T4",
        shape: "HIGH_RAM",
        state: "ready",
        gpu_name: "NVIDIA T4",
        vram_used_mib: 1200,
        vram_total_mib: 15360,
        loaded_model: "Qwen2.5 · 0.5B",
        location: {
          country: "US",
          country_name: "美国",
          region: "俄勒冈",
          zone: "us-west1-b",
        },
      },
      {
        id: "m-s-a100",
        managed: true,
        active: false,
        accelerator: "A100",
        shape: "HIGH_RAM",
        state: "ready",
        gpu_name: "NVIDIA A100",
        vram_used_mib: 6400,
        vram_total_mib: 40960,
        loaded_model: "Gemma-4-31B",
        location: {
          country: "SG",
          country_name: "新加坡",
          region: "",
          zone: "asia-southeast1-a",
        },
      },
      {
        id: "m-s-external",
        managed: false,
        active: false,
        accelerator: "G4",
        shape: "STANDARD",
        location: {
          country: "TW",
          country_name: "中国台湾",
          region: "彰化",
          zone: "asia-east1-c",
        },
      },
    ];
    state.runtime.id = "m-s-t4";
  });
  await expect(page.locator(".co-status-bar .co-location-chip")).toContainText("美国");
  await expect(
    page.locator(".co-status-bar .co-location-chip svg.co-flag-icon"),
  ).toBeVisible();
  await expect(page.locator(".co-device-badges .co-location-tag")).toContainText(
    "美国 · 俄勒冈",
  );
  const list = page.getByRole("region", { name: "账户运行实例" });
  await expect(list).toContainText("本客户端管理 2 台 · 其他来源 1 台");
  await expect(list).toContainText("已装载 Gemma-4-31B");
  await expect(list).toContainText("其他来源");
  await expect(list).toContainText("新加坡");
  await expect(list).toContainText("中国台湾 · 彰化");
  await expect(list.locator("svg.co-flag-icon")).toHaveCount(3);
  await expect(antSelect(page, "当前工作实例")).toBeVisible();

  const a100Card = list.locator(".co-instance-card", { hasText: "m-s-a100" });
  await a100Card.getByRole("button", { name: "切换使用", exact: true }).click();
  await expect(page.locator(".co-device-title")).toContainText("m-s-a100");
  await expect(page.locator(".co-status-bar .co-location-chip")).toContainText("新加坡");

  await chooseAntOption(page, antSelect(page, "GPU 型号"), /^L4\b/);
  await page.getByRole("button", { name: "新增实例", exact: true }).click();

  const extCard = list.locator(".co-instance-card", { hasText: "m-s-external" });
  await extCard.getByRole("button", { name: "单独停止", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("中国台湾");
  await expect(dialog.locator("svg.co-flag-icon")).toHaveCount(3);
  await expect(
    dialog.getByRole("button", { name: "确认停止所选（1）" }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "确认停止所选（1）" }).click();
  await expect(list).not.toContainText("m-s-external");
  await expect(list).toContainText("m-s-t4");

  const calls = await page.evaluate(() => (window as any).__calls);
  expect(
    calls.find((c: any) => c.action === "runtime.select")?.payload,
  ).toEqual({ endpoint: "m-s-a100" });
  expect(calls.find((c: any) => c.action === "runtime.start")?.payload).toEqual(
    {
      new: true,
      gpu: "L4",
    },
  );
  expect(
    calls.find((c: any) => c.action === "runtime.release.all")?.payload
      .endpoints,
  ).toEqual(["m-s-external"]);
});

test("billing is prominent and stop all reviews every instance before submission", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__state.account.assignments = 2;
  });
  await expect(page.locator(".co-billing-pill")).toContainText("正在扣费");
  await expect(page.locator(".co-billing-pill")).toHaveCSS(
    "color",
    "rgb(220, 38, 38)",
  );
  await expect(page.locator(".co-device-page")).toContainText("正在扣费");
  await page
    .getByRole("button", { name: "停止全部（2 个实例）", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("other-runtime");
  await expect(dialog).toContainText("其他来源的 Colab 实例");
  expect(
    (await page.evaluate(() => (window as any).__calls)).map(
      (x: any) => x.action,
    ),
  ).toEqual(["runtime.release.preview"]);
  await dialog.getByRole("button", { name: "确认停止全部（2）" }).click();
  await expect(page.locator(".co-billing-pill")).toContainText(/未扣费/);
  const calls = await page.evaluate(() => (window as any).__calls);
  expect(calls[1]).toEqual({
    action: "runtime.release.all",
    payload: { force: false, plan_id: "reviewed-plan" },
  });
});

test("activity sidebar follows progress across pages and can filter clear and collapse", async ({
  page,
}) => {
  await mock(page, true);
  await expect(page.locator(".co-logs-trigger")).toHaveText("运行日志");
  await expect(page.locator(".co-logs-count")).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  const panel = page.getByRole("complementary", { name: "运行日志" });
  await expect(panel).toContainText("已恢复本机保存的 Google 登录");
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.operation = {
      title: "部署远端管理服务",
      running: true,
    };
    state.logs.push({
      id: 2,
      time: Date.now() / 1000,
      source: "GPU",
      level: "error",
      message: "连接中断，请重试",
    });
  });
  await expect(panel).toContainText("部署远端管理服务");
  const opBanner = page.locator(".co-op-banner");
  await expect(opBanner).toContainText("部署远端管理服务");
  await expect(opBanner.locator(".ant-spin")).toBeVisible();
  await expect(opBanner.locator(".ant-alert-icon")).toHaveCount(0);
  const bannerBox = await page.evaluate(() => {
    const banner = document
      .querySelector(".co-op-banner")
      ?.getBoundingClientRect();
    const content = document
      .querySelector(".co-page-inner")
      ?.getBoundingClientRect();
    const shell = document
      .querySelector(".co-banners")
      ?.getBoundingClientRect();
    return banner && content && shell
      ? {
          leftGap: Math.round(banner.left - shell.left),
          rightGap: Math.round(shell.right - banner.right),
          leftAlignDiff: Math.abs(Math.round(banner.left - content.left)),
          rightAlignDiff: Math.abs(Math.round(banner.right - content.right)),
        }
      : null;
  });
  expect(bannerBox).not.toBeNull();
  expect(bannerBox!.leftGap).toBeGreaterThanOrEqual(12);
  expect(bannerBox!.rightGap).toBeGreaterThanOrEqual(12);
  expect(bannerBox!.leftAlignDiff).toBeLessThanOrEqual(2);
  expect(bannerBox!.rightAlignDiff).toBeLessThanOrEqual(2);
  await navItem(page, "对外 API").click();
  await expect(panel).toBeVisible();
  await segmentedItem(
    panel.locator('.ant-segmented[aria-label="日志级别"]'),
    "仅错误",
  ).click();
  await expect(panel).toContainText("连接中断，请重试");
  await expect(panel).not.toContainText("已恢复本机保存");
  await panel.getByRole("button", { name: "清空日志", exact: true }).click();
  await expect(panel).toContainText("暂无错误");
  await expect(panel.locator(".co-log-drawer-header")).toHaveCount(0);
  await page.getByRole("button", { name: "收起运行日志", exact: true }).click();
  await expect(panel).toBeHidden();
  await page.reload();
  await expect(panel).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(panel).toBeVisible();
});

test("desktop layout shows billing and activity without horizontal overflow", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mock(page, true);
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.runtime.gpu.name = "NVIDIA RTX PRO 6000 Blackwell Server Edition";
    state.account.rate = 10.05;
    state.logs.push(
      ...[
        "正在分配 G4",
        "等待远端内核",
        "部署远端管理服务",
        "远端 GPU 已连接",
      ].map((message, index) => ({
        id: index + 2,
        time: Date.now() / 1000,
        level: "info",
        source: "GPU",
        message,
      })),
    );
  });
  await expect(
    page.getByRole("button", { name: "停止实例", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "仅停止当前实例", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(page.getByRole("log")).toContainText("远端 GPU 已连接");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/blue-activity-ui.png", import.meta.url),
    ),
    fullPage: true,
  });
});

test("log drawer expands independently to the right without covering workspace and closes with Escape", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mock(page, true);
  const workspace = page.locator(".co-workspace");
  const panel = page.locator("#activity-panel");
  await expect(panel).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(panel).toBeVisible();

  // Wait for transition to settle before measuring non-overlapping geometry.
  await page.waitForTimeout(250);
  const wsBox = (await workspace.boundingBox())!;
  const drawerBox = (await panel.boundingBox())!;
  const topbarBox = (await page.locator(".co-status-bar").boundingBox())!;
  const toggleBtnBox = (await page
    .getByRole("button", { name: "收起运行日志", exact: true })
    .boundingBox())!;

  // Drawer sits strictly to the right of the main workspace (zero overlap!).
  expect(drawerBox.x).toBeGreaterThanOrEqual(wsBox.x + wsBox.width - 1);
  expect(drawerBox.width).toBeGreaterThanOrEqual(300);
  expect(topbarBox.x + topbarBox.width).toBeLessThanOrEqual(drawerBox.x + 1);
  expect(toggleBtnBox.x + toggleBtnBox.width).toBeLessThanOrEqual(
    drawerBox.x + 1,
  );

  // Native window rightward expansion API was notified.
  expect(await page.evaluate(() => (window as any).__logDrawerCalls)).toEqual([
    { open: true, width: 420 },
  ]);

  // Escape closes inline fallback drawer and notifies native host.
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).__logDrawerCalls)).toEqual([
    { open: true, width: 420 },
    { open: false, width: 420 },
  ]);

  // Top navbar pill toggles drawer cleanly without an inner redundant header row.
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(panel).toBeVisible();
  await expect(panel.locator(".co-log-drawer-header")).toHaveCount(0);
  await page.getByRole("button", { name: "收起运行日志", exact: true }).click();
  await expect(panel).toHaveCount(0);
});

test("standalone native log window opens independently without resizing main window and syncs close state", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1200, height: 820 });
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__onSetLogDrawer = (open: boolean) => ({
      open,
      standalone: true,
      expanded: false,
      delta: 0,
    });
  });

  const beforeBox = (await page.locator(".co-workspace").boundingBox())!;
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(
    page.getByRole("button", { name: "收起运行日志", exact: true }),
  ).toHaveClass(/is-active/);
  // Standalone OS window handles logs; main window keeps zero inline drawer and zero width change.
  await expect(page.locator("#activity-panel")).toHaveCount(0);
  const openBox = (await page.locator(".co-workspace").boundingBox())!;
  expect(Math.round(openBox.width)).toBe(Math.round(beforeBox.width));

  // Closing the standalone OS log window via its native close button syncs main window toggle.
  await page.evaluate(() => {
    window.dispatchEvent(
      new CustomEvent("colabone:log-window-state", { detail: { open: false } }),
    );
  });
  await expect(
    page.getByRole("button", { name: "展开运行日志", exact: true }),
  ).not.toHaveClass(/is-active/);
});

test("standalone log window route (?view=logs) renders full-bleed log viewer without redundant header bar", async ({
  page,
}) => {
  await page.setViewportSize({ width: 440, height: 820 });
  await mock(page, true, "/?view=logs");
  await expect(page).toHaveTitle("运行日志 · ColabOne");
  const panel = page.locator("#activity-panel.is-standalone");
  await expect(panel).toBeVisible();
  await expect(panel.locator(".co-log-drawer-header")).toHaveCount(0);
  await expect(panel.locator(".co-log-resizer")).toHaveCount(0);
  await expect(panel).toContainText("GPU 已就绪，等待操作");
  await expect(panel).toContainText("已恢复本机保存的 Google 登录");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);

  await panel.getByRole("button", { name: "复制日志", exact: true }).click();
  await expect(page.locator(".co-toast")).toContainText("已复制");
  await panel.getByRole("button", { name: "清空日志", exact: true }).click();
  await expect(panel).toContainText("暂无运行日志，开始操作后将在此实时显示");
  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/standalone-log-window.png", import.meta.url),
    ),
    fullPage: true,
  });
});

test("public API panel displays per-request token speed and live generation rate alongside right log drawer", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1480, height: 900 });
  await mock(page, true);
  const now = Math.floor(Date.now() / 1000);
  await page.evaluate((nowSec) => {
    const s = (window as any).__state;
    s.models[0].downloaded = true;
    s.models[0].loaded = true;
    s.runtime.loaded_model = "qwen";
    s.runtime.tasks = [
      {
        id: "req-3",
        kind: "public_responses",
        model_id: "qwen",
        state: "running",
        progress: 0,
        message: "",
        error: "",
      },
    ];
    s.runtime.public_api = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://api.example.com",
      error: "",
      streaming: true,
      checked_at: nowSec,
      requests: 3,
      active: 1,
      completed: 2,
      failed: 0,
      last_request: nowSec,
      last_tokens_per_second: 62.4,
      avg_tokens_per_second: 49.5,
      recent_requests: [
        {
          seq: 3,
          id: "req-3",
          kind: "responses",
          endpoint: "/v1/responses",
          model: "qwen",
          stream: true,
          state: "running",
          started_at: nowSec,
          duration_ms: 1450,
          ttft_ms: 210,
          prompt_tokens: 420,
          completion_tokens: 78,
          tokens_per_second: 62.4,
          estimated: true,
        },
        {
          seq: 2,
          id: "req-2",
          kind: "chat",
          endpoint: "/v1/chat/completions",
          model: "qwen",
          stream: true,
          state: "completed",
          started_at: nowSec - 20,
          finished_at: nowSec - 16,
          duration_ms: 3650,
          ttft_ms: 340,
          prompt_tokens: 1024,
          completion_tokens: 180,
          tokens_per_second: 54.2,
          prompt_tokens_per_second: 1420.0,
          estimated: false,
        },
        {
          seq: 1,
          id: "req-1",
          kind: "chat",
          endpoint: "/v1/chat/completions",
          model: "qwen",
          stream: false,
          state: "completed",
          started_at: nowSec - 60,
          finished_at: nowSec - 57,
          duration_ms: 2850,
          prompt_tokens: 128,
          completion_tokens: 128,
          tokens_per_second: 44.8,
          estimated: false,
        },
      ],
    };
    s.logs.push(
      {
        id: 2,
        time: nowSec - 57,
        source: "API",
        level: "info",
        message: "API 请求 #1 完成 · 44.8 tokens/s · 输出 128 tokens · 耗时 2.85s",
      },
      {
        id: 3,
        time: nowSec - 16,
        source: "API",
        level: "info",
        message:
          "API 请求 #2 完成 · 54.2 tokens/s · 输出 180 tokens · 首字 340ms · 耗时 3.65s",
      },
    );
  }, now);

  await navItem(page, "对外 API").click();
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await page.waitForTimeout(250);

  const speedSection = page.getByRole("region", {
    name: "每次外部调用 Token 速度",
  });
  await expect(speedSection).toContainText("实时生成速度");
  await expect(speedSection).toContainText("≈62.4 tokens/s");
  await expect(speedSection).toContainText("均值 49.5 tokens/s");
  await expect(speedSection).toContainText("#3");
  await expect(speedSection).toContainText("Responses");
  await expect(speedSection).toContainText("#2");
  await expect(speedSection).toContainText("54.2 tokens/s");
  await expect(speedSection).toContainText("首字 340ms");
  await expect(speedSection).toContainText("预填充 1420 tokens/s");
  await expect(speedSection).toContainText("#1");
  await expect(speedSection).toContainText("44.8 tokens/s");

  // Right-side log drawer shows live speed & historical speed entries simultaneously without covering the API page.
  const panel = page.locator("#activity-panel");
  await expect(panel).toContainText("≈62.4 tokens/s");
  await expect(panel).toContainText("54.2 tokens/s");
  await expect(page.locator(".co-task-bar")).toContainText("≈62.4 tokens/s");

  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/api-speed-and-log-drawer.png", import.meta.url),
    ),
    fullPage: true,
  });
});

test("connected settings omits device jump buttons, keeps non-clickable billing strip, and preserves idle policy", async ({
  page,
}) => {
  await mock(page, true);
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("button", { name: /查看 GPU|去启动 GPU|前往远端设备/ }),
  ).toHaveCount(0);
  // Clicking the topbar billing strip must NOT jump back to the device page.
  await page.locator(".co-billing-strip").click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/单次预算|单次 GPU 预算/)).toHaveCount(0);
  await expect(page.locator(".co-settings-page")).toContainText(
    /半小时|30\s*分钟/,
  );
  await expect(page.locator(".co-settings-page")).not.toContainText("闲置 3 分钟");
});

test("community search selects all shards and submits remote install with fixed revision", async ({
  page,
}) => {
  await mock(page, true);
  await navItem(page, "设置").click();
  const concurrencySeg = page.locator(
    '.ant-segmented[aria-label="下载并发线程"]',
  );
  await expect(segmentedItem(concurrencySeg, "16 线程")).toBeVisible();
  await expect(segmentedItem(concurrencySeg, "8 线程")).toHaveCount(0);
  await segmentedItem(concurrencySeg, "32 线程").click();
  const drawer = await openApiModelDrawer(page);
  await expect(
    drawer.locator('.ant-segmented[aria-label="下载并发线程"]'),
  ).toHaveCount(0);
  await drawer.getByRole("tab", { name: /^社\s*区$/ }).click();
  await drawer
    .getByRole("searchbox", { name: "搜索 Hugging Face 模型" })
    .fill("Qwen");
  await expect(
    drawer.getByRole("heading", { name: "org/Qwen-GGUF" }),
  ).toBeVisible();
  await drawer.getByRole("button", { name: "选择版本" }).click();
  await expect(
    drawer.getByRole("radiogroup", { name: "选择量化版本" }),
  ).toContainText(/2 个.*文件/);
  await drawer
    .locator(".co-hub-selection")
    .getByRole("button", { name: "仅下载" })
    .click();
  const calls = await page.evaluate(() => (window as any).__calls);
  expect(
    calls.find((c: any) => c.action === "settings.save").payload
      .download_threads,
  ).toBe(32);
  expect(calls.find((c: any) => c.action === "hub.install").payload).toEqual({
    repo: "org/Qwen-GGUF",
    filename: "Qwen-Q4_K_M-00001-of-00002.gguf",
    revision: "a".repeat(40),
    download: true,
  });
  await expect(drawer.getByRole("tab", { name: /^我\s*的/ })).toHaveAttribute(
    "aria-selected",
    "true",
  );
});

test("download telemetry shows actual rate bytes ETA and connections", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__state.runtime.tasks = [
      {
        id: "test",
        kind: "download",
        state: "running",
        progress: 0.25,
        message: "model.gguf",
        download: {
          downloaded_bytes: 1024 ** 3,
          total_bytes: 4 * 1024 ** 3,
          speed_bps: 128 * 1024 ** 2,
          eta_seconds: 24,
          threads: 32,
          connections: 24,
          phase: "downloading",
          file_index: 1,
          file_count: 2,
        },
      },
    ];
  });
  await expect(page.locator(".co-task-bar")).toContainText("128.0 MiB/s");
  await expect(page.locator(".co-task-bar")).toContainText("24/32 个连接");
  await expect(page.locator(".co-task-bar")).toContainText("预计剩余 0:24");
});

test("model search spans the row and results expose size range downloads and likes", async ({
  page,
}) => {
  await mock(page);
  await page.evaluate(() => {
    const original = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, request: any) => {
      const result = await original(method, request);
      if (request?.action !== "hub.files") return result;
      const detail = JSON.parse(result);
      detail.files.push({
        filename: "Qwen-Q8_0.gguf",
        size: 8 * 1024 ** 3,
        shards: 1,
        quantization: "Q8_0",
        supported: true,
        reason: "",
      });
      return JSON.stringify(detail);
    };
  });
  const drawer = await openApiModelDrawer(page);
  const localToolbar = drawer.locator(".co-list-toolbar");
  const localSearch = localToolbar.locator(".co-model-search");
  expect((await localSearch.boundingBox())!.width).toBeCloseTo(
    (await localToolbar.boundingBox())!.width,
    0,
  );
  await drawer.getByRole("tab", { name: /^社\s*区$/ }).click();
  const hubSearchBar = drawer.locator(".co-hub-search-bar");
  const hubBrowser = drawer.locator(".co-hub-browser");
  const hubSearchInput = drawer.locator(".co-hub-search-input");
  const barBox = (await hubSearchBar.boundingBox())!;
  const bodyBox = (await hubBrowser.boundingBox())!;
  const inputBox = (await hubSearchInput.boundingBox())!;
  expect(barBox.width).toBeGreaterThan(bodyBox.width - 20);
  expect(inputBox.width).toBeGreaterThan(barBox.width * 0.55);
  const facts = drawer.locator(".co-hub-facts");
  await expect(facts).toContainText(/3\.81 GiB – 8\.00 GiB/);
  await expect(facts).toContainText(/2,000/);
  await expect(facts).toContainText(/120/);
  await drawer.getByRole("button", { name: "选择版本" }).click();
  await expect(drawer.locator(".co-hub-download-summary")).toContainText("3.81 GiB");
  await expect(drawer.locator(".co-hub-download-summary")).toContainText(
    "共 2 个文件",
  );
  await drawer
    .getByRole("radiogroup", { name: "选择量化版本" })
    .getByRole("radio", { name: /Qwen-Q8_0\.gguf/ })
    .click();
  await expect(drawer.locator(".co-hub-download-summary")).toContainText("8.00 GiB");
  await expect(drawer.locator(".co-hub-download-summary")).toContainText(
    "共 1 个文件",
  );
  const calls = await page.evaluate(() => (window as any).__calls);
  expect(calls.filter((c: any) => c.action === "hub.files")).toHaveLength(1);
  expect(
    calls.every((c: any) => ["hub.search", "hub.files"].includes(c.action)),
  ).toBe(true);
});

test("loading more preserves pending size lookups and changed search clears selection", async ({
  page,
}) => {
  await mock(page);
  await page.evaluate(() => {
    const original = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, request: any) => {
      if (request?.action === "hub.search")
        return JSON.stringify({
          models: [
            {
              repo: request.payload.cursor ? "org/second" : "org/first",
              downloads: 10,
              likes: 2,
              gated: false,
            },
          ],
          next_cursor: request.payload.cursor ? "" : "page-2",
        });
      if (
        request?.action === "hub.files" &&
        request.payload.repo === "org/first"
      ) {
        await new Promise<void>((resolve) => {
          (window as any).__finishDetails = resolve;
        });
      }
      return original(method, request);
    };
  });
  const drawer = await openApiModelDrawer(page);
  await drawer.getByRole("tab", { name: /^社\s*区$/ }).click();
  await expect(drawer.locator(".co-hub-size")).toContainText("读取中");
  await drawer.getByRole("button", { name: "加载更多" }).click();
  await expect(drawer.locator(".co-hub-repo")).toHaveCount(2);
  await page.evaluate(() => (window as any).__finishDetails());
  await expect(drawer.locator(".co-hub-size strong")).toHaveText([
    "3.81 GiB",
    "3.81 GiB",
  ]);
  await drawer.getByRole("button", { name: "选择版本" }).first().click();
  await expect(
    drawer.getByRole("radiogroup", { name: "选择量化版本" }),
  ).toBeVisible();
  await drawer
    .getByRole("searchbox", { name: "搜索 Hugging Face 模型" })
    .fill("another");
  await expect(drawer.locator(".co-hub-selection")).toHaveCount(0);
});

test("idle countdown decrements locally and only real interaction resets remote activity", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__state.runtime.idle = {
      phase: "countdown",
      remaining_seconds: 60,
      countdown_seconds: 60,
      idle_seconds: 180,
      server_time: Date.now() / 1000,
    };
  });
  await expect(page.locator(".co-idle-timer")).toContainText(
    /(自动|即将)释放 1:00/,
  );
  await expect(page.locator(".co-idle-timer")).toContainText("0:59", {
    timeout: 4000,
  });
  expect(await page.evaluate(() => (window as any).__activity || 0)).toBe(0);
  await page.locator(".co-idle-timer").click();
  expect(await page.evaluate(() => (window as any).__activity)).toBe(1);
  await page.evaluate(() => {
    (window as any).__state.runtime.idle = {
      phase: "active",
      remaining_seconds: 240,
      countdown_seconds: 60,
      idle_seconds: 0,
      server_time: Date.now() / 1000,
    };
  });
  await expect(page.locator(".co-idle-timer")).toContainText(/暂停(自动)?释放/);
});

test("model load options pass real context settings from API drawer and top capsule navbar adapts across viewports", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__state.models[0].downloaded = true;
  });
  const modelDrawer = await openApiModelDrawer(page);
  const qwenRow = modelDrawer.locator(".co-model-card, .co-model-row", {
    hasText: "Qwen2.5 · 0.5B",
  });
  await qwenRow.getByRole("button", { name: /^参\s*数$/ }).click();
  const configDrawer = modelDrawer.locator(".co-model-config-drawer");
  await expect(configDrawer).toBeVisible();
  await configDrawer.getByRole("button", { name: "16K", exact: true }).click();
  const tempSlider = configDrawer.getByRole("slider");
  await tempSlider.focus();
  for (let i = 0; i < 4; i++) {
    await page.keyboard.press("ArrowLeft");
  }
  await expect(configDrawer).toContainText("0.3");
  await configDrawer.getByRole("button", { name: "装载显存" }).click();
  const call = await page.evaluate(() =>
    (window as any).__calls.find((c: any) => c.action === "model.load"),
  );
  expect(call.payload).toEqual({
    id: "qwen",
    context_length: 16384,
    temperature: 0.3,
  });
  await modelDrawer
    .locator(".co-vram-console")
    .getByRole("button", { name: "使用此模型" })
    .click();
  await expect(modelDrawer).toBeHidden();
  await expect(page.locator(".co-api-model-slot")).toContainText("Qwen2.5 · 0.5B");
  await expect(page.locator(".co-api-model-slot")).toContainText("16K · 温度 0.3");
  await expect(page.locator(".co-api-examples pre")).toContainText('"model": "qwen"');

  await expect(page.locator(".co-brand-icon svg")).toBeVisible();
  await expect(page.locator(".co-nav-pill-track")).toHaveCSS(
    "border-radius",
    "9999px",
  );
  await expect(
    page.locator(".co-navbar nav .co-nav-pill-item.is-active"),
  ).toHaveCSS("border-radius", "9999px");

  // Compact viewport collapses pill labels cleanly to icon pills without losing accessible names.
  await page.setViewportSize({ width: 720, height: 800 });
  await navItem(page, "远端设备").click();
  await expect(page.getByRole("heading", { name: "远端设备" })).toBeVisible();
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 820 });
});

test("model drawer in API page supports 我的 & 社区, inline repo version picking, removal, one-click download-and-load and quick unload", async ({
  page,
}) => {
  await mock(page, false);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.models[0].common = false;
    s.common_repos = [
      {
        repo: "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        name: "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        note: "默认模型",
      },
      {
        repo: "huihui-ai/Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
        name: "Huihui-DeepSeek-V4.1-Flash-1017-abliterated-GGUF",
        note: "默认模型",
      },
      {
        repo: "JonathanColetti/Qwen3.8-27B-Uncensored",
        name: "Qwen3.8-27B-Uncensored",
        note: "默认模型",
      },
    ];
  });
  const drawer = await openApiModelDrawer(page);
  const sourceTabs = drawer.locator(".co-model-tabs").getByRole("tablist");
  await expect(sourceTabs.getByRole("tab")).toHaveCount(2);
  await expect(sourceTabs.getByRole("tab", { name: /^我\s*的/ })).toBeVisible();
  await expect(sourceTabs.getByRole("tab", { name: /^社\s*区$/ })).toBeVisible();
  // Default "我的" view shows only the 3 default repositories (no extra built-in presets shelf).
  await expect(drawer.locator(".co-common-repo-row")).toHaveCount(3);
  await expect(drawer.locator(".preset-shelf")).toHaveCount(0);
  await expect(drawer.getByRole("button", { name: /手动导入/ })).toHaveCount(0);

  // Clicking "选择版本" expands inline version picker right inside "我的".
  const firstDefaultRow = drawer.locator(".co-common-repo-row").first();
  await firstDefaultRow.getByRole("button", { name: "选择版本" }).click();
  await expect(firstDefaultRow.locator(".co-repo-picker")).toBeVisible();
  await expect(
    firstDefaultRow.getByRole("button", { name: "下载并装载" }),
  ).toBeDisabled();
  await firstDefaultRow.getByRole("button", { name: "收起" }).click();
  await expect(firstDefaultRow.locator(".co-repo-picker")).toHaveCount(0);

  // Switch to "社区", pin a repo to "我的", then remove it from "我的".
  await sourceTabs.getByRole("tab", { name: /^社\s*区$/ }).click();
  await expect(drawer.locator(".curated-card")).toHaveCount(0);
  const hubCard = drawer.locator(".co-hub-repo", { hasText: "org/Qwen-GGUF" });
  await expect(hubCard).toBeVisible();
  await hubCard.getByRole("button", { name: /加入我的/ }).click();
  await expect(hubCard.getByRole("button", { name: /移出我的/ })).toBeVisible();
  await sourceTabs.getByRole("tab", { name: /^我\s*的/ }).click();
  const addedRepoRow = drawer.locator(".co-common-repo-row", {
    hasText: "org/Qwen-GGUF",
  });
  await expect(addedRepoRow).toBeVisible();
  await addedRepoRow.getByRole("button", { name: /移出我的/ }).click();
  await expect(
    drawer.locator(".co-common-repo-row", { hasText: "org/Qwen-GGUF" }),
  ).toHaveCount(0);

  // Bring GPU online and verify one-click download-and-load + quick unload.
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.models[0].common = true;
    s.runtime.state = "ready";
    s.runtime.protocol = 7;
    s.runtime.gpu = { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 };
  });
  const qwenCard = drawer.locator(".co-model-card", { hasText: "Qwen2.5 · 0.5B" });
  await qwenCard.getByRole("button", { name: "下载并装载" }).click();
  const dlCall = await page.evaluate(() =>
    (window as any).__calls.find((c: any) => c.action === "model.download"),
  );
  expect(dlCall?.payload).toMatchObject({ id: "qwen", load: true });
  await expect(qwenCard).toContainText("使用中");

  const vramConsole = drawer.locator(".co-vram-console");
  await expect(vramConsole).toContainText("当前使用：Qwen2.5 · 0.5B");
  await expect(
    vramConsole.getByRole("button", { name: "使用此模型" }),
  ).toBeVisible();
  await vramConsole.getByRole("button", { name: /^卸\s*载$/ }).click();
  await expect
    .poll(() =>
      page.evaluate(() =>
        (window as any).__calls.some((c: any) => c.action === "model.unload"),
      ),
    )
    .toBe(true);
  await expect(vramConsole).toHaveCount(0);
  await expect(qwenCard).toContainText("已下载");
});

test("API setup uses fixed domain only, remains local until enabled, and includes Codex config", async ({
  page,
}) => {
  await mock(page);
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  // Service model bar is a flat top-level card (not nested inside another hero card).
  await expect(page.locator(".co-api-page .ant-card .ant-card")).toHaveCount(0);
  await expect(page.locator(".co-api-hero-card")).toHaveCount(0);
  await expect(page.locator(".co-api-model-slot")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "复制 API 地址", exact: true }),
  ).toBeDisabled();
  // Temporary/quick domain option is completely removed; fixed-domain inputs are immediately visible.
  await expect(page.locator(".co-api-connection")).not.toContainText("临时地址");
  await expect(page.getByLabel("API 域名", { exact: true })).toBeVisible();
  await expect(page.locator(".co-api-examples pre")).toContainText(
    '"stream": true',
  );
  await expect(page.locator(".co-api-examples pre")).toContainText(
    '"model": "YOUR-MODEL-ID"',
  );
  await page.getByLabel("API 域名", { exact: true }).fill("api.example.com");
  await page
    .getByLabel("Cloudflare 安装命令或 Tunnel Token", { exact: true })
    .fill("offline-placeholder");
  await page.getByRole("button", { name: "保存配置", exact: true }).click();
  expect(
    (await page.evaluate(() => (window as any).__calls)).map(
      (x: any) => x[0] || x.action,
    ),
  ).toContain("api.save");
  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "Python",
  ).click();
  await expect(page.locator(".co-api-examples pre")).toContainText(
    "from openai import OpenAI",
  );
  await expect(page.locator(".co-api-examples pre")).toContainText(
    "client.chat.completions.create",
  );
  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "Codex",
  ).click();
  await expect(page.locator(".co-api-examples pre")).toContainText(
    'wire_api = "responses"',
  );
  await expect(page.locator(".co-api-examples pre")).toContainText(
    "model_auto_compact_token_limit",
  );
  const calls = await page.evaluate(() => (window as any).__calls);
  expect(JSON.stringify(calls)).not.toContain("runtime.start");
  expect(JSON.stringify(calls)).not.toContain("api.start");
});

test("API page starts and stops only the public service and preserves page width", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.api_settings = {
      mode: "named",
      domain: "https://api.example.com",
      has_token: true,
      has_key: true,
    };
    s.runtime.public_api = {
      state: "stopped",
      enabled: false,
      mode: "named",
      url: "",
      requests: 0,
      active: 0,
      completed: 0,
      failed: 0,
    };
  });
  await navItem(page, "对外 API").click();
  await page
    .getByRole("button", { name: "开启 API 服务", exact: true })
    .click();
  await page.evaluate(() => {
    (window as any).__state.runtime.public_api = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://api.example.com",
      requests: 2,
      active: 1,
      completed: 1,
      failed: 0,
    };
  });
  await expect(
    page.getByRole("button", { name: "关闭服务", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".co-api-access")).toContainText(
    "https://api.example.com/v1",
  );
  await expect(page.getByLabel("API 域名", { exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "关闭服务", exact: true }).click();
  const calls = JSON.stringify(
    await page.evaluate(() => (window as any).__calls),
  );
  expect(calls).toContain("api.start");
  expect(calls).toContain("api.stop");
  expect(calls).not.toContain("runtime.release");
  await page.setViewportSize({ width: 900, height: 800 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("ComfyUI Studio validates and imports API workflow offline, rejects canvas format, and runs with dynamic overrides", async ({
  page,
}) => {
  await mock(page, false);
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();
  const tablist = page.getByRole("tablist", { name: "ComfyUI 模式" });
  await expect(tablist.getByRole("tab")).toHaveCount(2);
  await expect(
    tablist.getByRole("tab", { name: /^工作流工坊/ }),
  ).toHaveAttribute("aria-selected", "true");

  // Open import modal while GPU is stopped (offline import).
  await page
    .getByRole("button", { name: "导入 API 工作流 (JSON)" })
    .click();
  const modal = page.getByRole("dialog", {
    name: "导入 ComfyUI 工作流 (API JSON)",
  });
  await expect(modal).toBeVisible();

  // Reject ComfyUI Canvas format JSON with actionable Save (API Format) hint.
  await modal.getByLabel("工作流名称").fill("画布误导出测试");
  await modal
    .getByLabel("API 工作流 JSON")
    .fill(JSON.stringify({ nodes: [{ id: 1, type: "KSampler" }], links: [] }));
  await expect(modal.getByRole("alert")).toContainText("Save (API Format)");
  await expect(
    modal.getByRole("button", { name: "导入工作流", exact: true }),
  ).toBeDisabled();

  // Valid API format JSON passes validation and imports offline.
  const apiWorkflow = {
    "3": {
      class_type: "CLIPTextEncode",
      _meta: { title: "Prompt" },
      inputs: { text: "ocean sunrise", clip: ["1", 0] },
    },
    "6": {
      class_type: "KSampler",
      _meta: { title: "Sampler" },
      inputs: { seed: 42, steps: 20, positive: ["3", 0] },
    },
  };
  await modal.getByLabel("工作流名称").fill("测试文生图工作流");
  await modal.getByLabel("API 工作流 JSON").fill(JSON.stringify(apiWorkflow));
  await expect(modal).toContainText("格式校验通过，检测到 2 个节点");
  await modal.getByRole("button", { name: "导入工作流", exact: true }).click();
  await expect(modal).toBeHidden();

  // Imported workflow renders dynamic form fields.
  await expect(page.locator(".co-comfy-workflow-list")).toContainText(
    "测试文生图工作流",
  );
  const promptArea = page.locator(".co-comfy-param-form textarea").first();
  await expect(promptArea).toHaveValue("ocean sunrise");

  // Bring GPU online, start ComfyUI engine, edit prompt, and submit run.
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.state = "ready";
    s.runtime.gpu = { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 };
  });
  await page
    .getByRole("button", { name: "启动 ComfyUI", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "停止 ComfyUI" }),
  ).toBeVisible();

  await promptArea.fill("neon harbor rain");
  await page.getByRole("button", { name: "运行工作流" }).click();
  const calls = await page.evaluate(() => (window as any).__calls);
  expect(
    calls.some((c: any) => c.action === "comfyui.workflow.import"),
  ).toBe(true);
  expect(calls.some((c: any) => c.action === "comfyui.start")).toBe(true);
  const runCall = calls.find((c: any) => c.action === "comfyui.run");
  expect(runCall?.payload?.overrides).toMatchObject({
    "3.text": "neon harbor rain",
    "6.seed": 42,
    "6.steps": 20,
  });
});

test("ComfyUI Native Canvas manages authenticated tunnel and opens window or browser without exposing token", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.active_engine = "comfyui";
    s.runtime.comfyui.running = true;
    s.runtime.comfyui.state = "ready";
  });
  await navItem(page, "ComfyUI").click();
  await page
    .getByRole("tablist", { name: "ComfyUI 模式" })
    .getByRole("tab", { name: /^原生画布/ })
    .click();
  await expect(
    page.getByRole("heading", { name: "原生节点画布安全访问" }),
  ).toBeVisible();

  // Fixed-domain mode only: temporary option is removed and domain + token inputs are visible directly.
  const canvasSection = page.locator(".co-comfy-canvas-section");
  await expect(canvasSection).not.toContainText("临时安全地址");
  await expect(page.getByLabel("画布域名")).toBeVisible();
  await page.getByLabel("画布域名").fill("comfy.example.com");
  await page
    .getByLabel("Cloudflare 安装命令或 Tunnel Token")
    .fill("offline-placeholder");

  // Start authenticated canvas tunnel.
  await page.getByRole("button", { name: "开启原生画布" }).click();
  await expect(page.locator(".co-comfy-canvas-access")).toContainText(
    "comfy.example.com",
  );
  // Auth token must never be rendered in visible DOM.
  await expect(page.locator("body")).not.toContainText(
    "mock-secret-canvas-token",
  );

  // Launch in dedicated native window and system browser via Go bridge bindings.
  await page.getByRole("button", { name: "在独立窗口打开" }).click();
  await page.getByRole("button", { name: "在系统浏览器打开" }).click();
  expect(await page.evaluate(() => (window as any).__canvasOpenCalls)).toEqual([
    "window",
    "browser",
  ]);

  // Stop canvas tunnel.
  await page.getByRole("button", { name: "关闭画布隧道" }).first().click();
  const actions = (await page.evaluate(() => (window as any).__calls)).map(
    (c: any) => c.action,
  );
  expect(actions).toContain("comfyui.canvas.start");
  expect(actions).toContain("comfyui.canvas.stop");
});

test("appearance follows system, persists explicit choices and updates native chrome", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await mock(page);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await navItem(page, "设置").click();
  const choices = page.locator('.ant-segmented[aria-label="外观模式"]');
  await expect(segmentedItem(choices, "跟随系统")).toHaveClass(
    /ant-segmented-item-selected/,
  );
  await expect(
    segmentedItem(choices, "跟随系统").locator('input[type="radio"]'),
  ).toBeChecked();
  await segmentedItem(choices, "浅色").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() => page.evaluate(() => (window as any).__nativeAppearance))
    .toBe("light");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await navItem(page, "设置").click();
  await expect(segmentedItem(choices, "浅色")).toHaveClass(
    /ant-segmented-item-selected/,
  );
  await expect(
    segmentedItem(choices, "浅色").locator('input[type="radio"]'),
  ).toBeChecked();
  await segmentedItem(choices, "跟随系统").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "切换到深色模式" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(segmentedItem(choices, "深色")).toHaveClass(
    /ant-segmented-item-selected/,
  );
  await expect(
    segmentedItem(choices, "深色").locator('input[type="radio"]'),
  ).toBeChecked();
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);
  await page
    .getByRole("button", { name: "添加到开始菜单", exact: true })
    .click();
  await expect(page.getByText("已加入开始菜单")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "更新开始菜单快捷方式", exact: true }),
  ).toBeVisible();
});

for (const appearance of ["light", "dark"] as const) {
  test(`${appearance} ColabOne capsule theme keeps high contrast, red billing and readable workspace controls`, async ({
    page,
  }) => {
    await page.emulateMedia({ colorScheme: appearance });
    await mock(page, true);
    const bg = appearance === "dark" ? "rgb(21, 22, 27)" : "rgb(255, 255, 255)";
    const elevatedBg =
      appearance === "dark" ? "rgb(28, 30, 38)" : "rgb(255, 255, 255)";
    const brandColor =
      appearance === "dark" ? "rgb(244, 244, 245)" : "rgb(17, 24, 39)";
    const activePillBg =
      appearance === "dark" ? "rgb(255, 255, 255)" : "rgb(17, 24, 39)";
    const activePillText =
      appearance === "dark" ? "rgb(17, 24, 39)" : "rgb(255, 255, 255)";
    await expect(page.locator(".co-brand")).toHaveCSS("color", brandColor);
    const activeNav = page.locator(".co-navbar nav .co-nav-pill-item.is-active");
    await expect(activeNav).toHaveCSS("background-color", activePillBg);
    await expect(activeNav).toHaveCSS("color", activePillText);
    await expect(activeNav).toHaveCSS("border-radius", "9999px");
    await expect(page.locator(".co-device-panel")).toHaveCSS(
      "background-color",
      bg,
    );
    await expect(page.locator(".co-billing-pill")).toHaveCSS(
      "color",
      appearance === "dark" ? "rgb(248, 113, 113)" : "rgb(220, 38, 38)",
    );
    await expect(page.locator(".co-stop-all")).toHaveCSS(
      "color",
      "rgb(255, 255, 255)",
    );
    await page.getByRole("button", { name: "展开运行日志" }).click();
    await expect(page.locator(".co-log-drawer")).toHaveCSS(
      "background-color",
      elevatedBg,
    );
    await page.getByRole("button", { name: "收起运行日志", exact: true }).click();
    await page.evaluate(() => {
      (window as any).__state.models[0].downloaded = true;
    });
    const checkWorkspace = async (label: string) => {
      const problems = await page.evaluate(() => {
        const failures: string[] = [];
        const rgba = (value: string) => {
          const m = value.match(/[\d.]+/g)!.map(Number);
          return [m[0], m[1], m[2], m.length >= 4 ? m[3] : 1];
        };
        const blend = (fg: number[], bg: number[]) => {
          const a = fg[3];
          return [
            fg[0] * a + bg[0] * (1 - a),
            fg[1] * a + bg[1] * (1 - a),
            fg[2] * a + bg[2] * (1 - a),
            1,
          ];
        };
        const effectiveBg = (node: HTMLElement) => {
          const layers: number[][] = [];
          let cur: HTMLElement | null = node;
          while (cur) {
            const bg = rgba(getComputedStyle(cur).backgroundColor);
            if (bg[3] > 0) layers.push(bg);
            if (bg[3] >= 1) break;
            cur = cur.parentElement;
          }
          let acc = [255, 255, 255, 1];
          for (let i = layers.length - 1; i >= 0; i--) {
            acc = blend(layers[i], acc);
          }
          return acc;
        };
        const luminance = (c: number[]) =>
          c
            .slice(0, 3)
            .map((v) => {
              v /= 255;
              return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
            })
            .reduce(
              (sum, value, i) => sum + value * [0.2126, 0.7152, 0.0722][i],
              0,
            );
        for (const el of document.querySelectorAll<HTMLElement>(
          "input:not([type=range]):not([type=radio]):not([type=checkbox]), select, textarea, .ant-select-selector, .ant-card, .co-vram-console, .co-model-card, .co-model-config-drawer, .co-common-repo-row, .co-comfy-card, .ant-segmented-item-selected",
        )) {
          if (!el.offsetWidth || (el as HTMLInputElement).disabled) continue;
          const css = getComputedStyle(el),
            rawBg = rgba(css.backgroundColor);
          if (rawBg[3] === 0) continue;
          const bg = effectiveBg(el),
            fg = blend(rgba(css.color), bg);
          const a = luminance(fg),
            b = luminance(bg);
          if ((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05) < 4.5)
            failures.push(`contrast ${el.className}`);
        }
        if (document.documentElement.scrollWidth > innerWidth)
          failures.push("horizontal overflow");
        if (document.documentElement.scrollHeight > innerHeight)
          failures.push("vertical document overflow");
        return failures;
      });
      expect(problems, label).toEqual([]);
    };
    for (const label of ["对外 API", "ComfyUI", "设置"] as const) {
      await navItem(page, label).click();
      await checkWorkspace(label);
      if (label === "对外 API") {
        await page
          .locator(".co-api-model-slot")
          .getByRole("button", { name: /选择模型|切换模型/ })
          .click();
        await expect(page.locator(".co-models-drawer")).toBeVisible();
        await checkWorkspace("对外 API - 模型抽屉");
        await page.keyboard.press("Escape");
        await expect(page.locator(".co-models-drawer")).toBeHidden();
      }
    }
    await page.screenshot({
      path: `../work/appearance-${appearance}.png`,
      fullPage: true,
    });
    await navItem(page, "远端设备").click();
    await page
      .getByRole("button", { name: "停止实例", exact: true })
      .click();
    await expect(page.locator(".ant-modal-content")).toHaveCSS(
      "background-color",
      elevatedBg,
    );
    expect(
      await page.evaluate(() => (window as any).__state.account.assignments),
    ).toBe(1);
  });
}

test("topbar and homepage display active billing, burn rate, balance, days-hours endurance, idle dropdown, and omit CU budget", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.account = {
      logged_in: true,
      balance: 2207.42,
      rate: 1.96,
      assignments: 1,
      checked_at: Date.now() / 1000,
    };
  });

  const topbar = page.locator(".co-status-bar");
  await expect(topbar).toContainText("正在扣费");
  await expect(topbar).toContainText("1.96");
  await expect(topbar).toContainText("2207.42");
  await expect(topbar).toContainText("46 天 22 小时");

  const devicePage = page.locator(".co-device-page");
  await expect(devicePage).toContainText("正在扣费");
  await expect(devicePage).toContainText("1.96");
  await expect(devicePage).toContainText("2207.42");
  await expect(devicePage).toContainText("46 天 22 小时");
  await expect(page.getByText(/单次预算|单次 GPU 预算/)).toHaveCount(0);

  const idleSelect = antSelect(devicePage, "空闲自动释放时长");
  await expect(idleSelect).toBeVisible();
  await expect(idleSelect).toContainText("30 分钟（默认）");

  await idleSelect.click();
  const dropdown = page
    .locator(".ant-select-dropdown:not(.ant-select-dropdown-hidden)")
    .last();
  await expect(dropdown).toBeVisible();
  await expect(dropdown).toContainText("30 分钟（默认）");
  await expect(dropdown).toContainText("45 分钟");
  await expect(dropdown).toContainText("1 小时");
  await dropdown
    .locator(".ant-select-item-option", { hasText: "45 分钟" })
    .first()
    .click();

  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as any).__calls
            .filter((c: any) => c.action === "settings.save")
            .at(-1)?.payload,
      ),
    )
    .toMatchObject({ idle_minutes: 45 });
  await expect(idleSelect).toContainText("45 分钟");

  await chooseAntOption(page, idleSelect, "1 小时");
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (window as any).__calls
            .filter((c: any) => c.action === "settings.save")
            .at(-1)?.payload,
      ),
    )
    .toMatchObject({ idle_minutes: 60 });
  await expect(idleSelect).toContainText("1 小时");

  await navItem(page, "设置").click();
  await expect(page.getByText(/单次预算|单次 GPU 预算/)).toHaveCount(0);
});

test("homepage renders ColabOne real-time resource monitor charts for GPU VRAM, RAM, Disk, and CPU", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    const now = Date.now() / 1000;
    s.runtime.resources = {
      gpu: {
        name: "NVIDIA A100-SXM4-40GB",
        total_mib: 40960,
        used_mib: 16384,
        util_percent: 68,
        temp_c: 64,
        power_w: 185,
        power_limit_w: 400,
      },
      memory: {
        total_bytes: 83.5 * 1024 ** 3,
        used_bytes: 29.2 * 1024 ** 3,
        available_bytes: 54.3 * 1024 ** 3,
        percent: 35.0,
      },
      disk: {
        total_bytes: 200 * 1024 ** 3,
        used_bytes: 56 * 1024 ** 3,
        free_bytes: 144 * 1024 ** 3,
        percent: 28.0,
        read_bps: 36 * 1024 ** 2,
        write_bps: 14 * 1024 ** 2,
      },
      cpu: {
        cores: 12,
        percent: 42.0,
      },
      history: [
        {
          t: now - 2,
          vram_pct: 38,
          gpu_util: 55,
          ram_pct: 34,
          cpu_pct: 36,
          disk_pct: 28,
          disk_read_bps: 20 * 1024 ** 2,
          disk_write_bps: 10 * 1024 ** 2,
        },
        {
          t: now - 1,
          vram_pct: 40,
          gpu_util: 68,
          ram_pct: 35,
          cpu_pct: 42,
          disk_pct: 28,
          disk_read_bps: 36 * 1024 ** 2,
          disk_write_bps: 14 * 1024 ** 2,
        },
      ],
    };
  });

  const monitor = page.locator(".co-monitor");
  await expect(monitor).toBeVisible();
  await expect(monitor).toContainText("GPU 显存");
  await expect(monitor).toContainText("系统内存");
  await expect(monitor).toContainText("远端磁盘");
  await expect(monitor).toContainText("处理器");
  await expect(monitor).toContainText("NVIDIA A100-SXM4-40GB");
  await expect(monitor).toContainText("35%");
  await expect(monitor).toContainText("36.0 MB/s");
  await expect(monitor).toContainText("12 vCPU");
  await expect(monitor.locator("svg.co-rm-svg")).toHaveCount(4);

  // Layout switcher is icon-only and toggling to rows renders all 4 charts stacked simultaneously
  const layoutSwitch = monitor.locator(".co-rm-layout-segmented");
  await expect(layoutSwitch).toBeVisible();
  const visibleToggleText = await layoutSwitch.evaluate((el) =>
    Array.from(el.querySelectorAll(".co-rm-seg-label"))
      .map((node) => {
        const clone = node.cloneNode(true) as HTMLElement;
        clone.querySelectorAll(".co-rm-sr-only").forEach((n) => n.remove());
        return (clone.textContent || "").trim();
      })
      .join(""),
  );
  expect(visibleToggleText).toBe("");

  await layoutSwitch.locator(".ant-segmented-item").nth(1).click();
  await expect(monitor.locator(".co-rm-rows")).toBeVisible();
  await expect(monitor.locator(".co-rm-row")).toHaveCount(4);
  await expect(monitor.locator(".co-rm-row svg.co-rm-svg")).toHaveCount(4);
  await expect(monitor.locator(".co-rm-rows-axis-ticks")).toContainText("60 秒前");
  await expect(monitor.locator(".co-rm-rows-axis-ticks")).toContainText("现在");

  const rowGeometry = await monitor.evaluate((el) => {
    const rows = Array.from(el.querySelectorAll(".co-rm-row")).map((r) =>
      r.getBoundingClientRect(),
    );
    const plots = Array.from(el.querySelectorAll(".co-rm-row .co-rm-row-plot")).map(
      (p) => p.getBoundingClientRect(),
    );
    const axis = el
      .querySelector(".co-rm-rows-axis-ticks")
      ?.getBoundingClientRect();
    return {
      monitorHeight: Math.round(el.getBoundingClientRect().height),
      rows: rows.map((r) => ({ top: r.top, bottom: r.bottom, width: r.width })),
      plots: plots.map((p) => ({
        left: p.left,
        right: p.right,
        width: p.width,
        height: p.height,
      })),
      axis: axis ? { left: axis.left, right: axis.right } : null,
    };
  });

  // Compact vertical footprint: all 4 stacked rows stay crisp without dominating the viewport
  expect(rowGeometry.monitorHeight).toBeLessThanOrEqual(480);
  for (const row of rowGeometry.rows) {
    expect(row.bottom - row.top).toBeLessThanOrEqual(100);
  }
  // All 4 rows stacked vertically, non-overlapping
  for (let i = 1; i < rowGeometry.rows.length; i++) {
    expect(rowGeometry.rows[i].top).toBeGreaterThanOrEqual(
      rowGeometry.rows[i - 1].bottom,
    );
  }
  // All 4 charts share exact left/right X alignment with the synchronized timeline axis
  const basePlot = rowGeometry.plots[0];
  expect(basePlot.width).toBeGreaterThan(400);
  for (const plot of rowGeometry.plots) {
    expect(Math.abs(plot.left - basePlot.left)).toBeLessThanOrEqual(1);
    expect(Math.abs(plot.right - basePlot.right)).toBeLessThanOrEqual(1);
  }
  expect(rowGeometry.axis).not.toBeNull();
  expect(Math.abs(rowGeometry.axis!.left - basePlot.left)).toBeLessThanOrEqual(
    2,
  );
  expect(
    Math.abs(rowGeometry.axis!.right - basePlot.right),
  ).toBeLessThanOrEqual(2);
});

test("resource monitor shows connecting state before kernel telemetry and live curves during deployment", async ({
  page,
}) => {
  await mock(page, false);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.setup.colab = true;
    s.runtime = { state: "starting" };
    s.operation = { title: "部署远端管理服务", running: true };
  });

  const monitor = page.locator(".co-monitor");
  await expect(monitor).toHaveClass(/is-connecting/);
  await expect(monitor).toContainText("正在连接");
  await expect(monitor).not.toContainText("待机预览");
  await expect(monitor).not.toContainText("20 MB/s");
  await expect(monitor).toContainText("正在初始化 T4 加速器");

  await page.evaluate(() => {
    const s = (window as any).__state;
    const now = Date.now() / 1000;
    s.runtime = {
      id: "gpu-t4-boot",
      state: "starting",
      gpu: {
        name: "Tesla T4",
        total_mib: 15360,
        used_mib: 512,
        util_percent: 12,
        temp_c: 40,
      },
      resources: {
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 512,
          util_percent: 12,
          temp_c: 40,
        },
        memory: {
          total_bytes: 16 * 1024 ** 3,
          used_bytes: 4 * 1024 ** 3,
          available_bytes: 12 * 1024 ** 3,
          percent: 25,
        },
        disk: {
          total_bytes: 100 * 1024 ** 3,
          used_bytes: 30 * 1024 ** 3,
          free_bytes: 70 * 1024 ** 3,
          percent: 30,
          read_bps: 4 * 1024 ** 2,
          write_bps: 2 * 1024 ** 2,
        },
        cpu: { cores: 4, percent: 48 },
        history: [
          {
            t: now - 1.5,
            vram_pct: 3.3,
            gpu_util: 12,
            ram_pct: 25,
            cpu_pct: 48,
            disk_pct: 30,
            disk_read_bps: 4 * 1024 ** 2,
            disk_write_bps: 2 * 1024 ** 2,
          },
        ],
      },
    };
  });

  await expect(monitor).not.toHaveClass(/is-connecting/);
  await expect(monitor).not.toHaveClass(/is-offline/);
  await expect(monitor).toContainText("部署中 · 实时采样");
  await expect(monitor).toContainText("Tesla T4");
  await expect(monitor).toContainText("4 vCPU");
  await expect(monitor).toContainText("48%");
});

test("simplified 2-tab model library exposes only 我的 and 社区 and displays concise status badges", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.loaded_model = "model-vram";
    s.models = [
      {
        id: "model-vram",
        name: "Qwen2.5 · 7B Instruct",
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
      {
        id: "model-disk",
        name: "DeepSeek-R1 · 8B",
        description: "推理模型 · 已缓存远端磁盘",
        repo: "unsloth/DeepSeek-R1-Distill-Llama-8B-GGUF",
        common: true,
        custom: true,
        size: 5_200_000_000,
        min_vram_mib: 6144,
        downloaded: true,
        loaded: false,
        files: [],
      },
      {
        id: "model-cloud",
        name: "Gemma 3 · 4B",
        description: "轻量多用途模型",
        repo: "bartowski/gemma-3-4b-it-GGUF",
        common: true,
        custom: true,
        size: 2_600_000_000,
        min_vram_mib: 4096,
        downloaded: false,
        loaded: false,
        files: [],
      },
    ];
  });

  const drawer = await openApiModelDrawer(page);
  const tablist = drawer.locator(".co-model-tabs").getByRole("tablist");
  await expect(tablist.getByRole("tab")).toHaveCount(2);
  await expect(tablist.getByRole("tab", { name: /^我\s*的/ })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(
    tablist.getByRole("tab", { name: /^社\s*区$/ }),
  ).toBeVisible();
  for (const legacyTab of [
    "常用模型",
    "常用",
    "在线开源社区",
    "当前收藏",
    "我的模型",
    "我的收藏",
    "已下载",
    "画图 / 视频",
    "在线搜索",
  ]) {
    await expect(tablist.getByRole("tab", { name: legacyTab, exact: true })).toHaveCount(0);
  }

  await expect(
    drawer.locator(".co-model-card", { hasText: "Qwen2.5 · 7B Instruct" }),
  ).toContainText("使用中");
  await expect(
    drawer.locator(".co-model-card", { hasText: "DeepSeek-R1 · 8B" }),
  ).toContainText("已下载");
  await expect(
    drawer.locator(".co-model-card", { hasText: "Gemma 3 · 4B" }),
  ).toContainText("下载并装载");
  await page.waitForTimeout(350);
  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/model-drawer-mine.png", import.meta.url),
    ),
    fullPage: true,
  });

  await tablist.getByRole("tab", { name: /^社\s*区$/ }).click();
  await expect(
    tablist.getByRole("tab", { name: /^社\s*区$/ }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(drawer.locator(".co-hub-browser")).toBeVisible();
  await expect(drawer.locator(".co-hub-repo").first()).toBeVisible();
  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/model-drawer-community.png", import.meta.url),
    ),
    fullPage: true,
  });
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();

  // Also verify "去选择模型 →" shortcut from 远端设备 jumps to 对外 API with the drawer open.
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.loaded_model = undefined;
    for (const m of s.models) m.loaded = false;
  });
  await navItem(page, "远端设备").click();
  await page.getByRole("button", { name: /去选择模型/ }).click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".co-models-drawer")).toBeVisible();
});

test("fullscreen widescreen layout aligns navbar, status bar and cards across all 4 sections", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1040 });
  await mock(page, true);
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  const checkRails = async (selector: string) => {
    const rails = await page.evaluate((sel) => {
      const brand = document.querySelector(".co-brand")?.getBoundingClientRect();
      const logs = document
        .querySelector(".co-logs-trigger")
        ?.getBoundingClientRect();
      const statusLeft = document
        .querySelector(".co-topbar-left")
        ?.getBoundingClientRect();
      const statusRight = document
        .querySelector(".co-topbar-right")
        ?.getBoundingClientRect();
      const main = document.querySelector(sel)?.getBoundingClientRect();
      return {
        navLeft: brand ? Math.round(brand.left) : -1,
        navRight: logs ? Math.round(logs.right) : -1,
        statusLeft: statusLeft ? Math.round(statusLeft.left) : -1,
        statusRight: statusRight ? Math.round(statusRight.right) : -1,
        mainLeft: main ? Math.round(main.left) : -1,
        mainRight: main ? Math.round(main.right) : -1,
        mainWidth: main ? Math.round(main.width) : 0,
        overflowX: document.documentElement.scrollWidth - window.innerWidth,
      };
    }, selector);
    expect(rails.overflowX).toBe(0);
    expect(rails.mainWidth).toBeGreaterThanOrEqual(1600);
    expect(Math.abs(rails.navLeft - rails.statusLeft)).toBeLessThanOrEqual(2);
    expect(Math.abs(rails.navLeft - rails.mainLeft)).toBeLessThanOrEqual(2);
    expect(Math.abs(rails.navRight - rails.statusRight)).toBeLessThanOrEqual(2);
    expect(Math.abs(rails.navRight - rails.mainRight)).toBeLessThanOrEqual(2);
  };

  // Device: wide hero row (GPU Control + Billing side by side) + 4-column ResourceMonitor
  await checkRails(".co-device-hero-grid");
  const deviceLayout = await page.evaluate(() => {
    const deviceCard = document
      .querySelector(".co-device-card")!
      .getBoundingClientRect();
    const billingCard = document
      .querySelector(".co-billing-card")!
      .getBoundingClientRect();
    const rmCards = Array.from(document.querySelectorAll(".co-rm-card")).map((el) =>
      Math.round(el.getBoundingClientRect().top),
    );
    return {
      heroSideBySide:
        Math.abs(deviceCard.top - billingCard.top) <= 2 &&
        billingCard.left > deviceCard.right,
      rmRowCount: new Set(rmCards).size,
      rmCardCount: rmCards.length,
    };
  });
  expect(deviceLayout).toEqual({
    heroSideBySide: true,
    rmRowCount: 1,
    rmCardCount: 4,
  });

  // API page: wide aligned rails + 2-column form inputs + equal-height cards
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await checkRails(".co-api-model-slot");
  const apiLayout = await page.evaluate(() => {
    const left = document
      .querySelector(".co-api-connection")!
      .getBoundingClientRect();
    const right = document
      .querySelector(".co-api-examples")!
      .getBoundingClientRect();
    const domain = document.querySelector("#api-domain")!.getBoundingClientRect();
    const tunnel = document.querySelector("#api-tunnel")!.getBoundingClientRect();
    return {
      equalHeight: Math.abs(left.height - right.height) <= 2,
      formSideBySide:
        Math.abs(domain.top - tunnel.top) <= 6 && tunnel.left > domain.right,
    };
  });
  expect(apiLayout).toEqual({ equalHeight: true, formSideBySide: true });

  // ComfyUI page: wide aligned rails + vertically centered pill tabs
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();
  await checkRails(".co-comfy-engine-card");
  const comfyTabAlign = await page.evaluate(() => {
    return Array.from(document.querySelectorAll(".co-comfy-tabs .ant-tabs-tab")).map(
      (tab) => {
        const t = tab.getBoundingClientRect();
        const label = tab
          .querySelector(".co-comfy-tab-label")!
          .getBoundingClientRect();
        const icon = tab
          .querySelector(".co-comfy-tab-label svg")!
          .getBoundingClientRect();
        const mid = (t.top + t.bottom) / 2;
        return {
          labelDelta: Math.abs((label.top + label.bottom) / 2 - mid),
          iconDelta: Math.abs((icon.top + icon.bottom) / 2 - mid),
        };
      },
    );
  });
  expect(comfyTabAlign.length).toBe(2);
  for (const item of comfyTabAlign) {
    expect(item.labelDelta).toBeLessThanOrEqual(1);
    expect(item.iconDelta).toBeLessThanOrEqual(1);
  }

  // Settings page: wide aligned rails + Bento 2-col primary & 3-col secondary rows
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();
  await checkRails(".co-about-card");
  const settingsLayout = await page.evaluate(() => {
    const primaryTops = Array.from(
      document.querySelectorAll(".co-settings-row-primary > .co-settings-card"),
    ).map((el) => Math.round(el.getBoundingClientRect().top));
    const secondaryTops = Array.from(
      document.querySelectorAll(".co-settings-row-secondary > .co-settings-card"),
    ).map((el) => Math.round(el.getBoundingClientRect().top));
    return {
      primaryCols: primaryTops.length,
      primaryRows: new Set(primaryTops).size,
      secondaryCols: secondaryTops.length,
      secondaryRows: new Set(secondaryTops).size,
    };
  });
  expect(settingsLayout).toEqual({
    primaryCols: 2,
    primaryRows: 1,
    secondaryCols: 3,
    secondaryRows: 1,
  });

  // Verify segmented control icons & labels stay vertically centered inside their pills
  const segAlign = await page.evaluate(() => {
    return Array.from(
      document.querySelectorAll(".co-appearance-section .ant-segmented-item"),
    ).map((item) => {
      const r = item.getBoundingClientRect();
      const inner = item.querySelector(".co-seg-item")!.getBoundingClientRect();
      const icon = item.querySelector(".co-seg-item svg")!.getBoundingClientRect();
      const mid = (r.top + r.bottom) / 2;
      return {
        innerDelta: Math.abs((inner.top + inner.bottom) / 2 - mid),
        iconDelta: Math.abs((icon.top + icon.bottom) / 2 - mid),
      };
    });
  });
  expect(segAlign.length).toBe(3);
  for (const item of segAlign) {
    expect(item.innerDelta).toBeLessThanOrEqual(1);
    expect(item.iconDelta).toBeLessThanOrEqual(1);
  }
});

test("theme toggle expands a ~1s circular arc reveal from the click point in both directions", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
  await mock(page);
  await navItem(page, "设置").click();

  await page.evaluate(() => {
    const calls: Array<{
      clipPath: string[];
      duration: number;
      pseudoElement?: string;
      fill?: string;
    }> = [];
    (window as any).__themeAnimCalls = calls;
    const orig = document.documentElement.animate.bind(document.documentElement);
    document.documentElement.animate = ((
      keyframes: any,
      options?: any,
    ) => {
      const clipPath = Array.isArray(keyframes)
        ? keyframes
            .map((frame: any) => frame?.clipPath)
            .filter((v: unknown): v is string => typeof v === "string")
        : Array.isArray(keyframes?.clipPath)
          ? keyframes.clipPath
          : [];
      if (clipPath.length >= 2) {
        calls.push({
          clipPath,
          duration: Number(options?.duration ?? 0),
          pseudoElement: options?.pseudoElement,
          fill: options?.fill,
        });
      }
      return orig(keyframes, options);
    }) as typeof document.documentElement.animate;
  });

  // Light -> Dark from top-right toggle button
  const toDarkBtn = page.getByRole("button", { name: "切换到深色模式" });
  const darkBox = (await toDarkBtn.boundingBox())!;
  const clickX = Math.round(darkBox.x + darkBox.width / 2);
  const clickY = Math.round(darkBox.y + darkBox.height / 2);
  await page.mouse.click(clickX, clickY);

  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect
    .poll(() => page.evaluate(() => (window as any).__themeAnimCalls.length))
    .toBe(1);

  const darkAnim = await page.evaluate(() => (window as any).__themeAnimCalls[0]);
  expect(darkAnim.pseudoElement).toBe("::view-transition-new(root)");
  expect(darkAnim.duration).toBe(1000);
  expect(darkAnim.fill).toBe("both");
  expect(darkAnim.clipPath[0]).toMatch(
    new RegExp(`^circle\\(0px at ${clickX}(?:\\.\\d+)?px ${clickY}(?:\\.\\d+)?px\\)$`),
  );
  const darkEnd = darkAnim.clipPath[darkAnim.clipPath.length - 1];
  const darkEndMatch = darkEnd.match(/^circle\((\d+)px at 1280px 0px\)$/);
  expect(Number(darkEndMatch?.[1] ?? 0)).toBeGreaterThan(1000);

  // Dark -> Light immediately (proves clicks are not blocked mid-transition and
  // both directions expand outward into a clean top-right corner arc).
  const toLightBtn = page.getByRole("button", { name: "切换到浅色模式" });
  await toLightBtn.click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() => page.evaluate(() => (window as any).__themeAnimCalls.length))
    .toBe(2);

  const lightAnim = await page.evaluate(() => (window as any).__themeAnimCalls[1]);
  expect(lightAnim.pseudoElement).toBe("::view-transition-new(root)");
  expect(lightAnim.duration).toBe(1000);
  expect(lightAnim.fill).toBe("both");
  expect(lightAnim.clipPath[0]).toMatch(/^circle\(0px at /);
  expect(lightAnim.clipPath[lightAnim.clipPath.length - 1]).toMatch(
    /^circle\(\d+px at 1280px 0px\)$/,
  );

  // Dark -> System (when WebView2 PreferredColorScheme echoes dark into prefers-color-scheme
  // while the real OS theme reported by SystemAppearance is light) must STILL run the circular arc.
  await page.getByRole("button", { name: "切换到深色模式" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect
    .poll(() => page.evaluate(() => (window as any).__themeAnimCalls.length))
    .toBe(3);

  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
  await page.evaluate(() => {
    (window as any).__mockSystemAppearance = "light";
  });
  const choices = page.locator('.ant-segmented[aria-label="外观模式"]');
  await segmentedItem(choices, "跟随系统").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() => page.evaluate(() => (window as any).__themeAnimCalls.length))
    .toBe(4);
  const sysAnim = await page.evaluate(() => (window as any).__themeAnimCalls[3]);
  expect(sysAnim.pseudoElement).toBe("::view-transition-new(root)");
  expect(sysAnim.duration).toBe(1000);
});

test("GPU-dependent pages show quick jump to 远端设备 only when GPU is not started", async ({
  page,
}) => {
  // 1) When GPU is stopped, 对外 API / 模型抽屉 / ComfyUI expose a quick jump to 远端设备.
  await mock(page, false);

  await navItem(page, "对外 API").click();
  const apiJump = page.getByRole("button", { name: "去启动 GPU" });
  await expect(apiJump).toBeVisible();
  await apiJump.click();
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  // From 模型抽屉 inside 对外 API: closes drawer and jumps to 远端设备.
  const drawer = await openApiModelDrawer(page);
  const drawerJump = drawer.getByRole("button", { name: "去启动 GPU" });
  await expect(drawerJump).toBeVisible();
  await drawerJump.click();
  await expect(drawer).toBeHidden();
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  // From ComfyUI engine header & 原生画布 tab.
  await navItem(page, "ComfyUI").click();
  const comfyJump = page.getByRole("button", { name: "去启动 GPU" });
  await expect(comfyJump).toBeVisible();
  await comfyJump.click();
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  await navItem(page, "ComfyUI").click();
  await page.getByRole("tab", { name: "原生画布" }).click();
  const canvasJump = page.getByRole("button", { name: "前往启动 GPU" });
  await expect(canvasJump).toBeVisible();
  await canvasJump.click();
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  // Clicking quick jumps must never allocate a GPU automatically.
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);

  // 2) Once GPU is ready, none of those GPU-dependent pages show the jump entry.
  await mock(page, true);

  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("button", { name: /去启动 GPU|前往启动 GPU|查看 GPU 启动进度/ }),
  ).toHaveCount(0);

  const readyDrawer = await openApiModelDrawer(page);
  await expect(
    readyDrawer.getByRole("button", {
      name: /去启动 GPU|前往启动 GPU|查看 GPU 启动进度/,
    }),
  ).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(readyDrawer).toBeHidden();

  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("button", { name: /去启动 GPU|前往启动 GPU|查看 GPU 启动进度/ }),
  ).toHaveCount(0);
  await page.getByRole("tab", { name: "原生画布" }).click();
  await expect(
    page.getByRole("button", { name: /去启动 GPU|前往启动 GPU|查看 GPU 启动进度/ }),
  ).toHaveCount(0);
});

test("switching or re-clicking top navbar tabs smoothly scrolls workspace content back to the top", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1024, height: 640 });
  await mock(page, true);
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();

  const scrollDown = async (target: number) =>
    page.evaluate((top) => {
      const el = document.querySelector(".co-content") as HTMLElement;
      el.scrollTop = top;
      return {
        scrollTop: el.scrollTop,
        maxScroll: el.scrollHeight - el.clientHeight,
      };
    }, target);

  const readScrollTop = () =>
    page.evaluate(() => {
      const el = document.querySelector(".co-content") as HTMLElement;
      return el.scrollTop;
    });

  // 1) Scroll down on 远端设备, then switch to 对外 API and verify smooth multi-step glide to 0.
  const deviceScroll = await scrollDown(500);
  expect(deviceScroll.scrollTop).toBeGreaterThan(200);

  await page.evaluate(() => {
    const el = document.querySelector(".co-content") as HTMLElement;
    const history: number[] = [];
    (window as any).__scrollHistory = history;
    el.addEventListener("scroll", () => {
      history.push(Math.round(el.scrollTop));
    });
  });

  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await expect.poll(readScrollTop).toBe(0);

  const apiHistory: number[] = await page.evaluate(
    () => (window as any).__scrollHistory,
  );
  // Smooth scroll emits intermediate scroll positions between the starting offset and 0.
  expect(apiHistory.some((y) => y > 0 && y < deviceScroll.scrollTop)).toBe(true);
  expect(apiHistory[apiHistory.length - 1]).toBe(0);

  // 2) Scroll down on 对外 API, then switch to ComfyUI and 设置; each returns to top.
  expect((await scrollDown(500)).scrollTop).toBeGreaterThan(150);
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();
  await expect.poll(readScrollTop).toBe(0);

  expect((await scrollDown(500)).scrollTop).toBeGreaterThan(150);
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();
  await expect.poll(readScrollTop).toBe(0);

  // 3) Re-clicking the already-active tab scrolls smoothly back to the top.
  expect((await scrollDown(500)).scrollTop).toBeGreaterThan(200);
  await navItem(page, "设置").click();
  await expect.poll(readScrollTop).toBe(0);

  // 4) Keyboard tab shortcut (Alt+1) while scrolled down also returns to the top.
  expect((await scrollDown(500)).scrollTop).toBeGreaterThan(200);
  await page.keyboard.press("Alt+1");
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();
  await expect.poll(readScrollTop).toBe(0);

  // 5) With prefers-reduced-motion: reduce, tab switch resets immediately to top.
  await page.emulateMedia({ reducedMotion: "reduce" });
  expect((await scrollDown(500)).scrollTop).toBeGreaterThan(200);
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  expect(await readScrollTop()).toBe(0);
});

test("resource monitor selector supports remote read-only GPU observation, switching, and pause/resume", async ({
  page,
}) => {
  await mock(page, false);

  await page.evaluate(() => {
    const s = (window as any).__state;
    const now = Date.now() / 1000;
    const remoteG4Runtime = {
      id: "gpu-g4-s-asso1c1-38s27c46byhtt",
      state: "ready",
      location: { country: "IN", country_name: "印度", region: "孟买" },
      gpu: {
        name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
        total_mib: 98304,
        used_mib: 24576,
        util_percent: 62,
        temp_c: 59,
        power_w: 185,
        power_limit_w: 300,
      },
      resources: {
        gpu: {
          name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
          total_mib: 98304,
          used_mib: 24576,
          util_percent: 62,
          temp_c: 59,
          power_w: 185,
          power_limit_w: 300,
        },
        memory: {
          total_bytes: 64 * 1024 ** 3,
          used_bytes: 20 * 1024 ** 3,
          available_bytes: 44 * 1024 ** 3,
          percent: 31.2,
        },
        disk: {
          total_bytes: 200 * 1024 ** 3,
          used_bytes: 48 * 1024 ** 3,
          free_bytes: 152 * 1024 ** 3,
          percent: 24.0,
          read_bps: 18 * 1024 ** 2,
          write_bps: 6 * 1024 ** 2,
        },
        cpu: { cores: 12, percent: 28.5 },
        history: [
          {
            t: now - 2,
            vram_pct: 24.5,
            gpu_util: 55,
            ram_pct: 30.0,
            cpu_pct: 25.0,
            disk_pct: 24.0,
          },
          {
            t: now - 1,
            vram_pct: 25.0,
            gpu_util: 62,
            ram_pct: 31.2,
            cpu_pct: 28.5,
            disk_pct: 24.0,
          },
        ],
      },
    };
    const localT4Runtime = {
      id: "gpu-t4-s-usw1b1-local001",
      state: "ready",
      location: { country: "US", country_name: "美国", region: "俄勒冈" },
      gpu: {
        name: "Tesla T4",
        total_mib: 15360,
        used_mib: 3840,
        util_percent: 18,
        temp_c: 46,
      },
      resources: {
        gpu: {
          name: "Tesla T4",
          total_mib: 15360,
          used_mib: 3840,
          util_percent: 18,
          temp_c: 46,
        },
        memory: {
          total_bytes: 16 * 1024 ** 3,
          used_bytes: 4 * 1024 ** 3,
          available_bytes: 12 * 1024 ** 3,
          percent: 25.0,
        },
        disk: {
          total_bytes: 112 * 1024 ** 3,
          used_bytes: 20 * 1024 ** 3,
          free_bytes: 92 * 1024 ** 3,
          percent: 17.9,
        },
        cpu: { cores: 4, percent: 12.0 },
        history: [
          {
            t: now - 1,
            vram_pct: 25.0,
            gpu_util: 18,
            ram_pct: 25.0,
            cpu_pct: 12.0,
            disk_pct: 17.9,
          },
        ],
      },
    };
    (window as any).__remoteG4Runtime = remoteG4Runtime;
    (window as any).__localT4Runtime = localT4Runtime;

    // 1) Local stopped, remote G4 running -> monitor defaults to remote G4
    s.setup.colab = true;
    s.runtime = { state: "stopped" };
    s.account = {
      logged_in: true,
      balance: 2116.33,
      rate: 10.05,
      assignments: 1,
      checked_at: now,
      instances: [
        {
          id: remoteG4Runtime.id,
          managed: false,
          active: false,
          accelerator: "G4",
          variant: "GPU",
          shape: "STANDARD",
          location: remoteG4Runtime.location,
        },
      ],
    };
    s.monitor = {
      mode: "auto",
      endpoint: remoteG4Runtime.id,
      managed: false,
      source: "remote_readonly",
      paused: false,
      error: "",
      runtime: remoteG4Runtime,
      loaded_model_name: "",
      drive_connected: false,
      drive_free_bytes: null,
      options: [
        {
          id: remoteG4Runtime.id,
          managed: false,
          active: false,
          accelerator: "G4",
          variant: "GPU",
          shape: "STANDARD",
          location: remoteG4Runtime.location,
          gpu_name: "NVIDIA RTX PRO 6000",
          loaded_model: "",
          source: "remote_readonly",
          state: "ready",
        },
      ],
    };

    const origCall = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, ...args: any[]) => {
      if (method === "ColabOneDesktop.Command") {
        const req = args[0] || {};
        if (req.action === "monitor.pause") {
          (window as any).__calls.push(req);
          s.monitor = { ...s.monitor, paused: Boolean(req.payload?.paused) };
          return JSON.stringify({ ok: true, paused: s.monitor.paused });
        }
        if (req.action === "monitor.select") {
          (window as any).__calls.push(req);
          const ep = String(req.payload?.endpoint || "");
          const isLocal = ep === localT4Runtime.id;
          s.monitor = {
            ...s.monitor,
            mode: isLocal ? "auto" : "manual",
            endpoint: ep,
            managed: isLocal,
            source: isLocal ? "local" : "remote_readonly",
            paused: false,
            runtime: isLocal ? localT4Runtime : remoteG4Runtime,
          };
          return JSON.stringify({ ok: true, endpoint: ep });
        }
      }
      return origCall(method, ...args);
    };
  });

  const monitor = page.locator(".co-monitor");
  await expect(monitor).toContainText("远端只读 · 实时采样");
  await expect(monitor).toContainText("NVIDIA RTX PRO 6000");
  await expect(monitor).not.toContainText("待机预览");
  await expect(monitor.locator(".co-rm-instance-select")).toBeVisible();

  // Pause & resume external observation
  const pauseBtn = monitor.locator(".co-rm-pause-btn");
  await expect(pauseBtn).toContainText("暂停观测");
  await pauseBtn.click();
  await expect(monitor).toContainText("远端只读 · 已暂停");
  await expect(pauseBtn).toContainText("恢复观测");
  await pauseBtn.click();
  await expect(monitor).toContainText("远端只读 · 实时采样");

  // Switch to 4-row stacked layout and confirm compact vertical height, balanced header spacing, and live right-side percentages
  await monitor.locator(".co-rm-layout-segmented .ant-segmented-item").nth(1).click();
  await expect(monitor.locator(".co-rm-rows")).toBeVisible();
  const scaleCaps = await monitor.locator(".co-rm-row .co-rm-scale-cap").allTextContents();
  expect(scaleCaps.map((t) => t.trim())).toEqual([
    "25.0%",
    "31.2%",
    "24.0%",
    "28.5%",
  ]);
  const measureHeader = async () =>
    monitor.evaluate((el) => {
      const head = el.querySelector(".ant-card-head")!.getBoundingClientRect();
      const primary = el
        .querySelector(".co-rm-heading-primary")!
        .getBoundingClientRect();
      const pills = el.querySelector(".co-rm-hw-pills")!.getBoundingClientRect();
      const extra = el.querySelector(".ant-card-extra")!.getBoundingClientRect();
      const contentTop = Math.min(primary.top, pills.top);
      const contentBottom = Math.max(primary.bottom, pills.bottom);
      return {
        height: Math.round(el.getBoundingClientRect().height),
        topGap: Math.round(contentTop - head.top),
        bottomGap: Math.round(head.bottom - contentBottom),
        extraHeight: Math.round(extra.height),
        wrapped: pills.top >= primary.bottom,
        betweenGap: Math.round(pills.top - primary.bottom),
      };
    });

  const wideMetrics = await measureHeader();
  expect(wideMetrics.height).toBeLessThanOrEqual(480);
  expect(wideMetrics.topGap).toBeGreaterThanOrEqual(8);
  expect(wideMetrics.bottomGap).toBeGreaterThanOrEqual(8);
  expect(Math.abs(wideMetrics.topGap - wideMetrics.bottomGap)).toBeLessThanOrEqual(3);

  // Also verify balanced vertical spacing when long GPU pills wrap onto a second header line (~1024px)
  await page.setViewportSize({ width: 1024, height: 850 });
  const wrappedMetrics = await measureHeader();
  expect(wrappedMetrics.wrapped).toBe(true);
  expect(wrappedMetrics.height).toBeLessThanOrEqual(480);
  expect(wrappedMetrics.topGap).toBeGreaterThanOrEqual(8);
  expect(wrappedMetrics.bottomGap).toBeGreaterThanOrEqual(8);
  expect(wrappedMetrics.betweenGap).toBeGreaterThanOrEqual(6);
  expect(
    Math.abs(wrappedMetrics.topGap - wrappedMetrics.bottomGap),
  ).toBeLessThanOrEqual(3);
  await page.setViewportSize({ width: 1280, height: 850 });

  // 2) Both local T4 and remote G4 running -> monitor defaults to local T4; dropdown switches to G4
  await page.evaluate(() => {
    const s = (window as any).__state;
    const localRt = (window as any).__localT4Runtime;
    const remoteRt = (window as any).__remoteG4Runtime;
    s.runtime = localRt;
    s.active_endpoint = localRt.id;
    s.account.assignments = 2;
    s.account.instances = [
      {
        id: localRt.id,
        managed: true,
        active: true,
        accelerator: "T4",
        variant: "GPU",
        shape: "HIGH_RAM",
        location: localRt.location,
        state: "ready",
        gpu_name: "Tesla T4",
      },
      {
        id: remoteRt.id,
        managed: false,
        active: false,
        accelerator: "G4",
        variant: "GPU",
        shape: "STANDARD",
        location: remoteRt.location,
      },
    ];
    s.monitor = {
      mode: "auto",
      endpoint: localRt.id,
      managed: true,
      source: "local",
      paused: false,
      error: "",
      runtime: localRt,
      loaded_model_name: "",
      drive_connected: false,
      drive_free_bytes: null,
      options: [
        {
          id: localRt.id,
          managed: true,
          active: true,
          accelerator: "T4",
          variant: "GPU",
          shape: "HIGH_RAM",
          location: localRt.location,
          gpu_name: "Tesla T4",
          loaded_model: "",
          source: "local",
          state: "ready",
        },
        {
          id: remoteRt.id,
          managed: false,
          active: false,
          accelerator: "G4",
          variant: "GPU",
          shape: "STANDARD",
          location: remoteRt.location,
          gpu_name: "NVIDIA RTX PRO 6000",
          loaded_model: "",
          source: "remote_readonly",
          state: "ready",
        },
      ],
    };
  });

  await expect(monitor).toContainText("实时 60s 采样");
  await expect(monitor).toContainText("Tesla T4");
  await expect(pauseBtn).toBeHidden();

  // Switch via monitor dropdown to remote G4 without altering local active runtime
  await monitor.locator(".co-rm-instance-select").click();
  await page
    .locator(".ant-select-dropdown .ant-select-item-option")
    .filter({ hasText: "gpu-g4-s-asso1c1-38s27c46byhtt" })
    .click();

  await expect(monitor).toContainText("远端只读 · 实时采样");
  await expect(monitor).toContainText("NVIDIA RTX PRO 6000");
  await expect(page.locator(".co-device-card")).toContainText("Tesla T4");

  const calls: any[] = await page.evaluate(() => (window as any).__calls);
  expect(
    calls.some(
      (c) =>
        c.action === "monitor.select" &&
        c.payload?.endpoint === "gpu-g4-s-asso1c1-38s27c46byhtt",
    ),
  ).toBe(true);
  expect(calls.some((c) => c.action === "monitor.pause")).toBe(true);
});

test("model drawer supports GPU VRAM filtering, curated version cards, modality/family chips, ComfyUI-only guard, and parameter presets", async ({
  page,
}) => {
  await mock(page, false);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.settings.gpu = "T4";
    s.models[0].common = false;
    s.common_repos = [
      {
        repo: "huihui-ai/Huihui-Qwen3.8-27B-GGUF",
        name: "Huihui-Qwen3.8-27B-GGUF",
        note: "默认模型",
      },
    ];
    const original = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, request: any) => {
      if (request?.action === "hub.search") {
        (window as any).__calls.push(request);
        const mod = request.payload?.modality || "text";
        if (mod === "video") {
          return JSON.stringify({
            models: [
              {
                repo: "city96/Wan2.1-T2V-14B-gguf",
                downloads: 42000,
                likes: 980,
                gated: false,
                modality: "video",
                comfy_only: true,
                family: "Wan",
                params: "14B",
              },
            ],
            next_cursor: "",
          });
        }
        return JSON.stringify({
          models: [
            {
              repo: "unsloth/Qwen3-32B-GGUF",
              downloads: 88000,
              likes: 1500,
              gated: false,
              modality: "text",
              comfy_only: false,
              family: "Qwen",
              params: "32B",
            },
          ],
          next_cursor: "",
        });
      }
      if (request?.action === "hub.files") {
        (window as any).__calls.push(request);
        if (request.payload?.repo === "city96/Wan2.1-T2V-14B-gguf") {
          return JSON.stringify({
            revision: "c".repeat(40),
            modality: "video",
            comfy_only: true,
            files: [
              {
                filename: "wan2.1-t2v-14b-Q4_K_M.gguf",
                size: 11 * 1024 ** 3,
                shards: 1,
                quantization: "Q4_K_M",
                supported: true,
                reason: "",
              },
            ],
          });
        }
        return JSON.stringify({
          revision: "b".repeat(40),
          modality: "text",
          comfy_only: false,
          files: [
            {
              filename: "Model-IQ2_XXS.gguf",
              size: 10 * 1024 ** 3,
              shards: 1,
              quantization: "IQ2_XXS",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-Q3_K_M.gguf",
              size: 15 * 1024 ** 3,
              shards: 1,
              quantization: "Q3_K_M",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-UD-Q4_K_XL-00001-of-00002.gguf",
              size: 20 * 1024 ** 3,
              shards: 2,
              quantization: "UD-Q4_K_XL",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-Q5_K_M.gguf",
              size: 24 * 1024 ** 3,
              shards: 1,
              quantization: "Q5_K_M",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-Q8_0.gguf",
              size: 34 * 1024 ** 3,
              shards: 1,
              quantization: "Q8_0",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-BF16.gguf",
              size: 62 * 1024 ** 3,
              shards: 2,
              quantization: "BF16",
              supported: true,
              reason: "",
            },
            {
              filename: "Model-F32-Huge.gguf",
              size: 108 * 1024 ** 3,
              shards: 4,
              quantization: "F32",
              supported: true,
              reason: "",
            },
          ],
        });
      }
      return original(method, request);
    };
  });

  const drawer = await openApiModelDrawer(page);
  const firstRepo = drawer.locator(".co-common-repo-row").first();
  await firstRepo.getByRole("button", { name: "选择版本" }).click();

  // On default T4 (16 GB): IQ2_XXS fits comfortably while Q4_K_XL / Q8_0 / BF16 overflow & disabled
  const radioGroup = firstRepo.getByRole("radiogroup", { name: "选择量化版本" });
  await expect(radioGroup).toBeVisible();
  // Curated view initially shows <= 4 cards and offers expand all
  expect(await radioGroup.getByRole("radio").count()).toBeLessThanOrEqual(4);
  await expect(
    firstRepo.getByRole("button", { name: /展开全部 7 个版本/ }),
  ).toBeVisible();
  // Offline browsing hides load-time runtime parameters
  await expect(firstRepo.getByText(/装载档位/)).toHaveCount(0);

  // Switch GPU target dropdown to G4 · 96 GB -> UD-Q4_K_XL becomes recommended & Q8_0 / BF16 unlock while 108 GiB F32 stays disabled
  await chooseAntOption(page, antSelect(drawer, "按 GPU 显存筛选"), "G4 · 96 GB");
  await expect(
    radioGroup.getByRole("radio", { name: /UD-Q4_K_XL/ }),
  ).toHaveAttribute("aria-checked", "true");
  await expect(
    radioGroup.getByRole("radio", { name: /UD-Q4_K_XL/ }),
  ).toContainText("推荐");
  await expect(
    radioGroup.getByRole("radio", { name: /UD-Q4_K_XL/ }),
  ).toContainText("适配 G4 96G");

  // Expand all 7 versions and verify 108 GiB F32 shows gray hybrid badge yet remains selectable
  await firstRepo.getByRole("button", { name: /展开全部 7 个版本/ }).click();
  await expect(radioGroup.getByRole("radio")).toHaveCount(7);
  const hugeRadio = radioGroup.getByRole("radio", {
    name: /Model-F32-Huge\.gguf/,
  });
  await expect(hugeRadio).toBeEnabled();
  await expect(hugeRadio).toContainText("混合装载 · 显存+内存");
  await hugeRadio.click();
  await expect(hugeRadio).toHaveAttribute("aria-checked", "true");
  await expect(firstRepo.locator(".co-vp-live-warn")).toContainText(
    "--fit on",
  );

  // Capture visual screenshot of redesigned beginner-friendly version picker in "我的"
  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/model-drawer-versions-g4.png", import.meta.url),
    ),
    fullPage: true,
  });

  // Switch to "社区" and verify family + modality chips and ComfyUI-only guard
  await drawer.getByRole("tab", { name: /^社\s*区$/ }).click();
  const familyGroup = drawer.getByRole("group", { name: "热门模型系列" });
  await familyGroup.getByRole("button", { name: "千问 Qwen" }).click();
  await expect(drawer.locator(".co-hub-repo").first()).toContainText(
    "适配 G4 96G",
  );
  await expect(drawer.locator(".co-hub-repo").first()).toContainText("32B");

  const modalityGroup = drawer.getByRole("group", { name: "模型类型筛选" });
  await modalityGroup.getByRole("button", { name: /视频生成/ }).click();
  await expect(drawer.locator(".co-comfy-modality-banner")).toContainText(
    "ComfyUI 专用",
  );
  const videoCard = drawer.locator(".co-hub-repo", {
    hasText: "Wan2.1-T2V-14B-gguf",
  });
  await expect(videoCard).toBeVisible();
  await expect(videoCard).toContainText("ComfyUI 专用");
  await expect(videoCard.getByRole("button", { name: /加入我的/ })).toHaveCount(
    0,
  );
  await videoCard.getByRole("button", { name: "查看显存" }).click();
  await expect(
    videoCard.getByRole("button", { name: /下载并装载|加入我的/ }),
  ).toHaveCount(0);
  await videoCard.getByRole("button", { name: /前往 ComfyUI 工作区/ }).click();
  await expect(drawer).toBeHidden();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();

  // Verify final-stage runtime parameter presets & oversized hybrid model loadability when GPU is ready
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.state = "ready";
    s.runtime.protocol = 7;
    s.runtime.gpu = {
      name: "NVIDIA RTX PRO 6000",
      total_mib: 98304,
      used_mib: 1024,
    };
    s.runtime.resources = {
      memory: {
        total_bytes: 170 * 1024 ** 3,
        used_bytes: 16 * 1024 ** 3,
      },
    };
    s.models[0].common = true;
    s.models[0].downloaded = true;
    s.models.push({
      id: "moe-111g",
      name: "Qwen3.8-Flash-Next · Q4",
      description: "111 GiB MoE hybrid model",
      size: 111 * 1024 ** 3,
      min_vram_mib: 0,
      context_length: 8192,
      temperature: 0.7,
      common: true,
      downloaded: true,
      loaded: false,
      custom: true,
    });
  });
  const readyDrawer = await openApiModelDrawer(page);
  const hybridCard = readyDrawer.locator(".co-model-card", {
    hasText: "Qwen3.8-Flash-Next · Q4",
  });
  await expect(hybridCard).toContainText("混合装载 · 显存+内存");
  await expect(hybridCard.locator(".co-model-vram-warning")).toContainText(
    "--fit on",
  );
  await expect(hybridCard.getByRole("button", { name: "装载" })).toBeEnabled();

  const savedCard = readyDrawer.locator(".co-model-card", {
    hasText: "Qwen2.5 · 0.5B",
  });
  await savedCard.getByRole("button", { name: /^参\s*数$/ }).click();
  const presetGroup = savedCard.getByRole("group", { name: "快捷参数档位" });
  await expect(presetGroup).toBeVisible();
  await presetGroup.getByRole("button", { name: /精准编程/ }).click();
  await expect(savedCard.locator(".co-param-effect-box")).toContainText(
    "16K 上下文",
  );
  await expect(savedCard.locator(".co-param-effect-box")).toContainText("严谨");
  await savedCard.getByRole("button", { name: "装载显存" }).click();
  const loadCall = await page.evaluate(() =>
    (window as any).__calls.find((c: any) => c.action === "model.load"),
  );
  expect(loadCall?.payload).toEqual({
    id: "qwen",
    context_length: 16384,
    temperature: 0.2,
  });
});

test("multi-language switcher renders ready-made SVG flags and translates all 6 locales cleanly", async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(err.message));

  await mock(page, true);

  // Verify topbar flag button renders a real loaded SVG image
  const trigger = page.getByTestId("topbar-locale-trigger");
  await expect(trigger).toBeVisible();
  const triggerImg = trigger.locator("img.co-locale-flag");
  await expect(triggerImg).toBeVisible();
  const triggerLoaded = await triggerImg.evaluate(
    (img: HTMLImageElement) => img.complete && img.naturalWidth > 0,
  );
  expect(triggerLoaded).toBe(true);

  const expectations = [
    {
      code: "en-US",
      deviceNav: "Devices",
      monitorTitle: "Live Hardware Monitor",
      apiHeading: "Public API",
      comfyHeading: "ComfyUI Workflow Engine",
      settingsTitle: "Settings",
      logsTitle: "Logs",
    },
    {
      code: "zh-TW",
      deviceNav: "遠端裝置",
      monitorTitle: "即時硬體監控",
      apiHeading: "對外 API",
      comfyHeading: "ComfyUI 工作流程引擎",
      settingsTitle: "設定",
      logsTitle: "執行日誌",
    },
    {
      code: "zh-HK",
      deviceNav: "遠端裝置",
      monitorTitle: "即時硬件監控",
      apiHeading: "對外 API",
      comfyHeading: "ComfyUI 工作流引擎",
      settingsTitle: "設定",
      logsTitle: "執行日誌",
    },
    {
      code: "ja-JP",
      deviceNav: "リモートデバイス",
      monitorTitle: "リアルタイムハードウェア監視",
      apiHeading: "外部 API",
      comfyHeading: "ComfyUI ワークフローエンジン",
      settingsTitle: "設定",
      logsTitle: "実行ログ",
    },
    {
      code: "ko-KR",
      deviceNav: "원격 기기",
      monitorTitle: "실시간 하드웨어 모니터",
      apiHeading: "외부 API",
      comfyHeading: "ComfyUI 워크플로 엔진",
      settingsTitle: "설정",
      logsTitle: "실행 로그",
    },
    {
      code: "zh-CN",
      deviceNav: "远端设备",
      monitorTitle: "实时硬件监控",
      apiHeading: "对外 API",
      comfyHeading: "ComfyUI 工作流引擎",
      settingsTitle: "设置",
      logsTitle: "运行日志",
    },
  ] as const;

  for (const spec of expectations) {
    await trigger.click();
    const menuItems = page.locator(".co-locale-menu-item");
    await expect(menuItems).toHaveCount(6);
    const targetItem = page.locator(
      `.co-locale-menu-item[data-locale="${spec.code}"]`,
    );
    const flagOk = await targetItem
      .locator("img.co-locale-flag")
      .evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0);
    expect(flagOk).toBe(true);
    await targetItem.click();

    await expect(page.locator("html")).toHaveAttribute("lang", spec.code);
    await expect(
      page.locator(".co-nav-pill-item").first(),
    ).toContainText(spec.deviceNav);
    await expect(page.locator(".co-monitor")).toContainText(spec.monitorTitle);

    // Check zero horizontal overflow on Device page
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflow).toBeLessThanOrEqual(2);

    // Check API page & Model Drawer
    await page.locator(".co-nav-pill-item").nth(1).click();
    await expect(page.locator(".co-api-header-title h1")).toHaveText(
      spec.apiHeading,
    );

    // Check ComfyUI page
    await page.locator(".co-nav-pill-item").nth(2).click();
    await expect(page.locator(".co-comfy-engine-title-row h2")).toHaveText(
      spec.comfyHeading,
    );

    // Check Settings page & 6-flag radio grid
    await page.locator(".co-nav-pill-item").nth(3).click();
    await expect(page.locator(".co-page-title")).toHaveText(spec.settingsTitle);
    const settingsGrid = page.getByTestId("settings-locale-grid");
    await expect(settingsGrid.locator("button.co-locale-card-btn")).toHaveCount(
      6,
    );
    await expect(
      settingsGrid.locator(`button[data-locale="${spec.code}"]`),
    ).toHaveAttribute("aria-checked", "true");

    // Return to Device page for next locale
    await page.locator(".co-nav-pill-item").nth(0).click();
  }

  // Open inline logs drawer and confirm localized panel + native sync calls
  await page.locator(".co-logs-trigger").click();
  await expect(page.locator("#activity-panel")).toHaveAttribute(
    "aria-label",
    "运行日志",
  );
  await expect(page.locator("#activity-panel .co-eyebrow")).toHaveText(
    "当前状态",
  );

  const nativeLocales: string[] = await page.evaluate(
    () => (window as any).__nativeLocales,
  );
  expect(nativeLocales).toContain("en-US");
  expect(nativeLocales).toContain("zh-TW");
  expect(nativeLocales).toContain("zh-HK");
  expect(nativeLocales).toContain("ja-JP");
  expect(nativeLocales).toContain("ko-KR");
  expect(pageErrors).toEqual([]);
});

test("auto-detects system language by default and prioritizes manual override across reloads", async ({
  browser,
}) => {
  const cases = [
    { browserLocale: "en-US", expected: "en-US", navText: "Devices" },
    { browserLocale: "zh-TW", expected: "zh-TW", navText: "遠端裝置" },
    { browserLocale: "zh-HK", expected: "zh-HK", navText: "遠端裝置" },
    { browserLocale: "ja-JP", expected: "ja-JP", navText: "リモートデバイス" },
    { browserLocale: "ko-KR", expected: "ko-KR", navText: "원격 기기" },
    { browserLocale: "de-DE", expected: "en-US", navText: "Devices" },
  ] as const;

  for (const item of cases) {
    const ctx = await browser.newContext({ locale: item.browserLocale });
    const p = await ctx.newPage();
    await mock(p, false);
    await expect(p.locator("html")).toHaveAttribute("lang", item.expected);
    await expect(p.locator(".co-nav-pill-item").first()).toContainText(
      item.navText,
    );
    // Auto-detection must NOT persist a manual override to localStorage
    const stored = await p.evaluate(() =>
      window.localStorage.getItem("colabone.locale"),
    );
    expect(stored).toBeNull();
    await ctx.close();
  }

  // Now verify manual override takes strict priority over system locale across reloads
  const overrideCtx = await browser.newContext({ locale: "en-US" });
  const overridePage = await overrideCtx.newPage();
  await mock(overridePage, true);
  await expect(overridePage.locator("html")).toHaveAttribute("lang", "en-US");

  // User manually switches to Japanese via Settings language card
  await overridePage.locator(".co-nav-pill-item").nth(3).click();
  await overridePage
    .getByTestId("settings-locale-grid")
    .locator('button[data-locale="ja-JP"]')
    .click();
  await expect(overridePage.locator("html")).toHaveAttribute("lang", "ja-JP");
  expect(
    await overridePage.evaluate(() =>
      window.localStorage.getItem("colabone.locale"),
    ),
  ).toBe("ja-JP");

  const persistCall = await overridePage.evaluate(() =>
    (window as any).__localeCommands.find(
      (c: any) => c.locale === "ja-JP" && c.persist === true,
    ),
  );
  expect(persistCall).toBeTruthy();

  // Reload under the SAME en-US browser locale: manual ja-JP must win!
  await overridePage.reload();
  await expect(overridePage.locator("html")).toHaveAttribute("lang", "ja-JP");
  await expect(overridePage.locator(".co-nav-pill-item").first()).toContainText(
    "リモートデバイス",
  );

  // Standalone LogWindow in the same context inherits manual ja-JP immediately
  const logPage = await overrideCtx.newPage();
  await mock(logPage, true, "/?view=logs");
  await expect(logPage.locator("html")).toHaveAttribute("lang", "ja-JP");
  await expect(logPage).toHaveTitle("実行ログ · ColabOne");
  await expect(logPage.locator("#activity-panel")).toHaveAttribute(
    "aria-label",
    "実行ログ",
  );
  await expect(logPage.locator("#activity-panel .co-eyebrow")).toHaveText(
    "現在の状態",
  );

  await overrideCtx.close();
});

test("tooltips dismiss immediately on click, dropdown switch, blank click, and dynamic title changes", async ({
  page,
}) => {
  await mock(page, true);

  // 1. Language switcher: hover -> tooltip opens -> click -> dropdown opens & tooltip closes ->
  //    pick English -> move to blank space -> tooltip MUST NOT re-appear or stay stuck!
  const trigger = page.getByTestId("topbar-locale-trigger");
  await trigger.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText(
    "当前语言：简体中文",
  );

  await trigger.click();
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
  const enOption = page.locator('.co-locale-menu-item[data-locale="en-US"]');
  await expect(enOption).toBeVisible();
  await enOption.click();

  // Move to blank area and click blank area; verify zero stuck tooltips after settle
  await page.mouse.move(16, 16);
  await page.mouse.click(16, 16);
  await page.waitForTimeout(350);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

  // Fresh hover shows updated English tooltip; moving away dismisses it cleanly
  await trigger.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText(
    "Language: English",
  );
  await page.mouse.move(16, 16);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

  // 2. Topbar theme toggle & logs buttons dismiss tooltip immediately on click
  const themeBtn = page.locator(".co-nav-circle-btn").nth(1);
  await themeBtn.hover();
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
  await themeBtn.click();
  await page.mouse.move(16, 16);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

  // 3. BillingBar #title-slot tooltip opens on hover and dismisses cleanly on leave / blank click
  const billingStrip = page.locator(".co-billing-strip");
  await billingStrip.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText("Burn Rate");
  await page.mouse.click(16, 16);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

  // 4. Dynamic empty-title transition (disabled button -> enabled -> disabled) never latches tooltip
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.state = "stopped";
    s.runtime.gpu = undefined;
  });
  await page.locator(".co-nav-pill-item").nth(1).click();
  const disabledStartBtn = page
    .locator(".co-api-header-action .ant-tooltip-disabled-compatible-wrapper")
    .first();
  await disabledStartBtn.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText(
    "Start a remote GPU first",
  );
  await page.mouse.move(16, 16);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
  // Simulate GPU becoming ready (:title -> "") and stopping again (:title -> non-empty) while hovered
  await disabledStartBtn.hover();
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(1);
  await page.evaluate(() => {
    (window as any).__state.runtime.state = "ready";
  });
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
  await page.mouse.move(16, 16);
  await page.evaluate(() => {
    (window as any).__state.runtime.state = "stopped";
  });
  await page.waitForTimeout(300);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);

  // 5. ComfyUI APopconfirm + ColabOneTooltip on custom workflow delete icon never collide
  await page.evaluate(() => {
    (window as any).__state.workflows = [
      {
        id: "wf-custom-1",
        name: "Custom Flow",
        description: "Test",
        source: "custom",
        node_count: 2,
        min_vram_mib: 0,
        fields: [],
      },
    ];
  });
  await page.locator(".co-nav-pill-item").nth(2).click();
  const deleteBtn = page.getByRole("button", { name: "Delete custom workflow" });
  await deleteBtn.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText(
    "Delete custom workflow",
  );
  await deleteBtn.click();
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
  await expect(page.locator(".ant-popconfirm:visible")).toBeVisible();
  await page.mouse.click(16, 16);
  await page.waitForTimeout(250);
  await expect(page.locator(".ant-tooltip:visible")).toHaveCount(0);
});

test("QA regressions: inline log drawer navbar layout, hub filter spacing, disabled tooltips, saved-repo loaded action, and live error banner translation", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1200, height: 800 });
  await mock(page, false);

  // Switch to English
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="en-US"]').click();
  await expect(page.locator(".co-nav-pill-item").nth(3)).toContainText(
    "Settings",
  );

  // 1. Live error toast translates when switching languages
  await page.evaluate(() => {
    (window as any).__state.error = "Error: 请先完成 Google 登录";
  });
  await expect(page.locator(".co-toast.is-error")).toContainText(
    "Please sign in with Google first",
  );
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="ja-JP"]').click();
  await expect(page.locator(".co-toast.is-error")).toContainText(
    "先に Google ログインを完了してください",
  );
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="en-US"]').click();
  await expect(page.locator(".co-toast.is-error")).toContainText(
    "Please sign in with Google first",
  );
  await page.locator(".co-toast.is-error .co-toast-close").click();
  await expect(page.locator(".co-toast.is-error")).toHaveCount(0);

  // 2. Opening inline LogDrawer in English at 1200px causes zero navbar collisions or horizontal overflow
  await page.locator(".co-logs-trigger").click();
  await expect(page.locator(".co-log-drawer")).toBeVisible();
  const navBoxes = await page.evaluate(() => {
    const rect = (sel: string) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom };
    };
    return {
      left: rect(".co-nav-left"),
      center: rect(".co-nav-center"),
      right: rect(".co-nav-right"),
      overflow: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  expect(navBoxes.overflow).toBe(0);
  expect(navBoxes.left && navBoxes.center && navBoxes.right).toBeTruthy();
  expect(navBoxes.left!.right).toBeLessThanOrEqual(navBoxes.center!.left + 1);
  expect(navBoxes.center!.right).toBeLessThanOrEqual(navBoxes.right!.left + 1);
  await page.locator(".co-logs-trigger").click();

  // 3. ModelDrawer: saved repo with loaded model offers "In Use" action, and HubBrowser shows no label overlap + disabled tooltip while busy
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.state = "ready";
    s.runtime.gpu = { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 };
    s.models[0].downloaded = true;
    s.models[0].loaded = true;
    s.runtime.loaded_model = s.models[0].id;
    s.common_repos = [
      {
        repo: "Qwen/Qwen2.5-0.5B-Instruct-GGUF",
        name: "Qwen2.5-0.5B-Instruct-GGUF",
        saved_at: Date.now() / 1000,
      },
    ];
  });
  await page.locator(".co-nav-pill-item").nth(1).click();
  await page.getByRole("button", { name: /Select Model|Switch Model/i }).first().click();
  await expect(page.locator(".ant-drawer-content")).toBeVisible();

  // Expand saved repo in Mine tab -> should render "Active" action button
  await page.locator(".co-common-repo-row").getByRole("button", { name: "Versions" }).click();
  await expect(
    page.locator(".co-repo-picker").getByRole("button", { name: "Active" }),
  ).toBeVisible();

  // Switch to Community Hub -> verify Family label does not overlap first chip, and disabled Download button has tooltip when another task is running
  await page.getByRole("tab", { name: /Community/i }).click();
  await expect(page.locator(".co-hub-filter-panel")).toBeVisible();
  const familyCollision = await page.evaluate(() => {
    const labels = Array.from(document.querySelectorAll(".co-filter-label"));
    for (const lbl of labels) {
      const next = lbl.nextElementSibling;
      if (!next) continue;
      if (lbl.getBoundingClientRect().right > next.getBoundingClientRect().left + 1) {
        return true;
      }
    }
    return false;
  });
  expect(familyCollision).toBe(false);

  // Set an active running task so Download & Load inside HubBrowser is disabled with reason tooltip
  await page.evaluate(() => {
    (window as any).__state.runtime.tasks = [
      {
        id: "t-busy",
        kind: "download",
        state: "running",
        progress: 0.5,
        message: "正在下载",
      },
    ];
  });
  await page
    .locator(".co-hub-repo")
    .first()
    .getByRole("button", { name: "Versions" })
    .click();
  const hubDisabledWrap = page
    .locator(
      ".co-hub-selection .co-hub-install-actions .ant-tooltip-disabled-compatible-wrapper",
    )
    .first();
  await expect(hubDisabledWrap).toBeVisible();
  await hubDisabledWrap.hover();
  await expect(page.locator(".ant-tooltip:visible")).toContainText(
    "Wait for the current task to finish",
  );
});

test("Countdown error toast (zero layout shift + hover pause + persistent connectivity banner) and unified code syntax highlighting", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 840 });
  await mock(page, true);

  await navItem(page, "对外 API").click();
  await expect(page.locator(".co-api-page-header")).toBeVisible();
  // Let the 160ms page entrance transform settle before measuring baseline top
  await page.waitForTimeout(240);

  // 1. Action validation error opens floating countdown toast with zero layout shift
  const headerTopBefore = await page
    .locator(".co-api-page-header")
    .evaluate((el) => Math.round(el.getBoundingClientRect().top));

  await page.evaluate(() => {
    const orig = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, ...args: any[]) => {
      if (
        method === "ColabOneDesktop.Command" &&
        args[0]?.action === "api.start"
      ) {
        throw new Error(
          "API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token",
        );
      }
      return orig(method, ...args);
    };
  });

  await page.getByRole("button", { name: "开启 API 服务", exact: true }).click();

  const errorToast = page.locator(".co-toast.is-error");
  await expect(errorToast).toBeVisible();
  await expect(errorToast).toContainText("API 配置无效");
  await expect(page.locator(".co-banners .ant-alert-error")).toHaveCount(0);

  const headerTopAfter = await page
    .locator(".co-api-page-header")
    .evaluate((el) => Math.round(el.getBoundingClientRect().top));
  expect(headerTopAfter).toBe(headerTopBefore);

  // Countdown progress bar is rendered & running, pauses cleanly on hover, resumes on mouse leave
  const progressBar = errorToast.locator(".co-toast-progress-bar");
  await expect(progressBar).toBeVisible();
  expect(
    await progressBar.evaluate(
      (el) => window.getComputedStyle(el).animationPlayState,
    ),
  ).toBe("running");

  await errorToast.hover();
  await expect(errorToast).toHaveAttribute("data-paused", "true");
  expect(
    await progressBar.evaluate(
      (el) => window.getComputedStyle(el).animationPlayState,
    ),
  ).toBe("paused");

  await page.mouse.move(12, 12);
  await expect(errorToast).toHaveAttribute("data-paused", "false");
  expect(
    await progressBar.evaluate(
      (el) => window.getComputedStyle(el).animationPlayState,
    ),
  ).toBe("running");

  await errorToast.locator(".co-toast-close").click();
  await expect(errorToast).toHaveCount(0);

  // Persistent connectivity monitor failure stays as top inline banner until resolved
  await page.evaluate(() => {
    (window as any).__state.error =
      "连接检查失败：ConnectionError。客户端会自动重试，请检查网络连接。";
  });
  await expect(page.locator(".co-banners .ant-alert-error")).toContainText(
    "连接检查失败",
  );
  await page.evaluate(() => {
    (window as any).__state.error = "";
  });
  await expect(page.locator(".co-banners .ant-alert-error")).toHaveCount(0);

  // 2. Unified syntax highlighting across cURL (with embedded JSON), Python, Codex TOML, and Dark theme
  const codeBlock = page.locator(".co-api-examples pre.co-code-highlighted");
  await expect(codeBlock).toBeVisible();
  await expect(codeBlock).toHaveAttribute("data-language", "shellscript");

  const lightCurlColors = await codeBlock.evaluate((pre) => {
    const tokens = Array.from(pre.querySelectorAll(".co-code-tok"));
    const byText = (needle: string) => {
      const hit = tokens.find((el) => el.textContent?.trim() === needle);
      return hit ? window.getComputedStyle(hit).color : null;
    };
    return {
      totalTokens: tokens.length,
      curl: byText("curl"),
      flag: byText("-H"),
      envVar: byText("$COLABONE_API_KEY"),
      jsonKey: byText('"model"'),
      jsonNumber: byText("512"),
      distinctCount: new Set(
        tokens
          .filter((el) => (el.textContent || "").trim().length > 0)
          .map((el) => window.getComputedStyle(el).color),
      ).size,
    };
  });
  expect(lightCurlColors.totalTokens).toBeGreaterThan(15);
  expect(lightCurlColors.distinctCount).toBeGreaterThanOrEqual(4);
  expect(lightCurlColors.curl).not.toBe(lightCurlColors.flag);
  expect(lightCurlColors.curl).not.toBe(lightCurlColors.envVar);
  expect(lightCurlColors.jsonKey).not.toBe(lightCurlColors.jsonNumber);

  // Switch to Python & Codex TOML
  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "Python",
  ).click();
  await expect(codeBlock).toHaveAttribute("data-language", "python");
  const pyDistinct = await codeBlock.evaluate((pre) => {
    const tokens = Array.from(pre.querySelectorAll(".co-code-tok"));
    return new Set(
      tokens
        .filter((el) => (el.textContent || "").trim().length > 0)
        .map((el) => window.getComputedStyle(el).color),
    ).size;
  });
  expect(pyDistinct).toBeGreaterThanOrEqual(4);

  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "Codex",
  ).click();
  await expect(codeBlock).toHaveAttribute("data-language", "toml");

  // Switch back to cURL and toggle Dark mode — verify syntax palette switches immediately
  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "cURL",
  ).click();
  await page.locator('button[aria-label="切换到深色模式"]').click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  const darkCurlColor = await codeBlock.evaluate((pre) => {
    const hit = Array.from(pre.querySelectorAll(".co-code-tok")).find(
      (el) => el.textContent?.trim() === "curl",
    );
    return hit ? window.getComputedStyle(hit).color : null;
  });
  expect(darkCurlColor).toBeTruthy();
  expect(darkCurlColor).not.toBe(lightCurlColors.curl);

  // 3. Narrow viewport (wrapped status bar) + active operation banner: toast dynamically clears top chrome
  await page.setViewportSize({ width: 960, height: 800 });
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.operation = {
      title: "部署远端管理服务",
      running: true,
    };
    s.error =
      "API 配置无效：请填写公网 HTTPS 固定域名，并粘贴有效的 Cloudflare Tunnel 安装命令或 Token";
  });
  await expect(page.locator(".co-op-banner")).toBeVisible();
  const narrowToast = page.locator(".co-toast.is-error");
  await expect(narrowToast).toBeVisible();
  const clearance = await page.evaluate(() => {
    const banner = document
      .querySelector(".co-op-banner")
      ?.getBoundingClientRect();
    const toast = document
      .querySelector(".co-toast.is-error")
      ?.getBoundingClientRect();
    return banner && toast
      ? {
          bannerBottom: Math.round(banner.bottom),
          toastTop: Math.round(toast.top),
        }
      : null;
  });
  expect(clearance).not.toBeNull();
  expect(clearance!.toastTop).toBeGreaterThanOrEqual(clearance!.bannerBottom);

  // Closing the state.error toast must not zombie-repop on subsequent 600ms polls
  await narrowToast.locator(".co-toast-close").click();
  await expect(narrowToast).toHaveCount(0);
  await page.waitForTimeout(800);
  await expect(narrowToast).toHaveCount(0);

  // Rapid locale switching deduplicates into a single language toast instead of stacking duplicates
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="en-US"]').click();
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="ja-JP"]').click();
  await expect(page.locator(".co-toast.is-success")).toHaveCount(1);
});

test("Toasts triggered inside ModelDrawer stay horizontally centered in the drawer across viewport widths and inline logs", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 840 });
  await mock(page, true);

  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.state = "ready";
    s.runtime.protocol = 7;
    s.runtime.loaded_model = "";
    s.models[0].common = true;
    s.models[0].downloaded = true;
    s.models[0].loaded = false;
  });

  await navItem(page, "对外 API").click();
  await expect(page.locator(".co-api-page-header")).toBeVisible();

  const drawer = await openApiModelDrawer(page);
  // Wait for drawer slide-in transition to settle before measuring bounding rects
  await page.waitForTimeout(350);

  const firstCard = drawer.locator(".co-model-card").first();
  await firstCard.getByRole("button", { name: "装载" }).click();

  const toast = page.locator(".co-toast.is-success");
  await expect(toast).toBeVisible();
  await expect(toast).toContainText("正在装载模型到显存");

  const measureDrawerToast = () =>
    page.evaluate(() => {
      const content = document
        .querySelector(".co-model-drawer-root .ant-drawer-content")
        ?.getBoundingClientRect();
      const header = document
        .querySelector(".co-model-drawer-root .ant-drawer-header")
        ?.getBoundingClientRect();
      const toastEl = document
        .querySelector(".co-toast")
        ?.getBoundingClientRect();
      if (!content || !header || !toastEl) return null;
      return {
        drawerLeft: content.left,
        drawerRight: content.right,
        drawerCenterX: (content.left + content.right) / 2,
        headerBottom: header.bottom,
        toastLeft: toastEl.left,
        toastRight: toastEl.right,
        toastTop: toastEl.top,
        toastCenterX: (toastEl.left + toastEl.right) / 2,
        viewportCenterX: window.innerWidth / 2,
      };
    });

  const wide = await measureDrawerToast();
  expect(wide).not.toBeNull();
  expect(Math.abs(wide!.toastCenterX - wide!.drawerCenterX)).toBeLessThanOrEqual(2);
  expect(wide!.toastLeft).toBeGreaterThanOrEqual(wide!.drawerLeft + 15);
  expect(wide!.toastRight).toBeLessThanOrEqual(wide!.drawerRight - 15);
  expect(wide!.toastTop).toBeGreaterThanOrEqual(wide!.headerBottom);

  // Resizing to a narrower viewport keeps the toast centered inside the resized drawer
  await page.setViewportSize({ width: 640, height: 800 });
  // Wait for Ant Design Drawer's 300ms width transition to settle
  await page.waitForTimeout(380);
  const narrow = await measureDrawerToast();
  expect(narrow).not.toBeNull();
  expect(
    Math.abs(narrow!.toastCenterX - narrow!.drawerCenterX),
  ).toBeLessThanOrEqual(2);
  expect(narrow!.toastLeft).toBeGreaterThanOrEqual(narrow!.drawerLeft + 15);
  expect(narrow!.toastRight).toBeLessThanOrEqual(narrow!.drawerRight - 15);

  // Closing the toast does not accidentally dismiss the underlying ModelDrawer
  await toast.locator(".co-toast-close").click();
  await expect(toast).toHaveCount(0);
  await expect(drawer).toBeVisible();

  // Close drawer, open inline LogDrawer (web preview), then reopen ModelDrawer:
  // ModelDrawer overlay still centers toasts inside the drawer (ignoring inline LogDrawer behind backdrop)
  await page.setViewportSize({ width: 1280, height: 840 });
  await page.locator(".co-model-drawer-root .ant-drawer-close").click();
  await expect(drawer).toBeHidden();

  await page.locator('button[aria-label="展开运行日志"]').click();
  await expect(page.locator(".co-log-drawer")).toBeVisible();

  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.loaded_model = "";
    s.runtime.tasks = [];
    s.models[0].loaded = false;
  });

  const drawerWithLogs = await openApiModelDrawer(page);
  await page.waitForTimeout(350);
  await drawerWithLogs
    .locator(".co-model-card")
    .first()
    .getByRole("button", { name: "装载" })
    .click();
  await expect(toast).toBeVisible();

  const withLogs = await measureDrawerToast();
  expect(withLogs).not.toBeNull();
  expect(
    Math.abs(withLogs!.toastCenterX - withLogs!.drawerCenterX),
  ).toBeLessThanOrEqual(2);

  // Closing the drawer while the toast is still visible re-centers it over the main workspace (left of inline LogDrawer)
  await page.locator(".co-model-drawer-root .ant-drawer-close").click();
  await expect(drawerWithLogs).toBeHidden();
  await expect(toast).toBeVisible();

  const workspaceToast = await page.evaluate(() => {
    const workspace = document
      .querySelector(".co-workspace")
      ?.getBoundingClientRect();
    const toastEl = document
      .querySelector(".co-toast")
      ?.getBoundingClientRect();
    if (!workspace || !toastEl) return null;
    return {
      workspaceCenterX: (workspace.left + workspace.right) / 2,
      toastCenterX: (toastEl.left + toastEl.right) / 2,
    };
  });
  expect(workspaceToast).not.toBeNull();
  expect(
    Math.abs(workspaceToast!.toastCenterX - workspaceToast!.workspaceCenterX),
  ).toBeLessThanOrEqual(3);
});
