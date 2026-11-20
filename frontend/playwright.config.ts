import fs from "node:fs";
import { defineConfig } from "@playwright/test";

const hasSystemChrome =
  !process.env.CI &&
  (process.platform === "darwin" ||
    process.platform === "win32" ||
    fs.existsSync("/opt/google/chrome/chrome"));

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.pw.ts",
  use: {
    baseURL: "http://127.0.0.1:4173",
    channel: hasSystemChrome ? "chrome" : undefined,
    viewport: { width: 1280, height: 850 },
  },
  webServer: {
    command: "npm run dev -- --port 4173",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: true,
  },
  reporter: "list",
});
