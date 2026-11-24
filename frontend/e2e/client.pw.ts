import { test, expect } from "@playwright/test";
import fs from "node:fs";
import { fileURLToPath } from "node:url";

const EXPECTED_APP_VERSION = `v${
  JSON.parse(
    fs.readFileSync(
      fileURLToPath(new URL("../../console/mygo.json", import.meta.url)),
      "utf8",
    ),
  ).version
}`;

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
          gpu: "G4",
          idle_minutes: 30,
          idle_countdown_seconds: 60,
          auto_drive: true,
          download_threads: 16,
          assign_max_attempts: 20,
        },
      };
      const calls: any[] = [];
      const canvasOpenCalls: any[] = [];
      const exportCalls: any[] = [];
      const logDrawerCalls: any[] = [];
      const nativeLocales: string[] = [];
      const localeCommands: any[] = [];
      const billingUsageCalls: any[] = [];
      const defaultUpdateState: any = {
        status: "up_to_date",
        current_version: "0.1.6",
        available_version: "",
        is_prerelease: false,
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
      };
      const updateState: any = (window as any).__initialUpdateState
        ? { ...defaultUpdateState, ...(window as any).__initialUpdateState }
        : { ...defaultUpdateState };
      const updateCalls: any[] = [];
      const defaultCloseBehaviorState = {
        behavior: "ask",
        default_choice: "quit",
        can_background: true,
      };
      const closeBehaviorState: any = (window as any).__initialCloseBehaviorState
        ? {
            ...defaultCloseBehaviorState,
            ...(window as any).__initialCloseBehaviorState,
          }
        : { ...defaultCloseBehaviorState };
      const closeBehaviorCalls: any[] = [];
      const openedUrls: string[] = [];
      const mygoEventListeners = new Map<string, Set<(payload: any) => void>>();
      const subscribeMyGoEvent = (
        name: string,
        listener: (payload: any) => void,
      ) => {
        let listeners = mygoEventListeners.get(name);
        if (!listeners) {
          listeners = new Set();
          mygoEventListeners.set(name, listeners);
        }
        listeners.add(listener);
        return () => {
          listeners?.delete(listener);
        };
      };
      const savedSecrets: { apiTunnelToken: string; canvasTunnelToken: string; hfToken: string } = {
        apiTunnelToken: "",
        canvasTunnelToken: "",
        hfToken: "",
      };
      (window as any).__state = state;
      (window as any).__calls = calls;
      (window as any).__savedSecrets = savedSecrets;
      (window as any).__canvasOpenCalls = canvasOpenCalls;
      (window as any).__exportCalls = exportCalls;
      (window as any).__logDrawerCalls = logDrawerCalls;
      (window as any).__nativeLocales = nativeLocales;
      (window as any).__localeCommands = localeCommands;
      (window as any).__billingUsageCalls = billingUsageCalls;
      (window as any).__updateState = updateState;
      (window as any).__updateCalls = updateCalls;
      (window as any).__closeBehaviorState = closeBehaviorState;
      (window as any).__closeBehaviorCalls = closeBehaviorCalls;
      (window as any).__openedUrls = openedUrls;
      (window as any).__emitMyGoEvent = (name: string, payload: any) => {
        const listeners = mygoEventListeners.get(name);
        if (!listeners) return;
        for (const listener of Array.from(listeners)) {
          listener(payload);
        }
      };
      (window as any).mygo = {
        call: async (method: string, ...args: any[]) => {
          const request = args[0];
          if (method === "ColabOneDesktop.OpenURL") {
            openedUrls.push(String(request ?? ""));
            return;
          }
          if (method === "ColabOneDesktop.GetCloseBehavior") {
            return { ...closeBehaviorState };
          }
          if (method === "ColabOneDesktop.SetCloseBehavior") {
            const nextBehavior = String(args[0] ?? "ask");
            closeBehaviorState.behavior = nextBehavior;
            closeBehaviorCalls.push({ method: "set", behavior: nextBehavior });
            const next = { ...closeBehaviorState };
            (window as any).__emitMyGoEvent("colabone-close-behavior", next);
            return next;
          }
          if (method === "ColabOneDesktop.ResolveCloseRequest") {
            const action = String(args[0] ?? "quit");
            const remember = Boolean(args[1]);
            closeBehaviorCalls.push({ method: "resolve", action, remember });
            if (remember && (action === "quit" || action === "background")) {
              closeBehaviorState.behavior = action;
              (window as any).__emitMyGoEvent("colabone-close-behavior", {
                ...closeBehaviorState,
              });
            }
            return;
          }
          if (
            method === "ColabOneDesktop.GetUpdateState" ||
            method === "ColabOneDesktop.CheckForUpdate" ||
            method === "ColabOneDesktop.StartUpdateInstall" ||
            method === "ColabOneDesktop.CancelUpdateInstall" ||
            method === "ColabOneDesktop.SkipUpdateVersion" ||
            method === "ColabOneDesktop.SetUpdatePreferences" ||
            method === "ColabOneDesktop.RelaunchForUpdate"
          ) {
            if (method === "ColabOneDesktop.CheckForUpdate") {
              updateCalls.push({ method: "check", manual: args[0] });
            } else if (method === "ColabOneDesktop.StartUpdateInstall") {
              updateCalls.push({ method: "install" });
            } else if (method === "ColabOneDesktop.CancelUpdateInstall") {
              updateCalls.push({ method: "cancel" });
            } else if (method === "ColabOneDesktop.SkipUpdateVersion") {
              updateCalls.push({ method: "skip", version: args[0] });
            } else if (method === "ColabOneDesktop.SetUpdatePreferences") {
              updateCalls.push({
                method: "prefs",
                autoCheck: args[0],
                includePrerelease: args[1],
              });
            } else if (method === "ColabOneDesktop.RelaunchForUpdate") {
              updateCalls.push({ method: "relaunch" });
            }
            if (typeof (window as any).__onUpdateCall === "function") {
              const override = await (window as any).__onUpdateCall(
                method,
                args,
                updateState,
              );
              if (override !== undefined) {
                return override;
              }
            }
            if (method === "ColabOneDesktop.GetUpdateState") {
              return { ...updateState };
            }
            if (method === "ColabOneDesktop.CheckForUpdate") {
              if (typeof (window as any).__onCheckForUpdate === "function") {
                return await (window as any).__onCheckForUpdate(
                  args[0],
                  updateState,
                );
              }
              return { ...updateState };
            }
            if (method === "ColabOneDesktop.StartUpdateInstall") {
              if (typeof (window as any).__onStartUpdateInstall === "function") {
                return await (window as any).__onStartUpdateInstall(updateState);
              }
              updateState.status = "ready_to_restart";
              updateState.progress_percent = 100;
              const next = { ...updateState };
              (window as any).__emitMyGoEvent("colabone-update", next);
              return next;
            }
            if (method === "ColabOneDesktop.CancelUpdateInstall") {
              updateState.status = "available";
              updateState.progress_percent = 0;
              const next = { ...updateState };
              (window as any).__emitMyGoEvent("colabone-update", next);
              return next;
            }
            if (method === "ColabOneDesktop.SkipUpdateVersion") {
              updateState.skipped_version = args[0];
              updateState.status = "up_to_date";
              updateState.available_version = "";
              const next = { ...updateState };
              (window as any).__emitMyGoEvent("colabone-update", next);
              return next;
            }
            if (method === "ColabOneDesktop.SetUpdatePreferences") {
              updateState.auto_check = Boolean(args[0]);
              updateState.include_prerelease = Boolean(args[1]);
              const next = { ...updateState };
              (window as any).__emitMyGoEvent("colabone-update", next);
              return next;
            }
            if (method === "ColabOneDesktop.RelaunchForUpdate") {
              return;
            }
          }
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
            if (request.action === "billing.usage") {
              billingUsageCalls.push(request.payload || {});
              const loggedIn = !!(state.account?.logged_in || state.setup?.colab);
              const start = Number(request.payload?.start || Math.floor(Date.now() / 1000) - 86400);
              const end = Number(request.payload?.end || Math.floor(Date.now() / 1000));
              if (!loggedIn) {
                return JSON.stringify({
                  logged_in: false,
                  bucket_seconds: 1800,
                  retention_days: 365,
                  start,
                  end,
                  available_dates: [],
                  active_dates: [],
                  buckets: [],
                });
              }
              const customFn = (window as any).__onBillingUsage;
              if (typeof customFn === "function") {
                return JSON.stringify(customFn(request.payload || {}, state));
              }
              const nowSec = Math.floor(Date.now() / 1000);
              const alignedNow = nowSec - (nowSec % 1800);
              const allBuckets: any[] = Array.isArray((window as any).__billingUsageBuckets)
                ? (window as any).__billingUsageBuckets
                : [
                    {
                      ts: alignedNow - 7200,
                      duration: 1800,
                      consumed: 0.98,
                      topup: 0,
                      balance_start: 102.48,
                      balance_end: 101.5,
                      has_balance: true,
                      rate_avg: 1.96,
                      rate_max: 1.96,
                      active_seconds: 1800,
                      max_instances: 1,
                      samples: 60,
                      estimated: false,
                    },
                    {
                      ts: alignedNow - 3600,
                      duration: 1800,
                      consumed: 1.5,
                      topup: 0,
                      balance_start: 101.5,
                      balance_end: 100.0,
                      has_balance: true,
                      rate_avg: 3.0,
                      rate_max: 3.5,
                      active_seconds: 1800,
                      max_instances: 1,
                      samples: 60,
                      estimated: false,
                    },
                  ];
              const fmtDate = (sec: number) => {
                const d = new Date(sec * 1000);
                return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
              };
              const availDates = Array.from(new Set(allBuckets.map((b) => fmtDate(b.ts)))).sort();
              const activeDates = Array.from(
                new Set(allBuckets.filter((b) => b.consumed > 0).map((b) => fmtDate(b.ts))),
              ).sort();
              const filtered = allBuckets.filter((b) => b.ts + (b.duration || 1800) > start && b.ts < end);
              const prior = allBuckets.filter((b) => b.ts + (b.duration || 1800) <= start && b.has_balance);
              const initialBalance = prior.length ? prior[prior.length - 1].balance_end : 102.48;
              return JSON.stringify({
                logged_in: true,
                bucket_seconds: 1800,
                retention_days: 365,
                start,
                end,
                current_balance: state.account?.balance ?? 100,
                current_rate: state.account?.rate ?? 0,
                checked_at: state.account?.checked_at ?? nowSec,
                initial_balance: initialBalance,
                available_dates: availDates,
                active_dates: activeDates,
                buckets: filtered,
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
                "assign_max_attempts",
              ]) {
                if (payload && k in payload) state.settings[k] = payload[k];
              }
              if (payload?.hf_token && String(payload.hf_token).trim()) {
                savedSecrets.hfToken = String(payload.hf_token).trim();
                state.setup.hf = true;
              }
            }
            if (action === "settings.hf_token") {
              return JSON.stringify({ token: savedSecrets.hfToken });
            }
            if (action === "api.save") {
              const rawDomain = (payload?.domain || "").trim();
              const normalized = rawDomain
                ? rawDomain.startsWith("https://")
                  ? rawDomain
                  : `https://${rawDomain}`
                : "";
              if (payload?.tunnel_token && String(payload.tunnel_token).trim()) {
                savedSecrets.apiTunnelToken = String(payload.tunnel_token).trim();
              }
              state.api_settings = {
                mode: "named",
                domain: normalized,
                has_token: !!savedSecrets.apiTunnelToken || !!state.api_settings?.has_token,
                has_key: !!state.api_settings?.has_key,
              };
              return JSON.stringify({ ok: true });
            }
            if (action === "api.tunnel_token") {
              return JSON.stringify({ token: savedSecrets.apiTunnelToken });
            }
            if (action === "api.key") {
              return JSON.stringify({ key: "sk-test-privacy-key-999" });
            }
            if (action === "runtime.start.retry") {
              if (
                state.operation?.kind === "runtime_start" &&
                state.operation?.waiting_retry &&
                !state.operation?.cancelling
              ) {
                const next = Number(state.operation.next_attempt || 2);
                const total = Number(state.operation.total_attempts || 10);
                const gpu = state.operation.gpu || state.settings.gpu || "G4";
                state.operation = {
                  ...state.operation,
                  title: `Google 算力紧张，正在排队分配 ${gpu}（第 ${next}/${total} 次）`,
                  waiting_retry: false,
                  retryable: false,
                  retry_at: 0,
                  attempt: next,
                  has_retried: true,
                };
                return JSON.stringify({ ok: true, triggered: true });
              }
              return JSON.stringify({ ok: true, triggered: false });
            }
            if (action === "runtime.start.cancel") {
              if (
                state.operation?.kind === "runtime_start" &&
                state.operation?.cancellable
              ) {
                if ((window as any).__deferStartCancel) {
                  state.operation = {
                    ...state.operation,
                    title: "正在取消 GPU 分配",
                    cancellable: false,
                    cancelling: true,
                    retryable: false,
                    waiting_retry: false,
                    retry_at: 0,
                  };
                } else {
                  state.operation = null;
                  state.runtime = { state: "stopped" };
                }
                return JSON.stringify({ ok: true, cancelled: true });
              }
              return JSON.stringify({ ok: true, cancelled: false });
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
              if (payload?.tunnel_token && String(payload.tunnel_token).trim()) {
                savedSecrets.canvasTunnelToken = String(payload.tunnel_token).trim();
              }
              state.comfy_canvas_settings = {
                mode: "named",
                domain: normalized,
                has_token: !!savedSecrets.canvasTunnelToken || !!state.comfy_canvas_settings?.has_token,
              };
              return JSON.stringify({ ok: true });
            }
            if (action === "comfyui.canvas.tunnel_token") {
              return JSON.stringify({ token: savedSecrets.canvasTunnelToken });
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
            if (action === "comfyui.input.upload") {
              return JSON.stringify({
                id: `asset-mock-${calls.length}`,
                filename: payload?.filename || "upload.bin",
              });
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
        callWith(method: string, args: any[] = []) {
          return this.call(method, ...args);
        },
        on(name: string, listener: (payload: any) => void) {
          return subscribeMyGoEvent(name, listener);
        },
        once(name: string, listener: (payload: any) => void) {
          const unsub = subscribeMyGoEvent(name, (payload: any) => {
            unsub();
            listener(payload);
          });
          return unsub;
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
  await expect(page.getByText("空空如也").first()).toBeVisible();
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
  await expect(panel).toContainText("空空如也");
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
  await expect(panel).toContainText("空空如也，开始操作后将在此实时显示");
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
  // Public API inference requests do not render the bottom task dock; cancel lives inline on the running request row.
  await expect(page.locator(".co-task-bar")).toHaveCount(0);
  const inlineCancelBtn = speedSection.getByRole("button", {
    name: "取消请求 #3",
  });
  await expect(inlineCancelBtn).toBeVisible();
  await inlineCancelBtn.click();
  await expect
    .poll(async () =>
      page.evaluate(() =>
        ((window as any).__calls || []).filter(
          (c: any) => c.action === "task.cancel",
        ),
      ),
    )
    .toEqual([{ action: "task.cancel", payload: { id: "req-3" } }]);

  await page.screenshot({
    path: fileURLToPath(
      new URL("../../work/api-speed-and-log-drawer.png", import.meta.url),
    ),
    fullPage: true,
  });

  // Concurrent non-API tasks (such as model download) still show the bottom task dock even when preceded by an API task.
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.tasks.push({
      id: "dl-concurrent-1",
      kind: "download",
      model_id: "qwen",
      state: "running",
      progress: 0.42,
      message: "正在下载模型权重",
      error: "",
    });
  });
  await expect(page.locator(".co-task-bar")).toBeVisible();
  await expect(page.locator(".co-task-bar")).toContainText("正在下载模型权重");
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

  // Outputs gallery is tucked inside a drawer by default; toolbar exposes the Outputs trigger button.
  await expect(page.locator(".co-comfy-outputs-drawer-root")).toHaveCount(0);
  await expect(page.locator(".co-comfy-param-drawer-root")).toHaveCount(0);
  await expect(page.locator(".co-comfy-gallery-card")).toHaveCount(0);
  await expect(page.locator(".co-comfy-param-card")).toHaveCount(0);
  const outputsTrigger = page.locator(".co-comfy-outputs-trigger");
  await expect(outputsTrigger).toBeVisible();
  await expect(outputsTrigger).toContainText("输出结果");

  // Empty workflow card renders cleanly on its own without any stretched side gallery.
  await expect(page.locator(".co-comfy-empty-card")).toBeVisible();

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

  // Imported workflow appears in the workflow list and automatically opens its Parameter Configuration Drawer.
  const workflowCard = page
    .locator(".co-comfy-workflow-list .co-comfy-workflow-item", {
      hasText: "测试文生图工作流",
    })
    .first();
  await expect(workflowCard).toBeVisible();
  const paramDrawer = page.locator(
    ".co-comfy-param-drawer-root .ant-drawer-content",
  );
  await expect(paramDrawer).toBeVisible();
  const promptArea = paramDrawer.locator(".co-comfy-param-form textarea").first();
  await expect(promptArea).toHaveValue("ocean sunrise");

  // Close the Parameter Configuration Drawer, bring GPU online, start ComfyUI, and reopen via the workflow card.
  await paramDrawer
    .locator(".ant-drawer-footer")
    .getByRole("button", { name: /^关\s*闭$/ })
    .click();
  await expect(paramDrawer).toBeHidden();

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

  await workflowCard.click();
  await expect(paramDrawer).toBeVisible();
  await expect(promptArea).toHaveValue("ocean sunrise");

  // Edit prompt inside the drawer and submit -> verifies comfyui.run overrides and auto-close.
  await promptArea.fill("neon harbor rain");
  await paramDrawer.getByRole("button", { name: "运行工作流" }).click();
  await expect(paramDrawer).toBeHidden();

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

  // Clicking the Outputs trigger opens the Output Results Drawer showing the clean empty state.
  await outputsTrigger.click();
  const outputsDrawer = page.locator(
    ".co-comfy-outputs-drawer-root .ant-drawer-content",
  );
  await expect(outputsDrawer).toBeVisible();
  await expect(outputsDrawer).toContainText("空空如也");
  await page.keyboard.press("Escape");
  await expect(outputsDrawer).toBeHidden();
});

test("ComfyUI built-in digital human template shows beginner essentials first and reveals license warning for non-commercial TTS in Advanced Settings", async ({
  page,
}) => {
  const comfyCatalog = JSON.parse(
    fs.readFileSync(
      fileURLToPath(new URL("../../catalog/comfyui.json", import.meta.url)),
      "utf8",
    ),
  );
  await mock(page, true);
  await page.evaluate((cat: any) => {
    const s = (window as any).__state;
    s.workflows = cat.templates;
    s.tts_engines = cat.tts_engines;
  }, comfyCatalog);

  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();

  const digitalHumanCard = page
    .locator(".co-comfy-workflow-list .co-comfy-workflow-item", {
      hasText: "口播数字人 · 长视频推荐版",
    })
    .first();
  await expect(digitalHumanCard).toBeVisible();
  await digitalHumanCard.click();

  const paramDrawer = page.locator(
    ".co-comfy-param-drawer-root .ant-drawer-content",
  );
  await expect(paramDrawer).toBeVisible();

  // Only beginner-essential basic fields are visible by default; Advanced Settings starts collapsed.
  await expect(paramDrawer).toContainText("主播人像照片");
  await expect(paramDrawer.locator(".co-comfy-segmented")).toBeVisible();
  await expect(paramDrawer).toContainText("口播文稿");
  await expect(paramDrawer).toContainText("输出画幅与清晰度");

  const advancedToggle = paramDrawer.locator(".co-comfy-advanced-toggle");
  await expect(advancedToggle).toBeVisible();
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "false");
  await expect(
    paramDrawer.locator("#comfy-field-pipeline_tts_engine"),
  ).toBeHidden();
  await expect(paramDrawer.locator(".co-comfy-license-alert")).toHaveCount(0);

  // Expanding Advanced Settings exposes the remote TTS engine selector; picking fish_s2_pro shows the license alert.
  await advancedToggle.click();
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "true");
  const ttsSelect = paramDrawer.locator(
    ".ant-select:has(#comfy-field-pipeline_tts_engine)",
  );
  await expect(ttsSelect).toBeVisible();
  await chooseAntOption(page, ttsSelect, /Fish Speech S2-Pro/);

  const licenseAlert = paramDrawer.locator(".co-comfy-license-alert");
  await expect(licenseAlert).toBeVisible();
  await expect(licenseAlert).toContainText("Fish Speech S2-Pro");

  // License alert remains visible even if Advanced Settings is collapsed again.
  await advancedToggle.click();
  await expect(advancedToggle).toHaveAttribute("aria-expanded", "false");
  await expect(licenseAlert).toBeVisible();
});

