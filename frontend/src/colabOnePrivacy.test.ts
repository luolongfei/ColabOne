import { afterEach, describe, expect, it, vi } from "vitest";
import { highlightColabOneCode } from "./colabOneHighlight";
import {
  applyColabOnePrivacyToCodeLines,
  buildColabOnePrivacyContext,
  colabOneApplyPrivacyInputPaste,
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

  it("always masks workflow share keys and sensitive query tokens even on loopback and example hosts", () => {
    const shareToken = "co_wf_a1b2c3d4.nonce987654.tag123456";

    // Public placeholder host with ?k=co_wf_...
    const exampleShareUrl = `https://share.example.com/share?k=${shareToken}`;
    const exampleSegs = segmentColabOneUrlText(exampleShareUrl);
    const exampleRendered = exampleSegs.map((s) => s.text).join("");
    expect(exampleSegs.some((s) => s.masked && s.original === shareToken)).toBe(true);
    expect(exampleRendered).toContain("https://share.example.com/share?k=");
    expect(exampleRendered).not.toContain(shareToken);
    expect(exampleRendered).not.toContain("nonce987654");
    expect(exampleRendered).not.toContain("tag123456");

    // Loopback 127.0.0.1 with ?k=co_wf_...
    const loopbackShareUrl = `http://127.0.0.1:3232/share?k=${shareToken}`;
    const loopbackSegs = segmentColabOneUrlText(loopbackShareUrl);
    const loopbackRendered = loopbackSegs.map((s) => s.text).join("");
    expect(loopbackSegs.some((s) => s.masked && s.original === shareToken)).toBe(true);
    expect(loopbackRendered).toContain("http://127.0.0.1:3232/share?k=");
    expect(loopbackRendered).not.toContain(shareToken);

    // Private host + share key masks both host and share token
    const privateShareUrl = `https://share.acme.dev/share?k=${shareToken}&tab=run`;
    const privateSegs = segmentColabOneUrlText(privateShareUrl);
    const privateRendered = privateSegs.map((s) => s.text).join("");
    expect(privateSegs.some((s) => s.masked && s.original === "share.acme.dev")).toBe(true);
    expect(privateSegs.some((s) => s.masked && s.original === shareToken)).toBe(true);
    expect(privateRendered).not.toContain("share.acme.dev");
    expect(privateRendered).not.toContain(shareToken);
    expect(privateRendered).toContain("&tab=run");

    // Loopback/localhost ?ticket= and ?token= query params are masked
    const ticketUrl = "http://localhost:3232/_colabone_web/login?ticket=tkt_secret_9876&next=/";
    const ticketRendered = segmentColabOneUrlText(ticketUrl).map((s) => s.text).join("");
    expect(ticketRendered).toContain("http://localhost:3232/_colabone_web/login?ticket=");
    expect(ticketRendered).toContain("&next=/");
    expect(ticketRendered).not.toContain("tkt_secret_9876");

    const tokenUrl = "http://127.0.0.1:3232/_colabone_auth?token=tok_canvas_5566";
    const tokenRendered = segmentColabOneUrlText(tokenUrl).map((s) => s.text).join("");
    expect(tokenRendered).toContain("http://127.0.0.1:3232/_colabone_auth?token=");
    expect(tokenRendered).not.toContain("tok_canvas_5566");

    // Plain-text masking also covers both URLs and standalone co_wf_ tokens
    const logLine = `Opened ${exampleShareUrl} and ${loopbackShareUrl} (raw ${shareToken})`;
    const maskedLog = colabOneMaskPlainText(logLine);
    expect(maskedLog).not.toContain(shareToken);
    expect(maskedLog).not.toContain("nonce987654");
    expect(maskedLog).not.toContain("tag123456");
    expect(maskedLog).toContain("https://share.example.com/share?k=");
    expect(maskedLog).toContain("http://127.0.0.1:3232/share?k=");
  });

  it("always masks output gallery share keys (co_gal_) in URLs and plain text", () => {
    const dottedToken = "co_gal_a1b2c3d4.nonce987654.tag654321";
    const compactToken = "co_gal_9f8e7d6c5b4a3210";

    // Private host + /gallery?k=co_gal_... masks both host and share token
    const privateGalleryUrl = `https://host.example/gallery?k=${dottedToken}`;
    const privateSegs = segmentColabOneUrlText(privateGalleryUrl);
    const privateRendered = privateSegs.map((s) => s.text).join("");
    expect(privateSegs.some((s) => s.masked && s.original === "host.example")).toBe(true);
    expect(privateSegs.some((s) => s.masked && s.original === dottedToken)).toBe(true);
    expect(privateRendered).toContain("https://");
    expect(privateRendered).toContain("/gallery?k=");
    expect(privateRendered).not.toContain("host.example");
    expect(privateRendered).not.toContain(dottedToken);
    expect(privateRendered).not.toContain("nonce987654");
    expect(privateRendered).not.toContain("tag654321");

    // Loopback / public placeholder host still masks co_gal_ token
    const loopbackGalleryUrl = `http://127.0.0.1:3232/gallery?k=${compactToken}`;
    const loopbackSegs = segmentColabOneUrlText(loopbackGalleryUrl);
    const loopbackRendered = loopbackSegs.map((s) => s.text).join("");
    expect(loopbackSegs.some((s) => s.masked && s.original === compactToken)).toBe(true);
    expect(loopbackRendered).toContain("http://127.0.0.1:3232/gallery?k=");
    expect(loopbackRendered).not.toContain(compactToken);

    const examplePathUrl = `https://gallery.example.com/g/${dottedToken}`;
    const examplePathRendered = segmentColabOneUrlText(examplePathUrl)
      .map((s) => s.text)
      .join("");
    expect(examplePathRendered).toContain("https://gallery.example.com/g/");
    expect(examplePathRendered).not.toContain(dottedToken);

    // Plain-text logs mask both URL-embedded and standalone co_gal_ tokens
    const logLine = `Shared ${privateGalleryUrl} with fallback ${compactToken} (${dottedToken})`;
    const maskedLog = colabOneMaskPlainText(logLine);
    expect(maskedLog).not.toContain("host.example");
    expect(maskedLog).not.toContain(dottedToken);
    expect(maskedLog).not.toContain(compactToken);
    expect(maskedLog).toContain("/gallery?k=");

    // Base64url share tokens ending in "-" and URL-encoded query parameters (%3Dco_gal_ / %3Dco_wf_)
    const hyphenTrailGal = "co_gal_AbCdEf12-3456_78-";
    const hyphenTrailWf = "co_wf_ZyXwVu98-7654_32-";
    const encodedRedirect = `https://studio.example.com/login?next=%2Fgallery%3Fk%3D${hyphenTrailGal}&wf=%2Fshare%3Fk%3D${hyphenTrailWf}`;
    const maskedEncoded = colabOneMaskPlainText(encodedRedirect);
    expect(maskedEncoded).not.toContain("AbCdEf12");
    expect(maskedEncoded).not.toContain("3456_78-");
    expect(maskedEncoded).not.toContain("ZyXwVu98");
    expect(maskedEncoded).not.toContain("7654_32-");

    const standaloneHyphen = colabOneMaskPlainText(
      `keys=${hyphenTrailGal},${hyphenTrailWf}`,
    );
    expect(standaloneHyphen).not.toContain("AbCdEf12");
    expect(standaloneHyphen).not.toContain("3456_78-");
    expect(standaloneHyphen).not.toContain("ZyXwVu98");
    expect(standaloneHyphen).not.toContain("7654_32-");
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

  it("masks single-segment Cloudflare tunnel tokens, RFC-3986 sub-delim userinfo, and live_external_llm base_url hosts", () => {
    const cfToken = btoa(
      JSON.stringify({
        a: "account-123456",
        t: "tunnel-abcdef",
        s: "secret-xyz789",
      }),
    );
    const nonTunnelB64 = btoa(
      JSON.stringify({ hello: "world-not-a-cloudflare-token" }),
    );

    // Valid single-segment Cloudflare tunnel token is masked in plain text and URLs
    const maskedCf = colabOneMaskPlainText(`cloudflared --token ${cfToken}`);
    expect(maskedCf).not.toContain(cfToken);
    expect(maskedCf).toContain("cloudflared --token ");

    const maskedCfUrl = segmentColabOneUrlText(
      `https://api.example.com/install/${cfToken}`,
    )
      .map((s) => s.text)
      .join("");
    expect(maskedCfUrl).not.toContain(cfToken);
    expect(maskedCfUrl).toContain("https://api.example.com/install/");

    // Non-tunnel eyJ... base64 JSON is NOT falsely masked
    expect(colabOneMaskPlainText(`payload=${nonTunnelB64}`)).toBe(
      `payload=${nonTunnelB64}`,
    );

    // Userinfo containing RFC-3986 sub-delims (! $ & ' ( ) * + , ; =) is fully masked
    const proxyLine = "Proxy failed: http://alice:p!a$s&w(o)r+d,;=1@proxy.acme.dev:7890/test";
    const maskedProxy = colabOneMaskPlainText(proxyLine);
    expect(maskedProxy).not.toContain("alice");
    expect(maskedProxy).not.toContain("p!a$s&w(o)r+d,;=1");
    expect(maskedProxy).not.toContain("proxy.acme.dev");
    expect(maskedProxy).toContain("http://");
    expect(maskedProxy).toContain("/test");

    // buildColabOnePrivacyContext extracts live_external_llm.base_url host
    const snap = createColabOneSnapshot();
    snap.live_external_llm = {
      base_url: "https://llm.private-studio.dev/v1",
      model: "gpt-4o-mini",
      has_api_key: true,
      masked_api_key: "sk-****1234",
    };
    const ctx = buildColabOnePrivacyContext(snap);
    expect(ctx.hosts).toContain("llm.private-studio.dev");
    const maskedErr = colabOneMaskPlainText(
      "Upstream timeout contacting llm.private-studio.dev",
      ctx,
    );
    expect(maskedErr).not.toContain("llm.private-studio.dev");
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

describe("ColabOne privacy input paste helper", () => {
  it("replaces or splices pasted text onto the real value without leaking synthetic glyphs", () => {
    expect(
      colabOneApplyPrivacyInputPaste("comfy.old.dev", "comfy.new.dev", null),
    ).toEqual({
      value: "comfy.new.dev",
      caret: "comfy.new.dev".length,
    });

    expect(
      colabOneApplyPrivacyInputPaste("https://old.dev", "new", {
        start: 8,
        end: 11,
      }),
    ).toEqual({
      value: "https://new.dev",
      caret: 11,
    });

    expect(
      colabOneApplyPrivacyInputPaste("https://acme.dev", "sub.", {
        start: 8,
        end: 8,
      }),
    ).toEqual({
      value: "https://sub.acme.dev",
      caret: 12,
    });

    expect(
      colabOneApplyPrivacyInputPaste("abc", "xyz", {
        start: -5,
        end: 99,
      }),
    ).toEqual({
      value: "xyz",
      caret: 3,
    });
  });
});

describe("ColabOne privacy token, percent-encoded query, and domain tail masking", () => {
  it("masks Google OAuth tokens, refresh tokens, client secrets, and short/hyphenated HF tokens", () => {
    const oauthAccess = "ya29.a0AfH6SMC_alpha-12345.xyz";
    const oauthRefresh = "1//0gRefreshToken_12345678";
    const gcpSecret = "GOCSPX-MyClientSecret_9876";
    const hfShort = "hf_ab-cd_1234";

    const logLine = `access=${oauthAccess} refresh=${oauthRefresh} secret=${gcpSecret} hf=${hfShort}`;
    const masked = colabOneMaskPlainText(logLine);
    expect(masked).not.toContain(oauthAccess);
    expect(masked).not.toContain(oauthRefresh);
    expect(masked).not.toContain(gcpSecret);
    expect(masked).not.toContain(hfShort);
    expect(masked).toContain("access=");
    expect(masked).toContain("refresh=");
    expect(masked).toContain("secret=");
    expect(masked).toContain("hf=");
  });

  it("masks Cloudflare tunnel tokens and nested query secrets after percent-encoded delimiters", () => {
    const tunnelJson = JSON.stringify({ a: "acct123", t: "tun456", s: "sec789" });
    const tunnelToken = btoa(tunnelJson);
    const encodedRedirect = `https://console.acme.dev/login?next=%2Fgallery%3Fk%3Dmy_nested_key_42%26ticket%3Dtick_9988%26token%3D${tunnelToken}%26tab%3Dcomfy`;

    const maskedText = colabOneMaskPlainText(encodedRedirect);
    expect(maskedText).not.toContain("my_nested_key_42");
    expect(maskedText).not.toContain("tick_9988");
    expect(maskedText).not.toContain(tunnelToken);
    expect(maskedText).toContain("%3Fk%3D");
    expect(maskedText).toContain("%26ticket%3D");
    expect(maskedText).toContain("%26tab%3Dcomfy");

    const urlSegments = segmentColabOneUrlText(encodedRedirect);
    const joinedUrl = urlSegments.map((s) => s.text).join("");
    expect(joinedUrl).not.toContain("my_nested_key_42");
    expect(joinedUrl).not.toContain("tick_9988");
    expect(joinedUrl).not.toContain(tunnelToken);

    // Percent-encoded prefix directly before a Cloudflare tunnel token outside query param names
    const rawEncodedTunnel = `https://example.com/hook?payload=%3D${tunnelToken}&ok=1`;
    expect(colabOneMaskPlainText(rawEncodedTunnel)).not.toContain(tunnelToken);
  });

  it("masks sensitive tokens and query params inside domain input tails while keeping clean paths intact", () => {
    const clean = segmentColabOneDomainInput("https://colabone.acme.dev/v1");
    expect(clean.map((s) => s.masked)).toEqual([false, true, false]);
    expect(clean[2].text).toBe("/v1");

    const leaky = colabOneSynthesizeDomainInput(
      "https://colabone.acme.dev/gallery?k=co_gal_secret1234567890&token=ya29.a0AfH6SMC_secret999",
    );
    expect(leaky).not.toContain("colabone.acme.dev");
    expect(leaky).not.toContain("co_gal_secret1234567890");
    expect(leaky).not.toContain("ya29.a0AfH6SMC_secret999");

    const bareLeaky = colabOneSynthesizeDomainInput(
      "colabone.acme.dev/redirect?next=%2Fgallery%3Fk%3Dnested_secret_99",
    );
    expect(bareLeaky).not.toContain("colabone.acme.dev");
    expect(bareLeaky).not.toContain("nested_secret_99");
  });

  it("masks companion #code=, ?code=, and percent-encoded %23code%3D secrets in URLs and plain text", () => {
    const hashCodeUrl =
      "https://live.example.com/_colabone_live/remote#code=CompSecretHash123456";
    const hashRendered = segmentColabOneUrlText(hashCodeUrl)
      .map((s) => s.text)
      .join("");
    expect(hashRendered).toContain(
      "https://live.example.com/_colabone_live/remote#code=",
    );
    expect(hashRendered).not.toContain("CompSecretHash123456");

    const queryCodeUrl =
      "http://127.0.0.1:3232/_colabone_live/remote?code=CompSecretQuery98765&mode=mic";
    const queryRendered = segmentColabOneUrlText(queryCodeUrl)
      .map((s) => s.text)
      .join("");
    expect(queryRendered).toContain(
      "http://127.0.0.1:3232/_colabone_live/remote?code=",
    );
    expect(queryRendered).toContain("&mode=mic");
    expect(queryRendered).not.toContain("CompSecretQuery98765");

    const encodedFragment =
      "https://studio.example.com/login?next=%2F_colabone_live%2Fremote%23code%3DEncodedCompCode777%26tab%3Dlive";
    const maskedEncoded = colabOneMaskPlainText(encodedFragment);
    expect(maskedEncoded).toContain("%23code%3D");
    expect(maskedEncoded).toContain("%26tab%3Dlive");
    expect(maskedEncoded).not.toContain("EncodedCompCode777");

    const plainLog = `Companion QR opened for ${hashCodeUrl} and ${queryCodeUrl}`;
    const maskedPlain = colabOneMaskPlainText(plainLog);
    expect(maskedPlain).not.toContain("CompSecretHash123456");
    expect(maskedPlain).not.toContain("CompSecretQuery98765");
  });

  it("does not falsely mask Hugging Face library or env identifiers like hf_transfer or hf_hub_download", () => {
    const log =
      "加速下载失败，自动关闭 hf_transfer 并回退 hf_hub_download（cache: hf_cache_home, hf_download, hf_endpoint_url, hf_xet_enabled）";
    expect(colabOneMaskPlainText(log)).toBe(log);

    const withRealToken = `${log} token=hf_AbCdEfGhIjKlMnOpQr1234`;
    const masked = colabOneMaskPlainText(withRealToken);
    expect(masked).toContain("hf_transfer");
    expect(masked).toContain("hf_hub_download");
    expect(masked).not.toContain("hf_AbCdEfGhIjKlMnOpQr1234");
  });

  it("processes 100 KB pathological alphanumeric and slash-heavy strings in linear time without freezing", () => {
    const longAlnum = "a".repeat(100_000);
    const longSlashes = `eyJ${"a/".repeat(50_000)}`;
    const t0 = performance.now();
    expect(colabOneMaskPlainText(longAlnum)).toBe(longAlnum);
    expect(colabOneMaskPlainText(longSlashes)).toBe(longSlashes);
    const elapsedMs = performance.now() - t0;
    expect(elapsedMs).toBeLessThan(500);
  });
});
