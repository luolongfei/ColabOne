import { describe, expect, it } from "vitest";
import manifest from "../../console/mygo.json";
import pkg from "../package.json";
import {
  COLABONE_APP_VERSION,
  COLABONE_APP_VERSION_LABEL,
  COLABONE_DEFAULT_VERSION,
  formatColabOneVersion,
  normalizeColabOneVersion,
} from "./colabOneVersion";

describe("colabOneVersion", () => {
  it("normalizes bare, prefixed, and empty version strings with 0.1.0 fallback", () => {
    expect(COLABONE_DEFAULT_VERSION).toBe("0.1.0");
    expect(normalizeColabOneVersion("0.1.0")).toBe("0.1.0");
    expect(normalizeColabOneVersion("v0.2.3")).toBe("0.2.3");
    expect(normalizeColabOneVersion("  V1.4.0-beta.1  ")).toBe("1.4.0-beta.1");
    expect(normalizeColabOneVersion("")).toBe("0.1.0");
    expect(normalizeColabOneVersion("v")).toBe("0.1.0");
    expect(normalizeColabOneVersion(null)).toBe("0.1.0");
    expect(normalizeColabOneVersion(undefined)).toBe("0.1.0");
  });

  it("formats display labels with a single lowercase v prefix", () => {
    expect(formatColabOneVersion("0.1.0")).toBe("v0.1.0");
    expect(formatColabOneVersion("v0.1.0")).toBe("v0.1.0");
    expect(formatColabOneVersion("V2.0.0")).toBe("v2.0.0");
    expect(formatColabOneVersion("")).toBe("v0.1.0");
  });

  it("matches the version declared in console/mygo.json and frontend/package.json", () => {
    expect(manifest.version).toBe(pkg.version);
    expect(COLABONE_APP_VERSION).toBe(manifest.version);
    expect(COLABONE_APP_VERSION_LABEL).toBe(`v${manifest.version}`);
  });
});