test("ComfyUI built-in digital human workflows translate cleanly to Japanese across cards, Parameter Drawer, and TTS engine", async ({
  page,
}) => {
  const comfyCatalog = JSON.parse(
    fs.readFileSync(
      fileURLToPath(new URL("../../catalog/comfyui.json", import.meta.url)),
      "utf8",
    ),
  );
  await mock(page, true);
  await page.evaluate((cat: any) => {
    const s = (window as any).__state;
    s.workflows = cat.templates;
    s.tts_engines = cat.tts_engines;
    s.runtime.active_engine = "comfyui";
    s.runtime.comfyui.running = true;
    s.runtime.comfyui.state = "ready";
  }, comfyCatalog);

  // Switch locale to ja-JP via the top navbar locale switcher.
  await page.getByTestId("topbar-locale-trigger").click();
  await page.locator('.co-locale-menu-item[data-locale="ja-JP"]').click();
  await expect(page.locator("html")).toHaveAttribute("lang", "ja-JP");

  // Open ComfyUI Studio and confirm localized header + both built-in workflow cards.
  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", {
      name: "ComfyUI ワークフローエンジン",
      exact: true,
    }),
  ).toBeVisible();

  const cards = page.locator(".co-comfy-workflow-list .co-comfy-workflow-item");
  await expect(cards).toHaveCount(2);

  const infiniteTalkCard = cards.nth(0);
  await expect(
    infiniteTalkCard.locator(".co-comfy-wf-card-title"),
  ).toHaveText("デジタルヒューマン・長尺おすすめ版");
  await expect(
    infiniteTalkCard.locator(".co-comfy-workflow-desc"),
  ).toContainText("人物写真と台本（または音声）を入力");
  await expect(infiniteTalkCard).not.toContainText("口播数字人");
  await expect(infiniteTalkCard).not.toContainText("输入人像照片");

  const longCatCard = cards.nth(1);
  await expect(longCatCard.locator(".co-comfy-wf-card-title")).toHaveText(
    "デジタルヒューマン・フラッグシップ HD 版",
  );
  await expect(longCatCard.locator(".co-comfy-workflow-desc")).toContainText(
    "ネイティブ 720p レンダリング",
  );
  await expect(longCatCard).not.toContainText("口播数字人");
  await expect(longCatCard).not.toContainText("输入人像照片");

  // Open the Parameter Drawer and verify Japanese header, guide/field descriptions, preflight strip, and TTS engine.
  await infiniteTalkCard.click();
  const paramDrawer = page.locator(
    ".co-comfy-param-drawer-root .ant-drawer-content",
  );
  await expect(paramDrawer).toBeVisible();
  await expect(
    paramDrawer.locator(".co-comfy-drawer-title-text"),
  ).toHaveText("デジタルヒューマン・長尺おすすめ版");
  await expect(
    paramDrawer.locator(".co-comfy-beginner-guide"),
  ).toContainText("入力：人物写真 + 原稿または音声");
  await expect(
    paramDrawer.locator(".co-comfy-field-desc").first(),
  ).toContainText("正面または上半身");
  await expect(paramDrawer.locator(".co-comfy-segmented")).toContainText(
    "台本を入力（AI 自動音声）",
  );
  await expect(paramDrawer.locator(".co-comfy-segmented")).toContainText(
    "既存音声をアップロード",
  );

  // T4 (15 GB) preflight VRAM warning strip renders in Japanese inside the drawer.
  const preflightWarning = paramDrawer.locator(
    ".co-comfy-preflight-strip.is-warning",
  );
  await expect(preflightWarning).toBeVisible();
  await expect(preflightWarning).toContainText("GPU VRAM が不足しています");
  await expect(preflightWarning).toContainText("40 GB");
  await expect(preflightWarning).toContainText("15 GB");

  // Expand Advanced Settings and verify TTS engine name & description render in Japanese.
  const advancedToggle = paramDrawer.locator(".co-comfy-advanced-toggle");
  await advancedToggle.click();
  await expect(
    paramDrawer.locator(".co-comfy-tts-strip-name"),
  ).toHaveText("VoxCPM2（OpenBMB・48kHz 商用推奨）");
  await expect(
    paramDrawer.locator(".co-comfy-tts-strip-desc"),
  ).toContainText("ゼロショット");

  // Confirm zero leaked Simplified Chinese labels/descriptions inside the drawer.
  await expect(paramDrawer).not.toContainText("口播数字人");
  await expect(paramDrawer).not.toContainText("输入人像照片");
  await expect(paramDrawer).not.toContainText("主播人像照片");
  await expect(paramDrawer).not.toContainText("商用推荐");
  await expect(paramDrawer).not.toContainText("面壁智能开源");
});

