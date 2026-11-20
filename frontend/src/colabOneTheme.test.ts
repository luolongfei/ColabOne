import { describe, expect, it } from "vitest";
import { theme } from "ant-design-vue";
import {
  COLABONE_BUTTON_RADIUS,
  COLABONE_CARD_RADIUS,
  COLABONE_PILL_RADIUS,
  COLABONE_UI_FONT_FAMILY,
  buildColabOneTheme,
  colabOneLocale,
} from "./colabOneTheme";

describe("colabOneTheme", () => {
  it("uses standard defaultAlgorithm / darkAlgorithm without compactAlgorithm so desktop typography stays readable", () => {
    const light = buildColabOneTheme("light");
    const dark = buildColabOneTheme("dark");

    expect(light.algorithm).toBe(theme.defaultAlgorithm);
    expect(dark.algorithm).toBe(theme.darkAlgorithm);
    expect(light.token?.fontSize).toBeGreaterThanOrEqual(14);
    expect(dark.token?.fontSize).toBeGreaterThanOrEqual(14);
    expect(light.token?.controlHeight).toBeGreaterThanOrEqual(34);
  });

  it("uses ColabOne rounded cards, crisp buttons, and full capsule pills for segmented/tag controls", () => {
    expect(COLABONE_BUTTON_RADIUS).toBe(10);
    expect(COLABONE_PILL_RADIUS).toBe(9999);
    expect(COLABONE_CARD_RADIUS).toBe(16);

    for (const mode of ["light", "dark"] as const) {
      const cfg = buildColabOneTheme(mode);

      expect(cfg.components?.Button?.borderRadius).toBe(10);
      expect(cfg.components?.Button?.borderRadiusSM).toBe(8);
      expect(cfg.components?.Button?.borderRadiusLG).toBe(12);
      expect(cfg.components?.Button?.borderRadiusXS).toBe(6);

      expect(cfg.components?.Card?.borderRadiusLG).toBe(16);
      expect(cfg.components?.Modal?.borderRadiusLG).toBe(16);
      expect(cfg.components?.Segmented?.borderRadius).toBe(9999);
      expect(cfg.components?.Tag?.borderRadiusSM).toBe(9999);
      expect(cfg.components?.Menu?.radiusItem).toBe(9999);
    }
  });

  it("returns ColabOne light (#fafbfc) and deep obsidian dark (#0b0c0e) surface tokens", () => {
    const light = buildColabOneTheme("light");
    const dark = buildColabOneTheme("dark");

    expect(light.token?.colorPrimary).toBe("#3b82f6");
    expect(dark.token?.colorPrimary).toBe("#3b82f6");

    expect(light.token?.colorBgLayout).toBe("#fafbfc");
    expect(dark.token?.colorBgLayout).toBe("#0b0c0e");
    expect(light.token?.colorBgLayout).not.toBe(dark.token?.colorBgLayout);

    expect(light.token?.colorBgContainer).toBe("#ffffff");
    expect(dark.token?.colorBgContainer).toBe("#15161b");
    expect(light.token?.colorBgContainer).not.toBe(
      dark.token?.colorBgContainer,
    );

    expect(light.token?.colorBgElevated).toBe("#ffffff");
    expect(dark.token?.colorBgElevated).toBe("#1c1e26");

    expect(light.components?.Layout?.colorBgHeader).toBe("#fafbfc");
    expect(dark.components?.Layout?.colorBgHeader).toBe("#0b0c0e");
    expect(light.components?.Layout?.colorBgBody).toBe("#fafbfc");
    expect(dark.components?.Layout?.colorBgBody).toBe("#0b0c0e");
  });

  it("exports Chinese (zh-cn) ColabOne UI locale", () => {
    expect(colabOneLocale.locale).toBe("zh-cn");
  });

  it("uses bundled Sarasa UI SC as the primary Chinese-optimized UI typeface", () => {
    for (const mode of ["light", "dark"] as const) {
      const cfg = buildColabOneTheme(mode);
      expect(cfg.token?.fontFamily).toBe(COLABONE_UI_FONT_FAMILY);
      expect(cfg.token?.fontFamily?.startsWith('"Sarasa UI SC"')).toBe(true);
    }
  });
});
