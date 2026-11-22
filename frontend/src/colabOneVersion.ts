declare const __COLABONE_APP_VERSION__: string | undefined;

export const COLABONE_DEFAULT_VERSION = "0.1.0";

export function normalizeColabOneVersion(raw: unknown): string {
  const cleaned = String(raw ?? "")
    .trim()
    .replace(/^[vV]+/, "");
  return cleaned || COLABONE_DEFAULT_VERSION;
}

export function formatColabOneVersion(raw: unknown): string {
  return `v${normalizeColabOneVersion(raw)}`;
}

export const COLABONE_APP_VERSION = normalizeColabOneVersion(
  typeof __COLABONE_APP_VERSION__ === "string"
    ? __COLABONE_APP_VERSION__
    : COLABONE_DEFAULT_VERSION,
);

export const COLABONE_APP_VERSION_LABEL = formatColabOneVersion(
  COLABONE_APP_VERSION,
);