test("ComfyUI digital human validates audio source toggle, IndexTTS-2 required reference audio, T4 preflight VRAM warning, and LogDrawer Escape coordination", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const comfyCatalog = JSON.parse(
    fs.readFileSync(
      fileURLToPath(new URL("../../catalog/comfyui.json", import.meta.url)),
      "utf8",
    ),
  );
  await mock(page, true);
  await page.evaluate((cat: any) => {
    const s = (window as any).__state;
    s.workflows = cat.templates;
    s.tts_engines = cat.tts_engines;
    s.runtime.active_engine = "comfyui";
    s.runtime.comfyui.running = true;
    s.runtime.comfyui.state = "ready";
  }, comfyCatalog);

  await navItem(page, "ComfyUI").click();
  await expect(
    page.getByRole("heading", { name: "ComfyUI 工作流引擎", exact: true }),
  ).toBeVisible();

  // Open inline LogDrawer first so both LogDrawer and Parameter Drawer are active simultaneously.
  const logPanel = page.locator("#activity-panel");
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(logPanel).toBeVisible();

  const digitalHumanCard = page
    .locator(".co-comfy-workflow-list .co-comfy-workflow-item", {
      hasText: "口播数字人 · 长视频推荐版",
    })
    .first();
  await expect(digitalHumanCard).toBeVisible();
  await digitalHumanCard.click();

  const paramDrawer = page.locator(
    ".co-comfy-param-drawer-root .ant-drawer-content",
  );
  await expect(paramDrawer).toBeVisible();
  await expect(logPanel).toBeVisible();

  // 1. Preflight VRAM warning banner on T4 (15 GB) for InfiniteTalk (40 GB+ required).
  const preflightWarning = paramDrawer.locator(
    ".co-comfy-preflight-strip.is-warning",
  );
  await expect(preflightWarning).toBeVisible();
  await expect(preflightWarning).toContainText(
    "当前 GPU 显存不足（需 40 GB，当前 15 GB）",
  );

  // 2. Escape coordination with LogDrawer: first Escape closes ONLY Parameter Drawer while keeping LogDrawer open.
  await page.keyboard.press("Escape");
  await expect(paramDrawer).toBeHidden();
  await expect(logPanel).toBeVisible();

  // Second Escape now closes LogDrawer cleanly.
  await page.keyboard.press("Escape");
  await expect(logPanel).toHaveCount(0);

  // Reopen Parameter Drawer for audio source toggle & IndexTTS-2 validation checks.
  await digitalHumanCard.click();
  await expect(paramDrawer).toBeVisible();

  // 3. Audio source toggle (tts <-> audio): switching to uploaded audio hides script_text and suppresses non-commercial TTS alert.
  const advancedToggle = paramDrawer.locator(".co-comfy-advanced-toggle");
  await advancedToggle.click();
  const ttsSelect = paramDrawer.locator(
    ".ant-select:has(#comfy-field-pipeline_tts_engine)",
  );
  await expect(ttsSelect).toBeVisible();
  await chooseAntOption(page, ttsSelect, /Fish Speech S2-Pro/);
  const licenseAlert = paramDrawer.locator(".co-comfy-license-alert");
  await expect(licenseAlert).toBeVisible();
  await expect(licenseAlert).toContainText("Fish Speech S2-Pro");

  const audioModeBtn = paramDrawer.locator(
    '.co-comfy-segmented button[role="radio"]',
    { hasText: "上传现成音频" },
  );
  const ttsModeBtn = paramDrawer.locator(
    '.co-comfy-segmented button[role="radio"]',
    { hasText: "输入文稿（AI 自动配音）" },
  );
  await audioModeBtn.click();
  await expect(
    paramDrawer.locator("#comfy-field-pipeline_script_text"),
  ).toHaveCount(0);
  await expect(paramDrawer.locator("#comfy-field-2_audio")).toHaveCount(1);
  await expect(licenseAlert).toHaveCount(0);

  // 4. Switch back to TTS, provide portrait + script, pick IndexTTS-2 without voice_ref_audio -> blocks run with validation error.
  await ttsModeBtn.click();
  const scriptInput = paramDrawer.locator("#comfy-field-pipeline_script_text");
  await expect(scriptInput).toBeVisible();
  await scriptInput.fill("大家好，这是一段用于离线回归验证的口播文稿。");

  // Upload a minimal PNG for the required presenter portrait so voice_ref_audio is the sole missing required field.
  await paramDrawer.locator("#comfy-field-1_image").setInputFiles({
    name: "presenter.png",
    mimeType: "image/png",
    buffer: Buffer.from([
      0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
    ]),
  });
  await expect(paramDrawer).toContainText("presenter.png");

  await chooseAntOption(page, ttsSelect, /IndexTTS-2/);
  await expect(licenseAlert).toBeVisible();
  await expect(licenseAlert).toContainText("IndexTTS-2");
  await expect(
    paramDrawer.locator(".co-comfy-field-required-hint"),
  ).toContainText("当前选用的 IndexTTS-2 引擎必须上传参考音色音频");

  await paramDrawer.locator(".co-comfy-drawer-run-btn").click();

  // Submission must be blocked: no comfyui.run emitted, drawer stays open, and error toast explains missing reference voice.
  const errorToast = page.locator(".co-toast.is-error .co-toast-message").last();
  await expect(errorToast).toBeVisible();
  await expect(errorToast).toContainText("请先填写或上传必填项：参考音色");
  await expect(errorToast).not.toContainText("（可选）");
  await expect(paramDrawer).toBeVisible();
  const runCalls = await page.evaluate(() =>
    ((window as any).__calls || []).filter((c: any) => c.action === "comfyui.run"),
  );
  expect(runCalls).toHaveLength(0);
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
    s.settings.gpu = "T4";
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

  // API page: wide aligned rails + 2-column form inputs + independent-height cards aligned at top
  await navItem(page, "对外 API").click();
  await expect(
    page.getByRole("heading", { name: "对外 API", exact: true }),
  ).toBeVisible();
  await checkRails(".co-api-model-slot");
  const apiLayout = await page.evaluate(() => {
    const grid = document.querySelector(".co-api-grid")!;
    const left = document
      .querySelector(".co-api-connection")!
      .getBoundingClientRect();
    const right = document
      .querySelector(".co-api-examples")!
      .getBoundingClientRect();
    const domain = document.querySelector("#api-domain")!.getBoundingClientRect();
    const tunnel = document.querySelector("#api-tunnel")!.getBoundingClientRect();
    return {
      topAligned: Math.abs(left.top - right.top) <= 2,
      sideBySide: right.left > left.right,
      gridAlignItems: window.getComputedStyle(grid).alignItems,
      formSideBySide:
        Math.abs(domain.top - tunnel.top) <= 6 && tunnel.left > domain.right,
    };
  });
  expect(apiLayout).toEqual({
    topAligned: true,
    sideBySide: true,
    gridAlignItems: "start",
    formSideBySide: true,
  });

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

  // Settings page: centered native single-column compact grouped setting rows (macOS / ChatGPT style)
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();
  const settingsLayout = await page.evaluate(() => {
    const inner = document
      .querySelector(".co-settings-inner")!
      .getBoundingClientRect();
    const shell = document
      .querySelector(".co-settings-page")!
      .getBoundingClientRect();
    const lists = Array.from(
      document.querySelectorAll(".co-settings-list"),
    );
    const rowCounts = lists.map(
      (list) => list.querySelectorAll(":scope > .co-settings-row").length,
    );
    return {
      overflowX: document.documentElement.scrollWidth - window.innerWidth,
      innerWidth: Math.round(inner.width),
      centeredDelta: Math.abs(
        Math.round((inner.left + inner.right) / 2 - (shell.left + shell.right) / 2),
      ),
      listCount: lists.length,
      rowCounts,
    };
  });
  expect(settingsLayout).toEqual({
    overflowX: 0,
    innerWidth: 840,
    centeredDelta: 0,
    listCount: 4,
    rowCounts: [3, 6, 3, 4],
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
  // Wait for the browser's final async `scroll` event at scrollTop=0 to flush
  // (the animation sets scrollTop=0 inside rAF before the final scroll event fires).
  await expect
    .poll(() =>
      page.evaluate(() => {
        const h = (window as any).__scrollHistory as number[];
        return h.length ? h[h.length - 1] : -1;
      }),
    )
    .toBe(0);

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
  // Popular video families including MiniMax H3 are exposed as quick filter chips
  for (const label of [
    "MiniMax H3",
    "Wan 万相",
    "LTX-Video",
    "混元视频",
    "SeedVR2",
    "SkyReels",
    "SCAIL",
  ]) {
    await expect(familyGroup.getByRole("button", { name: label })).toBeVisible();
  }
  await familyGroup.getByRole("button", { name: "MiniMax H3" }).click();
  await expect
    .poll(async () =>
      page.evaluate(() => {
        const calls = (window as any).__calls as any[];
        return [...calls].reverse().find((c) => c.action === "hub.search")
          ?.payload;
      }),
    )
    .toMatchObject({
      modality: "video",
      family: "MiniMax-H3",
    });
  await familyGroup.getByRole("button", { name: "全部" }).click();
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

  // 3. ModelDrawer: saved repo with loaded model offers "Use Model" action, and HubBrowser shows no label overlap + disabled tooltip while busy
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

  // Expand saved repo in Mine tab -> should render "Use Model" action button
  await page.locator(".co-common-repo-row").getByRole("button", { name: "Versions" }).click();
  await expect(
    page.locator(".co-repo-picker").getByRole("button", { name: "Use Model" }),
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

  // 2. Unified dark-surface syntax highlighting across cURL (with embedded JSON), Python, Codex TOML, in both Light and Dark themes
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
      bg: window.getComputedStyle(pre).backgroundColor,
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
  // Light mode renders the code block on a deep dark surface with bright One Dark Pro syntax tokens
  expect(lightCurlColors.bg).toBe("rgb(17, 19, 24)");
  expect(lightCurlColors.totalTokens).toBeGreaterThan(15);
  expect(lightCurlColors.distinctCount).toBeGreaterThanOrEqual(5);
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
  expect(pyDistinct).toBeGreaterThanOrEqual(5);

  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "Codex",
  ).click();
  await expect(codeBlock).toHaveAttribute("data-language", "toml");
  const tomlDistinct = await codeBlock.evaluate((pre) => {
    const tokens = Array.from(pre.querySelectorAll(".co-code-tok"));
    return new Set(
      tokens
        .filter((el) => (el.textContent || "").trim().length > 0)
        .map((el) => window.getComputedStyle(el).color),
    ).size;
  });
  expect(tomlDistinct).toBeGreaterThanOrEqual(5);

  // Switch back to cURL and toggle Dark mode — verify dark surface and syntax palette stay consistent
  await segmentedItem(
    page.locator('.ant-segmented[aria-label="调用示例语言"]'),
    "cURL",
  ).click();
  await page.locator('button[aria-label="切换到深色模式"]').click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  const darkCurlState = await codeBlock.evaluate((pre) => {
    const hit = Array.from(pre.querySelectorAll(".co-code-tok")).find(
      (el) => el.textContent?.trim() === "curl",
    );
    return {
      bg: window.getComputedStyle(pre).backgroundColor,
      curl: hit ? window.getComputedStyle(hit).color : null,
    };
  });
  expect(darkCurlState.bg).toBe("rgb(17, 19, 24)");
  expect(darkCurlState.curl).toBe(lightCurlColors.curl);

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

test("all frontend loading icons rotate at 4 seconds per revolution", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.operation = { title: "部署远端管理服务", detail: "" };
    state.account.checked_at = Math.floor(Date.now() / 1000);
  });

  // 1. Ant Design <ASpin> 4-dot spinner (.ant-spin-dot-spin)
  const spinDot = page.locator(".co-op-banner .ant-spin-dot-spin");
  await expect(spinDot).toBeVisible();
  const spinDotDuration = await spinDot.evaluate(
    (el) => window.getComputedStyle(el).animationDuration,
  );
  expect(spinDotDuration).toBe("4s");

  // Clear operation so navbar & billing refresh controls are enabled, then hold account.refresh in-flight.
  await page.evaluate(() => {
    (window as any).__state.operation = null;
    const origCall = (window as any).mygo.call;
    (window as any).mygo.call = async (method: string, ...args: any[]) => {
      if (
        method === "ColabOneDesktop.Command" &&
        args[0]?.action === "account.refresh"
      ) {
        await new Promise<void>((resolve) => {
          (window as any).__releaseRefresh = resolve;
        });
      }
      return origCall(method, ...args);
    };
  });

  const navRefreshBtn = page.locator('button[aria-label="刷新账户实例"]');
  await expect(navRefreshBtn).toBeEnabled();
  await navRefreshBtn.click();

  // 2. Top navbar refresh icon (.co-nav-circle-btn.is-spinning svg)
  const navSpinner = page.locator(".co-nav-circle-btn.is-spinning svg");
  await expect(navSpinner).toBeVisible();
  expect(
    await navSpinner.evaluate(
      (el) => window.getComputedStyle(el).animationDuration,
    ),
  ).toBe("4s");

  // 3. BillingCard refresh icon (.co-bc-refresh-btn .is-spinning)
  const billingSpinner = page.locator(".co-bc-refresh-btn .is-spinning");
  await expect(billingSpinner).toBeVisible();
  expect(
    await billingSpinner.evaluate(
      (el) => window.getComputedStyle(el).animationDuration,
    ),
  ).toBe("4s");

  // 4. Ant Design <AButton :loading> uses our 3/4-circle ring spinner (.anticon-spin)
  const btnSpinner = page.locator(".ant-btn-loading-icon .anticon-spin").first();
  await expect(btnSpinner).toBeVisible();
  expect(
    await btnSpinner.evaluate(
      (el) => window.getComputedStyle(el).animationDuration,
    ),
  ).toBe("4s");
  expect(
    await btnSpinner.evaluate((el) => {
      const afterStyle = window.getComputedStyle(el, "::after");
      const mask = afterStyle.maskImage || afterStyle.webkitMaskImage || "";
      const svgDisplay = el.querySelector("svg")
        ? window.getComputedStyle(el.querySelector("svg")!).display
        : "";
      return { mask, svgDisplay };
    }),
  ).toEqual({
    mask: expect.stringContaining("data:image/svg+xml"),
    svgDisplay: "none",
  });

  await page.evaluate(() => {
    (window as any).__releaseRefresh?.();
  });
  await expect(navSpinner).toHaveCount(0);
});

