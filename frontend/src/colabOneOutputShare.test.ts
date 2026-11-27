import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER,
  COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
  COLABONE_OUTPUT_SHARE_ROUTE_PATH,
  colabOneOutputShareDownloadURL,
  colabOneOutputShareMediaURL,
  colabOneOutputShareMode,
  colabOneOutputShareSessionExpired,
  colabOneOutputShareZipURL,
  colabOneWebCreateOutputShare,
  colabOneWebDeleteOutputShare,
  colabOneWebDownloadOutputsZipURL,
  colabOneWebGetOutputSharesState,
  colabOneWebRevokeOutputShare,
  createColabOneDefaultOutputShareListState,
  fetchColabOneOutputShareManifest,
  generateColabOneSharePassword,
  isColabOneOutputShareActive,
  isColabOneOutputSharePath,
  normalizeColabOneOutputShareListState,
  normalizeColabOneOutputShareSummary,
  probeColabOneOutputShareMediaError,
  triggerColabOneBrowserDownload,
} from "./colabOneOutputShare";
import {
  COLABONE_WEB_CLIENT_HEADER,
  COLABONE_WEB_CLIENT_VALUE,
  colabOneWebSessionExpired,
  decodeColabOneErrorHeader,
  notifyColabOneWebAuthRequired,
  resolveColabOneWebOutputShareOpenURL,
  validateColabOneWebOutputShareURL,
} from "./colabOneWebConsole";
import { colabOneOpenOutputShare } from "./api";

const globalWithMyGo = globalThis as typeof globalThis & {
  mygo?: { callWith?: (...args: unknown[]) => unknown };
};

function makeResponse(
  body: string,
  init: { status?: number; headers?: Record<string, string> } = {},
): Response {
  const status = init.status ?? 200;
  const headers = new Headers(init.headers);
  return new Response(body, { status, headers });
}

