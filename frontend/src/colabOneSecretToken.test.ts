import { describe, expect, it } from "vitest";
import {
  colabOneExtractHfToken,
  colabOneExtractTunnelToken,
  colabOneResolveSecretInput,
  colabOneResolveSecretPaste,
} from "./colabOneSecretToken";

function encodeBase64(obj: unknown, urlSafe = false, stripPadding = false): string {
  const bytes = new TextEncoder().encode(JSON.stringify(obj));
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  let out = btoa(binary);
  if (urlSafe) {
    out = out.replace(/\+/g, "-").replace(/\//g, "_");
  }
  if (stripPadding) {
    out = out.replace(/=+$/, "");
  }
  return out;
}

const VALID_TOKEN = encodeBase64({
  a: "account-123456",
  t: "tunnel-abcdef",
  s: "secret-xyz??>>",
});

const VALID_URLSAFE_TOKEN = encodeBase64(
  {
    a: "account-123456",
    t: "tunnel-abcdef",
    s: "secret-xyz??>>",
  },
  true,
  true,
);

const OTHER_TOKEN = encodeBase64({
  a: "account-999999",
  t: "tunnel-999999",
  s: "secret-999999",
});

describe("colabOneExtractTunnelToken", () => {
  it("extracts token from Linux, macOS, Windows, and Docker install commands", () => {
    const commands = [
      `sudo cloudflared service install ${VALID_TOKEN}`,
      `brew install cloudflared && sudo cloudflared service install ${VALID_TOKEN}`,
      `cloudflared.exe service install ${VALID_TOKEN}`,
      `docker run cloudflare/cloudflared:latest tunnel --no-autoupdate run --token ${VALID_TOKEN}`,
      `cloudflared tunnel run --token "${VALID_TOKEN}"`,
      `# Add cloudflare gpg key\nsudo apt-get install cloudflared\nsudo cloudflared service install ${VALID_TOKEN}\n`,
    ];

    for (const cmd of commands) {
      expect(colabOneExtractTunnelToken(cmd)).toEqual({
        value: VALID_TOKEN,
        changed: true,
        extractedFromWrapper: true,
      });
    }
  });

  it("accepts urlsafe base64 without padding and deduplicates repeated copies of the same token", () => {
    expect(
      colabOneExtractTunnelToken(
        `cloudflared service install ${VALID_URLSAFE_TOKEN}`,
      ),
    ).toEqual({
      value: VALID_URLSAFE_TOKEN,
      changed: true,
      extractedFromWrapper: true,
    });

    expect(
      colabOneExtractTunnelToken(`${VALID_TOKEN}\n${VALID_TOKEN}`),
    ).toEqual({
      value: VALID_TOKEN,
      changed: true,
      extractedFromWrapper: true,
    });
  });

  it("preserves a clean token without flagging wrapper extraction", () => {
    expect(colabOneExtractTunnelToken(VALID_TOKEN)).toEqual({
      value: VALID_TOKEN,
      changed: false,
      extractedFromWrapper: false,
    });

    expect(colabOneExtractTunnelToken(`  ${VALID_TOKEN}\n`)).toEqual({
      value: VALID_TOKEN,
      changed: true,
      extractedFromWrapper: false,
    });
  });

  it("rejects empty, invalid, incomplete, or ambiguous multi-token strings", () => {
    expect(colabOneExtractTunnelToken("")).toBeNull();
    expect(colabOneExtractTunnelToken("offline-placeholder")).toBeNull();
    expect(
      colabOneExtractTunnelToken(encodeBase64({ a: "only-a", t: "only-t" })),
    ).toBeNull();
    expect(
      colabOneExtractTunnelToken(encodeBase64({ a: "acc", t: "tun", s: "" })),
    ).toBeNull();
    expect(
      colabOneExtractTunnelToken(`${VALID_TOKEN} ${OTHER_TOKEN}`),
    ).toBeNull();
  });
});

describe("colabOneExtractHfToken", () => {
  it("extracts token from shell exports, CLI commands, Bearer headers, and quotes", () => {
    const token = "hf_AbCdEfGhIjKlMnOpQrStUvWxYz123456";
    const samples = [
      `export HF_TOKEN=${token}`,
      `export HUGGING_FACE_HUB_TOKEN="${token}"`,
      `huggingface-cli login --token ${token}`,
      `hf auth login --token '${token}'`,
      `Authorization: Bearer ${token}`,
    ];
    for (const sample of samples) {
      expect(colabOneExtractHfToken(sample)).toEqual({
        value: token,
        changed: true,
        extractedFromWrapper: true,
      });
    }
  });

  it("handles bare tokens, whitespace, and rejects ambiguous or non-matching strings", () => {
    const token = "hf_AbCdEfGhIjKlMnOpQrStUvWxYz123456";
    expect(colabOneExtractHfToken(token)).toEqual({
      value: token,
      changed: false,
      extractedFromWrapper: false,
    });
    expect(colabOneExtractHfToken(`  ${token}  \n`)).toEqual({
      value: token,
      changed: true,
      extractedFromWrapper: false,
    });
    expect(colabOneExtractHfToken(`${token} hf_AnotherToken123456`)).toBeNull();
    expect(colabOneExtractHfToken("my_hf_AbCdEfGhIjKlMnOp")).toBeNull();
    expect(colabOneExtractHfToken("hf_short")).toBeNull();
    // Non-secret Hugging Face library/env identifiers must not be treated as tokens.
    expect(colabOneExtractHfToken("hf_transfer")).toBeNull();
    expect(colabOneExtractHfToken("hf_download")).toBeNull();
    expect(colabOneExtractHfToken("hf_hub_download")).toBeNull();
    expect(colabOneExtractHfToken("hf_cache_home")).toBeNull();
    expect(colabOneExtractHfToken("hf_endpoint_url")).toBeNull();
    expect(colabOneExtractHfToken("hf_xet_enabled")).toBeNull();
  });
});

describe("colabOneResolveSecretPaste and colabOneResolveSecretInput", () => {
  it("replaces an existing revealed token cleanly when pasting a new command or token", () => {
    expect(
      colabOneResolveSecretPaste(
        OTHER_TOKEN,
        OTHER_TOKEN.length,
        OTHER_TOKEN.length,
        `sudo cloudflared service install ${VALID_TOKEN}`,
        colabOneExtractTunnelToken,
      ),
    ).toEqual({
      value: VALID_TOKEN,
      extractedFromWrapper: true,
    });
  });

  it("extracts token when completing a command across partial paste", () => {
    const half = Math.floor(VALID_TOKEN.length / 2);
    const prefix = `sudo cloudflared service install ${VALID_TOKEN.slice(0, half)}`;
    const suffix = VALID_TOKEN.slice(half);
    expect(
      colabOneResolveSecretPaste(
        prefix,
        prefix.length,
        prefix.length,
        suffix,
        colabOneExtractTunnelToken,
      ),
    ).toEqual({
      value: VALID_TOKEN,
      extractedFromWrapper: true,
    });
  });

  it("leaves non-token edits untouched so manual typing and error paths still work", () => {
    expect(
      colabOneResolveSecretPaste(
        "",
        0,
        0,
        "offline-placeholder",
        colabOneExtractTunnelToken,
      ),
    ).toBeNull();
    expect(
      colabOneResolveSecretInput(
        "offline-placeholder",
        colabOneExtractTunnelToken,
      ),
    ).toBeNull();
  });

  it("normalizes programmatic fill of a full Cloudflare install command", () => {
    expect(
      colabOneResolveSecretInput(
        `sudo cloudflared service install ${VALID_TOKEN}`,
        colabOneExtractTunnelToken,
      ),
    ).toEqual({
      value: VALID_TOKEN,
      extractedFromWrapper: true,
    });
  });
});
