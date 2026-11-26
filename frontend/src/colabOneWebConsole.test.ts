import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_WEB_AUTH_REQUIRED_EVENT,
  COLABONE_WEB_CLIENT_HEADER,
  COLABONE_WEB_CLIENT_VALUE,
  COLABONE_WEB_CONSOLE_DEFAULT_PORT,
  COLABONE_WEB_CONSOLE_MAX_PORT,
  COLABONE_WEB_CONSOLE_MIN_PORT,
  colabOneBuildCloudflaredQuickCommand,
  colabOneBuildCloudflaredServiceTarget,
  colabOneIsValidWebPort,
  colabOneNormalizePublicOriginInput,
  colabOneRuntimeCapabilities,
  colabOneWebBackendReady,
  colabOneWebCommandRaw,
  colabOneWebDownloadOutput,
  colabOneWebLogout,
  colabOneWebOpenCanvasURL,
  colabOneWebOpenDeferredTab,
  colabOneWebOpenExternalURL,
  colabOneWebSessionExpired,
  colabOneWebSnapshotRaw,
  createColabOneCapabilities,
  createColabOneDefaultWebConsoleState,
  fetchColabOneWebCapabilities,
  isColabOneDesktopRuntime,
  isColabOneWebRuntime,
  normalizeColabOneWebConsoleState,
  resetColabOneWebSnapshotCache,
  validateColabOneWebCanvasURL,
  validateColabOneWebExternalURL,
} from "./colabOneWebConsole";

const globalWithMyGo = globalThis as typeof globalThis & {
  mygo?: { callWith?: (...args: unknown[]) => unknown };
};

function makeResponse(
  body: string | Blob,
  init: { status?: number; headers?: Record<string, string> } = {},
): Response {
  const status = init.status ?? 200;
  const headers = new Headers(init.headers);
  if (status === 304) {
    return {
      ok: false,
      status: 304,
      headers,
      text: async () => "",
      json: async () => ({}),
      blob: async () => new Blob(),
    } as unknown as Response;
  }
  return new Response(body, { status, headers });
}

