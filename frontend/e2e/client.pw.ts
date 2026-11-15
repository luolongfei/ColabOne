import { test, expect } from "@playwright/test";

async function mock(page: any, ready = false) {
  await page.addInitScript(
    ({ ready }: any) => {
      const state: any = {
        runtime: {
          state: ready ? "ready" : "stopped",
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
        account: { logged_in: ready, balance: 100, rate: ready ? 1.5 : 0 },
        operation: null,
        error: "",
        auth_url: "",
        setup: { colab: ready, drive: ready, hf: false },
        settings: { gpu: "T4", max_cu: 5, idle_minutes: 30, auto_drive: true },
      };
      const calls: any[] = [];
      (window as any).__state = state;
      (window as any).__calls = calls;
      (window as any).mygo = {
        call: async (method: string, request: any) => {
          if (method === "Desktop.Snapshot") return JSON.stringify(state);
          if (method === "Desktop.Command") {
            calls.push(request);
            const { action, payload } = request;
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
