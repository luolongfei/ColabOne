import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import manifest from "../console/mygo.json" with { type: "json" };
import pkg from "./package.json" with { type: "json" };

const appVersion =
  String(manifest.version || pkg.version || "0.1.0")
    .trim()
    .replace(/^[vV]+/, "") || "0.1.0";

export default defineConfig({
  plugins: [vue()],
  base: "./",
  define: {
    __COLABONE_APP_VERSION__: JSON.stringify(appVersion),
  },
  build: { target: "es2022" },
});
