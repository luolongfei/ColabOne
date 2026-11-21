import { theme } from "ant-design-vue";
import type { ThemeConfig } from "ant-design-vue/es/config-provider/context";
import zhCN from "ant-design-vue/es/locale/zh_CN";
import type { ColabOneLocale } from "./i18n/flags";

export type ColabOneThemeMode = "light" | "dark";

export const colabOneLocale = zhCN;
export const COLABONE_BUTTON_RADIUS = 10;
export const COLABONE_PILL_RADIUS = 9999;
export const COLABONE_CARD_RADIUS = 16;
export const COLABONE_UI_FONT_FAMILY =
  '"Sarasa UI SC", -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Segoe UI", "Microsoft YaHei", sans-serif';

export const COLABONE_LOCALE_FONT_FAMILIES: Record<ColabOneLocale, string> = {
  "zh-CN": COLABONE_UI_FONT_FAMILY,
  "en-US":
    '-apple-system, BlinkMacSystemFont, "Segoe UI", "Inter", "Helvetica Neue", Arial, "PingFang SC", "Microsoft YaHei", sans-serif',
  "zh-TW":
    '-apple-system, BlinkMacSystemFont, "PingFang TC", "Microsoft JhengHei UI", "Microsoft JhengHei", "Noto Sans TC", "Segoe UI", "Sarasa UI SC", sans-serif',
  "zh-HK":
    '-apple-system, BlinkMacSystemFont, "PingFang HK", "PingFang TC", "Microsoft JhengHei UI", "Microsoft JhengHei", "Noto Sans HK", "Segoe UI", "Sarasa UI SC", sans-serif',
  "ja-JP":
    '-apple-system, BlinkMacSystemFont, "Hiragino Sans", "Hiragino Kaku Gothic ProN", "Yu Gothic UI", "Meiryo", "Noto Sans JP", "Segoe UI", sans-serif',
  "ko-KR":
    '-apple-system, BlinkMacSystemFont, "Apple SD Gothic Neo", "Malgun Gothic", "Noto Sans KR", "Segoe UI", sans-serif',
};

export function getColabOneFontFamily(locale?: ColabOneLocale): string {
  return (locale && COLABONE_LOCALE_FONT_FAMILIES[locale]) || COLABONE_UI_FONT_FAMILY;
}

export function buildColabOneTheme(
  mode: ColabOneThemeMode,
  locale?: ColabOneLocale,
): ThemeConfig {
  const isDark = mode === "dark";
  const fontFamily = getColabOneFontFamily(locale);

  return {
    algorithm: isDark ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      colorPrimary: "#3b82f6",
      colorInfo: "#3b82f6",
      colorSuccess: "#10b981",
      colorWarning: "#f59e0b",
      colorError: "#ef4444",
      colorBgLayout: isDark ? "#0b0c0e" : "#fafbfc",
      colorBgContainer: isDark ? "#15161b" : "#ffffff",
      colorBgElevated: isDark ? "#1c1e26" : "#ffffff",
      colorBgContainerDisabled: isDark ? "#111318" : "#f3f4f6",
      colorFillAlter: isDark ? "#111318" : "#f8fafc",
      colorFillQuaternary: isDark ? "#181a20" : "#f3f4f6",
      colorFillTertiary: isDark ? "#1e2028" : "#f3f4f6",
      colorBorder: isDark ? "#2a2c36" : "#e5e7eb",
      colorBorderSecondary: isDark ? "#232630" : "#eef0f4",
      colorText: isDark ? "#f4f4f5" : "#111827",
      colorTextSecondary: isDark ? "#a1a1aa" : "#4b5563",
      colorTextTertiary: isDark ? "#9499a6" : "#6b7280",
      borderRadius: COLABONE_BUTTON_RADIUS,
      borderRadiusLG: COLABONE_CARD_RADIUS,
      borderRadiusSM: 8,
      fontSize: 14,
      fontSizeSM: 13,
      fontSizeLG: 16,
      fontSizeXL: 20,
      fontSizeHeading1: 24,
      fontSizeHeading2: 18,
      fontSizeHeading3: 16,
      fontSizeHeading4: 15,
      controlHeight: 36,
      controlHeightSM: 30,
      controlHeightLG: 40,
      fontFamily,
      wireframe: false,
    },
    components: {
      Button: {
        borderRadius: COLABONE_BUTTON_RADIUS,
        borderRadiusSM: 8,
        borderRadiusLG: 12,
        borderRadiusXS: 6,
        controlOutline: isDark
          ? "rgba(59, 130, 246, 0.3)"
          : "rgba(59, 130, 246, 0.22)",
      },
      Segmented: {
        borderRadius: COLABONE_PILL_RADIUS,
        borderRadiusSM: COLABONE_PILL_RADIUS,
        borderRadiusLG: COLABONE_PILL_RADIUS,
        borderRadiusXS: COLABONE_PILL_RADIUS,
      },
      Tag: {
        borderRadiusSM: COLABONE_PILL_RADIUS,
      },
      Card: {
        borderRadiusLG: COLABONE_CARD_RADIUS,
        paddingLG: 22,
      },
      Modal: {
        borderRadiusLG: COLABONE_CARD_RADIUS,
      },
      Drawer: {
        paddingLG: 22,
      },
      Input: {
        borderRadius: COLABONE_BUTTON_RADIUS,
        borderRadiusSM: 8,
      },
      InputNumber: {
        borderRadius: COLABONE_BUTTON_RADIUS,
        borderRadiusSM: 8,
      },
      Select: {
        borderRadius: COLABONE_BUTTON_RADIUS,
        borderRadiusSM: 8,
      },
      Layout: {
        colorBgHeader: isDark ? "#0b0c0e" : "#fafbfc",
        colorBgContainer: isDark ? "#15161b" : "#ffffff",
        colorBgBody: isDark ? "#0b0c0e" : "#fafbfc",
        colorBgTrigger: isDark ? "#1c1e26" : "#ffffff",
      },
      Menu: {
        radiusItem: COLABONE_PILL_RADIUS,
        radiusSubMenuItem: COLABONE_PILL_RADIUS,
        controlHeightLG: 40,
        itemMarginInline: 10,
        colorItemBg: "transparent",
        colorSubItemBg: "transparent",
        colorActiveBarBorderSize: 0,
      },
    },
  };
}
