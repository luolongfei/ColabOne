import type { PublicAPIRequestMetric, PublicAPIStatus } from "./types";

export function formatTokenSpeed(
  tps?: number | null,
  estimated = false,
): string {
  if (typeof tps !== "number" || !Number.isFinite(tps) || tps <= 0) {
    return "—";
  }
  const rounded = tps >= 100 ? tps.toFixed(0) : tps.toFixed(1);
  return `${estimated ? "≈" : ""}${rounded} tokens/s`;
}

export function formatDurationMs(ms?: number | null): string {
  if (typeof ms !== "number" || !Number.isFinite(ms) || ms < 0) {
    return "—";
  }
  if (ms < 1000) {
    return `${Math.round(ms)}ms`;
  }
  return `${(ms / 1000).toFixed(2)}s`;
}

export function formatTokenCount(count?: number | null): string {
  if (typeof count !== "number" || !Number.isFinite(count) || count < 0) {
    return "0";
  }
  return Math.round(count).toLocaleString("en-US");
}

export function formatRequestClock(epochSeconds?: number | null): string {
  if (
    typeof epochSeconds !== "number" ||
    !Number.isFinite(epochSeconds) ||
    epochSeconds <= 0
  ) {
    return "--:--:--";
  }
  return new Date(epochSeconds * 1000).toLocaleTimeString("zh-CN", {
    hour12: false,
  });
}

export function requestKindLabel(kind: PublicAPIRequestMetric["kind"]): string {
  switch (kind) {
    case "responses":
      return "Responses";
    case "compact":
      return "Compact";
    default:
      return "Chat";
  }
}

export function requestStateLabel(
  state: PublicAPIRequestMetric["state"],
): string {
  switch (state) {
    case "running":
      return "生成中";
    case "completed":
      return "完成";
    case "cancelled":
      return "已取消";
    case "failed":
      return "失败";
    default:
      return state;
  }
}

export function resolveSpeedSummary(api?: PublicAPIStatus | null): {
  live: boolean;
  latestTps: number | null;
  latestEstimated: boolean;
  avgTps: number | null;
  completedWithSpeedCount: number;
  activeRequest: PublicAPIRequestMetric | null;
} {
  const recent = api?.recent_requests || [];
  const activeRequest = recent.find((r) => r.state === "running") || null;
  const completedWithSpeed = recent.filter(
    (r) =>
      r.state === "completed" &&
      typeof r.tokens_per_second === "number" &&
      r.tokens_per_second > 0,
  );
  if (
    activeRequest &&
    typeof activeRequest.tokens_per_second === "number" &&
    activeRequest.tokens_per_second > 0
  ) {
    return {
      live: true,
      latestTps: activeRequest.tokens_per_second,
      latestEstimated: !!activeRequest.estimated,
      avgTps:
        typeof api?.avg_tokens_per_second === "number" &&
        api.avg_tokens_per_second > 0
          ? api.avg_tokens_per_second
          : null,
      completedWithSpeedCount: completedWithSpeed.length,
      activeRequest,
    };
  }
  const latestCompleted = completedWithSpeed[0] || null;
  const latestTps =
    latestCompleted?.tokens_per_second ??
    (typeof api?.last_tokens_per_second === "number" &&
    api.last_tokens_per_second > 0
      ? api.last_tokens_per_second
      : null);
  const computedAvg =
    completedWithSpeed.length > 0
      ? Number(
          (
            completedWithSpeed.reduce(
              (acc, r) => acc + (r.tokens_per_second || 0),
              0,
            ) / completedWithSpeed.length
          ).toFixed(1),
        )
      : null;
  const avgTps =
    typeof api?.avg_tokens_per_second === "number" &&
    api.avg_tokens_per_second > 0
      ? api.avg_tokens_per_second
      : computedAvg;
  return {
    live: !!activeRequest,
    latestTps,
    latestEstimated: !!latestCompleted?.estimated,
    avgTps,
    completedWithSpeedCount: completedWithSpeed.length,
    activeRequest,
  };
}
