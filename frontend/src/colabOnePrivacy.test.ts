import { afterEach, describe, expect, it, vi } from "vitest";
import { highlightColabOneCode } from "./colabOneHighlight";
import {
  applyColabOnePrivacyToCodeLines,
  buildColabOnePrivacyContext,
  colabOneMaskPlainText,
  colabOnePrivacyActive,
  colabOnePrivacyContextKey,
  colabOnePrivacyDefault,
  colabOnePrivacyPeekResetTick,
  colabOneSynthesizeDomainInput,
  colabOneSynthesizeEmail,
  colabOneSynthesizeHost,
  initColabOnePrivacyState,
  isColabOnePublicHost,
  resetColabOnePrivacyPeeks,
  segmentColabOneDomainInput,
  segmentColabOnePrivacyText,
  segmentColabOneUrlText,
  setColabOnePrivacyActive,
  setColabOnePrivacyDefault,
  toggleColabOnePrivacyActive,
} from "./colabOnePrivacy";
import { COLABONE_STORAGE_KEYS } from "./storage";
import { createColabOneSnapshot } from "./types";

afterEach(() => {
  vi.unstubAllGlobals();
  setColabOnePrivacyDefault(false);
  setColabOnePrivacyActive(false);
});

describe("ColabOne privacy synthetic placeholders", () => {
  it("generates zero-secret length-matched email placeholders", () => {
    const raw = "alex.chen@gmail.com";
    const masked = colabOneSynthesizeEmail(raw);
    expect(masked).toContain("@");
    expect(masked.split("@")[1]).toContain(".");
    expect(masked.toLowerCase()).not.toContain("alex");
    expect(masked.toLowerCase()).not.toContain("gmail");
    expect(masked.length).toBeGreaterThanOrEqual(12);
    expect(masked.length).toBeLessThanOrEqual(26);
  });

  it("masks custom hostnames, ports, IPv4, IPv6, and proxy userinfo without leaking input", () => {
    const hostMasked = colabOneSynthesizeHost("colabone.acme.dev");
    expect(hostMasked.split(".")).toHaveLength(3);
    expect(hostMasked).not.toContain("colabone");
    expect(hostMasked).not.toContain("acme");

    const ipMasked = colabOneSynthesizeHost("192.168.10.22:7890");
    expect(ipMasked).toMatch(/^198\.51\.100\.42:\d{4}$/);
    expect(ipMasked).not.toContain("192.168");
    expect(ipMasked).not.toContain("7890");

    const authMasked = colabOneSynthesizeHost("alice:secret@proxy.myhome.net:1080");
    expect(authMasked).toContain("@");
    expect(authMasked).not.toContain("alice");
    expect(authMasked).not.toContain("secret");
    expect(authMasked).not.toContain("myhome");
    expect(authMasked).not.toContain("1080");

    const ipv6Masked = colabOneSynthesizeHost("[2606:4700::1111]:8443");
    expect(ipv6Masked).toContain("[2001:db8::");
    expect(ipv6Masked).not.toContain("2606");
  });

  it("preserves scheme and path shape when masking domain inputs", () => {
    const masked = colabOneSynthesizeDomainInput("https://colabone.acme.dev");
    expect(masked.startsWith("https://")).toBe(true);
    expect(masked).not.toContain("colabone");
    expect(masked).not.toContain("acme");

    const bare = colabOneSynthesizeDomainInput("comfy.myorg.dev");
    expect(bare.startsWith("https://")).toBe(false);
    expect(bare.split(".")).toHaveLength(3);
    expect(bare).not.toContain("myorg");

    const segs = segmentColabOneDomainInput("https://colabone.acme.dev/v1");
    expect(segs).toHaveLength(3);
    expect(segs[0]).toEqual({ text: "https://", masked: false });
    expect(segs[1].masked).toBe(true);
    expect(segs[1].original).toBe("colabone.acme.dev");
    expect(segs[1].text).not.toContain("colabone");
    expect(segs[2]).toEqual({ text: "/v1", masked: false });

    const bareSegs = segmentColabOneDomainInput("comfy.myorg.dev");
    expect(bareSegs).toHaveLength(1);
    expect(bareSegs[0].masked).toBe(true);
    expect(bareSegs[0].original).toBe("comfy.myorg.dev");
  });

  it("distinguishes public/placeholder hosts from private user hosts", () => {
    expect(isColabOnePublicHost("127.0.0.1")).toBe(true);
    expect(isColabOnePublicHost("localhost:7861")).toBe(true);
    expect(isColabOnePublicHost("YOUR-API-HOST")).toBe(true);
    expect(isColabOnePublicHost("api.example.com")).toBe(true);
    expect(isColabOnePublicHost("github.com")).toBe(true);
    expect(isColabOnePublicHost("huggingface.co")).toBe(true);
    expect(isColabOnePublicHost("one.dash.cloudflare.com")).toBe(true);

    expect(isColabOnePublicHost("colabone.acme.dev")).toBe(false);
    expect(isColabOnePublicHost("comfy.internal.dev")).toBe(false);
    expect(isColabOnePublicHost("192.168.1.50")).toBe(false);
  });
});