test("top-left brand displays app version badge beside ColabOne and adapts across themes and widths", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 820 });
  await mock(page, true);

  const brand = page.locator(".co-brand");
  const versionBadge = page.locator(".co-brand-version");
  await expect(brand).toBeVisible();
  await expect(versionBadge).toBeVisible();
  await expect(versionBadge).toHaveText(/^v\d+\.\d+\.\d+/);
  await expect(versionBadge).toHaveText(EXPECTED_APP_VERSION);
  await expect(versionBadge).toHaveAttribute(
    "aria-label",
    `ColabOne ${EXPECTED_APP_VERSION} · 点击检查更新`,
  );

  // Positioned cleanly to the right of ColabOne on the same top navbar row without overlapping center nav.
  const layout = await page.evaluate(() => {
    const b = document.querySelector(".co-brand")!.getBoundingClientRect();
    const v = document.querySelector(".co-brand-version")!.getBoundingClientRect();
    const nav = document.querySelector(".co-nav-pill-track")!.getBoundingClientRect();
    return {
      brandRight: b.right,
      brandMidY: (b.top + b.bottom) / 2,
      verLeft: v.left,
      verRight: v.right,
      verMidY: (v.top + v.bottom) / 2,
      navLeft: nav.left,
      overflowX: document.documentElement.scrollWidth - window.innerWidth,
    };
  });
  expect(layout.overflowX).toBe(0);
  expect(layout.verLeft).toBeGreaterThanOrEqual(layout.brandRight - 1);
  expect(layout.verRight).toBeLessThan(layout.navLeft - 8);
  expect(Math.abs(layout.verMidY - layout.brandMidY)).toBeLessThanOrEqual(8);

  // Version badge adapts cleanly in dark mode.
  await page.getByRole("button", { name: "切换到深色模式" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(versionBadge).toHaveCSS("color", "rgb(156, 163, 181)");

  // Compact desktop width (720px) keeps badge visible; narrow (<680px) collapses cleanly without overflow.
  await page.setViewportSize({ width: 720, height: 800 });
  await expect(versionBadge).toBeVisible();
  await page.setViewportSize({ width: 640, height: 800 });
  await expect(versionBadge).toBeHidden();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    ),
  ).toBe(0);
});

test("model drawer disk warning compares free disk against remaining download bytes", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const GIB = 1024 ** 3;
    const MIB = 1024 ** 2;
    const totalBytes = Math.round(175.3 * GIB);
    const downloadedBytes = Math.round(167.6 * GIB);
    const state = (window as any).__state;
    state.runtime.disk_free = Math.round(67.9 * GIB);
    state.runtime.gpu = {
      name: "NVIDIA RTX PRO 6000 Blackwell Server Edition",
      total_mib: 97887,
      used_mib: 0,
      util_percent: 0,
      temp_c: 29,
      power_w: 47,
      power_limit_w: 600,
    };
    state.runtime.resources = {
      gpu: state.runtime.gpu,
      memory: {
        total_bytes: Math.round(176.9 * GIB),
        used_bytes: Math.round(3.9 * GIB),
        available_bytes: Math.round(173.0 * GIB),
        percent: 2.2,
      },
      disk: {
        total_bytes: Math.round(235.7 * GIB),
        used_bytes: Math.round(167.8 * GIB),
        free_bytes: Math.round(67.9 * GIB),
        percent: 71.2,
      },
      cpu: { cores: 48, percent: 0.2 },
    };
    state.runtime.tasks = [
      {
        id: "dl-active",
        kind: "download",
        model_id: "downloading-big",
        state: "running",
        progress: 0.96,
        message: "Qwen3.8-Flash-Next-Q8_0-00006-of-00006.gguf",
        error: "",
        download: {
          downloaded_bytes: downloadedBytes,
          total_bytes: totalBytes,
          speed_bps: 229.9 * MIB,
          eta_seconds: 35,
          elapsed_seconds: 720,
          resumed_bytes: 0,
          threads: 32,
          connections: 32,
          phase: "downloading",
          filename: "unsloth-Q8_0/Qwen3.8-Flash-Next-Q8_0-00006-of-00006.gguf",
          file_index: 6,
          file_count: 6,
        },
      },
    ];
    state.models = [
      {
        id: "downloading-big",
        name: "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        description: "Q8_0 · unsloth-Q8_0",
        repo: "huihui-ai/Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
        common: true,
        custom: true,
        size: totalBytes,
        min_vram_mib: 185000,
        downloaded: false,
        loaded: false,
        files: [],
      },
      {
        id: "paused-partial",
        name: "Paused-Partial-Model-GGUF",
        description: "Q8_0",
        repo: "org/Paused-Partial-Model-GGUF",
        common: true,
        custom: true,
        size: totalBytes,
        cached_bytes: downloadedBytes,
        min_vram_mib: 185000,
        downloaded: false,
        loaded: false,
        files: [],
      },
      {
        id: "untouched-big",
        name: "Untouched-Huge-Model-GGUF",
        description: "Q8_0",
        repo: "org/Untouched-Huge-Model-GGUF",
        common: true,
        custom: true,
        size: totalBytes,
        min_vram_mib: 185000,
        downloaded: false,
        loaded: false,
        files: [],
      },
    ];
  });

  const drawer = await openApiModelDrawer(page);

  const activeCard = drawer.locator(".co-model-card", {
    hasText: "Huihui-Qwen3.8-Flash-Next-abliterated-v2-GGUF",
  });
  await expect(activeCard).toBeVisible();
  await expect(activeCard).not.toContainText("远端可用磁盘");

  const pausedCard = drawer.locator(".co-model-card", {
    hasText: "Paused-Partial-Model-GGUF",
  });
  await expect(pausedCard).toBeVisible();
  await expect(pausedCard).not.toContainText("远端可用磁盘");

  const untouchedCard = drawer.locator(".co-model-card", {
    hasText: "Untouched-Huge-Model-GGUF",
  });
  await expect(untouchedCard).toContainText(
    "远端可用磁盘 67.9 GB 小于模型体积 175.3 GB",
  );

  // If free disk genuinely drops below the remaining 7.7 GB, both active and paused cards warn against remaining bytes.
  await page.evaluate(() => {
    const GIB = 1024 ** 3;
    const state = (window as any).__state;
    state.runtime.disk_free = Math.round(5.0 * GIB);
    state.runtime.resources.disk.free_bytes = Math.round(5.0 * GIB);
    state.runtime.resources.disk.used_bytes = Math.round(230.7 * GIB);
  });
  await expect(activeCard).toContainText(
    "远端可用磁盘 5.0 GB 小于剩余待下载体积 7.7 GB（已缓存 167.6 GB）",
  );
  await expect(pausedCard).toContainText(
    "远端可用磁盘 5.0 GB 小于剩余待下载体积 7.7 GB（已缓存 167.6 GB）",
  );
});

test("remote service upgrade banner stays clickable with confirmation modal when active, upgrades directly when idle, and supports dismissal", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.runtime.id = "gpu-g4-inst-1";
    state.runtime.protocol = 7;
    state.runtime.service_revision = "rev-before-fix";
    state.runtime.upgrade_available = true;
    state.runtime.loaded_model = "qwen";
    state.runtime.active_engine = "llama";
    state.models[0].downloaded = true;
    state.models[0].loaded = true;
  });

  const banner = page.locator(".co-upgrade-banner");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("远端服务有小版本更新，不影响当前正常使用");
  await expect(banner).toContainText("点击即可热更新并自动按当前配置恢复模型与服务");
  await expect(banner).not.toContainText("以启用实时硬件监控");

  const upgradeBtn = banner.getByRole("button", { name: "更新远端服务" });
  await expect(upgradeBtn).toBeEnabled();

  // Clicking while a model is loaded opens a confirmation modal listing active items.
  await upgradeBtn.click();
  const modal = page.locator(".ant-modal-content").filter({
    hasText: "确认热更新远端服务",
  });
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("已装载模型：Qwen2.5 · 0.5B（更新后将按原规格自动重新装载）");
  await expect(modal).toContainText("已下载的模型权重、工作流、作品与参数配置都会完整保留");

  // Confirming sends runtime.upgrade with { force: true, restore: true }.
  await modal.getByRole("button", { name: "立即更新并自动恢复" }).click();
  await expect(modal).toBeHidden();
  const forceUpgradeCalls = await page.evaluate(() =>
    ((window as any).__calls || []).filter(
      (c: any) => c.action === "runtime.upgrade",
    ),
  );
  expect(forceUpgradeCalls).toEqual([
    { action: "runtime.upgrade", payload: { force: true, restore: true } },
  ]);

  // Optional update banner can be dismissed cleanly.
  const closeBtn = banner.locator(".ant-alert-close-icon");
  await expect(closeBtn).toBeVisible();
  await closeBtn.click();
  await expect(banner).toHaveCount(0);

  // Switching to a different instance re-displays the optional upgrade banner; when idle, clicking upgrades directly without modal.
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.runtime.id = "gpu-g4-inst-2";
    state.runtime.loaded_model = "";
    state.runtime.active_engine = null;
    state.models[0].loaded = false;
  });
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("可随时热更新");
  await expect(upgradeBtn).toBeEnabled();
  await upgradeBtn.click();
  await expect(modal).toBeHidden();
  const allUpgradeCalls = await page.evaluate(() =>
    ((window as any).__calls || []).filter(
      (c: any) => c.action === "runtime.upgrade",
    ),
  );
  expect(allUpgradeCalls).toEqual([
    { action: "runtime.upgrade", payload: { force: true, restore: true } },
    { action: "runtime.upgrade", payload: {} },
  ]);

  // Legacy protocol (< 7) shows required protocol upgrade copy and cannot be dismissed.
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.runtime.protocol = 6;
    state.runtime.upgrade_available = true;
  });
  await expect(banner).toContainText(
    "当前实例需要更新远端服务，以启用实时硬件监控、对外 API 与 ComfyUI 工作流引擎",
  );
  await expect(banner.locator(".ant-alert-close-icon")).toHaveCount(0);
});

