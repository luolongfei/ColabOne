import { describe, expect, it } from "vitest";
import { COLABONE_APP_VERSION } from "./colabOneVersion";
import {
  COLABONE_RELEASES_URL,
  COLABONE_UPDATE_SEEN_VERSION_STORAGE_KEY,
  createColabOneUpdateState,
  formatColabOneUpdateBytes,
  formatColabOneUpdateSpeed,
  hasColabOneActiveUpdatePill,
  hasColabOneBilingualNotes,
  hasColabOneUnreadUpdateDot,
  hasColabOneVisibleUpdateBadge,
  normalizeColabOneUpdateState,
  normalizeColabOneUpdateVersion,
  parseColabOneReleaseNotes,
  resolveColabOneUpdateErrorKey,
  selectColabOneUpdateNotes,
  splitColabOneInlineCode,
  type ColabOneNoteBlock,
  type ColabOneNoteInline,
} from "./colabOneUpdater";

describe("colabOneUpdater", () => {
  describe("createColabOneUpdateState", () => {
    it("exports the canonical ColabOne releases URL and returns safe defaults", () => {
      expect(COLABONE_RELEASES_URL).toBe(
        "https://github.com/luolongfei/ColabOne/releases",
      );

      const state = createColabOneUpdateState();
      expect(state).toEqual({
        status: "idle",
        current_version: COLABONE_APP_VERSION,
        available_version: "",
        is_prerelease: false,
        release_date: "",
        release_url: COLABONE_RELEASES_URL,
        notes_raw: "",
        notes_zh: "",
        notes_en: "",
        downloaded_bytes: 0,
        total_bytes: 0,
        progress_percent: 0,
        speed_bps: 0,
        can_self_update: false,
        disabled_reason: "",
        auto_check: true,
        include_prerelease: false,
        skipped_version: "",
        last_checked_at: 0,
        error_code: "",
        error_message: "",
      });
    });

    it("accepts a custom currentVersion and falls back when empty", () => {
      expect(createColabOneUpdateState("0.2.0").current_version).toBe("0.2.0");
      expect(createColabOneUpdateState("  0.2.1  ").current_version).toBe(
        "0.2.1",
      );
      expect(createColabOneUpdateState("").current_version).toBe(
        COLABONE_APP_VERSION,
      );
      expect(createColabOneUpdateState("   ").current_version).toBe(
        COLABONE_APP_VERSION,
      );
    });
  });

  describe("normalizeColabOneUpdateState", () => {
    it("returns safe defaults for null, undefined, or empty objects", () => {
      const defaults = createColabOneUpdateState();
      expect(normalizeColabOneUpdateState(null)).toEqual(defaults);
      expect(normalizeColabOneUpdateState(undefined)).toEqual(defaults);
      expect(normalizeColabOneUpdateState({})).toEqual(defaults);
    });

    it("fills missing fields, preserves explicit false booleans, and clamps progress_percent", () => {
      const input = {
        status: " downloading ",
        current_version: " 0.1.6 ",
        available_version: " 0.1.7 ",
        is_prerelease: true,
        release_date: "2026-11-24T00:00:00Z",
        release_url: "",
        notes_zh: "## 新增功能\n- 自动更新",
        downloaded_bytes: 12_582_912,
        total_bytes: 50_331_648,
        progress_percent: 145.2,
        speed_bps: 4_404_019,
        can_self_update: true,
        auto_check: false,
        include_prerelease: true,
        skipped_version: " 0.1.5 ",
        last_checked_at: 1_760_000_000,
      };
      const normalized = normalizeColabOneUpdateState(input);

      expect(normalized).toEqual({
        status: "downloading",
        current_version: "0.1.6",
        available_version: "0.1.7",
        is_prerelease: true,
        release_date: "2026-11-24T00:00:00Z",
        release_url: COLABONE_RELEASES_URL,
        notes_raw: "",
        notes_zh: "## 新增功能\n- 自动更新",
        notes_en: "",
        downloaded_bytes: 12_582_912,
        total_bytes: 50_331_648,
        progress_percent: 100,
        speed_bps: 4_404_019,
        can_self_update: true,
        disabled_reason: "",
        auto_check: false,
        include_prerelease: true,
        skipped_version: "0.1.5",
        last_checked_at: 1_760_000_000,
        error_code: "",
        error_message: "",
      });
    });

    it("clamps negative or non-finite numeric fields safely", () => {
      expect(
        normalizeColabOneUpdateState({ progress_percent: -10 })
          .progress_percent,
      ).toBe(0);
      expect(
        normalizeColabOneUpdateState({ progress_percent: 42.5 })
          .progress_percent,
      ).toBe(42.5);
      expect(
        normalizeColabOneUpdateState({ progress_percent: Number.NaN })
          .progress_percent,
      ).toBe(0);
      expect(
        normalizeColabOneUpdateState({
          progress_percent: Number.POSITIVE_INFINITY,
        }).progress_percent,
      ).toBe(0);

      const sanitized = normalizeColabOneUpdateState({
        status: "   ",
        current_version: "",
        downloaded_bytes: -500,
        total_bytes: Number.NaN,
        speed_bps: Number.NEGATIVE_INFINITY,
        last_checked_at: -1,
      });
      expect(sanitized.status).toBe("idle");
      expect(sanitized.current_version).toBe(COLABONE_APP_VERSION);
      expect(sanitized.downloaded_bytes).toBe(0);
      expect(sanitized.total_bytes).toBe(0);
      expect(sanitized.speed_bps).toBe(0);
      expect(sanitized.last_checked_at).toBe(0);
    });
  });

  describe("parseColabOneReleaseNotes", () => {
    it("returns an empty array for empty or whitespace-only markdown", () => {
      expect(parseColabOneReleaseNotes("")).toEqual([]);
      expect(parseColabOneReleaseNotes("   \n\r\n   ")).toEqual([]);
    });

    it("parses headings, dash and bullet items, and paragraphs while skipping blanks and separators", () => {
      const markdown = [
        "## **新增功能**",
        "- 新增 **应用内自动更新**，支持 `Ed25519` 签名校验",
        "  • 支持中文 / 英文双语更新日志切换",
        "",
        "### Details",
        "---",
        "***",
        "___",
        "Regular **summary** line with `colabone` CLI mention.",
        "- **Fix `max_tokens` overflow** in gateway",
        "* Standard markdown star bullet",
        "",
        "##",
        "-",
      ].join("\r\n");

      const expected: ColabOneNoteBlock[] = [
        { kind: "heading", text: "新增功能" },
        { kind: "item", text: "新增 应用内自动更新，支持 `Ed25519` 签名校验" },
        { kind: "item", text: "支持中文 / 英文双语更新日志切换" },
        { kind: "heading", text: "Details" },
        {
          kind: "paragraph",
          text: "Regular summary line with `colabone` CLI mention.",
        },
        { kind: "item", text: "Fix `max_tokens` overflow in gateway" },
        { kind: "item", text: "Standard markdown star bullet" },
      ];

      expect(parseColabOneReleaseNotes(markdown)).toEqual(expected);
    });

    it("leaves unmatched bold markers and literal angle brackets as plain text without HTML", () => {
      const blocks = parseColabOneReleaseNotes(
        "Use 2 ** 3 and escape <script>alert(1)</script> cleanly.",
      );
      expect(blocks).toEqual([
        {
          kind: "paragraph",
          text: "Use 2 ** 3 and escape <script>alert(1)</script> cleanly.",
        },
      ]);
    });
  });

  describe("splitColabOneInlineCode", () => {
    it("returns an empty array for empty strings and plain segment when no backticks exist", () => {
      expect(splitColabOneInlineCode("")).toEqual([]);
      expect(splitColabOneInlineCode("Plain release text")).toEqual([
        { text: "Plain release text", code: false },
      ]);
    });

    it("splits single, edge, multiple, and adjacent inline code spans", () => {
      const middle: ColabOneNoteInline[] = splitColabOneInlineCode(
        "Verify `Ed25519` signature",
      );
      expect(middle).toEqual([
        { text: "Verify ", code: false },
        { text: "Ed25519", code: true },
        { text: " signature", code: false },
      ]);

      expect(splitColabOneInlineCode("`v0.1.7` released")).toEqual([
        { text: "v0.1.7", code: true },
        { text: " released", code: false },
      ]);

      expect(splitColabOneInlineCode("Open `/v1/models`")).toEqual([
        { text: "Open ", code: false },
        { text: "/v1/models", code: true },
      ]);

      expect(splitColabOneInlineCode("`only-code`")).toEqual([
        { text: "only-code", code: true },
      ]);

      expect(splitColabOneInlineCode("Map `a` to `b` (`c``d`)")).toEqual([
        { text: "Map ", code: false },
        { text: "a", code: true },
        { text: " to ", code: false },
        { text: "b", code: true },
        { text: " (", code: false },
        { text: "c", code: true },
        { text: "d", code: true },
        { text: ")", code: false },
      ]);
    });

    it("handles unmatched backticks and empty backtick pairs safely as plain text", () => {
      expect(splitColabOneInlineCode("Unclosed `backtick here")).toEqual([
        { text: "Unclosed `backtick here", code: false },
      ]);
      expect(splitColabOneInlineCode("Empty `` pair")).toEqual([
        { text: "Empty `` pair", code: false },
      ]);
      expect(splitColabOneInlineCode("Valid `ok` then `dangling")).toEqual([
        { text: "Valid ", code: false },
        { text: "ok", code: true },
        { text: " then `dangling", code: false },
      ]);
    });
  });

  describe("selectColabOneUpdateNotes and hasColabOneBilingualNotes", () => {
    const bilingual = normalizeColabOneUpdateState({
      notes_raw: "## 新增功能\n- 中文\n\n---\n\n## New Features\n- English",
      notes_zh: "## 新增功能\n- 中文",
      notes_en: "## New Features\n- English",
    });

    it("selects full notes when showFull is true with zh/en fallbacks", () => {
      expect(selectColabOneUpdateNotes(bilingual, "en-US", true)).toBe(
        bilingual.notes_raw,
      );
      expect(
        selectColabOneUpdateNotes(
          normalizeColabOneUpdateState({
            notes_raw: "",
            notes_zh: "中文日志",
            notes_en: "English notes",
          }),
          "en-US",
          true,
        ),
      ).toBe("中文日志");
      expect(
        selectColabOneUpdateNotes(
          normalizeColabOneUpdateState({
            notes_raw: "   ",
            notes_zh: "",
            notes_en: "English notes",
          }),
          "zh-CN",
          true,
        ),
      ).toBe("English notes");
    });

    it("selects localized notes for zh* vs non-zh locales with fallback chains", () => {
      expect(selectColabOneUpdateNotes(bilingual, "zh-CN", false)).toBe(
        bilingual.notes_zh,
      );
      expect(selectColabOneUpdateNotes(bilingual, "zh-TW", false)).toBe(
        bilingual.notes_zh,
      );
      expect(selectColabOneUpdateNotes(bilingual, "zh-HK", false)).toBe(
        bilingual.notes_zh,
      );
      expect(selectColabOneUpdateNotes(bilingual, "ZH-cn", false)).toBe(
        bilingual.notes_zh,
      );
      expect(selectColabOneUpdateNotes(bilingual, "en-US", false)).toBe(
        bilingual.notes_en,
      );
      expect(selectColabOneUpdateNotes(bilingual, "ja-JP", false)).toBe(
        bilingual.notes_en,
      );
      expect(selectColabOneUpdateNotes(bilingual, "ko-KR", false)).toBe(
        bilingual.notes_en,
      );

      expect(
        selectColabOneUpdateNotes(
          normalizeColabOneUpdateState({
            notes_zh: "",
            notes_raw: "raw fallback",
            notes_en: "en fallback",
          }),
          "zh-CN",
          false,
        ),
      ).toBe("raw fallback");

      expect(
        selectColabOneUpdateNotes(
          normalizeColabOneUpdateState({
            notes_en: "",
            notes_raw: "",
            notes_zh: "zh only",
          }),
          "en-US",
          false,
        ),
      ).toBe("zh only");

      expect(
        selectColabOneUpdateNotes(createColabOneUpdateState(), "en-US", false),
      ).toBe("");
    });

    it("detects bilingual notes only when zh and en are both non-empty and differ", () => {
      expect(hasColabOneBilingualNotes(bilingual)).toBe(true);
      expect(
        hasColabOneBilingualNotes(
          normalizeColabOneUpdateState({
            notes_zh: "## Changes\n- Same",
            notes_en: "  ## Changes\n- Same  ",
          }),
        ),
      ).toBe(false);
      expect(
        hasColabOneBilingualNotes(
          normalizeColabOneUpdateState({
            notes_zh: "中文",
            notes_en: "   ",
          }),
        ),
      ).toBe(false);
      expect(hasColabOneBilingualNotes(createColabOneUpdateState())).toBe(
        false,
      );
    });
  });

  describe("formatColabOneUpdateBytes and formatColabOneUpdateSpeed", () => {
    it("formats bytes across B, KB, MB, and GB ranges", () => {
      expect(formatColabOneUpdateBytes(0)).toBe("0 B");
      expect(formatColabOneUpdateBytes(-128)).toBe("0 B");
      expect(formatColabOneUpdateBytes(Number.NaN)).toBe("0 B");
      expect(formatColabOneUpdateBytes(Number.POSITIVE_INFINITY)).toBe("0 B");
      expect(formatColabOneUpdateBytes(512)).toBe("512 B");
      expect(formatColabOneUpdateBytes(1023)).toBe("1023 B");
      expect(formatColabOneUpdateBytes(1024)).toBe("1 KB");
      expect(formatColabOneUpdateBytes(512 * 1024)).toBe("512 KB");
      expect(formatColabOneUpdateBytes(12.4 * 1024 * 1024)).toBe("12.4 MB");
      expect(formatColabOneUpdateBytes(1.25 * 1024 * 1024 * 1024)).toBe(
        "1.25 GB",
      );
    });

    it("formats transfer speed and returns empty string for non-positive or invalid values", () => {
      expect(formatColabOneUpdateSpeed(0)).toBe("");
      expect(formatColabOneUpdateSpeed(-1024)).toBe("");
      expect(formatColabOneUpdateSpeed(Number.NaN)).toBe("");
      expect(formatColabOneUpdateSpeed(Number.POSITIVE_INFINITY)).toBe("");
      expect(formatColabOneUpdateSpeed(800)).toBe("800 B/s");
      expect(formatColabOneUpdateSpeed(512 * 1024)).toBe("512 KB/s");
      expect(formatColabOneUpdateSpeed(4.2 * 1024 * 1024)).toBe("4.2 MB/s");
      expect(formatColabOneUpdateSpeed(1.25 * 1024 * 1024 * 1024)).toBe(
        "1.25 GB/s",
      );
    });
  });

  describe("resolveColabOneUpdateErrorKey", () => {
    it("maps known error codes, empty string, and defaults to unknown", () => {
      expect(resolveColabOneUpdateErrorKey("")).toBe("");
      expect(resolveColabOneUpdateErrorKey("   ")).toBe("");
      expect(resolveColabOneUpdateErrorKey("proxy")).toBe("update.errProxy");
      expect(resolveColabOneUpdateErrorKey("timeout")).toBe("update.errTimeout");
      expect(resolveColabOneUpdateErrorKey("tls")).toBe("update.errTls");
      expect(resolveColabOneUpdateErrorKey("network")).toBe("update.errNetwork");
      expect(resolveColabOneUpdateErrorKey("not_found")).toBe(
        "update.errNotFound",
      );
      expect(resolveColabOneUpdateErrorKey("rate_limited")).toBe(
        "update.errRateLimited",
      );
      expect(resolveColabOneUpdateErrorKey("signature")).toBe(
        "update.errSignature",
      );
      expect(resolveColabOneUpdateErrorKey("permission")).toBe(
        "update.errPermission",
      );
      expect(resolveColabOneUpdateErrorKey(" NETWORK ")).toBe(
        "update.errNetwork",
      );
      expect(resolveColabOneUpdateErrorKey("unknown")).toBe("update.errUnknown");
      expect(resolveColabOneUpdateErrorKey("cancelled")).toBe(
        "update.errUnknown",
      );
      expect(resolveColabOneUpdateErrorKey("something_else")).toBe(
        "update.errUnknown",
      );
    });
  });

  describe("hasColabOneVisibleUpdateBadge", () => {
    it("returns true for actionable/in-progress update states and false otherwise", () => {
      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({
            status: "available",
            available_version: "0.1.7",
          }),
        ),
      ).toBe(true);
      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({
            status: "available",
            available_version: "",
          }),
        ),
      ).toBe(false);
      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({
            status: "available",
            available_version: "   ",
          }),
        ),
      ).toBe(false);

      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({ status: "downloading" }),
        ),
      ).toBe(true);
      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({ status: "installing" }),
        ),
      ).toBe(true);
      expect(
        hasColabOneVisibleUpdateBadge(
          normalizeColabOneUpdateState({ status: "ready_to_restart" }),
        ),
      ).toBe(true);

      for (const status of [
        "idle",
        "checking",
        "up_to_date",
        "disabled",
        "error",
        "unexpected",
      ]) {
        expect(
          hasColabOneVisibleUpdateBadge(
            normalizeColabOneUpdateState({
              status,
              available_version: "0.1.7",
            }),
          ),
        ).toBe(false);
      }
    });
  });

  describe("hasColabOneUnreadUpdateDot and hasColabOneActiveUpdatePill", () => {
    it("shows the unread red dot only for unseen available versions and clears once seen", () => {
      expect(COLABONE_UPDATE_SEEN_VERSION_STORAGE_KEY).toBe(
        "colabone.update.seenVersion",
      );
      expect(normalizeColabOneUpdateVersion("  v0.1.7 ")).toBe("0.1.7");
      expect(normalizeColabOneUpdateVersion("V1.2.3-rc.1")).toBe("1.2.3-rc.1");

      const available = normalizeColabOneUpdateState({
        status: "available",
        available_version: "0.1.7",
      });
      expect(hasColabOneUnreadUpdateDot(available, "")).toBe(true);
      expect(hasColabOneUnreadUpdateDot(available, "0.1.6")).toBe(true);
      expect(hasColabOneUnreadUpdateDot(available, "0.1.7")).toBe(false);
      expect(hasColabOneUnreadUpdateDot(available, "v0.1.7")).toBe(false);

      const newer = normalizeColabOneUpdateState({
        status: "available",
        available_version: "0.1.8",
      });
      expect(hasColabOneUnreadUpdateDot(newer, "0.1.7")).toBe(true);

      const blankVersion = normalizeColabOneUpdateState({
        status: "available",
        available_version: "   ",
      });
      expect(hasColabOneUnreadUpdateDot(blankVersion, "")).toBe(false);

      for (const status of [
        "idle",
        "checking",
        "up_to_date",
        "downloading",
        "installing",
        "ready_to_restart",
        "disabled",
        "error",
      ]) {
        expect(
          hasColabOneUnreadUpdateDot(
            normalizeColabOneUpdateState({
              status,
              available_version: "0.1.7",
            }),
            "",
          ),
        ).toBe(false);
      }
    });

    it("shows the inline active status pill only during download, install, or ready_to_restart", () => {
      expect(
        hasColabOneActiveUpdatePill(
          normalizeColabOneUpdateState({
            status: "available",
            available_version: "0.1.7",
          }),
        ),
      ).toBe(false);
      expect(
        hasColabOneActiveUpdatePill(
          normalizeColabOneUpdateState({ status: "downloading" }),
        ),
      ).toBe(true);
      expect(
        hasColabOneActiveUpdatePill(
          normalizeColabOneUpdateState({ status: "installing" }),
        ),
      ).toBe(true);
      expect(
        hasColabOneActiveUpdatePill(
          normalizeColabOneUpdateState({ status: "ready_to_restart" }),
        ),
      ).toBe(true);
      expect(
        hasColabOneActiveUpdatePill(
          normalizeColabOneUpdateState({ status: "up_to_date" }),
        ),
      ).toBe(false);
    });
  });
});