describe("isColabOneOutputSharePath & active detection", () => {
  const originalWindow = globalThis.window;
  const originalMyGo = globalWithMyGo.mygo;

  afterEach(() => {
    colabOneOutputShareMode.value = false;
    globalWithMyGo.mygo = originalMyGo;
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
  });

  it("matches /gallery and /gallery/* paths while rejecting unrelated routes", () => {
    expect(COLABONE_OUTPUT_SHARE_ROUTE_PATH).toBe("/gallery");
    expect(isColabOneOutputSharePath("/gallery")).toBe(true);
    expect(isColabOneOutputSharePath("/gallery/")).toBe(true);
    expect(isColabOneOutputSharePath("/gallery/abc123")).toBe(true);
    expect(isColabOneOutputSharePath("/gallery?k=co_gal_12345678")).toBe(true);
    expect(isColabOneOutputSharePath("/gallery#top")).toBe(true);
    expect(isColabOneOutputSharePath("/gallery2")).toBe(false);
    expect(isColabOneOutputSharePath("/share")).toBe(false);
    expect(isColabOneOutputSharePath("/api/gallery/manifest")).toBe(false);
    expect(isColabOneOutputSharePath("/")).toBe(false);
  });

  it("reflects browser /gallery route and explicit mode accurately", () => {
    delete globalWithMyGo.mygo;
    Object.defineProperty(globalThis, "window", {
      value: { location: { pathname: "/gallery" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneOutputSharePath()).toBe(true);
    expect(isColabOneOutputShareActive()).toBe(true);

    globalWithMyGo.mygo = { callWith: vi.fn() };
    expect(isColabOneOutputShareActive()).toBe(false);

    colabOneOutputShareMode.value = true;
    expect(isColabOneOutputShareActive()).toBe(true);
  });
});

describe("output share state normalization", () => {
  it("creates empty default list state and safely rejects invalid summaries", () => {
    expect(createColabOneDefaultOutputShareListState()).toEqual({
      shares: [],
      active_count: 0,
      total_active_sessions: 0,
    });
    expect(normalizeColabOneOutputShareSummary(null)).toBeNull();
    expect(normalizeColabOneOutputShareSummary({})).toBeNull();
    expect(normalizeColabOneOutputShareSummary({ id: "  " })).toBeNull();
    expect(normalizeColabOneOutputShareListState(null)).toEqual(
      createColabOneDefaultOutputShareListState(),
    );
  });

  it("normalizes summary fields, password presence, and falls back invalid enums", () => {
    const summary = normalizeColabOneOutputShareSummary({
      id: " gal-1 ",
      title: " Autumn Lookbook ",
      note: " For client review ",
      ttl_mode: "30d",
      status: "invalid_status",
      allow_download: false,
      password: " AbC234Xy ",
      created_at: 1700000000,
      expires_at: 1702592000,
      active_sessions: 3.9,
      group_count: 2,
      item_count: 9,
      url: " https://studio.acme.dev/gallery?k=co_gal_1234.5678 ",
      local_url: "  ",
      uses_public_host: true,
      can_copy_link: true,
    });

    expect(summary).toEqual({
      id: "gal-1",
      title: "Autumn Lookbook",
      note: "For client review",
      ttl_mode: "30d",
      status: "expired",
      allow_download: false,
      has_password: true,
      password: "AbC234Xy",
      created_at: 1700000000,
      expires_at: 1702592000,
      active_sessions: 3,
      group_count: 2,
      item_count: 9,
      url: "https://studio.acme.dev/gallery?k=co_gal_1234.5678",
      uses_public_host: true,
      can_copy_link: true,
    });
  });

  it("deduplicates share list entries and recomputes active counts and session totals", () => {
    const state = normalizeColabOneOutputShareListState({
      active_count: 99,
      total_active_sessions: 99,
      shares: [
        {
          id: "g1",
          title: "Set A",
          ttl_mode: "7d",
          status: "active",
          active_sessions: 2,
          group_count: 1,
          item_count: 6,
          can_copy_link: true,
        },
        {
          id: "g1",
          title: "Duplicate",
          ttl_mode: "24h",
          status: "active",
          active_sessions: 10,
        },
        {
          id: "g2",
          title: "Revoked",
          ttl_mode: "forever",
          status: "revoked",
          active_sessions: 0,
        },
        { id: "" },
      ],
    });

    expect(state.shares.map((s) => s.id)).toEqual(["g1", "g2"]);
    expect(state.active_count).toBe(1);
    expect(state.total_active_sessions).toBe(2);
  });
});

describe("generateColabOneSharePassword", () => {
  it("generates unambiguous alphanumeric passwords of requested length without 0/O/o/1/I/i/l", () => {
    for (let i = 0; i < 20; i += 1) {
      const pwd = generateColabOneSharePassword();
      expect(pwd).toHaveLength(8);
      expect(pwd).not.toMatch(/[0Oo1Iil]/);
      expect(pwd).toMatch(/^[A-Za-z0-9]+$/);
    }
    const longPwd = generateColabOneSharePassword(14);
    expect(longPwd).toHaveLength(14);
    expect(longPwd).not.toMatch(/[0Oo1Iil]/);
  });
});

describe("URL builders", () => {
  it("encodes media, download, gallery ZIP, and host multi-output ZIP URLs cleanly", () => {
    expect(colabOneOutputShareMediaURL("abc123")).toBe(
      "/api/gallery/outputs/abc123/media",
    );
    expect(colabOneOutputShareDownloadURL("abc123")).toBe(
      "/api/gallery/outputs/abc123/download",
    );
    expect(colabOneOutputShareZipURL()).toBe("/api/gallery/download.zip");
    expect(colabOneOutputShareZipURL("album:task 1")).toBe(
      "/api/gallery/download.zip?group=album%3Atask%201",
    );
    expect(
      colabOneWebDownloadOutputsZipURL(["id1", "id2", "id1", ""], "Lookbook 2026"),
    ).toBe("/api/outputs/zip?ids=id1%2Cid2&name=Lookbook+2026");
    expect(colabOneWebDownloadOutputsZipURL(["id1"])).toBe(
      "/api/outputs/zip?ids=id1",
    );
  });
});

describe("gallery & host web-console HTTP transport", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    colabOneOutputShareSessionExpired.value = false;
    colabOneWebSessionExpired.value = false;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    colabOneOutputShareSessionExpired.value = false;
    colabOneWebSessionExpired.value = false;
    vi.restoreAllMocks();
  });

  it("fetches /api/gallery/manifest with X-ColabOne-Client: web-gallery and normalizes groups", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      makeResponse(
        JSON.stringify({
          runtime: "web-gallery",
          version: "0.3.0",
          share_id: "gal-abc",
          title: "Spring Collection",
          allow_download: true,
          expires_at: 1700500000,
          created_at: 1700000000,
          groups: [
            {
              id: "album:t1",
              title: "Look 01",
              workflow_id: "fashion-tryon-h3",
              workflow_name: "Fashion Try-On",
              task_id: "t1",
              items: [
                {
                  id: "out1",
                  filename: "look_1.png",
                  kind: "image",
                  mime: "image/png",
                  size: 2048,
                  workflow_id: "fashion-tryon-h3",
                  workflow_name: "Fashion Try-On",
                  created_at: 1700000010,
                  task_id: "t1",
                  is_inspect: false,
                  available: true,
                },
                {
                  id: "out2",
                  filename: "rmbg_inspect_1.png",
                  kind: "image",
                  mime: "image/png",
                  size: 1024,
                  workflow_id: "fashion-tryon-h3",
                  workflow_name: "Fashion Try-On",
                  created_at: 1700000011,
                  task_id: "t1",
                  available: false,
                },
              ],
            },
          ],
        }),
      ),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const manifest = await fetchColabOneOutputShareManifest();
    expect(manifest.share_id).toBe("gal-abc");
    expect(manifest.title).toBe("Spring Collection");
    expect(manifest.allow_download).toBe(true);
    expect(manifest.groups).toHaveLength(1);
    expect(manifest.groups[0].items[0].is_inspect).toBe(false);
    expect(manifest.groups[0].items[1].is_inspect).toBe(true);
    expect(manifest.groups[0].items[1].available).toBe(false);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/gallery/manifest",
      expect.objectContaining({
        method: "GET",
        credentials: "same-origin",
        headers: {
          [COLABONE_OUTPUT_SHARE_CLIENT_HEADER]:
            COLABONE_OUTPUT_SHARE_CLIENT_HEADER_VALUE,
        },
      }),
    );
  });

  it("marks gallery session expired and dispatches auth event on 401", async () => {
    const originalWindow = globalThis.window;
    const dispatchEvent = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { dispatchEvent },
      configurable: true,
      writable: true,
    });

    globalThis.fetch = vi
      .fn()
      .mockResolvedValueOnce(makeResponse("Unauthorized", { status: 401 }))
      .mockResolvedValueOnce(
        makeResponse("Unauthorized", { status: 401 }),
      ) as unknown as typeof fetch;

    try {
      await expect(fetchColabOneOutputShareManifest()).rejects.toThrow(
        "Unauthorized",
      );
      expect(colabOneOutputShareSessionExpired.value).toBe(true);

      colabOneOutputShareSessionExpired.value = false;
      await expect(
        triggerColabOneBrowserDownload("/api/gallery/download.zip"),
      ).rejects.toThrow("Unauthorized");
      expect(colabOneOutputShareSessionExpired.value).toBe(true);

      expect(dispatchEvent).toHaveBeenCalledTimes(2);
      for (const [evt] of dispatchEvent.mock.calls) {
        expect((evt as Event).type).toBe(
          COLABONE_OUTPUT_SHARE_AUTH_REQUIRED_EVENT,
        );
      }
    } finally {
      Object.defineProperty(globalThis, "window", {
        value: originalWindow,
        configurable: true,
        writable: true,
      });
    }
  });

  it("handles HEAD download preflight and 405 GET fallback cleanly", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse("", {
          status: 200,
          headers: {
            "Content-Disposition": 'attachment; filename="lookbook.zip"',
          },
        }),
      )
      .mockResolvedValueOnce(makeResponse("", { status: 405 }))
      .mockResolvedValueOnce(
        makeResponse("zip-bytes", {
          status: 200,
          headers: {
            "Content-Disposition":
              "attachment; filename*=UTF-8''album%20set.zip",
          },
        }),
      );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const first = await triggerColabOneBrowserDownload(
      "/api/gallery/download.zip",
    );
    expect(first).toBe("lookbook.zip");

    const second = await triggerColabOneBrowserDownload(
      "/api/gallery/download.zip?group=g1",
    );
    expect(second).toBe("album set.zip");
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("calls host web-console output-share endpoints with X-ColabOne-Client matching colabOneWebConsole", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse(
          JSON.stringify({
            shares: [{ id: "g1", title: "Demo", status: "active" }],
            active_count: 1,
            total_active_sessions: 0,
          }),
        ),
      )
      .mockResolvedValueOnce(
        makeResponse(
          JSON.stringify({
            id: "g2",
            title: "New Gallery",
            status: "active",
            ttl_mode: "7d",
            allow_download: true,
            can_copy_link: true,
            url: "https://studio.acme.dev/gallery?k=co_gal_abc12345",
          }),
        ),
      )
      .mockResolvedValueOnce(
        makeResponse(
          JSON.stringify({
            shares: [{ id: "g2", title: "New Gallery", status: "revoked" }],
          }),
        ),
      )
      .mockResolvedValueOnce(makeResponse(JSON.stringify({ shares: [] })));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const list = await colabOneWebGetOutputSharesState();
    expect(list.shares).toHaveLength(1);
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/output-shares",
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({
          [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
        }),
      }),
    );

    const created = await colabOneWebCreateOutputShare({
      title: "New Gallery",
      groups: [{ title: "Set 1", output_ids: ["o1", "o2"] }],
      ttl_mode: "7d",
      allow_download: true,
    });
    expect(created.id).toBe("g2");
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/output-shares",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          "Content-Type": "application/json",
          [COLABONE_WEB_CLIENT_HEADER]: COLABONE_WEB_CLIENT_VALUE,
        }),
      }),
    );

    const revoked = await colabOneWebRevokeOutputShare("g2");
    expect(revoked.shares[0].status).toBe("revoked");
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      "/api/output-shares/g2/revoke",
      expect.objectContaining({ method: "POST" }),
    );

    const deleted = await colabOneWebDeleteOutputShare("g2");
    expect(deleted.shares).toEqual([]);
    expect(fetchMock).toHaveBeenNthCalledWith(
      4,
      "/api/output-shares/g2",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("preserves manifest extension and summary counts while omitting unsupported note field", async () => {
    globalThis.fetch = vi.fn().mockResolvedValueOnce(
      makeResponse(
        JSON.stringify({
          runtime: "web-gallery",
          version: "0.4.0",
          share_id: "gal-ext",
          title: "Lookbook",
          note: "legacy-ignored",
          allow_download: true,
          item_count: 2,
          available_count: 1,
          group_count: 1,
          groups: [
            {
              id: "album:t1",
              title: "Lookbook",
              items: [
                {
                  id: "o1",
                  filename: "look_1.png",
                  extension: "png",
                  kind: "image",
                  mime: "image/png",
                  size: 1200,
                  available: true,
                },
                {
                  id: "o2",
                  filename: "00_抠衣质检_top.png",
                  extension: "png",
                  kind: "image",
                  mime: "image/png",
                  size: 800,
                  available: false,
                },
              ],
            },
          ],
        }),
      ),
    ) as unknown as typeof fetch;

    const manifest = await fetchColabOneOutputShareManifest();
    expect(manifest.item_count).toBe(2);
    expect(manifest.available_count).toBe(1);
    expect(manifest.group_count).toBe(1);
    expect(manifest.groups[0].items[0].extension).toBe("png");
    expect(manifest.groups[0].items[1].is_inspect).toBe(true);
    expect("note" in manifest).toBe(false);
  });

  it("probes media element errors via HEAD to distinguish 401 expiry from 503/404 offline", async () => {
    const originalWindow = globalThis.window;
    const dispatchEvent = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { dispatchEvent },
      configurable: true,
      writable: true,
    });

    globalThis.fetch = vi
      .fn()
      .mockResolvedValueOnce(makeResponse("", { status: 401 }))
      .mockResolvedValueOnce(makeResponse("", { status: 503 }))
      .mockResolvedValueOnce(makeResponse("", { status: 404 }))
      .mockRejectedValueOnce(new Error("network down")) as unknown as typeof fetch;

    try {
      expect(await probeColabOneOutputShareMediaError("img-1")).toBe(
        "unauthorized",
      );
      expect(colabOneOutputShareSessionExpired.value).toBe(true);
      expect(dispatchEvent).toHaveBeenCalledTimes(1);

      expect(await probeColabOneOutputShareMediaError("img-2")).toBe("offline");
      expect(await probeColabOneOutputShareMediaError("img-3")).toBe("offline");
      expect(await probeColabOneOutputShareMediaError("img-4")).toBe("error");
    } finally {
      Object.defineProperty(globalThis, "window", {
        value: originalWindow,
        configurable: true,
        writable: true,
      });
    }
  });

  it("prevents stale in-flight output-share responses from clearing a newer web auth-expiry epoch", async () => {
    let resolveResponse!: (res: Response) => void;
    const pendingPromise = new Promise<Response>((resolve) => {
      resolveResponse = resolve;
    });
    globalThis.fetch = vi
      .fn()
      .mockReturnValueOnce(pendingPromise) as unknown as typeof fetch;

    const listPromise = colabOneWebGetOutputSharesState();
    // Simulate a concurrent 401 from another endpoint advancing the shared auth epoch
    notifyColabOneWebAuthRequired();
    expect(colabOneWebSessionExpired.value).toBe(true);

    resolveResponse(
      makeResponse(
        JSON.stringify({
          shares: [],
          active_count: 0,
          total_active_sessions: 0,
        }),
      ),
    );
    await listPromise;
    expect(colabOneWebSessionExpired.value).toBe(true);
  });

  it("aborts cleanly after fetch resolution when AbortSignal fires in-flight for manifest and media probe", async () => {
    const originalWindow = globalThis.window;
    const dispatchEvent = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { dispatchEvent },
      configurable: true,
      writable: true,
    });

    try {
      // 1. fetchColabOneOutputShareManifest throws AbortError even if fetch resolves 401 after abort
      const manifestAbort = new AbortController();
      globalThis.fetch = vi.fn(async () => {
        manifestAbort.abort();
        return makeResponse("Unauthorized", { status: 401 });
      }) as unknown as typeof fetch;

      await expect(
        fetchColabOneOutputShareManifest(manifestAbort.signal),
      ).rejects.toMatchObject({ name: "AbortError" });
      expect(colabOneOutputShareSessionExpired.value).toBe(false);
      expect(dispatchEvent).not.toHaveBeenCalled();

      // 2. probeColabOneOutputShareMediaError returns "error" without marking expired when aborted
      const probeAbort = new AbortController();
      globalThis.fetch = vi.fn(async () => {
        probeAbort.abort();
        return makeResponse("", { status: 401 });
      }) as unknown as typeof fetch;

      expect(
        await probeColabOneOutputShareMediaError("img-aborted", probeAbort.signal),
      ).toBe("error");
      expect(colabOneOutputShareSessionExpired.value).toBe(false);
      expect(dispatchEvent).not.toHaveBeenCalled();
    } finally {
      Object.defineProperty(globalThis, "window", {
        value: originalWindow,
        configurable: true,
        writable: true,
      });
    }
  });
});