test("secret inputs auto-extract tokens on paste and reveal saved secrets on eye click across API, ComfyUI, and Settings", async ({
  page,
}) => {
  await mock(page, true);
  const apiToken = Buffer.from(
    JSON.stringify({ a: "acc-api-1", t: "tun-api-1", s: "sec-api-1" }),
  ).toString("base64");
  const canvasToken = Buffer.from(
    JSON.stringify({ a: "acc-comfy-2", t: "tun-comfy-2", s: "sec-comfy-2" }),
  ).toString("base64");
  const hfToken = "hf_ColabOneSecretToken1234567890";

  // 1) Public API Tunnel Token: paste install command -> auto-extract -> save -> reveal saved (stopped & running)
  await navItem(page, "对外 API").click();
  await page.getByLabel("API 域名", { exact: true }).fill("api.example.com");
  const apiTunnelInput = page.locator("#api-tunnel");
  await apiTunnelInput.evaluate((el, text) => {
    const dt = new DataTransfer();
    dt.setData("text/plain", text);
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, `sudo cloudflared service install ${apiToken}`);
  await expect(apiTunnelInput).toHaveValue(apiToken);

  const apiEyeBtn = page.locator(".co-api-connection .co-secret-toggle");
  await apiEyeBtn.click();
  await expect(apiTunnelInput).toHaveAttribute("type", "text");
  await expect(apiTunnelInput).toHaveValue(apiToken);

  await page.getByRole("button", { name: "保存配置", exact: true }).click();
  await expect(apiTunnelInput).toHaveValue("");
  await expect(apiTunnelInput).toHaveAttribute("type", "password");
  await expect(apiTunnelInput).toHaveAttribute("placeholder", "已安全保存；留空继续使用");

  // Click eye button after saving -> loads and reveals saved tunnel token
  await apiEyeBtn.click();
  await expect(apiTunnelInput).toHaveAttribute("type", "text");
  await expect(apiTunnelInput).toHaveValue(apiToken);
  // Click eye again -> hides it back
  await apiEyeBtn.click();
  await expect(apiTunnelInput).toHaveAttribute("type", "password");
  await expect(apiTunnelInput).toHaveValue("");

  // Even while Public API is running (locked), clicking the eye reveals the saved token read-only
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.runtime.public_api = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://api.example.com",
      error: "",
      streaming: true,
      checked_at: Date.now() / 1000,
      requests: 0,
      active: 0,
      completed: 0,
      failed: 0,
      last_request: 0,
    };
  });
  await expect(apiTunnelInput).toBeDisabled();
  await expect(apiEyeBtn).toBeEnabled();
  await apiEyeBtn.click();
  await expect(apiTunnelInput).toHaveAttribute("readonly", "");
  await expect(apiTunnelInput).toHaveAttribute("type", "text");
  await expect(apiTunnelInput).toHaveValue(apiToken);
  await apiEyeBtn.click();
  await expect(apiTunnelInput).toBeDisabled();
  await expect(apiTunnelInput).toHaveValue("");

  // 2) ComfyUI Native Canvas Tunnel Token: paste install command -> auto-extract -> save -> reveal saved
  await navItem(page, "ComfyUI").click();
  await page.locator(".co-comfy-tabs .ant-tabs-tab", { hasText: "原生画布" }).click();
  await page.locator("#comfy-canvas-domain").fill("comfy.example.com");
  const canvasTokenInput = page.locator("#comfy-canvas-token");
  await canvasTokenInput.evaluate((el, text) => {
    const dt = new DataTransfer();
    dt.setData("text/plain", text);
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, `docker run cloudflare/cloudflared:latest tunnel --no-autoupdate run --token ${canvasToken}`);
  await expect(canvasTokenInput).toHaveValue(canvasToken);
  await page.getByRole("button", { name: "保存配置", exact: true }).click();
  await expect(canvasTokenInput).toHaveValue("");

  const canvasEyeBtn = page.locator(".co-comfy-canvas-grid .co-secret-toggle");
  await canvasEyeBtn.click();
  await expect(canvasTokenInput).toHaveAttribute("type", "text");
  await expect(canvasTokenInput).toHaveValue(canvasToken);

  // 3) Settings Hugging Face Token: paste export snippet -> auto-extract -> save -> reveal saved without enabling Save button
  await navItem(page, "设置").click();
  const hfInput = page.locator("#settings-hf-token");
  const hfSaveBtn = page.getByRole("button", { name: "保存 Token", exact: true });
  await expect(hfSaveBtn).toBeDisabled();
  await hfInput.evaluate((el, text) => {
    const dt = new DataTransfer();
    dt.setData("text/plain", text);
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, `export HF_TOKEN="${hfToken}"`);
  await expect(hfInput).toHaveValue(hfToken);
  await expect(hfSaveBtn).toBeEnabled();
  await hfSaveBtn.click();
  await expect(hfInput).toHaveValue("");
  await expect(hfSaveBtn).toBeDisabled();

  const hfEyeBtn = page.locator(".co-hf-row .co-secret-toggle");
  await hfEyeBtn.click();
  await expect(hfInput).toHaveAttribute("type", "text");
  await expect(hfInput).toHaveValue(hfToken);
  // Viewing saved HF token does not dirty the draft or enable re-saving unchanged value
  await expect(hfSaveBtn).toBeDisabled();
  await hfEyeBtn.click();
  await expect(hfInput).toHaveAttribute("type", "password");
  await expect(hfInput).toHaveValue("");
});

test("allocation banner and device card provide cancel and retry-now controls during congested startup", async ({
  page,
}) => {
  await mock(page, false);

  // 1) Initial allocation attempt: Cancel button visible & enabled in both banner and device card; Retry Now hidden until backoff
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.setup.colab = true;
    s.settings.gpu = "G4";
    s.runtime = { state: "starting" };
    s.operation = {
      title: "正在分配 G4",
      running: true,
      kind: "runtime_start",
      cancellable: true,
      cancelling: false,
      retryable: false,
      waiting_retry: false,
      retry_at: 0,
      has_retried: false,
      gpu: "G4",
    };
  });

  const opBanner = page.locator(".co-op-banner");
  await expect(opBanner).toContainText("正在分配 G4");
  const bannerCancel = opBanner.locator(".co-op-cancel-btn");
  const cardCancel = page.locator(".co-device-cancel-btn");
  await expect(bannerCancel).toBeVisible();
  await expect(bannerCancel).toBeEnabled();
  await expect(cardCancel).toBeVisible();
  await expect(cardCancel).toBeEnabled();
  await expect(opBanner.locator(".co-op-retry-btn")).toHaveCount(0);

  // 2) Capacity congestion enters retry countdown: countdown text + enabled Retry Now + enabled Cancel appear
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.operation = {
      title: "Google 算力紧张，正在排队分配 G4（第 8/10 次）",
      running: true,
      kind: "runtime_start",
      cancellable: true,
      cancelling: false,
      retryable: true,
      waiting_retry: true,
      retry_at: Date.now() / 1000 + 25,
      attempt: 8,
      next_attempt: 9,
      total_attempts: 10,
      retry_kind: "capacity",
      has_retried: true,
      gpu: "G4",
    };
  });

  await expect(opBanner).toContainText(/Google 算力紧张，\d+ 秒后自动重试分配 G4（第 9\/10 次）/);
  const bannerRetry = opBanner.locator(".co-op-retry-btn");
  const cardRetry = page.locator(".co-device-retry-btn");
  await expect(bannerRetry).toBeVisible();
  await expect(bannerRetry).toBeEnabled();
  await expect(bannerRetry).toContainText("立即重试");
  await expect(cardRetry).toBeVisible();
  await expect(cardRetry).toBeEnabled();

  // Banner actions stay reachable even when switched to another tab
  await navItem(page, "对外 API").click();
  await expect(bannerRetry).toBeVisible();
  await expect(bannerCancel).toBeVisible();

  // 3) Click Retry Now -> triggers runtime.start.retry and switches to requesting state (button disabled/loading while request is in flight)
  await bannerRetry.click();
  await expect(opBanner).toContainText("Google 算力紧张，正在排队分配 G4（第 9/10 次）");
  await expect(bannerRetry).toBeDisabled();
  await expect(bannerRetry).toContainText("正在请求…");
  await expect(bannerCancel).toBeEnabled();

  // 4) Click Cancel while in-flight (deferred completion) -> immediately displays cancelling state, then returns cleanly to stopped
  await navItem(page, "远端设备").click();
  await page.evaluate(() => {
    (window as any).__deferStartCancel = true;
  });
  await cardCancel.click();
  await expect(opBanner).toContainText("正在取消 GPU 分配");
  await expect(bannerCancel).toBeDisabled();
  await expect(bannerCancel).toContainText("正在取消…");
  await expect(opBanner.locator(".co-op-retry-btn")).toHaveCount(0);

  await page.evaluate(() => {
    const s = (window as any).__state;
    s.operation = null;
    s.runtime = { state: "stopped" };
  });
  await expect(opBanner).toHaveCount(0);
  await expect(page.getByRole("button", { name: "启动 GPU" })).toBeVisible();
  await expect(
    page.locator(".co-toast.is-info", { hasText: "已取消 GPU 分配" }),
  ).toBeVisible();
  await expect(page.locator(".co-toast.is-error")).toHaveCount(0);

  const actions = await page.evaluate(() =>
    ((window as any).__calls || []).map((c: any) => c.action),
  );
  expect(actions).toContain("runtime.start.retry");
  expect(actions).toContain("runtime.start.cancel");
});

test("settings shows bottom floating save bar only when top save button scrolls out of view", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 760 });
  await mock(page, true);
  await navItem(page, "设置").click();
  await expect(
    page.getByRole("heading", { name: "设置", exact: true }),
  ).toBeVisible();

  const saveButtons = page.getByRole("button", { name: "保存设置", exact: true });
  const topSlot = page.locator(".co-settings-top-save-slot");
  const floatingBar = page.locator(".co-settings-floating-bar");

  // 1) At top: top save button visible, floating bar hidden, strictly 1 save button
  await expect(saveButtons).toHaveCount(1);
  await expect(topSlot.getByRole("button", { name: "保存设置", exact: true })).toBeVisible();
  await expect(floatingBar).toHaveCount(0);
  const initialHeaderHeight = await page.evaluate(
    () =>
      Math.round(
        document
          .querySelector(".co-settings-page .co-page-header")!
          .getBoundingClientRect().height,
      ),
  );

  // 2) Scroll slightly so the top save button is still partially visible -> floating bar stays hidden
  await page.evaluate(() => {
    const content = document.querySelector(".co-content") as HTMLElement;
    const slot = document.querySelector(".co-settings-top-save-slot") as HTMLElement;
    const cRect = content.getBoundingClientRect();
    const sRect = slot.getBoundingClientRect();
    // Leave ~10px of the top save button visible
    content.scrollTop = Math.max(1, Math.round(sRect.bottom - cRect.top - 10));
  });
  await expect(floatingBar).toHaveCount(0);
  await expect(saveButtons).toHaveCount(1);

  // 3) Scroll past the top save button -> floating bar appears at bottom, top button unmounts (still strictly 1 button, 0px header height shift)
  await page.evaluate(() => {
    const content = document.querySelector(".co-content") as HTMLElement;
    content.scrollTop = 320;
  });
  await expect(floatingBar).toBeVisible();
  await expect(saveButtons).toHaveCount(1);
  await expect(topSlot.getByRole("button", { name: "保存设置", exact: true })).toHaveCount(0);
  await expect(
    floatingBar.getByRole("button", { name: "保存设置", exact: true }),
  ).toBeVisible();

  await expect
    .poll(async () =>
      page.evaluate(() => {
        const header = document
          .querySelector(".co-settings-page .co-page-header")!
          .getBoundingClientRect();
        const inner = document
          .querySelector(".co-settings-inner")!
          .getBoundingClientRect();
        const bar = document
          .querySelector(".co-settings-floating-bar")!
          .getBoundingClientRect();
        const content = document
          .querySelector(".co-content")!
          .getBoundingClientRect();
        return {
          headerHeight: Math.round(header.height),
          widthDelta: Math.abs(Math.round(bar.width - inner.width)),
          bottomGap: Math.round(content.bottom - bar.bottom),
          overflowX: document.documentElement.scrollWidth - window.innerWidth,
        };
      }),
    )
    .toEqual({
      headerHeight: initialHeaderHeight,
      widthDelta: expect.any(Number),
      bottomGap: 16,
      overflowX: 0,
    });
  const floatingWidthDelta = await page.evaluate(() => {
    const inner = document
      .querySelector(".co-settings-inner")!
      .getBoundingClientRect();
    const bar = document
      .querySelector(".co-settings-floating-bar")!
      .getBoundingClientRect();
    return Math.abs(Math.round(bar.width - inner.width));
  });
  expect(floatingWidthDelta).toBeLessThanOrEqual(4);

  // 4) Clicking the floating save button triggers settings.save
  await floatingBar
    .getByRole("button", { name: "保存设置", exact: true })
    .click();
  await expect
    .poll(async () =>
      page.evaluate(() =>
        ((window as any).__calls || []).map((c: any) => c.action),
      ),
    )
    .toContain("settings.save");

  // 5) Scroll to the very bottom -> About row clears the floating bar without overlap
  await page.evaluate(() => {
    const content = document.querySelector(".co-content") as HTMLElement;
    content.scrollTop = content.scrollHeight;
  });
  await expect(floatingBar).toBeVisible();
  await expect(saveButtons).toHaveCount(1);
  const bottomClearance = await page.evaluate(() => {
    const about = document
      .querySelector(".co-about-card")!
      .getBoundingClientRect();
    const bar = document
      .querySelector(".co-settings-floating-bar")!
      .getBoundingClientRect();
    return Math.round(bar.top - about.bottom);
  });
  expect(bottomClearance).toBeGreaterThanOrEqual(8);

  // 6) Scroll back to top -> floating bar disappears, top save button returns (still strictly 1 button)
  await page.evaluate(() => {
    const content = document.querySelector(".co-content") as HTMLElement;
    content.scrollTop = 0;
  });
  await expect(floatingBar).toHaveCount(0);
  await expect(topSlot.getByRole("button", { name: "保存设置", exact: true })).toBeVisible();
  await expect(saveButtons).toHaveCount(1);

  // 7) Switching away from Settings while scrolled down immediately hides the floating bar
  await page.evaluate(() => {
    const content = document.querySelector(".co-content") as HTMLElement;
    content.scrollTop = 360;
  });
  await expect(floatingBar).toBeVisible();
  await navItem(page, "远端设备").click();
  await expect(floatingBar).toHaveCount(0);
});

