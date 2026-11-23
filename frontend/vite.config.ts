import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import manifest from "../console/mygo.json" with { type: "json" };
import pkg from "./package.json" with { type: "json" };

const appVersion =
  String(manifest.version || pkg.version || "0.1.0")
    .trim()
    .replace(/^[vV]+/, "") || "0.1.0";

// Keep stylesheets out of manual code-splitting groups so CSS cascade order
// (e.g. ant-design-vue/dist/reset.css imported before ./style.css in main.ts)
// remains inside the importing chunk instead of being emitted in a later vendor CSS link.
const cssAssetRe =
  /\.(css|less|sass|scss|styl|stylus|pcss|postcss|sss)(?:$|[?&#])/i;

const matchNonCss =
  (pattern: RegExp) =>
  (id: string): boolean =>
    !cssAssetRe.test(id) && pattern.test(id);

export default defineConfig({
  plugins: [vue()],
  base: "./",
  define: {
    __COLABONE_APP_VERSION__: JSON.stringify(appVersion),
  },
  build: {
    target: "es2022",
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: "vendor-vue",
              test: matchNonCss(/[\\/]node_modules[\\/](vue|@vue)[\\/]/),
              priority: 40,
            },
            {
              name: "vendor-shiki",
              test: matchNonCss(/[\\/]node_modules[\\/](@shikijs|oniguruma-)/),
              priority: 35,
            },
            {
              name: "vendor-icons",
              test: matchNonCss(/[\\/]node_modules[\\/]@phosphor-icons[\\/]/),
              priority: 30,
              entriesAware: true,
              entriesAwareMergeThreshold: 48 * 1024,
            },
            {
              name: "vendor-antd-icons",
              test: matchNonCss(/[\\/]node_modules[\\/]@ant-design[\\/]/),
              priority: 25,
            },
            {
              name: "vendor-antd",
              test: matchNonCss(
                /[\\/]node_modules[\\/](ant-design-vue|lodash-es|dom-align|async-validator|dayjs|compute-scroll-into-view|scroll-into-view-if-needed|resize-observer-polyfill|vue-types)[\\/]/,
              ),
              priority: 20,
              entriesAware: true,
              entriesAwareMergeThreshold: 64 * 1024,
            },
            {
              name: "i18n-locales",
              test: matchNonCss(/[\\/]src[\\/]i18n[\\/]locales[\\/]/),
              priority: 18,
            },
            {
              name: "i18n-backend",
              test: matchNonCss(/[\\/]src[\\/]i18n[\\/]backend/),
              priority: 16,
            },
          ],
        },
      },
    },
  },
});
