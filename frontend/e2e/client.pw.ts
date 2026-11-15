import { test, expect } from "@playwright/test";

async function mock(page: any, ready = false) {
  await page.addInitScript(
    ({ ready }: any) => {
      const state: any = {
        runtime: {
          state: ready ? "ready" : "stopped",
          protocol: 2,
          gpu: ready
            ? { name: "NVIDIA T4", total_mib: 15360, used_mib: 700 }
            : undefined,
          id: ready ? "test-runtime" : undefined,
          disk_free: 90 * 1024 ** 3,
          tasks: [],
          sync: { pending: 0, error: "", connected: ready },
        },
        models: [
          {
            id: "qwen",
            name: "Qwen2.5 · 0.5B",
            description: "轻量验收模型 · Q4_K_M",
            size: 491000000,
            min_vram_mib: 2048,
            downloaded: false,
            loaded: false,
            files: [],
          },
        ],
        conversations: [],
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
        setup: { colab: ready, drive: ready, hf: false },
        settings: {
          gpu: "T4",
          max_cu: 5,
          idle_minutes: 3,
          idle_countdown_seconds: 60,
          download_threads: 8,
          auto_drive: true,
        },
      };
      const calls: any[] = [];
      (window as any).__state = state;
      (window as any).__calls = calls;
      (window as any).mygo = {
        call: async (method: string, request: any) => {
          if (method === "Desktop.Snapshot") return JSON.stringify(state);
          if (method === "Desktop.Command") {
            if (request.action === "runtime.activity") {
              (window as any).__activity =
                ((window as any).__activity || 0) + 1;
              return JSON.stringify({ ok: true });
            }
            calls.push(request);
            const { action, payload } = request;
            if (action === "runtime.release.preview")
              return JSON.stringify({
                id: "reviewed-plan",
                instances: [
                  { id: "test-runtime", managed: true },
                  { id: "other-runtime", managed: false },
                ],
              });
            if (action === "runtime.release.all") {
              state.runtime.state = "stopped";
              state.account.assignments = 0;
              state.account.rate = 0;
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
            if (action === "model.download") state.models[0].downloaded = true;
            if (action === "model.load") {
              state.models[0].loaded = true;
              state.runtime.loaded_model = "qwen";
            }
            if (action === "model.delete") state.models[0].downloaded = false;
            if (action === "chat.send") {
              state.conversations = [
                {
                  id: "conversation",
                  title: payload.text,
                  updated_at: 1,
                  messages: [
                    { role: "user", content: payload.text, status: "complete" },
                    {
                      role: "assistant",
                      content:
                        '你好，远端连接正常。\n\n```python\nprint("hello")\n```',
                      status: "complete",
                    },
                  ],
                },
              ];
              return JSON.stringify({
                conversation_id: "conversation",
                id: "task",
              });
            }
            return JSON.stringify({ ok: true });
          }
          return "";
        },
      };
    },
    { ready },
  );
  await page.goto("/");
}

test("disconnected client requires setup and does not allocate on navigation", async ({
  page,
}) => {
  await mock(page);
  await expect(
    page.getByRole("button", { name: "启动 GPU", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "模型库", exact: true }).click();
  await expect(page.getByRole("button", { name: "下载到远端" })).toBeDisabled();
  await page.getByRole("button", { name: "聊天", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "消息", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "云备份", exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);
});

test("remote download load chat and explicit deletion", async ({ page }) => {
  await mock(page, true);
  await page.getByRole("button", { name: "模型库", exact: true }).click();
  await page.getByRole("button", { name: "下载到远端" }).click();
  await page.getByRole("button", { name: "加载", exact: true }).click();
  await page.getByRole("button", { name: "聊天", exact: true }).click();
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("你好");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect(page.getByText("你好，远端连接正常。")).toBeVisible();
  await expect(page.locator(".markdown pre")).toContainText('print("hello")');
  expect(
    (await page.evaluate(() => (window as any).__calls)).map(
      (x: any) => x.action,
    ),
  ).toEqual(["model.download", "model.load", "chat.send"]);
  await page.screenshot({
    path: new URL("../../work/chat-ui.png", import.meta.url).pathname,
    fullPage: true,
  });
});

test("model deletion needs confirmation and markdown is sanitized", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    const s = (window as any).__state;
    s.models[0].downloaded = true;
    s.conversations = [
      {
        id: "x",
        title: "Safety",
        messages: [
          {
            role: "assistant",
            content: '<img src=x onerror="window.__injected=true">',
            status: "complete",
          },
        ],
      },
    ];
  });
  await page.getByRole("button", { name: "模型库", exact: true }).click();
  await page.getByRole("button", { name: "删除远端模型文件" }).click();
  expect(await page.evaluate(() => (window as any).__calls)).toEqual([]);
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page.getByRole("button", { name: "聊天", exact: true }).click();
  await page.getByRole("button", { name: "Safety" }).click();
  await expect(page.locator(".markdown img")).toHaveCount(1);
  expect(await page.evaluate(() => (window as any).__injected)).toBeUndefined();
});

test("compact desktop layout stays within viewport", async ({ page }) => {
  await page.setViewportSize({ width: 950, height: 700 });
  await mock(page, true);
  await expect(
    page.getByRole("heading", { name: "远端设备", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: new URL("../../work/device-ui.png", import.meta.url).pathname,
    fullPage: true,
  });
});

test("setup offers Google login and whole-page paste without API credentials", async ({
  page,
}) => {
  await mock(page);
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(page.getByText("OAuth Client ID")).toHaveCount(0);
  await expect(page.getByText("私有网络", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "登录 Google", exact: true }).click();
  expect((await page.evaluate(() => (window as any).__calls))[0].action).toBe(
    "auth.colab.auto",
  );
  await page.getByText("浏览器有一段回显？粘贴到这里", { exact: true }).click();
  await page
    .getByRole("textbox", { name: "Google 授权回显" })
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
    page.getByRole("textbox", { name: "Google 授权回显" }),
  ).toHaveValue("");
  await page.screenshot({
    path: new URL("../../work/setup-ui.png", import.meta.url).pathname,
    fullPage: true,
  });
});

