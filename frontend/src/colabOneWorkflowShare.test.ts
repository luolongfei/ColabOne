import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  colabOneApplyMigrationPreview,
  colabOneCancelMigrationSession,
  colabOneCancelUpdateInstall,
  colabOneCheckForUpdate,
  colabOneCommand,
  colabOneDecryptMigrationPreview,
  colabOneExportMigrationFile,
  colabOneExportWorkflowOutput,
  colabOneGetMigrationExportSummary,
  colabOneGetWebConsoleState,
  colabOneGetWorkflowSharesState,
  colabOneListRollbackVersions,
  colabOneOutputPreviewURL,
  colabOnePickMigrationFile,
  colabOneRelaunchForUpdate,
  colabOneResolveCloseRequest,
  colabOneSetCloseBehavior,
  colabOneSetUpdatePreferences,
  colabOneSkipUpdateVersion,
  colabOneSnapshotRaw,
  colabOneStartUpdateInstall,
  colabOneStartUpdateRollback,
  isColabOneWorkflowShareActive,
} from "./api";
import {
  colabOneWebBackendReady,
  colabOneWebOutputPreviewURL,
  colabOneWebSessionExpired,
  colabOneWebSnapshotRaw,
  resetColabOneWebSnapshotCache,
} from "./colabOneWebConsole";
import {
  COLABONE_SHARE_AUTH_REQUIRED_EVENT,
  COLABONE_SHARE_CLIENT_HEADER,
  COLABONE_SHARE_CLIENT_VALUE,
  colabOneWorkflowShareCapabilities,
  colabOneWorkflowShareCommandRaw,
  colabOneWorkflowShareDownloadOutput,
  colabOneWorkflowShareId,
  colabOneWorkflowShareMode,
  colabOneWorkflowShareOutputPreviewURL,
  colabOneWorkflowShareSessionExpired,
  colabOneWorkflowShareSnapshotRaw,
  createColabOneDefaultWorkflowShareListState,
  fetchColabOneWorkflowShareCapabilities,
  isColabOneWorkflowSharePath,
  normalizeColabOneWorkflowShareListState,
  normalizeColabOneWorkflowShareSummary,
  resetColabOneWorkflowShareSnapshotCache,
} from "./colabOneWorkflowShare";
import {
  createColabOneWorkflowDraftClient,
  mergeColabOneWorkflowDraftFields,
} from "./comfyDrafts";
import {
  COLABONE_FASHION_MODEL_ROLES_KEY,
  serializeColabOneFashionPhotoMeta,
} from "./comfyui";
import {
  readStoredColabOneLocale,
  setColabOneLocale,
  syncColabOneLocaleToNative,
} from "./i18n";
import { COLABONE_STORAGE_KEYS } from "./storage";
import type { WorkflowField } from "./types";

const globalWithMyGo = globalThis as typeof globalThis & {
  mygo?: { callWith?: (...args: unknown[]) => unknown };
};

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeResponse(
  body: string,
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

function createMemoryStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    clear() {
      store.clear();
    },
    getItem(key: string) {
      return store.has(key) ? store.get(key)! : null;
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string) {
      store.delete(key);
    },
    setItem(key: string, value: string) {
      store.set(key, String(value));
    },
  };
}