test("status badge dots stay vertically centered without phantom empty-text gap across themes and locales", async ({
  page,
}) => {
  await mock(page, true);
  await page.goto("/");
  await expect(page.locator(".co-runtime-tag")).toBeVisible();

  const inspectBadge = async (selector: string) =>
    page.evaluate((sel) => {
      const el = document.querySelector(sel) as HTMLElement | null;
      if (!el) return null;
      const rect = el.getBoundingClientRect();
      const centerY = rect.top + rect.height / 2;
      const dot = el.querySelector(".ant-badge-status-dot") as HTMLElement | null;
      const dotRect = dot?.getBoundingClientRect() ?? null;
      const dotCs = dot ? getComputedStyle(dot) : null;
      const statusText = el.querySelector(".ant-badge-status-text") as HTMLElement | null;
      const statusTextCs = statusText ? getComputedStyle(statusText) : null;
      const label = el.querySelector(":scope > span:not(.ant-badge)") as HTMLElement | null;
      const labelRect = label?.getBoundingClientRect() ?? null;
      const tagCs = getComputedStyle(el);
      return {
        dotTop: dotCs?.top ?? null,
        dotWidth: dotCs?.width ?? null,
        dotHeight: dotCs?.height ?? null,
        dotDeltaY: dotRect ? Math.abs(dotRect.top + dotRect.height / 2 - centerY) : null,
        emptyTextDisplay:
          statusText && statusText.textContent === "" ? statusTextCs?.display ?? null : null,
        dotToLabelGap:
          dotRect && labelRect ? Math.round((labelRect.left - dotRect.right) * 10) / 10 : null,
        textBoxTrim: tagCs.textBoxTrim,
      };
    }, selector);

  const zhRuntime = await inspectBadge(".co-runtime-tag");
  expect(zhRuntime).not.toBeNull();
  expect(zhRuntime!.dotTop).toBe("0px");
  expect(zhRuntime!.dotWidth).toBe("6px");
  expect(zhRuntime!.dotHeight).toBe("6px");
  expect(zhRuntime!.dotDeltaY!).toBeLessThanOrEqual(0.5);
  expect(zhRuntime!.emptyTextDisplay).toBe("none");
  expect(zhRuntime!.dotToLabelGap!).toBeGreaterThanOrEqual(4);
  expect(zhRuntime!.dotToLabelGap!).toBeLessThanOrEqual(6);
  expect(zhRuntime!.textBoxTrim).toBe("none");

  const rmLive = await inspectBadge(".co-rm-live-badge");
  expect(rmLive).not.toBeNull();
  expect(rmLive!.dotTop).toBe("0px");
  expect(rmLive!.dotDeltaY!).toBeLessThanOrEqual(0.5);

  await navItem(page, "对外 API").click();
  await expect(page.locator(".co-api-status-tag")).toBeVisible();
  const apiStatus = await inspectBadge(".co-api-status-tag");
  expect(apiStatus).not.toBeNull();
  expect(apiStatus!.dotTop).toBe("0px");
  expect(apiStatus!.dotDeltaY!).toBeLessThanOrEqual(0.5);
  expect(apiStatus!.emptyTextDisplay).toBe("none");
  expect(apiStatus!.dotToLabelGap!).toBeLessThanOrEqual(6);

  // Switch to en-US and toggle theme to verify dynamic CSS-in-JS never regresses alignment
  await page.evaluate(() => {
    document.documentElement.lang = "en-US";
  });
  await page.getByRole("button", { name: /切换到深色模式|切换到浅色模式/ }).click();
  await page.waitForTimeout(150);

  const enApiStatus = await inspectBadge(".co-api-status-tag");
  expect(enApiStatus).not.toBeNull();
  expect(enApiStatus!.dotTop).toBe("0px");
  expect(enApiStatus!.dotDeltaY!).toBeLessThanOrEqual(0.5);
  expect(enApiStatus!.emptyTextDisplay).toBe("none");
  expect(enApiStatus!.textBoxTrim).toBe("trim-both");
});

test("CU consumption trend chart defaults to live 24h, supports calendar date selection, current time return, range drill-down, metric switching, and hover tooltip", async ({
  page,
}) => {
  await mock(page, true);
  const cuCard = page.locator(".co-cu-usage-card");
  await expect(cuCard).toBeVisible();
  await expect(cuCard).toContainText("计算单元消耗走势");
  await expect(cuCard.locator(".co-cu-window-tag")).toContainText("近 24 小时");
  // Default mock has 0.98 + 1.50 = 2.48 CU consumed across the last 24h
  await expect(cuCard.locator(".co-cu-kpi-strip")).toContainText("2.48");

  // Quick-jump icon on BillingCard scrolls to and highlights the CU chart
  await page.locator(".co-bc-trend-btn").click();
  await expect(cuCard).toHaveClass(/is-highlighted/);

  // Hovering on the chart shows tooltip and causes zero horizontal overflow
  const stage = cuCard.locator(".co-cu-plot-stage");
  await stage.scrollIntoViewIfNeeded();
  await stage.hover({ position: { x: 420, y: 100 } });
  await expect(cuCard.locator(".co-cu-tooltip")).toBeVisible();
  await expect(cuCard.locator(".co-cu-tooltip")).toContainText("本时段消耗");
  const overflowOnHover = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflowOnHover).toBe(0);
  await cuCard.locator(".co-cu-title").hover();

  // Switch metrics: 消耗量 -> 消耗速率 -> 余额走势 -> 消耗量
  const metricSeg = cuCard.locator('.ant-segmented[aria-label="切换统计指标"]');
  await segmentedItem(metricSeg, "消耗速率").click();
  await expect(cuCard.locator(".co-cu-rate-stroke")).toBeVisible();
  await segmentedItem(metricSeg, "余额走势").click();
  await expect(cuCard.locator(".co-cu-balance-stroke").first()).toBeVisible();
  await segmentedItem(metricSeg, "消耗量").click();
  await expect(cuCard.locator('.co-cu-bar:not([height="0"])').first()).toBeVisible();
  await expect(cuCard.locator(".co-cu-trend-stroke")).toBeVisible();

  // Open date picker popover, pick yesterday's date, then jump back to current time
  await cuCard.getByTestId("cu-date-trigger").click();
  const cal = page.locator(".co-cu-calendar");
  await expect(cal).toBeVisible();
  const yesterday = await page.evaluate(() => {
    const d = new Date();
    d.setDate(d.getDate() - 1);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  });
  const yBtn = cal.locator(`button[data-date="${yesterday}"]`);
  if ((await yBtn.count()) > 0) {
    await yBtn.click();
  } else {
    // Yesterday was in previous month
    await cal.locator('button[aria-label="上个月"]').click();
    await cal.locator(`button[data-date="${yesterday}"]`).click();
  }
  await expect(cuCard.locator(".co-cu-window-tag")).toContainText(yesterday);
  await expect(cuCard.getByTestId("cu-now-btn")).toContainText("回到当前");

  // Jump back to live current time (last 24h) and ensure only a single billing.usage RPC is sent (no duplicate fetch)
  const callsBeforeJump = await page.evaluate(
    () => (window as any).__billingUsageCalls.length,
  );
  await cuCard.getByTestId("cu-now-btn").click();
  await expect(cuCard.locator(".co-cu-window-tag")).toContainText("近 24 小时");
  await expect(cuCard.getByTestId("cu-now-btn")).toContainText("当前时间");
  const { callsAfterJump, lastPayload } = await page.evaluate(() => {
    const arr = (window as any).__billingUsageCalls as any[];
    return { callsAfterJump: arr.length, lastPayload: arr[arr.length - 1] };
  });
  expect(callsAfterJump - callsBeforeJump).toBe(1);
  expect(typeof lastPayload?.timezone).toBe("string");
  expect((lastPayload?.timezone || "").length).toBeGreaterThan(0);

  // Switch to Last 7d and verify 7 bars render cleanly
  const rangeSeg = cuCard.locator('.ant-segmented[aria-label="切换时间范围"]');
  await segmentedItem(rangeSeg, "近 7 天").click();
  await expect(cuCard.locator(".co-cu-bar")).toHaveCount(7);

  // Clicking today's bar drills down into that calendar day's hourly breakdown
  const stageBox7d = (await stage.boundingBox())!;
  await stage.click({
    position: {
      x: Math.max(80, Math.round(stageBox7d.width - 36)),
      y: Math.round(stageBox7d.height * 0.5),
    },
  });
  await expect(cuCard.locator(".co-cu-window-tag")).toContainText("全天");
  expect(await cuCard.locator(".co-cu-bar").count()).toBeGreaterThanOrEqual(23);

  // Return to live 24h and verify dark mode + narrow-viewport tooltip containment + zero overflow
  await cuCard.getByTestId("cu-now-btn").click();
  await page.getByRole("button", { name: /切换到深色模式|切换到浅色模式/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const overflowDark = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflowDark).toBe(0);

  await page.setViewportSize({ width: 420, height: 820 });
  await stage.scrollIntoViewIfNeeded();
  const narrowBox = (await stage.boundingBox())!;
  await stage.hover({
    position: {
      x: Math.round(narrowBox.width * 0.52),
      y: Math.round(narrowBox.height * 0.5),
    },
  });
  const tipBox = (await cuCard.locator(".co-cu-tooltip").boundingBox())!;
  expect(tipBox.x).toBeGreaterThanOrEqual(narrowBox.x - 1);
  expect(tipBox.x + tipBox.width).toBeLessThanOrEqual(
    narrowBox.x + narrowBox.width + 1,
  );
});

test("default GPU selection is G4 across devices, settings, top bar, and model drawer", async ({
  page,
}) => {
  await mock(page, false);

  await expect(page.locator(".co-connection-gpu")).toContainText("预选 G4");
  await expect(antSelect(page, "GPU 型号")).toContainText(
    "G4 · 96 GB 显存（RTX PRO 6000）",
  );

  await navItem(page, "设置").click();
  await expect(antSelect(page, "默认 GPU 型号")).toContainText(
    "G4 · 96 GB 显存（RTX PRO 6000）",
  );

  const drawer = await openApiModelDrawer(page);
  await expect(antSelect(drawer, "按 GPU 显存筛选")).toContainText(
    "G4 · 96 GB",
  );
});