describe("validateColabOneWebOutputShareURL", () => {
  const originalWindow = globalThis.window;

  afterEach(() => {
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
  });

  it("permits same-origin, configured public_origin, and local_url /gallery links while rejecting untrusted URLs", () => {
    Object.defineProperty(globalThis, "window", {
      value: { location: { origin: "http://127.0.0.1:3232" } },
      configurable: true,
      writable: true,
    });

    const cfg = {
      public_origin: "https://studio.acme.dev",
      local_url: "http://127.0.0.1:3232",
    };

    expect(
      validateColabOneWebOutputShareURL("/gallery?k=co_gal_1234", cfg),
    ).toBe("http://127.0.0.1:3232/gallery?k=co_gal_1234");
    expect(
      validateColabOneWebOutputShareURL(
        "https://studio.acme.dev/gallery?k=co_gal_1234",
        cfg,
      ),
    ).toBe("https://studio.acme.dev/gallery?k=co_gal_1234");

    expect(() =>
      validateColabOneWebOutputShareURL(
        "https://evil.example.com/gallery?k=co_gal_1234",
        cfg,
      ),
    ).toThrow("Unsupported output share URL");
    expect(() =>
      validateColabOneWebOutputShareURL(
        "https://user:pass@studio.acme.dev/gallery?k=co_gal_1234",
        cfg,
      ),
    ).toThrow("Unsupported output share URL");
    expect(() =>
      validateColabOneWebOutputShareURL(
        "https://studio.acme.dev/admin?k=co_gal_1234",
        cfg,
      ),
    ).toThrow("Unsupported output share URL");
    expect(() =>
      validateColabOneWebOutputShareURL("javascript:alert(1)", cfg),
    ).toThrow("Unsupported output share URL");
  });

  it("allows sibling loopback aliases (localhost, 127.0.0.1, [::1]) on same scheme+port and configured lan_urls", () => {
    Object.defineProperty(globalThis, "window", {
      value: { location: { origin: "http://localhost:3232" } },
      configurable: true,
      writable: true,
    });

    const loopbackCfg = {
      public_origin: "",
      local_url: "http://127.0.0.1:3232",
      lan_urls: ["http://192.168.1.42:3232", "http://10.0.0.8:3232"],
    };

    expect(
      validateColabOneWebOutputShareURL(
        "http://127.0.0.1:3232/gallery?k=co_gal_1234",
        loopbackCfg,
      ),
    ).toBe("http://127.0.0.1:3232/gallery?k=co_gal_1234");
    expect(
      validateColabOneWebOutputShareURL(
        "http://[::1]:3232/gallery?k=co_gal_1234",
        loopbackCfg,
      ),
    ).toBe("http://[::1]:3232/gallery?k=co_gal_1234");
    expect(
      validateColabOneWebOutputShareURL(
        "http://192.168.1.42:3232/gallery?k=co_gal_1234",
        loopbackCfg,
      ),
    ).toBe("http://192.168.1.42:3232/gallery?k=co_gal_1234");

    // Different port or scheme on loopback stays rejected
    expect(() =>
      validateColabOneWebOutputShareURL(
        "http://localhost:9999/gallery?k=co_gal_1234",
        loopbackCfg,
      ),
    ).toThrow("Unsupported output share URL");
    expect(() =>
      validateColabOneWebOutputShareURL(
        "https://127.0.0.1:3232/gallery?k=co_gal_1234",
        loopbackCfg,
      ),
    ).toThrow("Unsupported output share URL");

    // IPv6 loopback base origin accepts 127.0.0.1 and localhost on same port
    Object.defineProperty(globalThis, "window", {
      value: { location: { origin: "http://[::1]:3232" } },
      configurable: true,
      writable: true,
    });
    expect(
      validateColabOneWebOutputShareURL(
        "http://127.0.0.1:3232/gallery?k=co_gal_1234",
        { local_url: "http://127.0.0.1:3232" },
      ),
    ).toBe("http://127.0.0.1:3232/gallery?k=co_gal_1234");
    expect(
      validateColabOneWebOutputShareURL(
        "http://localhost:3232/gallery?k=co_gal_1234",
        { local_url: "http://127.0.0.1:3232" },
      ),
    ).toBe("http://localhost:3232/gallery?k=co_gal_1234");
  });

  it("rewrites non-public host share URLs to current browser origin for LAN clients and enforces shareId in Desktop mode", async () => {
    const openSpy = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: {
        location: { origin: "http://192.168.1.42:3232" },
        open: openSpy,
      },
      configurable: true,
      writable: true,
    });

    const lanCfg = {
      public_origin: "",
      local_url: "http://127.0.0.1:3232",
      lan_urls: ["http://192.168.1.42:3232", "http://10.0.0.8:3232"],
    };

    // Host loopback URL or alternate LAN NIC URL rewrites to active client origin
    expect(
      resolveColabOneWebOutputShareOpenURL(
        "http://127.0.0.1:3232/gallery?k=co_gal_lan1",
        lanCfg,
      ),
    ).toBe("http://192.168.1.42:3232/gallery?k=co_gal_lan1");
    expect(
      resolveColabOneWebOutputShareOpenURL(
        "http://10.0.0.8:3232/gallery?k=co_gal_lan2",
        lanCfg,
      ),
    ).toBe("http://192.168.1.42:3232/gallery?k=co_gal_lan2");

    // Configured custom public_origin is preserved verbatim
    expect(
      resolveColabOneWebOutputShareOpenURL(
        "https://studio.acme.dev/gallery?k=co_gal_pub1",
        {
          ...lanCfg,
          public_origin: "https://studio.acme.dev",
        },
      ),
    ).toBe("https://studio.acme.dev/gallery?k=co_gal_pub1");

    const originalMyGo = globalWithMyGo.mygo;
    try {
      delete globalWithMyGo.mygo;
      await colabOneOpenOutputShare(
        "share-1",
        "http://127.0.0.1:3232/gallery?k=co_gal_lan1",
        lanCfg,
      );
      expect(openSpy).toHaveBeenCalledWith(
        "http://192.168.1.42:3232/gallery?k=co_gal_lan1",
        "_blank",
        "noopener,noreferrer",
      );

      // Desktop mode must throw explicitly when shareId is missing instead of calling openURL
      const callWith = vi.fn();
      globalWithMyGo.mygo = { callWith };
      await expect(
        colabOneOpenOutputShare("", "http://127.0.0.1:3232/gallery?k=co_gal_1", lanCfg),
      ).rejects.toThrow("Missing output share ID");
      expect(callWith).not.toHaveBeenCalled();
    } finally {
      globalWithMyGo.mygo = originalMyGo;
    }
  });

  it("decodes HEAD X-ColabOne-Error PathEscape headers (%2B and %20) without corrupting literal plus signs", async () => {
    expect(decodeColabOneErrorHeader("C%2B%2B%20runtime%20error")).toBe(
      "C++ runtime error",
    );
    expect(decodeColabOneErrorHeader("A+B")).toBe("A+B");
    expect(decodeColabOneErrorHeader("A+B%20failed")).toBe("A+B failed");

    const originalFetch = globalThis.fetch;
    try {
      globalThis.fetch = vi.fn(async () =>
        makeResponse("", {
          status: 500,
          headers: {
            "X-ColabOne-Error": "C%2B%2B%20runtime%20error",
          },
        }),
      ) as unknown as typeof fetch;
      await expect(
        triggerColabOneBrowserDownload("/api/gallery/download?item=1"),
      ).rejects.toThrow("C++ runtime error");

      globalThis.fetch = vi.fn(async () =>
        makeResponse("", {
          status: 500,
          headers: {
            "X-ColabOne-Error": "GPU+CUDA",
          },
        }),
      ) as unknown as typeof fetch;
      await expect(
        triggerColabOneBrowserDownload("/api/gallery/download?item=1"),
      ).rejects.toThrow("GPU+CUDA");
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it("sanitizes unsafe fallbackFilename values on browser download", async () => {
    const originalFetch = globalThis.fetch;
    const originalDoc = globalThis.document;
    try {
      const createdAnchors: Array<{ download: string; href: string }> = [];
      Object.defineProperty(globalThis, "document", {
        value: {
          body: {
            appendChild: vi.fn(),
            removeChild: vi.fn(),
          },
          createElement: vi.fn(() => {
            const anchor = {
              href: "",
              download: "",
              rel: "",
              style: { display: "" },
              click: vi.fn(),
              remove: vi.fn(),
            };
            createdAnchors.push(anchor);
            return anchor;
          }),
        },
        configurable: true,
        writable: true,
      });
      globalThis.fetch = vi.fn(async () =>
        makeResponse("", { status: 200 }),
      ) as unknown as typeof fetch;

      const resolved = await triggerColabOneBrowserDownload(
        "/api/gallery/download?item=1",
        { fallbackFilename: "../../evil/path\\..\\report\r\n.zip" },
      );
      expect(resolved).not.toContain("/");
      expect(resolved).not.toContain("\\");
      expect(resolved).not.toContain("..");
      expect(resolved).not.toMatch(/[\r\n]/);
      expect(resolved).toBe("report.zip");
      expect(createdAnchors[0]?.download).toBe("report.zip");
    } finally {
      globalThis.fetch = originalFetch;
      Object.defineProperty(globalThis, "document", {
        value: originalDoc,
        configurable: true,
        writable: true,
      });
    }
  });
});