test("Google login alone enables GPU startup and Drive can be connected later", async ({
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

test("billing is prominent and stop all reviews every instance before submission", async ({
  page,
}) => {
  await mock(page, true);
  await page.evaluate(() => {
    (window as any).__state.account.assignments = 2;
  });
  await expect(page.locator(".billing-active")).toContainText("正在计费");
  await expect(page.locator(".billing-active strong")).toHaveCSS(
    "color",
    "rgb(211, 47, 67)",
  );
  await page
    .getByRole("button", { name: "停止全部（2 个实例）", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("other-runtime");
  await expect(dialog).toContainText("其他 Colab 实例");
  expect(
    (await page.evaluate(() => (window as any).__calls)).map(
      (x: any) => x.action,
    ),
  ).toEqual(["runtime.release.preview"]);
  await dialog.getByRole("button", { name: "确认停止全部（2）" }).click();
  await expect(page.locator(".billing-pill")).toContainText(
    "0 个实例 · 未计费",
  );
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
  await page.getByRole("button", { name: "展开运行日志" }).click();
  const panel = page.getByRole("complementary", { name: "运行日志" });
  await expect(panel).toContainText("已恢复本机保存的 Google 登录");
  await page.evaluate(() => {
    const state = (window as any).__state;
    state.operation = {
      title: "已收到云盘授权，正在挂载到远端 GPU",
      running: true,
    };
    state.logs.push({
      id: 2,
      time: Date.now() / 1000,
      source: "云盘",
      level: "error",
      message: "连接中断，请重试",
    });
  });
  await expect(panel).toContainText("正在挂载到远端 GPU");
  await page.getByRole("button", { name: "模型库", exact: true }).click();
  await expect(panel).toBeVisible();
  await panel.getByLabel("日志级别").selectOption("error");
  await expect(panel).toContainText("连接中断，请重试");
  await expect(panel).not.toContainText("已恢复本机保存");
  await panel.getByRole("button", { name: "清空日志", exact: true }).click();
  await expect(panel).toContainText("暂无错误");
  await panel.getByRole("button", { name: "关闭日志侧栏" }).click();
  await expect(panel).toHaveCount(0);
  await page.reload();
  await expect(panel).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(panel).toBeVisible();
});

test("blue desktop layout shows billing and activity without horizontal overflow", async ({
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
        "已收到云盘授权，正在挂载到远端 GPU",
        "Google 云盘已连接，会话与模型清单已写入",
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
    page.getByRole("button", { name: "停止全部（1 个实例）", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(page.getByRole("log")).toContainText("Google 云盘已连接");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: new URL("../../work/blue-activity-ui.png", import.meta.url).pathname,
    fullPage: true,
  });
});

test("log drawer overlays without changing content width and closes with Escape", async ({
  page,
}) => {
  await mock(page, true);
  const content = page.locator(".workspace");
  const before = await content.boundingBox();
  await expect(page.locator("#activity-panel")).toHaveCount(0);
  await page.getByRole("button", { name: "展开运行日志" }).click();
  await expect(page.locator("#activity-panel")).toBeVisible();
  expect(await content.boundingBox()).toEqual(before);
  await page.keyboard.press("Escape");
  await expect(page.locator("#activity-panel")).toHaveCount(0);
});

test("connected settings reflects GPU state and has the agreed idle policy", async ({
  page,
}) => {
  await mock(page, true);
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "查看 GPU", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "去启动 GPU", exact: true }),
  ).toHaveCount(0);
  await expect(page.locator(".idle-policy")).toContainText(
    "闲置 3 分钟后，倒计时 60 秒",
  );
});

test("community search selects all shards and submits remote install with fixed revision", async ({
  page,
}) => {
  await mock(page, true);
  await page.getByRole("button", { name: "模型库", exact: true }).click();
  await page.getByLabel("下载并发线程").selectOption("32");
  await page.getByRole("tab", { name: "在线搜索", exact: true }).click();
  await page
    .getByRole("searchbox", { name: "搜索 Hugging Face 模型" })
    .fill("Qwen");
  await expect(
    page.getByRole("heading", { name: "org/Qwen-GGUF" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "选择版本" }).click();
  await expect(page.getByLabel("选择量化版本")).toContainText("2 个文件");
  await page.getByRole("button", { name: "添加并下载到远端" }).click();
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
  await expect(
    page.getByRole("tab", { name: "我的模型", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
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
  await expect(page.locator(".task-bar")).toContainText("128.0 MiB/s");
  await expect(page.locator(".task-bar")).toContainText("24/32 个连接");
  await expect(page.locator(".task-bar")).toContainText("预计剩余 0:24");
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
  await expect(page.locator(".idle-timer")).toContainText("自动释放 1:00");
  await expect(page.locator(".idle-timer")).toContainText("0:59", {
    timeout: 4000,
  });
  expect(await page.evaluate(() => (window as any).__activity || 0)).toBe(0);
  await page.locator(".idle-timer").click();
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
  await expect(page.locator(".idle-timer")).toContainText(
    "任务进行中 · 暂停释放",
  );
});