describe("ColabOne privacy text and URL segmentation", () => {
  it("masks only the host segment in custom URLs while keeping scheme and path crisp", () => {
    const segments = segmentColabOneUrlText(
      "https://colabone.acme.dev/v1/chat/completions",
    );
    expect(segments).toHaveLength(3);
    expect(segments[0]).toEqual({ text: "https://", masked: false });
    expect(segments[1].masked).toBe(true);
    expect(segments[1].text).not.toContain("colabone");
    expect(segments[1].original).toBe("colabone.acme.dev");
    expect(segments[2]).toEqual({
      text: "/v1/chat/completions",
      masked: false,
    });

    const schemeless = segmentColabOneUrlText("colabone.acme.dev/v1");
    expect(schemeless).toHaveLength(2);
    expect(schemeless[0].masked).toBe(true);
    expect(schemeless[0].original).toBe("colabone.acme.dev");
    expect(schemeless[1]).toEqual({ text: "/v1", masked: false });

    const placeholder = segmentColabOneUrlText("https://YOUR-API-HOST/v1");
    expect(placeholder).toEqual([
      { text: "https://YOUR-API-HOST/v1", masked: false },
    ]);
  });

  it("masks emails, custom domains, proxy URLs, IPs, GCP quota project IDs, and tokens while leaving CU balances and public docs untouched", () => {
    const snap = createColabOneSnapshot();
    snap.account.email = "alex.chen@gmail.com";
    snap.api_settings = {
      mode: "named",
      domain: "https://colabone.acme.dev",
      has_token: true,
      has_key: true,
    };
    snap.comfy_canvas_settings = {
      mode: "named",
      domain: "comfy.acme.dev",
      has_token: true,
    };
    snap.assign_proxy_settings = {
      enabled: true,
      has_proxy: true,
      masked_url: "socks5://***@10.24.0.8:1080",
      has_auth: true,
    };
    const ctx = buildColabOnePrivacyContext(snap);

    const samples = [
      {
        input: "请在浏览器中使用 alex.chen@gmail.com 勾选全部云盘权限",
        forbidden: ["alex.chen@gmail.com", "alex", "gmail"],
        kept: ["请在浏览器中使用", "勾选全部云盘权限"],
      },
      {
        input: "Account alice@github.com synced with balance 1975.28 CU",
        forbidden: ["alice@github.com"],
        kept: ["1975.28 CU"],
      },
      {
        input: "已保存分配代理（socks5://***@10.24.0.8:1080）",
        forbidden: ["10.24.0.8", "1080"],
        kept: ["已保存分配代理", "socks5://"],
      },
      {
        input: "分配代理连通性测试通过（http://***@proxy.home.lan:7890 · 42 ms）",
        forbidden: ["proxy.home.lan", "7890"],
        kept: ["42 ms", "http://"],
      },
      {
        input: "已确认免弹窗云盘配额项目 (colabone-drive-98213)，后续所有实例免弹窗自动挂载",
        forbidden: ["colabone-drive-98213"],
        kept: ["已确认免弹窗云盘配额项目", "后续所有实例免弹窗自动挂载"],
      },
      {
        input: "未能为免费 Drive 配额项目 (colabone-drive-2090a4cd4c) 启用 Drive API（HTTP 403 · PERMISSION_DENIED/SERVICE_DISABLED）",
        forbidden: ["colabone-drive-2090a4cd4c"],
        kept: ["未能为免费 Drive 配额项目", "HTTP 403"],
      },
      {
        input: "Verified zero-prompt Drive quota project (my-gcp-proj-42); future instances will mount automatically",
        forbidden: ["my-gcp-proj-42"],
        kept: ["Verified zero-prompt Drive quota project"],
      },
      {
        input: "무팝업 드라이브 할당량 프로젝트(kr-proj-77)를 확인했습니다.",
        forbidden: ["kr-proj-77"],
        kept: ["할당량 프로젝트"],
      },
      {
        input: "公网地址 https://colabone.acme.dev/v1 与画布 comfy.acme.dev 已就绪",
        forbidden: ["colabone.acme.dev", "comfy.acme.dev"],
        kept: ["https://", "/v1", "已就绪"],
      },
      {
        input: "Token leak guard: hf_AbCdEfGhIjKlMnOpQr and sk-1234567890abcdef1234",
        forbidden: ["hf_AbCdEfGhIjKlMnOpQr", "sk-1234567890abcdef1234"],
        kept: ["Token leak guard:"],
      },
    ];

    for (const sample of samples) {
      const rendered = colabOneMaskPlainText(sample.input, ctx);
      const segs = segmentColabOnePrivacyText(sample.input, ctx);
      expect(segs.some((s) => s.masked)).toBe(true);
      for (const secret of sample.forbidden) {
        expect(rendered).not.toContain(secret);
      }
      for (const keep of sample.kept) {
        expect(rendered).toContain(keep);
      }
    }

    // Public guide links, loopback addresses, and CU balances stay unmasked.
    const safeLine =
      "余额 1975.28 CU · 0.00 CU/小时 · 转发至 http://127.0.0.1:7861 · https://one.dash.cloudflare.com/";
    expect(segmentColabOnePrivacyText(safeLine, ctx)).toEqual([
      { text: safeLine, masked: false },
    ]);
  });

  it("handles scheme-prefixed single-label hosts and CJK punctuation between public URLs and emails without swallowing text", () => {
    // Regression: m[0].indexOf(authority) previously matched scheme "http" at index 0 for http://http:8080.
    const schemeCollision = segmentColabOnePrivacyText("代理 http://http:8080 异常");
    expect(schemeCollision).toHaveLength(3);
    expect(schemeCollision[0]).toEqual({ text: "代理 http://", masked: false });
    expect(schemeCollision[1].masked).toBe(true);
    expect(schemeCollision[1].original).toBe("http:8080");
    expect(schemeCollision[2]).toEqual({ text: " 异常", masked: false });

    // Regression: userinfo matching must not cross CJK/fullwidth punctuation from a public URL into a later email.
    const adjacent = segmentColabOnePrivacyText(
      "访问（https://github.com）联系alex.chen@gmail.com",
    );
    expect(adjacent).toHaveLength(2);
    expect(adjacent[0]).toEqual({
      text: "访问（https://github.com）联系",
      masked: false,
    });
    expect(adjacent[1].masked).toBe(true);
    expect(adjacent[1].original).toBe("alex.chen@gmail.com");
  });
});

