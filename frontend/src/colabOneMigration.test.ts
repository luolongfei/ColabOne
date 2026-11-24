import { describe, expect, it, vi } from "vitest";
import {
  applyColabOneMigrationUIPrefs,
  COLABONE_MIGRATION_MODULES,
  collectColabOneMigrationUIPrefs,
  defaultImportModuleSelection,
  formatMigrationFileSize,
  formatMigrationSourceOS,
  validateColabOneExportPassword,
} from "./colabOneMigration";
import { COLABONE_STORAGE_KEYS } from "./storage";
import { translateColabOneActivitySource, translateColabOneBackendText } from "./i18n/backendMessages";

function createMemoryStorage(initial: Record<string, string> = {}) {
  const map = new Map<string, string>(Object.entries(initial));
  return {
    getItem: (k: string) => (map.has(k) ? map.get(k)! : null),
    setItem: (k: string, v: string) => {
      map.set(k, String(v));
    },
    dump: () => Object.fromEntries(map.entries()),
  };
}

describe("colabOneMigration helpers", () => {
  it("validates export passwords with rune length, byte cap, and confirmation match", () => {
    expect(validateColabOneExportPassword("", "")).toBe("tooShort");
    expect(validateColabOneExportPassword("12345", "12345")).toBe("tooShort");
    expect(validateColabOneExportPassword("      ", "      ")).toBe("tooShort");
    expect(validateColabOneExportPassword("123456", "123457")).toBe("mismatch");
    expect(validateColabOneExportPassword("123456", "123456")).toBeNull();
    expect(validateColabOneExportPassword("密码六个字符", "密码六个字符")).toBeNull();
    const huge = "a".repeat(513);
    expect(validateColabOneExportPassword(huge, huge)).toBe("tooLong");
  });

  it("collects and hot-applies WebView UI preferences cleanly", () => {
    const srcStorage = createMemoryStorage({
      [COLABONE_STORAGE_KEYS.monitorLayout]: "rows",
      [COLABONE_STORAGE_KEYS.cuUsageMetric]: "rate",
      [COLABONE_STORAGE_KEYS.logsWidth]: "480",
    });
    const collected = collectColabOneMigrationUIPrefs(srcStorage);
    expect(collected.monitor_layout).toBe("rows");
    expect(collected.cu_usage_metric).toBe("rate");
    expect(collected.logs_width).toBe(480);

    // Unset layout/metric defaults should still be captured so target machines reset cleanly.
    const defaultCollected = collectColabOneMigrationUIPrefs(createMemoryStorage());
    expect(defaultCollected.monitor_layout).toBe("grid");
    expect(defaultCollected.cu_usage_metric).toBe("consumed");

    const dstStorage = createMemoryStorage();
    const setAppearance = vi.fn();
    const setLocale = vi.fn();
    const setPrivacyDefault = vi.fn();
    const setPrivacyActive = vi.fn();
    const setLogsWidth = vi.fn();

    applyColabOneMigrationUIPrefs(
      {
        appearance: "dark",
        locale: "ja-JP",
        privacy_mask_default: true,
        monitor_layout: "rows",
        cu_usage_metric: "balance",
        logs_width: 512,
      },
      {
        storage: dstStorage,
        setAppearance,
        setLocale,
        setPrivacyDefault,
        setPrivacyActive,
        setLogsWidth,
      },
    );

    expect(setAppearance).toHaveBeenCalledWith("dark");
    expect(setLocale).toHaveBeenCalledWith("ja-JP");
    expect(setPrivacyDefault).toHaveBeenCalledWith(true);
    expect(setPrivacyActive).toHaveBeenCalledWith(true);
    expect(setLogsWidth).toHaveBeenCalledWith(512);
    expect(dstStorage.dump()).toEqual({
      [COLABONE_STORAGE_KEYS.monitorLayout]: "rows",
      [COLABONE_STORAGE_KEYS.cuUsageMetric]: "balance",
      [COLABONE_STORAGE_KEYS.logsWidth]: "512",
    });
  });

  it("auto-deselects account module on import when running GPU instances exist", () => {
    const modules = COLABONE_MIGRATION_MODULES.map((id) => ({
      id,
      included: id !== "cu_usage",
      has_data: true,
    }));

    const withoutRunning = defaultImportModuleSelection({
      preview_id: "p1",
      filename: "test.colabone",
      app_version: "v0.1.0",
      exported_at: "2026-11-24T00:00:00Z",
      source_os: "windows",
      schema_version: 1,
      has_active_operation: false,
      has_running_instances: false,
      can_run_in_background: true,
      modules,
    });
    expect(withoutRunning).toEqual([
      "account",
      "credentials",
      "settings",
      "library_workflows",
      "workflow_drafts",
    ]);

    const withRunning = defaultImportModuleSelection({
      preview_id: "p2",
      filename: "test.colabone",
      app_version: "v0.1.0",
      exported_at: "2026-11-24T00:00:00Z",
      source_os: "darwin",
      schema_version: 1,
      has_active_operation: false,
      has_running_instances: true,
      can_run_in_background: true,
      modules,
    });
    expect(withRunning).not.toContain("account");
    expect(withRunning).toEqual([
      "credentials",
      "settings",
      "library_workflows",
      "workflow_drafts",
    ]);
  });

  it("formats archive sizes, OS names, and backend migration messages cleanly", () => {
    expect(formatMigrationFileSize(512)).toBe("512 B");
    expect(formatMigrationFileSize(2048)).toBe("2.0 KB");
    expect(formatMigrationFileSize(2 * 1024 * 1024)).toBe("2.00 MB");
    expect(formatMigrationSourceOS("windows")).toBe("Windows");
    expect(formatMigrationSourceOS("darwin")).toBe("macOS");
    expect(formatMigrationSourceOS("linux")).toBe("Linux");

    expect(translateColabOneActivitySource("迁移", "en-US")).toBe("Migration");
    expect(
      translateColabOneBackendText("迁移密码错误或文件已被篡改，请重试", "en-US"),
    ).toContain("Incorrect migration password");
    expect(
      translateColabOneBackendText("已导出加密迁移文件（6 项模块）", "en-US"),
    ).toBe("Exported encrypted migration archive (6 modules)");
    expect(
      translateColabOneBackendText(
        "已导入迁移配置（Google 账号与云盘认证、软件与外观设置）",
        "en-US",
      ),
    ).toBe(
      "Imported migration configuration (Google Account & Drive Auth, App & Appearance Settings)",
    );

    const dynamicErrors = [
      "不支持的迁移模块：foo",
      "迁移文件中的 Google 授权范围格式无效",
      "迁移文件中的对外 API Key 格式无效",
      "迁移文件中的 Hugging Face Token 无效：密钥长度超出允许范围",
      "迁移文件中的自定义工作流无效：工作流名称不能为空",
      "写入软件设置失败：disk full",
      "写入 CU 算力消耗历史失败：permission denied",
    ];
    for (const msg of dynamicErrors) {
      const en = translateColabOneBackendText(msg, "en-US");
      expect(en).not.toMatch(/[\u4e00-\u9fff]/);
    }
  });
});