test("auto-update surfaces navbar badge, renders bilingual release notes safely, downloads, and respects active GPU/operation guards", async ({
  page,
}) => {
  const dialogs: string[] = [];
  page.on("dialog", async (dialog) => {
    dialogs.push(dialog.message());
    await dialog.dismiss();
  });

  const notesZh = [
    "### 本次更新",
    "- 新增应用内增量自动更新与签名校验",
    "- 安全测试：<script>alert(1)</script>",
  ].join("\n");
  const notesEn = [
    "### What's New",
    "- Added signed in-app delta auto-update support",
    "- Security test: <script>alert(1)</script>",
  ].join("\n");
  const notesRaw = `${notesZh}\n\n---\n\n${notesEn}`;

  await page.addInitScript(
    ({ notesRaw, notesZh, notesEn }) => {
      (window as any).__initialUpdateState = {
        status: "available",
        current_version: "0.1.6",
        available_version: "0.1.7",
        is_prerelease: false,
        release_date: "2026-11-24T00:00:00Z",
        release_url:
          "https://github.com/luolongfei/ColabOne/releases/tag/v0.1.7",
        notes_raw: notesRaw,
        notes_zh: notesZh,
        notes_en: notesEn,
        can_self_update: true,
        disabled_reason: "",
        auto_check: true,
        include_prerelease: false,
        last_checked_at: Math.floor(Date.now() / 1000) - 120,
      };
    },
    { notesRaw, notesZh, notesEn },
  );

  await mock(page, true);

  const versionBtn = page.getByTestId("nav-version-btn");
  const versionDot = page.getByTestId("nav-update-dot");
  await expect(versionBtn).toBeVisible();
  await expect(versionDot).toBeVisible();
  await expect(page.getByTestId("nav-update-pill")).toHaveCount(0);

  await versionBtn.click();
  // Clicking the version badge immediately clears the red dot regardless of whether the user updates.
  await expect(versionDot).toHaveCount(0);
  const modal = page.getByTestId("update-modal");
  await expect(modal).toBeVisible();
  // Anchored compact popover under the version badge (no full-page dimmed modal backdrop).
  await expect(page.locator(".ant-modal-mask")).toHaveCount(0);
  const [btnBox, popBox] = await Promise.all([
    versionBtn.boundingBox(),
    modal.boundingBox(),
  ]);
  expect(btnBox).not.toBeNull();
  expect(popBox).not.toBeNull();
  if (btnBox && popBox) {
    expect(popBox.width).toBeLessThanOrEqual(420);
    expect(popBox.y).toBeGreaterThanOrEqual(btnBox.y + btnBox.height - 2);
    expect(popBox.y - (btnBox.y + btnBox.height)).toBeLessThan(28);
    expect(Math.abs(popBox.x - btnBox.x)).toBeLessThan(48);
  }
  await expect(modal).toContainText("v0.1.6 → v0.1.7");

  // Chinese notes render by default; attempted script tag renders as literal text with zero XSS.
  const notesBody = modal.getByTestId("update-notes-body");
  await expect(notesBody).toContainText("新增应用内增量自动更新与签名校验");
  await expect(notesBody).toContainText("<script>alert(1)</script>");
  await expect(notesBody).not.toContainText(
    "Added signed in-app delta auto-update support",
  );
  await expect(modal.locator("script")).toHaveCount(0);
  expect(dialogs).toEqual([]);

  // Toggling full bilingual notes displays both Chinese and English sections safely.
  await modal.getByRole("button", { name: "查看中英完整原文" }).click();
  await expect(notesBody).toContainText("新增应用内增量自动更新与签名校验");
  await expect(notesBody).toContainText(
    "Added signed in-app delta auto-update support",
  );
  await expect(modal.locator("script")).toHaveCount(0);
  expect(dialogs).toEqual([]);

  // Active GPU notice reassures the user that restarting the desktop client keeps the remote GPU alive.
  await expect(modal).toContainText("不会停止正在运行的远端 GPU");

  // Dismiss modal without updating: red dot must stay removed and seen version persisted.
  await modal.getByRole("button", { name: "稍后提醒" }).click();
  await expect(modal).toBeHidden();
  await expect(versionDot).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(() =>
        window.localStorage.getItem("colabone.update.seenVersion"),
      ),
    )
    .toBe("0.1.7");

  await navItem(page, "设置").click();
  const updateRow = page.getByTestId("settings-update-row");
  await updateRow.scrollIntoViewIfNeeded();
  await expect(updateRow).toBeVisible();
  await expect(updateRow).toContainText("有新版本 v0.1.7");

  const autoCheckSwitch = updateRow.getByTestId(
    "settings-update-auto-check-switch",
  );
  const prereleaseSwitch = updateRow.getByTestId(
    "settings-update-prerelease-switch",
  );
  await expect(autoCheckSwitch).toHaveAttribute("aria-checked", "true");
  await expect(prereleaseSwitch).toHaveAttribute("aria-checked", "false");

  await autoCheckSwitch.click();
  await expect(autoCheckSwitch).toHaveAttribute("aria-checked", "false");
  await expect(prereleaseSwitch).toBeEnabled();
  await prereleaseSwitch.click();
  await expect(prereleaseSwitch).toHaveAttribute("aria-checked", "true");

  const prefCalls = await page.evaluate(() =>
    (window as any).__updateCalls.filter((c: any) => c.method === "prefs"),
  );
  expect(prefCalls).toEqual([
    { method: "prefs", autoCheck: false, includePrerelease: false },
    { method: "prefs", autoCheck: false, includePrerelease: true },
  ]);

  // Clicking the Settings action button opens the anchored popover, and Escape dismisses it cleanly.
  await updateRow.getByTestId("settings-update-now-btn").click();
  await expect(modal).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(modal).toBeHidden();

  // Reopen update popover from version badge, install update, verify ready_to_restart + active operation guard + relaunch.
  await versionBtn.click();
  await expect(modal).toBeVisible();

  const installBtn = page.getByTestId("update-install-btn");
  await expect(installBtn).toBeVisible();
  await installBtn.click();

  const relaunchBtn = page.getByTestId("update-relaunch-btn");
  await expect(relaunchBtn).toBeVisible();
  await expect(relaunchBtn).toBeEnabled();
  await expect(modal).toContainText("更新已就绪");
  await expect(versionBtn).toContainText("重启更新");

  // Active local operation disables relaunch and warns the user until it finishes.
  await page.evaluate(() => {
    (window as any).__state.operation = {
      kind: "runtime_start",
      title: "部署远端管理服务",
      running: true,
    };
  });
  await expect(modal).toContainText("当前有正在执行的本地操作");
  await expect(relaunchBtn).toBeDisabled();

  // Clearing the active operation re-enables relaunch and invokes RelaunchForUpdate.
  await page.evaluate(() => {
    (window as any).__state.operation = null;
  });
  await expect(relaunchBtn).toBeEnabled();
  await relaunchBtn.click();

  await expect
    .poll(() =>
      page.evaluate(() =>
        (window as any).__updateCalls.map((c: any) => c.method),
      ),
    )
    .toContain("relaunch");

  const updateCalls = await page.evaluate(() => (window as any).__updateCalls);
  expect(updateCalls).toContainEqual({ method: "install" });
  expect(updateCalls).toContainEqual({ method: "relaunch" });
});

test("auto-update read-only fallback opens GitHub Releases and skip version suppresses badge", async ({
  page,
}) => {
  const releaseUrl =
    "https://github.com/luolongfei/ColabOne/releases/tag/v0.1.7";

  await page.addInitScript(
    ({ releaseUrl }) => {
      (window as any).__initialUpdateState = {
        status: "available",
        current_version: "0.1.6",
        available_version: "0.1.7",
        is_prerelease: false,
        release_date: "2026-11-24T00:00:00Z",
        release_url: releaseUrl,
        notes_raw: "### 更新\n- 修复若干已知问题",
        notes_zh: "### 更新\n- 修复若干已知问题",
        notes_en: "### Changes\n- Fixed several issues",
        can_self_update: false,
        disabled_reason: "read_only_install",
        auto_check: true,
        include_prerelease: false,
        last_checked_at: Math.floor(Date.now() / 1000) - 60,
      };
    },
    { releaseUrl },
  );

  await mock(page, false);

  const versionBtn = page.getByTestId("nav-version-btn");
  const versionDot = page.getByTestId("nav-update-dot");
  await expect(versionBtn).toBeVisible();
  await expect(versionDot).toBeVisible();

  await versionBtn.click();
  await expect(versionDot).toHaveCount(0);
  const modal = page.getByTestId("update-modal");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("当前安装目录缺少写入权限");
  await expect(page.getByTestId("update-install-btn")).toHaveCount(0);

  const manualBtn = page.getByTestId("update-manual-btn");
  await expect(manualBtn).toBeVisible();
  await manualBtn.click();

  await expect
    .poll(() => page.evaluate(() => (window as any).__openedUrls))
    .toEqual([releaseUrl]);

  const skipBtn = page.getByTestId("update-skip-btn");
  await expect(skipBtn).toBeVisible();
  await skipBtn.click();

  await expect
    .poll(() =>
      page.evaluate(() =>
        (window as any).__updateCalls.filter((c: any) => c.method === "skip"),
      ),
    )
    .toEqual([{ method: "skip", version: "0.1.7" }]);

  await expect(modal).toBeHidden();
  await expect(versionDot).toHaveCount(0);
  await expect(page.getByTestId("nav-update-pill")).toHaveCount(0);
  await expect(versionBtn).not.toContainText("重启更新");

  // A subsequent newer release brings the unread red dot back until clicked again.
  await page.evaluate(() => {
    (window as any).__emitMyGoEvent("colabone-update", {
      status: "available",
      current_version: "0.1.6",
      available_version: "0.1.8",
      can_self_update: true,
      auto_check: true,
      include_prerelease: false,
    });
  });
  await expect(versionDot).toBeVisible();
  await versionBtn.click();
  await expect(versionDot).toHaveCount(0);
});

test("close window prompt defaults to quit, supports remember choice, and syncs with Settings", async ({
  page,
}) => {
  await mock(page, true);
  await expect(navItem(page, "设置")).toBeVisible();

  // Trigger native close-window request event (as sent by taskbar "Close window" or titlebar X).
  await page.evaluate(() => {
    (window as any).__emitMyGoEvent("colabone-close-request", {
      default_choice: "quit",
      can_background: true,
    });
  });

  const modal = page.getByTestId("close-modal");
  await expect(modal).toBeVisible();
  const quitOption = modal.getByTestId("close-option-quit");
  const bgOption = modal.getByTestId("close-option-background");
  const confirmBtn = modal.getByTestId("close-confirm-btn");
  const cancelBtn = modal.getByTestId("close-cancel-btn");
  const rememberBox = modal.getByTestId("close-remember-checkbox");

  // Quit is selected by default with clear "默认" badge and matching primary action label.
  await expect(quitOption).toHaveAttribute("aria-checked", "true");
  await expect(bgOption).toHaveAttribute("aria-checked", "false");
  await expect(quitOption).toContainText("默认");
  await expect(confirmBtn).toContainText("直接退出");

  // Active local operation shows warning only while Quit is selected.
  await page.evaluate(() => {
    (window as any).__state.operation = {
      kind: "runtime_start",
      title: "部署远端管理服务",
      running: true,
    };
  });
  await expect(modal).toContainText("直接退出会中断本地进度");
  await bgOption.click();
  await expect(bgOption).toHaveAttribute("aria-checked", "true");
  await expect(confirmBtn).toContainText("后台运行");
  await expect(modal).not.toContainText("直接退出会中断本地进度");

  // Cancel closes modal without dispatching ResolveCloseRequest.
  await cancelBtn.click();
  await expect(modal).toBeHidden();
  expect(
    await page.evaluate(() => (window as any).__closeBehaviorCalls.length),
  ).toBe(0);

  // Re-trigger close prompt: resets back to default Quit + unchecked Remember.
  await page.evaluate(() => {
    (window as any).__state.operation = null;
    (window as any).__emitMyGoEvent("colabone-close-request", {
      default_choice: "quit",
      can_background: true,
    });
  });
  await expect(modal).toBeVisible();
  await expect(quitOption).toHaveAttribute("aria-checked", "true");
  await expect(rememberBox).not.toBeChecked();

  // Select Background + check "记住我的选择" and confirm.
  await bgOption.click();
  await rememberBox.check();
  await confirmBtn.click();
  await expect(modal).toBeHidden();

  await expect
    .poll(() => page.evaluate(() => (window as any).__closeBehaviorCalls))
    .toEqual([{ method: "resolve", action: "background", remember: true }]);

  // Open Settings → Desktop & About: close behavior segmented control reflects remembered "后台运行".
  await navItem(page, "设置").click();
  const row = page.getByTestId("settings-close-behavior-row");
  await row.scrollIntoViewIfNeeded();
  await expect(row).toBeVisible();
  const seg = row.getByTestId("settings-close-behavior-segmented");
  await expect(seg.locator(".ant-segmented-item-selected")).toContainText(
    "后台运行",
  );

  // User can change close behavior anytime in Settings (to Quit and back to Ask).
  await segmentedItem(seg, "直接退出").click();
  await expect(seg.locator(".ant-segmented-item-selected")).toContainText(
    "直接退出",
  );
  await segmentedItem(seg, "每次询问").click();
  await expect(seg.locator(".ant-segmented-item-selected")).toContainText(
    "每次询问",
  );

  const setCalls = await page.evaluate(() =>
    (window as any).__closeBehaviorCalls.filter((c: any) => c.method === "set"),
  );
  expect(setCalls).toEqual([
    { method: "set", behavior: "quit" },
    { method: "set", behavior: "ask" },
  ]);

  // Confirm default Quit directly from modal without remembering (and test keyboard navigation + Escape guard + dark mode).
  await page.evaluate(() => {
    (window as any).__emitMyGoEvent("colabone-close-request", {
      default_choice: "quit",
      can_background: true,
    });
  });
  await expect(modal).toBeVisible();
  await expect(quitOption).toHaveAttribute("aria-checked", "true");
  await expect(quitOption).toHaveAttribute("tabindex", "0");
  await expect(bgOption).toHaveAttribute("tabindex", "-1");

  // Pressing Alt+1 while the close modal is open must NOT switch pages underneath.
  await page.keyboard.press("Alt+1");
  await expect(page.getByTestId("settings-close-behavior-row")).toBeVisible();

  // Roving-tabindex keyboard navigation moves both aria-checked and real DOM focus.
  await quitOption.focus();
  await expect(quitOption).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(bgOption).toHaveAttribute("aria-checked", "true");
  await expect(bgOption).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(quitOption).toHaveAttribute("aria-checked", "true");
  await expect(quitOption).toBeFocused();

  // Dark mode + zero horizontal overflow with modal open.
  const overflowModal = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflowModal).toBe(0);

  // Pressing Enter on the focused Quit radio option confirms immediately.
  await page.keyboard.press("Enter");
  await expect(modal).toBeHidden();

  await expect
    .poll(() =>
      page.evaluate(() =>
        (window as any).__closeBehaviorCalls.filter(
          (c: any) => c.method === "resolve",
        ),
      ),
    )
    .toEqual([
      { method: "resolve", action: "background", remember: true },
      { method: "resolve", action: "quit", remember: false },
    ]);
});