describe("isColabOneWorkflowSharePath", () => {
  const originalWindow = globalThis.window;

  afterEach(() => {
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
  });

  it("matches /share and /share/* paths and rejects non-share routes", () => {
    expect(isColabOneWorkflowSharePath("/share")).toBe(true);
    expect(isColabOneWorkflowSharePath("/share/")).toBe(true);
    expect(isColabOneWorkflowSharePath("/share/demo")).toBe(true);
    expect(isColabOneWorkflowSharePath("/share?foo=1")).toBe(true);
    expect(isColabOneWorkflowSharePath("/")).toBe(false);
    expect(isColabOneWorkflowSharePath("/_colabone_web/login")).toBe(false);
    expect(isColabOneWorkflowSharePath("/share2")).toBe(false);
    expect(isColabOneWorkflowSharePath("/api/share/snapshot")).toBe(false);
  });

  it("reads window.location.pathname when pathname is omitted", () => {
    Object.defineProperty(globalThis, "window", {
      value: { location: { pathname: "/share" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneWorkflowSharePath()).toBe(true);

    Object.defineProperty(globalThis, "window", {
      value: { location: { pathname: "/_colabone_web/login" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneWorkflowSharePath()).toBe(false);
  });
});

describe("workflow share state normalization", () => {
  it("creates empty default list state and handles invalid inputs safely", () => {
    expect(createColabOneDefaultWorkflowShareListState()).toEqual({
      shares: [],
      active_count: 0,
      total_active_sessions: 0,
    });
    expect(normalizeColabOneWorkflowShareSummary(null)).toBeNull();
    expect(normalizeColabOneWorkflowShareSummary({})).toBeNull();
    expect(normalizeColabOneWorkflowShareSummary({ id: "   " })).toBeNull();
    expect(normalizeColabOneWorkflowShareListState(null)).toEqual(
      createColabOneDefaultWorkflowShareListState(),
    );
  });

  it("normalizes summary fields, dedupes workflow IDs, and falls back invalid enums", () => {
    const summary = normalizeColabOneWorkflowShareSummary({
      id: " share-1 ",
      label: " Demo Team ",
      title: " Digital Human Preview ",
      workflow_ids: [" wf-a ", "wf-b", "wf-a", "", 123],
      ttl_mode: "once",
      status: "invalid_status",
      created_at: 1700000000,
      expires_at: 1700086400,
      consumed_at: 1700001234,
      revoked_at: 0,
      active_sessions: 2.8,
      output_count: -3,
      url: " https://example.com/s/token ",
      local_url: "  ",
      uses_public_host: true,
      can_copy_link: true,
    });

    expect(summary).toEqual({
      id: "share-1",
      label: "Demo Team",
      title: "Digital Human Preview",
      workflow_ids: ["wf-a", "wf-b"],
      ttl_mode: "once",
      single_use: true,
      status: "expired",
      created_at: 1700000000,
      expires_at: 1700086400,
      consumed_at: 1700001234,
      active_sessions: 2,
      output_count: 0,
      url: "https://example.com/s/token",
      uses_public_host: true,
      can_copy_link: true,
    });
  });

  it("normalizes share list state, deduplicates shares, and computes fallback counts", () => {
    const state = normalizeColabOneWorkflowShareListState({
      shares: [
        {
          id: "s1",
          label: "Active 1",
          title: "T1",
          workflow_ids: ["wf1"],
          ttl_mode: "24h",
          status: "active",
          active_sessions: 2,
          output_count: 1,
          uses_public_host: true,
          can_copy_link: true,
        },
        {
          id: "s1",
          label: "Duplicate",
          title: "Dup",
          workflow_ids: ["wf2"],
          ttl_mode: "7d",
          status: "active",
          active_sessions: 9,
          output_count: 9,
          uses_public_host: false,
          can_copy_link: false,
        },
        {
          id: "s2",
          label: "Revoked",
          title: "T2",
          workflow_ids: ["wf2"],
          ttl_mode: "forever",
          status: "revoked",
          active_sessions: 0,
          output_count: 3,
          uses_public_host: false,
          can_copy_link: false,
        },
        { id: "" },
      ],
    });

    expect(state.shares).toHaveLength(2);
    expect(state.shares.map((s) => s.id)).toEqual(["s1", "s2"]);
    expect(state.active_count).toBe(1);
    expect(state.total_active_sessions).toBe(2);

    // Recomputes counts from deduplicated shares even when raw counts disagree.
    const mismatched = normalizeColabOneWorkflowShareListState({
      active_count: 99,
      total_active_sessions: 88,
      shares: [
        {
          id: "s1",
          status: "active",
          active_sessions: 3,
        },
        {
          id: "s1",
          status: "active",
          active_sessions: 7,
        },
        {
          id: "s2",
          status: "unknown_future_state",
          active_sessions: 1,
        },
      ],
    });
    expect(mismatched.shares).toHaveLength(2);
    expect(mismatched.shares[1].status).toBe("expired");
    expect(mismatched.active_count).toBe(1);
    expect(mismatched.total_active_sessions).toBe(4);
  });
});

describe("isColabOneWorkflowShareActive", () => {
  const originalWindow = globalThis.window;
  const originalMyGo = globalWithMyGo.mygo;

  afterEach(() => {
    colabOneWorkflowShareMode.value = false;
    globalWithMyGo.mygo = originalMyGo;
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
  });

  it("reflects explicit mode and non-desktop /share routes accurately", () => {
    delete globalWithMyGo.mygo;
    Object.defineProperty(globalThis, "window", {
      value: { location: { pathname: "/" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneWorkflowShareActive()).toBe(false);

    Object.defineProperty(globalThis, "window", {
      value: { location: { pathname: "/share" } },
      configurable: true,
      writable: true,
    });
    expect(isColabOneWorkflowShareActive()).toBe(true);

    // Desktop runtime on /share is not implicitly share mode unless explicitly enabled.
    globalWithMyGo.mygo = { callWith: vi.fn() };
    expect(isColabOneWorkflowShareActive()).toBe(false);

    colabOneWorkflowShareMode.value = true;
    expect(isColabOneWorkflowShareActive()).toBe(true);
  });
});

describe("workflow share HTTP transport", () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    resetColabOneWorkflowShareSnapshotCache();
    colabOneWorkflowShareMode.value = false;
    colabOneWorkflowShareSessionExpired.value = false;
    colabOneWorkflowShareId.value = "";
    colabOneWorkflowShareCapabilities.value = null;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    resetColabOneWorkflowShareSnapshotCache();
    colabOneWorkflowShareMode.value = false;
    colabOneWorkflowShareSessionExpired.value = false;
    colabOneWorkflowShareId.value = "";
    colabOneWorkflowShareCapabilities.value = null;
    vi.restoreAllMocks();
  });

  it("fetches /api/share/capabilities with web-share header and updates state", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      makeResponse(
        JSON.stringify({
          runtime: "web-share",
          version: "0.2.1",
          share_id: "share-abc",
          title: "Studio Demo",
          allowed_workflow_ids: ["wf-1", "wf-2"],
          can_download_output: true,
          can_delete_output: true,
          can_export_to_host_drive: true,
        }),
      ),
    );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const caps = await fetchColabOneWorkflowShareCapabilities();
    expect(caps).toEqual({
      runtime: "web-share",
      version: "0.2.1",
      share_id: "share-abc",
      title: "Studio Demo",
      allowed_workflow_ids: ["wf-1", "wf-2"],
      can_download_output: true,
      can_delete_output: true,
      can_export_to_host_drive: true,
    });
    expect(colabOneWorkflowShareId.value).toBe("share-abc");
    expect(colabOneWorkflowShareCapabilities.value).toEqual(caps);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/share/capabilities",
      expect.objectContaining({
        method: "GET",
        credentials: "same-origin",
        headers: {
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
        },
      }),
    );
  });

  it("caches /api/share/snapshot via ETag and returns cached payload on 304", async () => {
    const body = JSON.stringify({ runtime: { state: "ready" }, workflows: [] });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse(body, { status: 200, headers: { ETag: '"etag-1"' } }),
      )
      .mockResolvedValueOnce(makeResponse("", { status: 304 }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const first = await colabOneWorkflowShareSnapshotRaw();
    const second = await colabOneWorkflowShareSnapshotRaw();

    expect(first).toBe(body);
    expect(second).toBe(body);
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      "/api/share/snapshot",
      expect.objectContaining({
        method: "GET",
        credentials: "same-origin",
        headers: {
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
        },
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      "/api/share/snapshot",
      expect.objectContaining({
        method: "GET",
        credentials: "same-origin",
        headers: {
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
          "If-None-Match": '"etag-1"',
        },
      }),
    );
  });

  it("sends POST /api/share/command with web-share CSRF header", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(makeResponse(JSON.stringify({ ok: true })));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const raw = await colabOneWorkflowShareCommandRaw("comfyui.run", {
      workflow_id: "wf-1",
    });

    expect(raw).toBe(JSON.stringify({ ok: true }));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/share/command",
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/json",
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
        },
        body: JSON.stringify({
          action: "comfyui.run",
          payload: { workflow_id: "wf-1" },
        }),
      }),
    );
  });

  it("preflights output preview and download via /api/share/outputs/{id}/*", async () => {
    const outputId = "a".repeat(32);
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(makeResponse("", { status: 200 }))
      .mockResolvedValueOnce(
        makeResponse("", {
          status: 200,
          headers: {
            "Content-Disposition": 'attachment; filename="result.mp4"',
          },
        }),
      );
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const previewUrl = await colabOneWorkflowShareOutputPreviewURL(outputId);
    expect(previewUrl).toBe(`/api/share/outputs/${outputId}/media`);

    const downloaded = await colabOneWorkflowShareDownloadOutput(outputId);
    expect(downloaded).toBe("result.mp4");
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      `/api/share/outputs/${outputId}/media`,
      expect.objectContaining({
        method: "HEAD",
        credentials: "same-origin",
        headers: {
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
        },
      }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      `/api/share/outputs/${outputId}/download`,
      expect.objectContaining({
        method: "HEAD",
        credentials: "same-origin",
        headers: {
          [COLABONE_SHARE_CLIENT_HEADER]: COLABONE_SHARE_CLIENT_VALUE,
        },
      }),
    );
  });

  it("marks session expired and dispatches auth-required event on HTTP 401", async () => {
    const originalWindow = globalThis.window;
    const dispatchEvent = vi.fn();
    Object.defineProperty(globalThis, "window", {
      value: { dispatchEvent },
      configurable: true,
      writable: true,
    });

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(makeResponse("Unauthorized", { status: 401 }))
      .mockResolvedValueOnce(makeResponse("Unauthorized", { status: 401 }))
      .mockResolvedValueOnce(makeResponse("Unauthorized", { status: 401 }));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    try {
      await expect(
        colabOneWorkflowShareCommandRaw("comfyui.run", { workflow_id: "wf-1" }),
      ).rejects.toThrow("Unauthorized");
      expect(colabOneWorkflowShareSessionExpired.value).toBe(true);

      colabOneWorkflowShareSessionExpired.value = false;
      await expect(fetchColabOneWorkflowShareCapabilities()).rejects.toThrow(
        "Unauthorized",
      );
      expect(colabOneWorkflowShareSessionExpired.value).toBe(true);

      colabOneWorkflowShareSessionExpired.value = false;
      await expect(colabOneWorkflowShareSnapshotRaw()).rejects.toThrow(
        "Unauthorized",
      );
      expect(colabOneWorkflowShareSessionExpired.value).toBe(true);

      expect(dispatchEvent).toHaveBeenCalledTimes(3);
      for (const [evt] of dispatchEvent.mock.calls) {
        expect((evt as Event).type).toBe(COLABONE_SHARE_AUTH_REQUIRED_EVENT);
      }
    } finally {
      Object.defineProperty(globalThis, "window", {
        value: originalWindow,
        configurable: true,
        writable: true,
      });
    }
  });

  it("guards ETag 304 and out-of-order 200 responses against cache races in share and web clients", async () => {
    const body1 = JSON.stringify({ rev: 1 });
    const body2 = JSON.stringify({ rev: 2 });
    const bodyStale = JSON.stringify({ rev: 0 });

    // Seed share snapshot cache with rev 1 / etag-1
    const dReqA = createDeferred<Response>();
    const dReqB = createDeferred<Response>();
    const shareFetch = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse(body1, { status: 200, headers: { ETag: '"etag-1"' } }),
      )
      .mockImplementationOnce(() => dReqA.promise)
      .mockImplementationOnce(() => dReqB.promise);
    globalThis.fetch = shareFetch as unknown as typeof fetch;

    expect(await colabOneWorkflowShareSnapshotRaw()).toBe(body1);

    // Start two concurrent snapshot polls while cache holds etag-1 / body1
    const pA = colabOneWorkflowShareSnapshotRaw();
    const pB = colabOneWorkflowShareSnapshotRaw();

    // Req B completes first with newer rev 2 / etag-2
    dReqB.resolve(
      makeResponse(body2, { status: 200, headers: { ETag: '"etag-2"' } }),
    );
    expect(await pB).toBe(body2);

    // Req A completes second with 304 for its sent etag-1; it must return body1 (not body2)
    dReqA.resolve(makeResponse("", { status: 304 }));
    expect(await pA).toBe(body1);

    // Out-of-order 200 must not overwrite newer cached ETag
    const dOld = createDeferred<Response>();
    const dNew = createDeferred<Response>();
    const nextCalls: Array<Record<string, string>> = [];
    globalThis.fetch = vi
      .fn()
      .mockImplementationOnce(() => dOld.promise)
      .mockImplementationOnce(() => dNew.promise)
      .mockImplementationOnce((_url: string, init?: RequestInit) => {
        nextCalls.push((init?.headers as Record<string, string>) || {});
        return Promise.resolve(makeResponse("", { status: 304 }));
      }) as unknown as typeof fetch;

    const pOld = colabOneWorkflowShareSnapshotRaw();
    const pNew = colabOneWorkflowShareSnapshotRaw();

    dNew.resolve(
      makeResponse(body2, { status: 200, headers: { ETag: '"etag-new"' } }),
    );
    expect(await pNew).toBe(body2);

    dOld.resolve(
      makeResponse(bodyStale, { status: 200, headers: { ETag: '"etag-old"' } }),
    );
    expect(await pOld).toBe(bodyStale);

    expect(await colabOneWorkflowShareSnapshotRaw()).toBe(body2);
    expect(nextCalls[0]["If-None-Match"]).toBe('"etag-new"');

    // Mirror test on web snapshot client
    resetColabOneWebSnapshotCache();
    colabOneWebSessionExpired.value = false;
    colabOneWebBackendReady.value = false;

    const wA = createDeferred<Response>();
    const wB = createDeferred<Response>();
    globalThis.fetch = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse(body1, { status: 200, headers: { ETag: '"web-1"' } }),
      )
      .mockImplementationOnce(() => wA.promise)
      .mockImplementationOnce(() => wB.promise) as unknown as typeof fetch;

    expect(await colabOneWebSnapshotRaw()).toBe(body1);
    const pwA = colabOneWebSnapshotRaw();
    const pwB = colabOneWebSnapshotRaw();
    wB.resolve(
      makeResponse(body2, { status: 200, headers: { ETag: '"web-2"' } }),
    );
    expect(await pwB).toBe(body2);
    wA.resolve(makeResponse("", { status: 304 }));
    expect(await pwA).toBe(body1);
  });

  it("clears cached ETag on 401 and prevents stale pre-401 responses from clearing expired state", async () => {
    const body1 = JSON.stringify({ rev: 1 });
    const dSlow200 = createDeferred<Response>();
    const dFast401 = createDeferred<Response>();
    const postRecoveryHeaders: Array<Record<string, string>> = [];

    globalThis.fetch = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse(body1, { status: 200, headers: { ETag: '"etag-live"' } }),
      )
      .mockImplementationOnce(() => dSlow200.promise)
      .mockImplementationOnce(() => dFast401.promise)
      .mockResolvedValueOnce(
        makeResponse(
          JSON.stringify({
            runtime: "web-share",
            version: "0.2.1",
            share_id: "share-new",
            title: "Recovered",
            allowed_workflow_ids: ["wf-1"],
          }),
        ),
      )
      .mockImplementationOnce((_url: string, init?: RequestInit) => {
        postRecoveryHeaders.push((init?.headers as Record<string, string>) || {});
        return Promise.resolve(makeResponse(body1, { status: 200 }));
      }) as unknown as typeof fetch;

    expect(await colabOneWorkflowShareSnapshotRaw()).toBe(body1);

    const pSlow = colabOneWorkflowShareSnapshotRaw();
    const p401 = colabOneWorkflowShareCommandRaw("comfyui.run", {});

    dFast401.resolve(makeResponse("Unauthorized", { status: 401 }));
    await expect(p401).rejects.toThrow("Unauthorized");
    expect(colabOneWorkflowShareSessionExpired.value).toBe(true);

    // Late 200 from pre-401 epoch must NOT resurrect session or repopulate cache
    dSlow200.resolve(
      makeResponse(body1, { status: 200, headers: { ETag: '"etag-stale"' } }),
    );
    expect(await pSlow).toBe(body1);
    expect(colabOneWorkflowShareSessionExpired.value).toBe(true);

    // Explicit capabilities check started after 401 clears expired state cleanly
    await fetchColabOneWorkflowShareCapabilities();
    expect(colabOneWorkflowShareSessionExpired.value).toBe(false);

    // Next snapshot must NOT send the stale cached ETag
    await colabOneWorkflowShareSnapshotRaw();
    expect(postRecoveryHeaders[0]["If-None-Match"]).toBeUndefined();
  });

  it("handles AbortSignal pre-flight and mid-error-read aborts, X-ColabOne-Error, and Content-Disposition edge cases", async () => {
    const outputId = "b".repeat(32);

    // Pre-aborted signals throw AbortError without calling fetch
    const fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
    const preAborted = new AbortController();
    preAborted.abort();
    await expect(
      colabOneWorkflowShareOutputPreviewURL(outputId, preAborted.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    await expect(
      colabOneWebOutputPreviewURL(outputId, preAborted.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(fetchSpy).not.toHaveBeenCalled();

    // Abort while reading error response text re-throws AbortError instead of HTTP status
    const midAbort = new AbortController();
    globalThis.fetch = vi.fn(async () => ({
      ok: false,
      status: 500,
      headers: new Headers(),
      text: async () => {
        midAbort.abort();
        throw new DOMException("Aborted", "AbortError");
      },
    })) as unknown as typeof fetch;
    await expect(
      colabOneWorkflowShareOutputPreviewURL(outputId, midAbort.signal),
    ).rejects.toMatchObject({ name: "AbortError" });

    // Decodes X-ColabOne-Error when body is HTML error page or empty
    globalThis.fetch = vi.fn(async () =>
      makeResponse("<!doctype html><html><body>Bad Gateway</body></html>", {
        status: 502,
        headers: {
          "X-ColabOne-Error": encodeURIComponent("上游服务暂不可用"),
        },
      }),
    ) as unknown as typeof fetch;
    await expect(colabOneWorkflowShareSnapshotRaw()).rejects.toThrow(
      "上游服务暂不可用",
    );

    // Content-Disposition parsing & sanitization edge cases
    const cases: Array<{ header: string; expected: string }> = [
      {
        header: 'attachment; xfilename="evil.txt"; filename="good.mp4"',
        expected: "good.mp4",
      },
      {
        header: "attachment; filename*=UTF-8'en-US'render%20clip.mp4",
        expected: "render clip.mp4",
      },
      {
        header: "attachment; filename=  'spaced.png'  ",
        expected: "spaced.png",
      },
      {
        header: 'attachment; filename="clean\x07\x1f\x7fname.png"',
        expected: "cleanname.png",
      },
      {
        header: 'attachment; filename="../../etc/passwd"',
        expected: "passwd",
      },
      {
        header: 'attachment; filename=".."',
        expected: `ColabOne-${outputId.slice(0, 8)}`,
      },
    ];
    for (const tc of cases) {
      globalThis.fetch = vi.fn(async () =>
        makeResponse("", {
          status: 200,
          headers: { "Content-Disposition": tc.header },
        }),
      ) as unknown as typeof fetch;
      expect(await colabOneWorkflowShareDownloadOutput(outputId)).toBe(
        tc.expected,
      );
    }
  });

  it("routes api.ts helpers to /api/share/* on /share path and propagates desktop IPC errors on state getters", async () => {
    const originalWindow = globalThis.window;
    const originalMyGo = globalWithMyGo.mygo;
    const outputId = "c".repeat(32);

    try {
      delete globalWithMyGo.mygo;
      colabOneWorkflowShareMode.value = false;
      Object.defineProperty(globalThis, "window", {
        value: { location: { pathname: "/share" } },
        configurable: true,
        writable: true,
      });

      const urls: string[] = [];
      globalThis.fetch = vi.fn(async (url: string | URL | Request) => {
        const s = String(url);
        urls.push(s);
        if (s === "/api/share/snapshot") {
          return makeResponse(JSON.stringify({ runtime: { state: "ready" } }));
        }
        if (s === "/api/share/command") {
          return makeResponse(JSON.stringify({ ok: true }));
        }
        return makeResponse("", {
          status: 200,
          headers: { "Content-Disposition": 'attachment; filename="out.png"' },
        });
      }) as unknown as typeof fetch;

      await colabOneSnapshotRaw();
      await colabOneCommand("comfyui.run", { workflow_id: "wf-1" });
      expect(await colabOneOutputPreviewURL(outputId)).toBe(
        `/api/share/outputs/${outputId}/media`,
      );
      expect(await colabOneExportWorkflowOutput(outputId)).toBe("out.png");

      expect(urls).toEqual([
        "/api/share/snapshot",
        "/api/share/command",
        `/api/share/outputs/${outputId}/media`,
        `/api/share/outputs/${outputId}/download`,
      ]);

      // Non-desktop runtime returns empty defaults cleanly
      expect(await colabOneGetWorkflowSharesState()).toEqual(
        createColabOneDefaultWorkflowShareListState(),
      );
      expect((await colabOneGetWebConsoleState()).enabled).toBe(false);

      // Desktop runtime propagates IPC failures instead of wiping live state
      globalWithMyGo.mygo = {
        callWith: vi.fn().mockRejectedValue(new Error("ipc timeout")),
      };
      await expect(colabOneGetWorkflowSharesState()).rejects.toThrow(
        "ipc timeout",
      );
      await expect(colabOneGetWebConsoleState()).rejects.toThrow("ipc timeout");
    } finally {
      globalWithMyGo.mygo = originalMyGo;
      Object.defineProperty(globalThis, "window", {
        value: originalWindow,
        configurable: true,
        writable: true,
      });
    }
  });
});

describe("comfyDrafts isolation in workflow share mode", () => {
  afterEach(() => {
    colabOneWorkflowShareMode.value = false;
    colabOneWorkflowShareId.value = "";
  });

  it("stores drafts under share-scoped localStorage key and never calls comfyui.draft.* backend commands", async () => {
    const storage = createMemoryStorage();
    storage.setItem(
      COLABONE_STORAGE_KEYS.comfyDrafts,
      JSON.stringify({
        "wf-1": {
          workflow_id: "wf-1",
          fields: { prompt: "host-secret-prompt" },
          updated_at: 100,
        },
      }),
    );

    const invoker = vi.fn().mockRejectedValue(new Error("must not call backend"));
    const client = createColabOneWorkflowDraftClient(invoker, storage, {
      allowLocalFallback: false,
    });

    colabOneWorkflowShareMode.value = true;
    colabOneWorkflowShareId.value = "share-alpha";

    // Host drafts must remain invisible to the guest share.
    expect(await client.loadColabOneWorkflowDraft("wf-1")).toBeNull();

    // Saving a guest draft writes strictly under colabone.share.drafts.share-alpha.
    const saved = await client.saveColabOneWorkflowDraft("wf-1", {
      prompt: "guest-alpha-prompt",
      steps: 12,
    });
    expect(saved?.fields).toEqual({
      prompt: "guest-alpha-prompt",
      steps: 12,
    });

    const alphaKey = `${COLABONE_STORAGE_KEYS.workflowShareDraftsPrefix}share-alpha`;
    expect(storage.getItem(alphaKey)).toContain("guest-alpha-prompt");
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toContain(
      "host-secret-prompt",
    );

    const loaded = await client.loadColabOneWorkflowDraft("wf-1");
    expect(loaded?.fields).toEqual({
      prompt: "guest-alpha-prompt",
      steps: 12,
    });

    // Different share IDs are isolated from each other.
    colabOneWorkflowShareId.value = "share-beta";
    expect(await client.loadColabOneWorkflowDraft("wf-1")).toBeNull();

    // Media draft operations gracefully no-op without calling backend.
    colabOneWorkflowShareId.value = "share-alpha";
    expect(
      await client.saveColabOneWorkflowDraftMedia("wf-1", "1.image", {
        filename: "guest.png",
        kind: "image",
        data: "Zm9v",
      }),
    ).toBeNull();
    expect(
      await client.readColabOneWorkflowDraftMedia("wf-1", "1.image"),
    ).toBeNull();
    await expect(
      client.deleteColabOneWorkflowDraftMedia("wf-1", "1.image"),
    ).resolves.toBeUndefined();

    // Clearing guest draft removes only share-alpha entry and keeps host draft intact.
    await client.clearColabOneWorkflowDraft("wf-1");
    expect(storage.getItem(alphaKey)).toBeNull();
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toContain(
      "host-secret-prompt",
    );

    expect(invoker).not.toHaveBeenCalled();
  });

  it("no-ops cleanly when shareId is empty without writing a default bucket", async () => {
    const storage = createMemoryStorage();
    const invoker = vi.fn().mockRejectedValue(new Error("must not call backend"));
    const client = createColabOneWorkflowDraftClient(invoker, storage, {
      allowLocalFallback: false,
    });

    colabOneWorkflowShareMode.value = true;
    colabOneWorkflowShareId.value = "   ";

    expect(await client.loadColabOneWorkflowDraft("wf-1")).toBeNull();
    expect(
      await client.saveColabOneWorkflowDraft("wf-1", { prompt: "pending" }),
    ).toBeNull();
    await expect(
      client.clearColabOneWorkflowDraft("wf-1"),
    ).resolves.toBeUndefined();

    expect(storage.length).toBe(0);
    expect(
      storage.getItem(`${COLABONE_STORAGE_KEYS.workflowShareDraftsPrefix}default`),
    ).toBeNull();
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toBeNull();
    expect(invoker).not.toHaveBeenCalled();
  });

  it("captures share mode and storageKey synchronously so queued writes on unmount never leak to host", async () => {
    const storage = createMemoryStorage();
    storage.setItem(
      COLABONE_STORAGE_KEYS.comfyDrafts,
      JSON.stringify({
        "wf-1": {
          workflow_id: "wf-1",
          fields: { prompt: "host-intact" },
          updated_at: 50,
        },
      }),
    );

    const blockHost = createDeferred<unknown>();
    const invoker = vi.fn(() => blockHost.promise);
    const client = createColabOneWorkflowDraftClient(invoker, storage, {
      allowLocalFallback: false,
    });

    // Block queue with an initial host save so subsequent guest saves queue behind it.
    colabOneWorkflowShareMode.value = false;
    const pendingHost = client.saveColabOneWorkflowDraft("wf-1", {
      prompt: "host-intact",
    });

    // Enter share mode and enqueue guest save + media ops + clear, then immediately unmount.
    colabOneWorkflowShareMode.value = true;
    colabOneWorkflowShareId.value = "share-unmount";

    const queuedGuestSave = client.saveColabOneWorkflowDraft("wf-1", {
      prompt: "guest-queued",
    });
    const queuedGuestMedia = client.saveColabOneWorkflowDraftMedia(
      "wf-1",
      "1.image",
      { filename: "x.png", kind: "image", data: "Zm9v" },
    );
    const queuedGuestReadMedia = client.readColabOneWorkflowDraftMedia(
      "wf-1",
      "1.image",
    );
    const queuedGuestDeleteMedia = client.deleteColabOneWorkflowDraftMedia(
      "wf-1",
      "1.image",
    );

    // Simulate guest unmount flipping reactive refs BEFORE queued tasks execute.
    colabOneWorkflowShareMode.value = false;
    colabOneWorkflowShareId.value = "";

    blockHost.resolve({
      draft: {
        workflow_id: "wf-1",
        fields: { prompt: "host-intact" },
        media: {},
        updated_at: 51,
      },
    });
    await pendingHost;
    const savedGuest = await queuedGuestSave;
    expect(savedGuest?.fields).toEqual({ prompt: "guest-queued" });
    expect(await queuedGuestMedia).toBeNull();
    expect(await queuedGuestReadMedia).toBeNull();
    await expect(queuedGuestDeleteMedia).resolves.toBeUndefined();

    // Only the initial host save called invoker; zero guest ops touched host backend or storage!
    expect(invoker).toHaveBeenCalledTimes(1);
    expect(
      storage.getItem(
        `${COLABONE_STORAGE_KEYS.workflowShareDraftsPrefix}share-unmount`,
      ),
    ).toContain("guest-queued");
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).toContain(
      "host-intact",
    );
    expect(storage.getItem(COLABONE_STORAGE_KEYS.comfyDrafts)).not.toContain(
      "guest-queued",
    );
  });

  it("prunes fashion photo-role metadata when persisted media entry has an empty filename", () => {
    const fields: WorkflowField[] = [
      {
        key: "1.image",
        label: "Model 1",
        node_id: "1",
        input_name: "image",
        field_type: "image",
        default: "",
      },
      {
        key: COLABONE_FASHION_MODEL_ROLES_KEY,
        label: "Model roles",
        node_id: "0",
        input_name: "roles",
        field_type: "text",
        default: "",
      },
    ];
    const rolesMeta = serializeColabOneFashionPhotoMeta({
      order: ["1.image"],
      roles: { "1.image": "front" },
    });
    const merged = mergeColabOneWorkflowDraftFields(
      fields,
      { [COLABONE_FASHION_MODEL_ROLES_KEY]: rolesMeta },
      "en-US",
      {
        "1.image": {
          id: "a".repeat(32),
          filename: "",
          kind: "image",
          mime: "image/png",
          size: 0,
          sha256: "",
          updated_at: 0,
        },
      },
    );
    expect(merged[COLABONE_FASHION_MODEL_ROLES_KEY]).toBe("");
  });
});

describe("share-mode locale sync guard", () => {
  const originalWindow = globalThis.window;
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    colabOneWorkflowShareMode.value = false;
    Object.defineProperty(globalThis, "window", {
      value: originalWindow,
      configurable: true,
      writable: true,
    });
    vi.stubGlobal("fetch", originalFetch);
  });

  it("never calls backend locale.set and isolates localStorage key in workflow share mode", () => {
    const fetchMock = vi.fn();
    const storage = createMemoryStorage();
    storage.setItem(COLABONE_STORAGE_KEYS.locale, "zh-CN");

    vi.stubGlobal("fetch", fetchMock);
    Object.defineProperty(globalThis, "window", {
      value: {
        location: { pathname: "/share" },
        localStorage: storage,
      },
      configurable: true,
      writable: true,
    });

    // Even before colabOneWorkflowShareMode is toggled, /share route skips backend locale.set.
    colabOneWorkflowShareMode.value = false;
    syncColabOneLocaleToNative("en-US", false);
    expect(fetchMock).not.toHaveBeenCalled();

    // Switching locale on /share writes colabone.share.locale without mutating host colabone.locale.
    setColabOneLocale("ja-JP", { persist: true, syncNative: true });
    expect(storage.getItem(COLABONE_STORAGE_KEYS.workflowShareLocale)).toBe("ja-JP");
    expect(storage.getItem(COLABONE_STORAGE_KEYS.locale)).toBe("zh-CN");
    expect(readStoredColabOneLocale()).toBe("ja-JP");
    expect(fetchMock).not.toHaveBeenCalled();

    // And when colabOneWorkflowShareMode is active, switching locale also skips backend locale.set.
    Object.defineProperty(globalThis, "window", {
      value: {
        location: { pathname: "/" },
        localStorage: storage,
      },
      configurable: true,
      writable: true,
    });
    colabOneWorkflowShareMode.value = true;
    syncColabOneLocaleToNative("ko-KR", true);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("api.ts desktop-only web runtime guards", () => {
  const originalMyGo = globalWithMyGo.mygo;

  afterEach(() => {
    globalWithMyGo.mygo = originalMyGo;
  });

  it("rejects desktop-only migration, updater, and close-behavior mutators outside desktop runtime", async () => {
    delete globalWithMyGo.mygo;
    const expected = "该桌面专属操作仅支持在宿主机客户端执行";

    await expect(colabOneGetMigrationExportSummary(null)).rejects.toThrow(expected);
    await expect(
      colabOneExportMigrationFile({ modules: ["settings"], password: "secret-password" }),
    ).rejects.toThrow(expected);
    await expect(colabOnePickMigrationFile()).rejects.toThrow(expected);
    await expect(
      colabOneDecryptMigrationPreview("tok", "secret-password"),
    ).rejects.toThrow(expected);
    await expect(
      colabOneApplyMigrationPreview({ preview_id: "p1", modules: ["settings"] }),
    ).rejects.toThrow(expected);
    await expect(colabOneCancelMigrationSession("s1")).rejects.toThrow(expected);

    await expect(colabOneCheckForUpdate(true)).rejects.toThrow(expected);
    await expect(colabOneStartUpdateInstall()).rejects.toThrow(expected);
    await expect(colabOneListRollbackVersions(false)).rejects.toThrow(expected);
    await expect(colabOneStartUpdateRollback("0.1.0")).rejects.toThrow(expected);
    await expect(colabOneCancelUpdateInstall()).rejects.toThrow(expected);
    await expect(colabOneSkipUpdateVersion("0.2.0")).rejects.toThrow(expected);
    await expect(colabOneSetUpdatePreferences(true, false)).rejects.toThrow(expected);
    await expect(colabOneRelaunchForUpdate()).rejects.toThrow(expected);

    await expect(colabOneSetCloseBehavior("quit")).rejects.toThrow(expected);
    await expect(colabOneResolveCloseRequest("quit", false)).rejects.toThrow(expected);
  });
});