describe("ColabOne privacy code-block token masking", () => {
  it("masks only the personal host across cURL, Python, and Codex snippets while keeping syntax intact", () => {
    const ctx = { hosts: ["colabone.acme.dev"] };
    const snippets: Array<{ lang: string; code: string }> = [
      {
        lang: "curl",
        code: `curl -N "https://colabone.acme.dev/v1/chat/completions" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY"`,
      },
      {
        lang: "python",
        code: `client = OpenAI(\n    base_url="https://colabone.acme.dev/v1",\n    api_key=os.environ["COLABONE_API_KEY"],\n)`,
      },
      {
        lang: "codex",
        code: `[model_providers.colabone]\nbase_url = "https://colabone.acme.dev/v1"\nenv_key = "COLABONE_API_KEY"`,
      },
    ];

    for (const { lang, code } of snippets) {
      const highlighted = highlightColabOneCode(code, lang);
      const maskedLines = applyColabOnePrivacyToCodeLines(
        highlighted,
        true,
        ctx,
      );
      const flatText = maskedLines
        .map((line) => line.map((t) => t.content).join(""))
        .join("\n");
      const maskedTokens = maskedLines
        .flat()
        .filter((tok) => tok.masked);

      expect(maskedTokens.length).toBe(1);
      expect(maskedTokens[0].original).toBe("colabone.acme.dev");
      expect(flatText).not.toContain("colabone.acme.dev");
      expect(flatText).toContain("https://");
      expect(flatText).toContain("/v1");
      expect(flatText).toContain("COLABONE_API_KEY");
    }

    // Generic placeholder URL stays unmasked.
    const placeholderHighlighted = highlightColabOneCode(
      `curl -N "https://YOUR-API-HOST/v1/chat/completions"`,
      "curl",
    );
    const placeholderMasked = applyColabOnePrivacyToCodeLines(
      placeholderHighlighted,
      true,
      ctx,
    );
    expect(placeholderMasked.flat().some((t) => t.masked)).toBe(false);
  });
});