test("privacy mask blurs personal info across Devices, API, ComfyUI, Settings, and Logs while keeping CU balance and real clipboard copy", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const st = (window as any).__state;
    st.account.email = "alex.chen@gmail.com";
    st.account.balance = 1975.28;
    st.api_settings = {
      mode: "named",
      domain: "https://colabone.acme.dev",
      has_token: true,
      has_key: true,
    };
    st.runtime.public_api = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://colabone.acme.dev",
      error: "",
      streaming: true,
      checked_at: Date.now() / 1000,
      requests: 3,
      active: 0,
      completed: 3,
      failed: 0,
      last_request: Date.now() / 1000,
    };
    st.comfy_canvas_settings = {
      mode: "named",
      domain: "comfy.acme.dev",
      has_token: true,
    };
    st.runtime.comfyui.running = true;
    st.runtime.comfyui.state = "ready";
    st.runtime.comfyui.canvas = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://comfy.acme.dev/?colabone_auth=secret",
      error: "",
      checked_at: Date.now() / 1000,
      active_connections: 1,
    };
    st.assign_proxy_settings = {
      enabled: true,
      has_proxy: true,
      masked_url: "socks5://***@10.24.0.8:1080",
      has_auth: true,
    };
    st.settings.assign_proxy_enabled = true;
    st.logs = [
      {
        id: 101,
        time: Date.now() / 1000 - 30,
        source: "登录",
        level: "info",
        message: "请在浏览器中使用 alex.chen@gmail.com 勾选全部云盘权限并点击允许（仅本实例确认一次）",
      },
      {
        id: 102,
        time: Date.now() / 1000 - 20,
        source: "云盘",
        level: "info",
        message: "已确认免弹窗云盘配额项目 (colabone-proj-8891)，后续所有实例免弹窗自动挂载",
      },
      {
        id: 103,
        time: Date.now() / 1000 - 10,
        source: "设置",
        level: "info",
        message: "已保存分配代理（socks5://***@10.24.0.8:1080）",
      },
      {
        id: 104,
        time: Date.now() / 1000 - 5,
        source: "API",
        level: "info",
        message: "对外 API 已就绪：https://colabone.acme.dev/v1",
      },
    ];
  });

  // Wait for updated snapshot to render on Devices page (initially unmasked).
  const billingSub = page.getByTestId("billing-account-subtitle");
  await expect(billingSub).toContainText("alex.chen@gmail.com");
  await expect(page.locator(".co-billing-card")).toContainText("1975.28");

  const privacyBtn = page.getByTestId("nav-privacy-toggle");
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "false");

  // 1. Enable privacy mask via topbar quick toggle.
  await privacyBtn.click();
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "true");
  await expect(privacyBtn).toHaveClass(/is-active/);

  // Devices page: email is frosted-masked with zero real characters in DOM text or title, while CU balance stays visible.
  const billingMask = billingSub.locator('[data-privacy-masked="true"]');
  await expect(billingMask).toBeVisible();
  await expect(billingSub).not.toContainText("alex.chen");
  await expect(billingSub).not.toContainText("gmail.com");
  expect(await billingSub.getAttribute("title")).toBeNull();
  await expect(page.locator(".co-billing-card")).toContainText("1975.28");

  // Hovering the mosaic automatically reveals the original email, preserves min-width (no shrink flicker),
  // collapses on window blur, and re-masks when the pointer moves away.
  const preHoverBox = (await billingMask.boundingBox())!;
  await billingMask.hover();
  await expect(billingMask).toHaveAttribute("data-privacy-peeking", "true");
  await expect(billingSub).toContainText("alex.chen@gmail.com");
  const peekingBox = (await billingMask.boundingBox())!;
  expect(peekingBox.width).toBeGreaterThanOrEqual(Math.floor(preHoverBox.width));
  await page.evaluate(() => window.dispatchEvent(new Event("blur")));
  await expect(billingMask).not.toHaveAttribute("data-privacy-peeking", "true");
  await expect(billingSub).not.toContainText("alex.chen");
  await page.mouse.move(10, 10);
  await billingMask.hover();
  await expect(billingSub).toContainText("alex.chen@gmail.com");
  await page.mouse.move(10, 10);
  await expect(billingMask).not.toHaveAttribute("data-privacy-peeking", "true");
  await expect(billingSub).not.toContainText("alex.chen");

  // 2. Public API page: domain input is masked & readonly, Base URL & code block blur ONLY the personal domain,
  //    hovering each mosaic peeks the original value, and Copy Base URL / Copy Code always copy real values.
  await navItem(page, "对外 API").click();
  const domainInput = page.locator("#api-domain");
  await expect(domainInput).toHaveAttribute("readonly", "");
  const maskedDomainVal = await domainInput.inputValue();
  expect(maskedDomainVal.startsWith("https://")).toBe(true);
  expect(maskedDomainVal).not.toContain("colabone.acme.dev");

  // Hovering the API domain input host reveals the real domain even while the input is disabled/readonly.
  await domainInput.locator("..").hover();
  await expect(domainInput).toHaveValue("https://colabone.acme.dev");
  await page.mouse.move(10, 10);
  expect(await domainInput.inputValue()).not.toContain("colabone.acme.dev");

  const baseUrlEl = page.getByTestId("api-base-url");
  const baseUrlMask = baseUrlEl.locator('[data-privacy-masked="true"]');
  await expect(baseUrlMask).toBeVisible();
  await expect(baseUrlEl).toContainText("https://");
  await expect(baseUrlEl).toContainText("/v1");
  await expect(baseUrlEl).not.toContainText("colabone.acme.dev");
  await baseUrlMask.hover();
  await expect(baseUrlEl).toContainText("https://colabone.acme.dev/v1");
  await page.mouse.move(10, 10);
  await expect(baseUrlEl).not.toContainText("colabone.acme.dev");

  const codeBlock = page.locator(".co-api-code-block");
  const codeMask = codeBlock.locator('[data-privacy-masked="true"]');
  await expect(codeMask).toBeVisible();
  await expect(codeBlock).toContainText("https://");
  await expect(codeBlock).toContainText("/v1/chat/completions");
  await expect(codeBlock).not.toContainText("colabone.acme.dev");
  await codeMask.hover();
  await expect(codeMask).toHaveAttribute("data-privacy-peeking", "true");
  await expect(codeBlock).toContainText("https://colabone.acme.dev/v1/chat/completions");
  // Telemetry poll updates with unchanged domains must NOT drop an active hover peek.
  await page.evaluate(() => {
    (window as any).__state.runtime.gpu.used_mib += 64;
  });
  await page.waitForTimeout(1100);
  await expect(codeMask).toHaveAttribute("data-privacy-peeking", "true");
  await expect(codeBlock).toContainText("https://colabone.acme.dev/v1/chat/completions");
  await page.mouse.move(10, 10);
  await expect(codeBlock).not.toContainText("colabone.acme.dev");

  // Auto-repeated Ctrl+Shift+M keydown events must be ignored (no rapid flicker).
  await page.evaluate(() => {
    window.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "M",
        code: "KeyM",
        ctrlKey: true,
        shiftKey: true,
        repeat: true,
        bubbles: true,
      }),
    );
  });
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "true");

  // Copy Base URL & Copy Code still write the real URL to clipboard.
  await page.getByRole("button", { name: "复制 API 地址" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__clipboard))
    .toBe("https://colabone.acme.dev/v1");

  await page.getByRole("button", { name: "复制代码" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__clipboard))
    .toContain("https://colabone.acme.dev/v1/chat/completions");

  // Temporarily turn mask OFF via Ctrl+Shift+M, reveal API key, then turn mask back ON -> API key auto-collapses.
  await page.keyboard.press("Control+Shift+M");
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "false");
  await expect(domainInput).toHaveValue("https://colabone.acme.dev");
  await page.getByRole("button", { name: "显示 API 密钥" }).click();
  await expect(page.locator(".co-api-access")).toContainText("sk-test-privacy-key-999");
  await privacyBtn.click();
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".co-api-access")).not.toContainText("sk-test-privacy-key-999");

  // 3. ComfyUI Canvas tab: canvas domain input and public host are masked.
  await navItem(page, "ComfyUI").click();
  await page.getByRole("tab", { name: /原生画布/ }).click();
  const canvasDomainInput = page.locator("#comfy-canvas-domain");
  await expect(canvasDomainInput).toHaveAttribute("readonly", "");
  expect(await canvasDomainInput.inputValue()).not.toContain("comfy.acme.dev");
  const canvasHostEl = page.getByTestId("comfy-canvas-public-host");
  const canvasHostMask = canvasHostEl.locator('[data-privacy-masked="true"]');
  await expect(canvasHostMask).toBeVisible();
  await expect(canvasHostEl).not.toContainText("comfy.acme.dev");
  await canvasHostMask.hover();
  await expect(canvasHostEl).toContainText("comfy.acme.dev");
  await page.mouse.move(10, 10);
  await expect(canvasHostEl).not.toContainText("comfy.acme.dev");

  // 4. Logs drawer: emails, quota project IDs, proxy IPs, and custom domains are masked visually,
  //    hovering a mosaic peeks its original value, and Copy Logs copies the real text.
  await page.locator(".co-logs-trigger").click();
  const logFeed = page.locator(".co-log-feed");
  await expect(logFeed).toBeVisible();
  await expect(logFeed).not.toContainText("alex.chen@gmail.com");
  await expect(logFeed).not.toContainText("colabone-proj-8891");
  await expect(logFeed).not.toContainText("10.24.0.8");
  await expect(logFeed).not.toContainText("colabone.acme.dev");
  expect(await logFeed.locator('[data-privacy-masked="true"]').count()).toBeGreaterThanOrEqual(4);
  const firstLogMask = logFeed.locator('[data-privacy-masked="true"]').first();
  await firstLogMask.hover();
  await expect(logFeed).toContainText("alex.chen@gmail.com");
  await page.mouse.move(10, 10);
  await expect(logFeed).not.toContainText("alex.chen@gmail.com");

  await page.getByRole("button", { name: "复制日志" }).click();
  await expect
    .poll(() => page.evaluate(() => (window as any).__clipboard))
    .toContain("alex.chen@gmail.com");
  await page.locator(".co-logs-trigger").click();

  // 5. Settings page: email masked, expanded proxy hides masked_url IP, and startup default is decoupled from current session.
  await navItem(page, "设置").click();
  const googleRow = page.locator(".co-settings-row").first();
  await expect(googleRow.locator('[data-privacy-masked="true"]')).toBeVisible();
  await expect(googleRow).not.toContainText("alex.chen@gmail.com");

  await page.getByRole("button", { name: /^编\s*辑$/ }).click();
  const proxyInput = page.locator("#settings-assign-proxy");
  await expect(proxyInput).toBeVisible();
  expect(await proxyInput.getAttribute("placeholder")).not.toContain("10.24.0.8");

  const defaultRow = page.getByTestId("settings-privacy-default-row");
  await defaultRow.scrollIntoViewIfNeeded();
  await expect(defaultRow.getByTestId("settings-privacy-active-tag")).toBeVisible();
  const defaultSwitch = page.getByTestId("settings-privacy-default-switch");
  await expect(defaultSwitch).toHaveAttribute("aria-checked", "false");

  // Turn OFF active session first, then turn ON startup default -> active session must remain OFF (decoupled).
  await privacyBtn.click();
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "false");
  await defaultSwitch.click();
  await expect(defaultSwitch).toHaveAttribute("aria-checked", "true");
  await expect(privacyBtn).toHaveAttribute("aria-pressed", "false");
  expect(
    await page.evaluate(() => window.localStorage.getItem("colabone.privacy.default")),
  ).toBe("1");

  // Reload (simulating app relaunch): boots into masked state automatically because startup default is ON.
  await page.reload();
  await expect(page.getByTestId("nav-privacy-toggle")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  // Toggling session OFF does NOT flip the startup default setting.
  await page.getByTestId("nav-privacy-toggle").click();
  await expect(page.getByTestId("nav-privacy-toggle")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  expect(
    await page.evaluate(() => window.localStorage.getItem("colabone.privacy.default")),
  ).toBe("1");
});

test("settings page customizes GPU allocation max attempts with 20 default and 1-100 clamping", async ({
  page,
}) => {
  await mock(page, true);
  await navItem(page, "设置").click();

  const attemptsInput = page.locator("#settings-assign-max-attempts");
  await expect(attemptsInput).toBeVisible();
  await expect(attemptsInput).toHaveValue("20");

  // Enter custom value (32) and commit with Enter -> triggers settings.save immediately
  await attemptsInput.click();
  await attemptsInput.fill("32");
  await attemptsInput.press("Enter");
  await expect
    .poll(async () => {
      const calls = await page.evaluate(() => (window as any).__calls);
      return calls
        .filter((c: any) => c.action === "settings.save")
        .map((c: any) => c.payload?.assign_max_attempts);
    })
    .toContain(32);
  await expect(attemptsInput).toHaveValue("32");

  // Click step-up button -> increments to 33 and persists immediately
  const stepUp = page.locator(".co-sr-attempts-input .ant-input-number-handler-up");
  await stepUp.click({ force: true });
  await expect
    .poll(async () => {
      const calls = await page.evaluate(() => (window as any).__calls);
      return calls
        .filter((c: any) => c.action === "settings.save")
        .map((c: any) => c.payload?.assign_max_attempts);
    })
    .toContain(33);
  await expect(attemptsInput).toHaveValue("33");

  // Out-of-range value clamps to 100 on blur
  await attemptsInput.click();
  await attemptsInput.fill("250");
  await attemptsInput.blur();
  await expect
    .poll(async () => {
      const calls = await page.evaluate(() => (window as any).__calls);
      return calls
        .filter((c: any) => c.action === "settings.save")
        .map((c: any) => c.payload?.assign_max_attempts);
    })
    .toContain(100);
  await expect(attemptsInput).toHaveValue("100");
});
