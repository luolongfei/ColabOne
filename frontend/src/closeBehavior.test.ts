import { describe, expect, it } from "vitest";
import {
  normalizeColabOneCloseAction,
  normalizeColabOneCloseBehavior,
} from "./api";

describe("ColabOne window close behavior normalization", () => {
  it("normalizes supported close behaviors and falls back safely to ask", () => {
    expect(normalizeColabOneCloseBehavior("ask")).toBe("ask");
    expect(normalizeColabOneCloseBehavior("quit")).toBe("quit");
    expect(normalizeColabOneCloseBehavior("background")).toBe("background");
    expect(normalizeColabOneCloseBehavior("")).toBe("ask");
    expect(normalizeColabOneCloseBehavior("unknown")).toBe("ask");
    expect(normalizeColabOneCloseBehavior(undefined)).toBe("ask");
  });

  it("normalizes close actions with quit as the default", () => {
    expect(normalizeColabOneCloseAction("quit")).toBe("quit");
    expect(normalizeColabOneCloseAction("background")).toBe("background");
    expect(normalizeColabOneCloseAction("ask")).toBe("quit");
    expect(normalizeColabOneCloseAction("")).toBe("quit");
    expect(normalizeColabOneCloseAction(null)).toBe("quit");
  });
});
