import { describe, expect, it } from "vitest";
import {
  formatDurationMs,
  formatTokenCount,
  formatTokenSpeed,
  requestKindLabel,
  requestStateLabel,
  resolveSpeedSummary,
} from "./apiSpeed";
import type { PublicAPIStatus } from "./types";

describe("apiSpeed formatting and summary helpers", () => {
  it("formats token speed and duration crisply", () => {
    expect(formatTokenSpeed(null)).toBe("—");
    expect(formatTokenSpeed(0)).toBe("—");
    expect(formatTokenSpeed(48.64)).toBe("48.6 tokens/s");
    expect(formatTokenSpeed(48.64, true)).toBe("≈48.6 tokens/s");
    expect(formatTokenSpeed(132.4)).toBe("132 tokens/s");

    expect(formatDurationMs(null)).toBe("—");
    expect(formatDurationMs(420)).toBe("420ms");
    expect(formatDurationMs(3050)).toBe("3.05s");

    expect(formatTokenCount(1280)).toBe("1,280");
    expect(requestKindLabel("chat")).toBe("Chat");
    expect(requestKindLabel("responses")).toBe("Responses");
    expect(requestKindLabel("compact")).toBe("Compact");
    expect(requestStateLabel("running")).toBe("生成中");
    expect(requestStateLabel("completed")).toBe("完成");
  });

  it("prioritizes live running request speed and computes averages", () => {
    const api: PublicAPIStatus = {
      state: "ready",
      enabled: true,
      mode: "named",
      url: "https://api.example.com",
      error: "",
      streaming: true,
      checked_at: 1700000000,
      requests: 3,
      active: 1,
      completed: 2,
      failed: 0,
      last_request: 1700000000,
      last_tokens_per_second: 61.2,
      avg_tokens_per_second: 45.0,
      recent_requests: [
        {
          seq: 3,
          id: "req-3",
          kind: "responses",
          endpoint: "/v1/responses",
          model: "qwen",
          stream: true,
          state: "running",
          started_at: 1700000000,
          duration_ms: 1200,
          ttft_ms: 180,
          completion_tokens: 64,
          tokens_per_second: 61.2,
          estimated: true,
        },
        {
          seq: 2,
          id: "req-2",
          kind: "chat",
          endpoint: "/v1/chat/completions",
          model: "qwen",
          stream: true,
          state: "completed",
          started_at: 1699999900,
          duration_ms: 2100,
          completion_tokens: 100,
          tokens_per_second: 50.0,
        },
        {
          seq: 1,
          id: "req-1",
          kind: "chat",
          endpoint: "/v1/chat/completions",
          model: "qwen",
          stream: false,
          state: "completed",
          started_at: 1699999800,
          duration_ms: 2500,
          completion_tokens: 100,
          tokens_per_second: 40.0,
        },
      ],
    };
    const liveSummary = resolveSpeedSummary(api);
    expect(liveSummary.live).toBe(true);
    expect(liveSummary.latestTps).toBe(61.2);
    expect(liveSummary.latestEstimated).toBe(true);
    expect(liveSummary.avgTps).toBe(45.0);
    expect(liveSummary.completedWithSpeedCount).toBe(2);

    // Once active request finishes, latest completed speed is shown.
    const idleApi: PublicAPIStatus = {
      ...api,
      active: 0,
      recent_requests: api.recent_requests!.slice(1),
    };
    const idleSummary = resolveSpeedSummary(idleApi);
    expect(idleSummary.live).toBe(false);
    expect(idleSummary.latestTps).toBe(50.0);
    expect(idleSummary.latestEstimated).toBe(false);
    expect(idleSummary.avgTps).toBe(45.0);
  });
});