describe("colabOneWebConsole runtime detection and normalization", () => {
  const originalMyGo = globalWithMyGo.mygo;
  const originalWindow = globalThis.window;

  afterEach(() => {
    globalWithMyGo.mygo = originalMyGo;
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
    resetColabOneWebSnapshotCache();
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = false;
    vi.restoreAllMocks();
  });

  it("distinguishes desktop runtime from browser web runtime", () => {
    delete globalWithMyGo.mygo;
    Object.defineProperty(globalThis, "window", {
      value: { location: { protocol: "https:" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneDesktopRuntime()).toBe(false);
    expect(isColabOneWebRuntime()).toBe(true);

    Object.defineProperty(globalThis, "window", {
      value: { location: { protocol: "file:" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneWebRuntime()).toBe(false);

    globalWithMyGo.mygo = { callWith: vi.fn() };
    Object.defineProperty(globalThis, "window", {
      value: { location: { protocol: "http:" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneDesktopRuntime()).toBe(true);
    expect(isColabOneWebRuntime()).toBe(false);
  });

  it("creates default and normalized web console states safely", () => {
    expect(createColabOneDefaultWebConsoleState()).toEqual({
      enabled: false,
      running: false,
      bind_mode: "loopback",
      listen_addr: "127.0.0.1:3232",
      port: 3232,
      local_url: "http://127.0.0.1:3232",
      lan_urls: [],
      public_origin: "",
      has_passcode: false,
      passcode_preview: "",
      active_sessions: 0,
      last_error: "",
    });

    expect(normalizeColabOneWebConsoleState(null)).toEqual(
      createColabOneDefaultWebConsoleState(),
    );

    expect(
      normalizeColabOneWebConsoleState({
        enabled: true,
        running: true,
        bind_mode: "lan",
        listen_addr: " 0.0.0.0:4545 ",
        port: 4545,
        local_url: "http://127.0.0.1:4545",
        lan_urls: [
          " http://192.168.1.10:4545 ",
          "http://192.168.1.10:4545",
          "",
          42,
        ],
        public_origin: " https://console.example.com ",
        has_passcode: true,
        passcode_preview: "ab****yz",
        active_sessions: 2.9,
        last_error: "",
      }),
    ).toEqual({
      enabled: true,
      running: true,
      bind_mode: "lan",
      listen_addr: "0.0.0.0:4545",
      port: 4545,
      local_url: "http://127.0.0.1:4545",
      lan_urls: ["http://192.168.1.10:4545"],
      public_origin: "https://console.example.com",
      has_passcode: true,
      passcode_preview: "ab****yz",
      active_sessions: 2,
      last_error: "",
    });

    // Invalid bind_mode, port, and active_sessions fall back cleanly
    const fallback = normalizeColabOneWebConsoleState({
      bind_mode: "invalid",
      port: 80,
      active_sessions: -5,
    });
    expect(fallback.bind_mode).toBe("loopback");
    expect(fallback.port).toBe(COLABONE_WEB_CONSOLE_DEFAULT_PORT);
    expect(fallback.active_sessions).toBe(0);
  });

  it("builds desktop and web capability defaults accurately", () => {
    const desktop = createColabOneCapabilities("desktop");
    expect(desktop.runtime).toBe("desktop");
    expect(desktop.can_manage_web_console).toBe(true);
    expect(desktop.can_open_native_canvas_window).toBe(true);
    expect(desktop.can_reveal_host_secrets).toBe(true);
    expect(desktop.can_download_output).toBe(true);

    const web = createColabOneCapabilities("web");
    expect(web.runtime).toBe("web");
    expect(web.can_open_native_canvas_window).toBe(false);
    expect(web.can_standalone_log_window).toBe(false);
    expect(web.can_manage_web_console).toBe(false);
    expect(web.can_manage_close_behavior).toBe(false);
    expect(web.can_manage_shortcuts).toBe(false);
    expect(web.can_self_update).toBe(false);
    expect(web.can_migrate).toBe(false);
    expect(web.can_reveal_host_secrets).toBe(false);
    expect(web.can_loopback_oauth).toBe(false);
    expect(web.can_download_output).toBe(true);
  });
});

describe("colabOneWebConsole HTTP transport helpers", () => {
  const originalMyGo = globalWithMyGo.mygo;
  const originalWindow = globalThis.window;
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    delete globalWithMyGo.mygo;
    resetColabOneWebSnapshotCache();
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = false;
  });

  afterEach(() => {
    globalWithMyGo.mygo = originalMyGo;
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
    globalThis.fetch = originalFetch;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("fetches web capabilities, marks backend ready, and short-circuits in desktop mode", async () => {
    const fetchMock = vi.fn(async () =>
      makeResponse(
        JSON.stringify({
          runtime: "web",
          version: "0.1.20",
          can_download_output: true,
          can_manage_web_console: false,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const webCaps = await fetchColabOneWebCapabilities();
    expect(fetchMock).toHaveBeenCalledWith("/api/web/capabilities", {
      method: "GET",
      credentials: "same-origin",
      headers: { [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE },
    });
    expect(webCaps.runtime).toBe("web");
    expect(webCaps.version).toBe("0.1.20");
    expect(colabOneRuntimeCapabilities.value).toEqual(webCaps);
    expect(colabOneWebBackendReady.value).toBe(true);

    // Desktop runtime returns desktop capabilities immediately without calling fetch
    fetchMock.mockClear();
    globalWithMyGo.mygo = { callWith: vi.fn() };
    const desktopCaps = await fetchColabOneWebCapabilities();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(desktopCaps.runtime).toBe("desktop");
    expect(desktopCaps.can_manage_web_console).toBe(true);
    expect(colabOneWebBackendReady.value).toBe(false);
  });

  it("caches snapshot ETag, reuses cached JSON on 304, and resets cleanly", async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    let step = 0;
    globalThis.fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init });
      step += 1;
      if (step === 1) {
        return makeResponse('{"ok":true,"rev":1}', {
          status: 200,
          headers: { ETag: '"etag-1"' },
        });
      }
      if (step === 2) {
        return makeResponse("", { status: 304 });
      }
      return makeResponse('{"ok":true,"rev":2}', {
        status: 200,
        headers: { ETag: '"etag-2"' },
      });
    }) as unknown as typeof fetch;

    const first = await colabOneWebSnapshotRaw();
    expect(first).toBe('{"ok":true,"rev":1}');
    expect(colabOneWebBackendReady.value).toBe(true);
    expect((calls[0].init?.headers as Record<string, string>)["If-None-Match"]).toBeUndefined();

    const second = await colabOneWebSnapshotRaw();
    expect(second).toBe('{"ok":true,"rev":1}');
    expect((calls[1].init?.headers as Record<string, string>)["If-None-Match"]).toBe('"etag-1"');

    resetColabOneWebSnapshotCache();
    const third = await colabOneWebSnapshotRaw();
    expect(third).toBe('{"ok":true,"rev":2}');
    expect((calls[2].init?.headers as Record<string, string>)["If-None-Match"]).toBeUndefined();
  });

  it("sends POST /api/command, extracts JSON errors, and dispatches auth event on 401", async () => {
    const dispatchEvent = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: {
        location: { protocol: "https:" },
        dispatchEvent,
      },
      configurable: true,
      writable: true,
    });

    globalThis.fetch = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body || "{}"));
      if (body.action === "ok.action") {
        expect(url).toBe("/api/command");
        expect(init?.credentials).toBe("same-origin");
        expect((init?.headers as Record<string, string>)[COLABONE_WEB_CLIENT_HEADER]).toBe(
          COLABONE_WEB_CLIENT_VALUE,
        );
        return makeResponse('{"ok":true}', { status: 200 });
      }
      if (body.action === "err.action") {
        return makeResponse(JSON.stringify({ error: "host rejected action" }), {
          status: 403,
        });
      }
      return makeResponse(JSON.stringify({ error: "unauthorized" }), {
        status: 401,
      });
    }) as unknown as typeof fetch;

    const okRaw = await colabOneWebCommandRaw("ok.action", { a: 1 });
    expect(okRaw).toBe('{"ok":true}');

    await expect(colabOneWebCommandRaw("err.action", {})).rejects.toThrow(
      "host rejected action",
    );

    colabOneWebBackendReady.value = true;
    await expect(colabOneWebCommandRaw("expired.action", {})).rejects.toThrow(
      "Unauthorized",
    );
    expect(colabOneWebSessionExpired.value).toBe(true);
    expect(colabOneWebBackendReady.value).toBe(false);
    expect(dispatchEvent).toHaveBeenCalledTimes(1);
    expect(dispatchEvent.mock.calls[0][0]?.type).toBe(
      COLABONE_WEB_AUTH_REQUIRED_EVENT,
    );
  });

  it("logs out via POST /_colabone_web/logout, clears cache, and navigates to login", async () => {
    const assign = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: {
        location: { protocol: "https:", assign },
      },
      configurable: true,
      writable: true,
    });

    const fetchMock = vi.fn(async (url: string | URL | Request) => {
      if (url === "/api/snapshot") {
        return makeResponse('{"cached":true}', {
          status: 200,
          headers: { ETag: '"etag-x"' },
        });
      }
      return makeResponse("", { status: 200 });
    });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    await colabOneWebSnapshotRaw();
    colabOneWebBackendReady.value = true;

    await colabOneWebLogout();
    expect(fetchMock).toHaveBeenCalledWith("/_colabone_web/logout", {
      method: "POST",
      credentials: "same-origin",
      headers: { [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE },
    });
    expect(colabOneWebBackendReady.value).toBe(false);
    expect(assign).toHaveBeenCalledWith("/_colabone_web/login");
  });

  it("validates artifact IDs, preflights via HEAD, and triggers direct browser streaming download", async () => {
    const validId = "a1b2c3d4e5f60718293a4b5c6d7e8f90";

    await expect(colabOneWebDownloadOutput("not-valid")).rejects.toThrow();
    await expect(colabOneWebDownloadOutput("../etc/passwd")).rejects.toThrow();
    await expect(
      colabOneWebDownloadOutput(validId.toUpperCase()),
    ).rejects.toThrow();

    const click = vi.fn();
    const remove = vi.fn();
    const appendChild = vi.fn();
    const createdAnchor = {
      href: "",
      download: "",
      rel: "",
      style: { display: "" },
      click,
      remove,
    };
    const originalDoc = globalThis.document;
    Object.defineProperty(globalThis, "document", {
      value: {
        createElement: vi.fn(() => createdAnchor),
        body: { appendChild },
      },
      configurable: true,
      writable: true,
    });

    try {
      const headFetch = vi.fn(async () =>
        makeResponse("", {
          status: 200,
          headers: {
            "Content-Disposition":
              "attachment; filename=\"fallback.png\"; filename*=UTF-8''render%20final.mp4",
          },
        }),
      );
      globalThis.fetch = headFetch as unknown as typeof fetch;

      const filename = await colabOneWebDownloadOutput(validId);
      expect(headFetch).toHaveBeenCalledWith(`/api/outputs/${validId}/download`, {
        method: "HEAD",
        credentials: "same-origin",
        headers: { [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE },
      });
      expect(filename).toBe("render final.mp4");
      expect(createdAnchor.download).toBe("render final.mp4");
      expect(createdAnchor.href).toBe(`/api/outputs/${validId}/download`);
      expect(appendChild).toHaveBeenCalledWith(createdAnchor);
      expect(click).toHaveBeenCalledTimes(1);
      expect(remove).toHaveBeenCalledTimes(1);

      // Falls back to ColabOne-<prefix> when Content-Disposition is omitted
      globalThis.fetch = vi.fn(async () =>
        makeResponse("", { status: 200 }),
      ) as unknown as typeof fetch;
      const fallbackName = await colabOneWebDownloadOutput(validId);
      expect(fallbackName).toBe(`ColabOne-${validId.slice(0, 8)}`);

      // Decodes percent-encoded X-ColabOne-Error from HEAD error responses
      globalThis.fetch = vi.fn(async () =>
        makeResponse("", {
          status: 502,
          headers: {
            "X-ColabOne-Error": encodeURIComponent("输出文件元数据无效"),
          },
        }),
      ) as unknown as typeof fetch;
      await expect(colabOneWebDownloadOutput(validId)).rejects.toThrow(
        "输出文件元数据无效",
      );
    } finally {
      Object.defineProperty(globalThis, "document", {
        value: originalDoc,
        configurable: true,
        writable: true,
      });
    }
  });
});

describe("colabOneWebConsole URL validators and Cloudflared helpers", () => {
  const originalWindow = globalThis.window;

  afterEach(() => {
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
    vi.restoreAllMocks();
  });

  it("enforces the exact HTTPS external URL host allowlist", () => {
    expect(
      validateColabOneWebExternalURL("https://github.com/luolongfei/ColabOne"),
    ).toBe("https://github.com/luolongfei/ColabOne");
    expect(
      validateColabOneWebExternalURL("https://ACCOUNTS.GOOGLE.COM./"),
    ).toBe("https://accounts.google.com./");
    expect(
      validateColabOneWebExternalURL("https://one.dash.cloudflare.com/"),
    ).toBe("https://one.dash.cloudflare.com/");

    const invalidUrls = [
      "",
      "http://github.com/luolongfei/ColabOne",
      "javascript:alert(1)",
      "https:github.com",
      "https://user@github.com/",
      "https://github.com:443/",
      "https://github.com:8443/",
      "https://evil.example.com/",
      "https://github.com.evil.example/",
      " https://github.com/",
    ];
    for (const raw of invalidUrls) {
      expect(() => validateColabOneWebExternalURL(raw), raw).toThrow();
    }

    const openSpy = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { open: openSpy },
      configurable: true,
      writable: true,
    });
    colabOneWebOpenExternalURL("https://huggingface.co/models");
    expect(openSpy).toHaveBeenCalledWith(
      "https://huggingface.co/models",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("validates ComfyUI canvas URLs strictly before opening a browser tab", () => {
    const openSpy = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { open: openSpy },
      configurable: true,
      writable: true,
    });

    colabOneWebOpenCanvasURL(
      "https://comfy.example.com/_colabone_auth?token=abc123",
    );
    expect(openSpy).toHaveBeenCalledWith(
      "https://comfy.example.com/_colabone_auth?token=abc123",
      "_blank",
      "noopener,noreferrer",
    );

    const rejected = [
      "",
      "http://comfy.example.com/_colabone_auth?token=abc",
      "https://comfy.example.com/_colabone_auth",
      "https://comfy.example.com/_colabone_auth?token=",
      "https://comfy.example.com/other?token=abc",
      "https://foo.trycloudflare.com/_colabone_auth?token=abc",
      "https://trycloudflare.com/_colabone_auth?token=abc",
      "https://localhost/_colabone_auth?token=abc",
      "https://sub.localhost/_colabone_auth?token=abc",
      "https://127.0.0.1/_colabone_auth?token=abc",
      "https://[::1]/_colabone_auth?token=abc",
      "https://[::ffff:127.0.0.1]/_colabone_auth?token=abc",
      "https://[::1%25lo0]/_colabone_auth?token=abc",
      "https://user@comfy.example.com/_colabone_auth?token=abc",
    ];
    for (const raw of rejected) {
      expect(() => colabOneWebOpenCanvasURL(raw), raw).toThrow();
    }
  });

  it("opens deferred tabs synchronously before async URL resolution and closes blank tabs on failure", async () => {
    const replace = vi.fn();
    const close = vi.fn();
    const fakePopup = {
      opener: {} as unknown,
      closed: false,
      location: { replace, href: "" },
      close,
    };
    const openSpy = vi.fn(() => fakePopup);
    Object.defineProperty(globalThis, "window", {
      value: { open: openSpy },
      configurable: true,
      writable: true,
    });

    const resolved = await colabOneWebOpenDeferredTab(
      async () => "https://comfy.example.com/_colabone_auth?token=ok123",
      validateColabOneWebCanvasURL,
    );
    expect(resolved).toBe("https://comfy.example.com/_colabone_auth?token=ok123");
    expect(openSpy).toHaveBeenCalledWith("about:blank", "_blank");
    expect(fakePopup.opener).toBeNull();
    expect(replace).toHaveBeenCalledWith(
      "https://comfy.example.com/_colabone_auth?token=ok123",
    );
    expect(close).not.toHaveBeenCalled();

    // Validation or fetch error closes the pre-opened blank tab cleanly
    fakePopup.opener = {};
    await expect(
      colabOneWebOpenDeferredTab(
        async () => "http://evil.example.com/bad",
        validateColabOneWebCanvasURL,
      ),
    ).rejects.toThrow();
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("validates ports, builds Cloudflared helpers, and normalizes public origin inputs", () => {
    expect(colabOneIsValidWebPort(COLABONE_WEB_CONSOLE_MIN_PORT)).toBe(true);
    expect(colabOneIsValidWebPort(COLABONE_WEB_CONSOLE_DEFAULT_PORT)).toBe(true);
    expect(colabOneIsValidWebPort(COLABONE_WEB_CONSOLE_MAX_PORT)).toBe(true);
    expect(colabOneIsValidWebPort(1023)).toBe(false);
    expect(colabOneIsValidWebPort(65536)).toBe(false);
    expect(colabOneIsValidWebPort(3232.5)).toBe(false);
    expect(colabOneIsValidWebPort(Number.NaN)).toBe(false);
    expect(colabOneIsValidWebPort("3232")).toBe(false);

    expect(colabOneBuildCloudflaredServiceTarget(3232)).toBe(
      "http://127.0.0.1:3232",
    );
    expect(colabOneBuildCloudflaredQuickCommand(3232)).toBe(
      "cloudflared tunnel --url http://127.0.0.1:3232",
    );
    expect(colabOneBuildCloudflaredQuickCommand(80)).toBe(
      "cloudflared tunnel --url http://127.0.0.1:3232",
    );

    expect(colabOneNormalizePublicOriginInput("")).toBe("");
    expect(colabOneNormalizePublicOriginInput("   ")).toBe("");
    expect(colabOneNormalizePublicOriginInput("console.example.com/")).toBe(
      "https://console.example.com",
    );
    expect(
      colabOneNormalizePublicOriginInput("  https://console.example.com/// "),
    ).toBe("https://console.example.com");
  });
});
