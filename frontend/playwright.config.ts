import fs from "node:fs";
import { fileURLToPath } from "node:url";
import { defineConfig } from "@playwright/test";

const hasSystemChrome =
  !process.env.CI &&
  (process.platform === "darwin" ||
    process.platform === "win32" ||
    fs.existsSync("/opt/google/chrome/chrome"));

const distIndexPath = fileURLToPath(
  new URL("./dist/index.html", import.meta.url),
);
const usePrebuiltDist = Boolean(
  process.env.CI && fs.existsSync(distIndexPath),
);

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.pw.ts",
  use: {
    baseURL: "http://127.0.0.1:4173",
    channel: hasSystemChrome ? "chrome" : undefined,
    locale: "zh-CN",
    viewport: { width: 1280, height: 850 },
  },
  webServer: {
    command: usePrebuiltDist
      ? "npm run preview -- --port 4173 --strictPort"
      : "npm run preview:e2e",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: !process.env.CI,
  },
  reporter: "list",
});