describe("ColabOne privacy startup default vs session toggle separation", () => {
  it("keeps startup default and active session independent and syncs companion log windows", () => {
    const store = new Map<string, string>();
    const listeners = new Set<(ev: StorageEvent) => void>();

    vi.stubGlobal("window", {
      location: { search: "" },
      localStorage: {
        getItem: (key: string) => store.get(key) ?? null,
        setItem: (key: string, val: string) => {
          store.set(key, String(val));
        },
        removeItem: (key: string) => {
          store.delete(key);
        },
      },
      addEventListener: (type: string, fn: (ev: StorageEvent) => void) => {
        if (type === "storage") listeners.add(fn);
      },
    });

    // 1. Fresh launch with default OFF
    initColabOnePrivacyState({ isLogView: false });
    expect(colabOnePrivacyDefault.value).toBe(false);
    expect(colabOnePrivacyActive.value).toBe(false);

    // 2. Enabling startup default in Settings does NOT flip current session
    setColabOnePrivacyDefault(true);
    expect(colabOnePrivacyDefault.value).toBe(true);
    expect(store.get(COLABONE_STORAGE_KEYS.privacyMaskDefault)).toBe("1");
    expect(colabOnePrivacyActive.value).toBe(false);

    // 3. Restarting main window boots with default ON
    initColabOnePrivacyState({ isLogView: false });
    expect(colabOnePrivacyDefault.value).toBe(true);
    expect(colabOnePrivacyActive.value).toBe(true);

    // 4. Toggling quick button in current session turns OFF masking without changing default
    const next = toggleColabOnePrivacyActive();
    expect(next).toBe(false);
    expect(colabOnePrivacyActive.value).toBe(false);
    expect(colabOnePrivacyDefault.value).toBe(true);
    expect(store.get(COLABONE_STORAGE_KEYS.privacyMaskDefault)).toBe("1");

    // 5. Companion standalone log window inherits active session state (OFF), not startup default (ON)
    const logInit = initColabOnePrivacyState({ isLogView: true });
    expect(logInit.defaultEnabled).toBe(true);
    expect(logInit.active).toBe(false);

    // 6. Another restart of main window goes back to default ON
    const restartInit = initColabOnePrivacyState({ isLogView: false });
    expect(restartInit.active).toBe(true);

    // 7. Shared peek reset tick bumps when active mask state changes or reset helper is called
    const tickBefore = colabOnePrivacyPeekResetTick.value;
    setColabOnePrivacyActive(false);
    expect(colabOnePrivacyPeekResetTick.value).toBeGreaterThan(tickBefore);
    const tickAfterToggle = colabOnePrivacyPeekResetTick.value;
    resetColabOnePrivacyPeeks();
    expect(colabOnePrivacyPeekResetTick.value).toBe(tickAfterToggle + 1);

    // 8. Context key stays structurally identical across fresh snapshot objects with unchanged PII
    const keyA = colabOnePrivacyContextKey({
      emails: ["alex.chen@gmail.com"],
      hosts: ["colabone.acme.dev"],
    });
    const keyB = colabOnePrivacyContextKey({
      emails: ["alex.chen@gmail.com"],
      hosts: ["colabone.acme.dev"],
    });
    const keyC = colabOnePrivacyContextKey({
      emails: ["other@gmail.com"],
      hosts: ["colabone.acme.dev"],
    });
    expect(keyA).toBe(keyB);
    expect(keyA).not.toBe(keyC);
  });
});
