/**
 * Zero-dependency offline ISO/IEC 18004 QR Code generator (Byte mode, ECC Level M,
 * Versions 1 through 15, up to 412 UTF-8 bytes).
 *
 * Used by Live Studio to render scannable HTTPS pairing QR codes for remote
 * mobile companion devices with zero external network or CDN calls.
 */

export interface ColabOneQrMatrix {
  version: number;
  size: number;
  modules: boolean[][];
}

interface QrVersionSpec {
  version: number;
  capacityBytes: number;
  ecPerBlock: number;
  g1Blocks: number;
  g1Data: number;
  g2Blocks: number;
  g2Data: number;
  alignment: number[];
}

// ISO/IEC 18004 Table 9 (ECC Level M) & Annex E alignment pattern centers
const QR_VERSION_SPECS_M: readonly QrVersionSpec[] = [
  { version: 1, capacityBytes: 14, ecPerBlock: 10, g1Blocks: 1, g1Data: 16, g2Blocks: 0, g2Data: 0, alignment: [] },
  { version: 2, capacityBytes: 26, ecPerBlock: 16, g1Blocks: 1, g1Data: 28, g2Blocks: 0, g2Data: 0, alignment: [6, 18] },
  { version: 3, capacityBytes: 42, ecPerBlock: 26, g1Blocks: 1, g1Data: 44, g2Blocks: 0, g2Data: 0, alignment: [6, 22] },
  { version: 4, capacityBytes: 62, ecPerBlock: 18, g1Blocks: 2, g1Data: 32, g2Blocks: 0, g2Data: 0, alignment: [6, 26] },
  { version: 5, capacityBytes: 84, ecPerBlock: 24, g1Blocks: 2, g1Data: 43, g2Blocks: 0, g2Data: 0, alignment: [6, 30] },
  { version: 6, capacityBytes: 106, ecPerBlock: 16, g1Blocks: 4, g1Data: 27, g2Blocks: 0, g2Data: 0, alignment: [6, 34] },
  { version: 7, capacityBytes: 122, ecPerBlock: 18, g1Blocks: 4, g1Data: 31, g2Blocks: 0, g2Data: 0, alignment: [6, 22, 38] },
  { version: 8, capacityBytes: 152, ecPerBlock: 22, g1Blocks: 2, g1Data: 38, g2Blocks: 2, g2Data: 39, alignment: [6, 24, 42] },
  { version: 9, capacityBytes: 180, ecPerBlock: 22, g1Blocks: 3, g1Data: 36, g2Blocks: 2, g2Data: 37, alignment: [6, 26, 46] },
  { version: 10, capacityBytes: 213, ecPerBlock: 26, g1Blocks: 4, g1Data: 43, g2Blocks: 1, g2Data: 44, alignment: [6, 28, 50] },
  { version: 11, capacityBytes: 251, ecPerBlock: 30, g1Blocks: 1, g1Data: 50, g2Blocks: 4, g2Data: 51, alignment: [6, 30, 54] },
  { version: 12, capacityBytes: 287, ecPerBlock: 22, g1Blocks: 6, g1Data: 36, g2Blocks: 2, g2Data: 37, alignment: [6, 32, 58] },
  { version: 13, capacityBytes: 331, ecPerBlock: 22, g1Blocks: 8, g1Data: 37, g2Blocks: 1, g2Data: 38, alignment: [6, 34, 62] },
  { version: 14, capacityBytes: 362, ecPerBlock: 24, g1Blocks: 4, g1Data: 40, g2Blocks: 5, g2Data: 41, alignment: [6, 26, 46, 66] },
  { version: 15, capacityBytes: 412, ecPerBlock: 24, g1Blocks: 5, g1Data: 41, g2Blocks: 5, g2Data: 42, alignment: [6, 26, 48, 70] },
];

// GF(256) lookup tables with primitive polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11d)
const GF_EXP = new Uint8Array(512);
const GF_LOG = new Uint8Array(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i += 1) {
    GF_EXP[i] = x;
    GF_LOG[x] = i;
    x <<= 1;
    if (x & 0x100) {
      x ^= 0x11d;
    }
  }
  for (let i = 255; i < 512; i += 1) {
    GF_EXP[i] = GF_EXP[i - 255];
  }
})();

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return GF_EXP[GF_LOG[a] + GF_LOG[b]];
}

function rsGeneratorPoly(degree: number): Uint8Array {
  let poly = new Uint8Array([1]);
  for (let i = 0; i < degree; i += 1) {
    const next = new Uint8Array(poly.length + 1);
    const factor = GF_EXP[i];
    for (let j = 0; j < poly.length; j += 1) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMul(poly[j], factor);
    }
    poly = next;
  }
  return poly;
}

function rsComputeEc(data: Uint8Array, ecCount: number): Uint8Array {
  const gen = rsGeneratorPoly(ecCount);
  const rem = new Uint8Array(ecCount);
  for (let i = 0; i < data.length; i += 1) {
    const factor = data[i] ^ rem[0];
    for (let j = 0; j < ecCount - 1; j += 1) {
      rem[j] = rem[j + 1];
    }
    rem[ecCount - 1] = 0;
    if (factor !== 0) {
      for (let j = 0; j < ecCount; j += 1) {
        rem[j] ^= gfMul(gen[j + 1], factor);
      }
    }
  }
  return rem;
}

function bchFormatBits(mask: number): number {
  // ECC Level M = 00 (binary 00 << 3 | mask)
  const data = (0b00 << 3) | (mask & 0x7);
  let rem = data << 10;
  for (let bit = 14; bit >= 10; bit -= 1) {
    if ((rem >> bit) & 1) {
      rem ^= 0x537 << (bit - 10);
    }
  }
  return ((data << 10) | rem) ^ 0x5412;
}

function bchVersionBits(version: number): number {
  let rem = version << 12;
  for (let bit = 17; bit >= 12; bit -= 1) {
    if ((rem >> bit) & 1) {
      rem ^= 0x1f25 << (bit - 12);
    }
  }
  return (version << 12) | rem;
}

function encodeDataCodewords(bytes: Uint8Array, spec: QrVersionSpec): Uint8Array {
  const totalDataCw = spec.g1Blocks * spec.g1Data + spec.g2Blocks * spec.g2Data;
  const bits: number[] = [];
  const pushBits = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i -= 1) {
      bits.push((val >> i) & 1);
    }
  };

  // Mode indicator: Byte mode = 0100
  pushBits(0b0100, 4);
  // Character count indicator: 8 bits for versions 1-9, 16 bits for version 10+
  pushBits(bytes.length, spec.version <= 9 ? 8 : 16);
  for (let i = 0; i < bytes.length; i += 1) {
    pushBits(bytes[i], 8);
  }

  const maxBits = totalDataCw * 8;
  const termLen = Math.min(4, maxBits - bits.length);
  pushBits(0, termLen);
  while (bits.length % 8 !== 0) {
    bits.push(0);
  }

  const out = new Uint8Array(totalDataCw);
  let outIdx = 0;
  for (let i = 0; i < bits.length; i += 8) {
    let byte = 0;
    for (let b = 0; b < 8; b += 1) {
      byte = (byte << 1) | bits[i + b];
    }
    out[outIdx++] = byte;
  }
  const padBytes = [0xec, 0x11];
  let padIdx = 0;
  while (outIdx < totalDataCw) {
    out[outIdx++] = padBytes[padIdx & 1];
    padIdx += 1;
  }
  return out;
}

function buildFinalCodewords(dataCw: Uint8Array, spec: QrVersionSpec): Uint8Array {
  const dataBlocks: Uint8Array[] = [];
  const ecBlocks: Uint8Array[] = [];
  let offset = 0;

  for (let i = 0; i < spec.g1Blocks; i += 1) {
    const blk = dataCw.subarray(offset, offset + spec.g1Data);
    offset += spec.g1Data;
    dataBlocks.push(blk);
    ecBlocks.push(rsComputeEc(blk, spec.ecPerBlock));
  }
  for (let i = 0; i < spec.g2Blocks; i += 1) {
    const blk = dataCw.subarray(offset, offset + spec.g2Data);
    offset += spec.g2Data;
    dataBlocks.push(blk);
    ecBlocks.push(rsComputeEc(blk, spec.ecPerBlock));
  }

  const maxDataLen = Math.max(spec.g1Data, spec.g2Data);
  const totalLen =
    dataCw.length + (spec.g1Blocks + spec.g2Blocks) * spec.ecPerBlock;
  const result = new Uint8Array(totalLen);
  let pos = 0;

  for (let col = 0; col < maxDataLen; col += 1) {
    for (let b = 0; b < dataBlocks.length; b += 1) {
      if (col < dataBlocks[b].length) {
        result[pos++] = dataBlocks[b][col];
      }
    }
  }
  for (let col = 0; col < spec.ecPerBlock; col += 1) {
    for (let b = 0; b < ecBlocks.length; b += 1) {
      result[pos++] = ecBlocks[b][col];
    }
  }
  return result;
}

function buildFunctionTemplate(spec: QrVersionSpec): {
  size: number;
  modules: boolean[][];
  isFunc: boolean[][];
} {
  const size = spec.version * 4 + 17;
  const modules: boolean[][] = Array.from({ length: size }, () =>
    Array<boolean>(size).fill(false),
  );
  const isFunc: boolean[][] = Array.from({ length: size }, () =>
    Array<boolean>(size).fill(false),
  );

  const setFn = (r: number, c: number, dark: boolean) => {
    if (r < 0 || r >= size || c < 0 || c >= size) return;
    modules[r][c] = dark;
    isFunc[r][c] = true;
  };

  // Finder patterns + 1-module white separators
  const drawFinder = (top: number, left: number) => {
    for (let dr = -1; dr <= 7; dr += 1) {
      for (let dc = -1; dc <= 7; dc += 1) {
        const r = top + dr;
        const c = left + dc;
        if (r < 0 || r >= size || c < 0 || c >= size) continue;
        const dist = Math.max(Math.abs(dr - 3), Math.abs(dc - 3));
        const dark = dist !== 2 && dist !== 4;
        setFn(r, c, dark);
      }
    }
  };
  drawFinder(0, 0);
  drawFinder(0, size - 7);
  drawFinder(size - 7, 0);

  // Timing patterns
  for (let i = 8; i < size - 8; i += 1) {
    setFn(6, i, i % 2 === 0);
    setFn(i, 6, i % 2 === 0);
  }

  // Alignment patterns
  const centers = spec.alignment;
  for (let i = 0; i < centers.length; i += 1) {
    for (let j = 0; j < centers.length; j += 1) {
      if (
        (i === 0 && j === 0) ||
        (i === 0 && j === centers.length - 1) ||
        (i === centers.length - 1 && j === 0)
      ) {
        continue;
      }
      const cr = centers[i];
      const cc = centers[j];
      for (let dr = -2; dr <= 2; dr += 1) {
        for (let dc = -2; dc <= 2; dc += 1) {
          const dist = Math.max(Math.abs(dr), Math.abs(dc));
          setFn(cr + dr, cc + dc, dist !== 1);
        }
      }
    }
  }

  // Reserve format info strips + fixed dark module
  for (let i = 0; i <= 8; i += 1) {
    if (i !== 6) {
      setFn(8, i, false);
      setFn(i, 8, false);
    }
  }
  for (let i = 0; i < 8; i += 1) {
    setFn(8, size - 1 - i, false);
  }
  for (let i = 0; i < 7; i += 1) {
    setFn(size - 1 - i, 8, false);
  }
  setFn(size - 8, 8, true);

  // Version info blocks (versions >= 7)
  if (spec.version >= 7) {
    const vBits = bchVersionBits(spec.version);
    for (let i = 0; i < 18; i += 1) {
      const bit = ((vBits >> i) & 1) === 1;
      const a = Math.floor(i / 3);
      const b = size - 11 + (i % 3);
      setFn(a, b, bit);
      setFn(b, a, bit);
    }
  }

  return { size, modules, isFunc };
}

function drawFormatBits(modules: boolean[][], mask: number): void {
  const size = modules.length;
  const bits = bchFormatBits(mask);
  const bitAt = (i: number) => ((bits >> i) & 1) === 1;

  for (let i = 0; i <= 5; i += 1) modules[i][8] = bitAt(i);
  modules[7][8] = bitAt(6);
  modules[8][8] = bitAt(7);
  modules[8][7] = bitAt(8);
  for (let i = 9; i < 15; i += 1) modules[8][14 - i] = bitAt(i);

  for (let i = 0; i < 8; i += 1) modules[8][size - 1 - i] = bitAt(i);
  for (let i = 8; i < 15; i += 1) modules[size - 15 + i][8] = bitAt(i);
  modules[size - 8][8] = true;
}

function maskPredicate(mask: number, r: number, c: number): boolean {
  switch (mask) {
    case 0:
      return (r + c) % 2 === 0;
    case 1:
      return r % 2 === 0;
    case 2:
      return c % 3 === 0;
    case 3:
      return (r + c) % 3 === 0;
    case 4:
      return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0;
    case 5:
      return ((r * c) % 2) + ((r * c) % 3) === 0;
    case 6:
      return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0;
    case 7:
      return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0;
    default:
      return false;
  }
}

export function colabOneScoreQrPenalty(modules: boolean[][]): number {
  const n = modules.length;
  let penalty = 0;

  // Rule 1: runs of 5+ identical modules in rows and columns
  for (let r = 0; r < n; r += 1) {
    let runColor = modules[r][0];
    let runLen = 1;
    for (let c = 1; c < n; c += 1) {
      if (modules[r][c] === runColor) {
        runLen += 1;
      } else {
        if (runLen >= 5) penalty += 3 + (runLen - 5);
        runColor = modules[r][c];
        runLen = 1;
      }
    }
    if (runLen >= 5) penalty += 3 + (runLen - 5);
  }
  for (let c = 0; c < n; c += 1) {
    let runColor = modules[0][c];
    let runLen = 1;
    for (let r = 1; r < n; r += 1) {
      if (modules[r][c] === runColor) {
        runLen += 1;
      } else {
        if (runLen >= 5) penalty += 3 + (runLen - 5);
        runColor = modules[r][c];
        runLen = 1;
      }
    }
    if (runLen >= 5) penalty += 3 + (runLen - 5);
  }

  // Rule 2: 2x2 blocks of identical color
  for (let r = 0; r < n - 1; r += 1) {
    for (let c = 0; c < n - 1; c += 1) {
      const v = modules[r][c];
      if (
        v === modules[r][c + 1] &&
        v === modules[r + 1][c] &&
        v === modules[r + 1][c + 1]
      ) {
        penalty += 3;
      }
    }
  }

  // Rule 3: 1:1:3:1:1 finder-like patterns preceded or followed by 4 light modules (+40 each)
  for (let r = 0; r < n; r += 1) {
    const row = modules[r];
    for (let c = 0; c <= n - 11; c += 1) {
      const matchesForward =
        row[c] &&
        !row[c + 1] &&
        row[c + 2] &&
        row[c + 3] &&
        row[c + 4] &&
        !row[c + 5] &&
        row[c + 6] &&
        !row[c + 7] &&
        !row[c + 8] &&
        !row[c + 9] &&
        !row[c + 10];
      const matchesReverse =
        !row[c] &&
        !row[c + 1] &&
        !row[c + 2] &&
        !row[c + 3] &&
        row[c + 4] &&
        !row[c + 5] &&
        row[c + 6] &&
        row[c + 7] &&
        row[c + 8] &&
        !row[c + 9] &&
        row[c + 10];
      if (matchesForward || matchesReverse) {
        penalty += 40;
      }
    }
  }
  for (let c = 0; c < n; c += 1) {
    for (let r = 0; r <= n - 11; r += 1) {
      const matchesForward =
        modules[r][c] &&
        !modules[r + 1][c] &&
        modules[r + 2][c] &&
        modules[r + 3][c] &&
        modules[r + 4][c] &&
        !modules[r + 5][c] &&
        modules[r + 6][c] &&
        !modules[r + 7][c] &&
        !modules[r + 8][c] &&
        !modules[r + 9][c] &&
        !modules[r + 10][c];
      const matchesReverse =
        !modules[r][c] &&
        !modules[r + 1][c] &&
        !modules[r + 2][c] &&
        !modules[r + 3][c] &&
        modules[r + 4][c] &&
        !modules[r + 5][c] &&
        modules[r + 6][c] &&
        modules[r + 7][c] &&
        modules[r + 8][c] &&
        !modules[r + 9][c] &&
        modules[r + 10][c];
      if (matchesForward || matchesReverse) {
        penalty += 40;
      }
    }
  }

  // Rule 4: dark/light balance
  let dark = 0;
  for (let r = 0; r < n; r += 1) {
    for (let c = 0; c < n; c += 1) {
      if (modules[r][c]) dark += 1;
    }
  }
  const pct = (dark * 100) / (n * n);
  const k = Math.floor(Math.abs(pct - 50) / 5);
  penalty += k * 10;

  return penalty;
}

const SAFE_QR_COLOR_RE =
  /^(?:#(?:[0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})|[a-z]{1,20}|(?:rgb|hsl)a?\(\s*[0-9.,%\s/]+\))$/i;

function escapeQrXmlAttr(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function sanitizeQrColor(raw: unknown, fallback: string): string {
  if (typeof raw !== "string") return fallback;
  const trimmed = raw.trim();
  if (!trimmed || !SAFE_QR_COLOR_RE.test(trimmed)) {
    return fallback;
  }
  return escapeQrXmlAttr(trimmed);
}

export function colabOneEncodeQrMatrix(text: string): ColabOneQrMatrix {
  const bytes = new TextEncoder().encode(String(text || ""));
  const spec = QR_VERSION_SPECS_M.find((s) => bytes.length <= s.capacityBytes);
  if (!spec) {
    throw new Error("QR payload exceeds supported length (max 412 bytes)");
  }

  const dataCw = encodeDataCodewords(bytes, spec);
  const codewords = buildFinalCodewords(dataCw, spec);
  const { size, modules: baseModules, isFunc } = buildFunctionTemplate(spec);

  // Place data bits in standard upward/downward 2-column zigzag
  let bitIdx = 0;
  const totalBits = codewords.length * 8;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    const upward = ((right + 1) & 2) === 0;
    for (let vert = 0; vert < size; vert += 1) {
      const r = upward ? size - 1 - vert : vert;
      for (let j = 0; j < 2; j += 1) {
        const c = right - j;
        if (isFunc[r][c]) continue;
        let dark = false;
        if (bitIdx < totalBits) {
          dark = ((codewords[bitIdx >> 3] >> (7 - (bitIdx & 7))) & 1) === 1;
          bitIdx += 1;
        }
        baseModules[r][c] = dark;
      }
    }
  }

  // Evaluate all 8 masks and choose lowest penalty
  let bestMask = 0;
  let bestScore = Number.POSITIVE_INFINITY;
  let bestModules = baseModules;

  for (let mask = 0; mask < 8; mask += 1) {
    const cand = baseModules.map((row, r) =>
      row.map((val, c) =>
        isFunc[r][c] ? val : maskPredicate(mask, r, c) ? !val : val,
      ),
    );
    drawFormatBits(cand, mask);
    const sc = colabOneScoreQrPenalty(cand);
    if (sc < bestScore) {
      bestScore = sc;
      bestMask = mask;
      bestModules = cand;
    }
  }
  drawFormatBits(bestModules, bestMask);

  return {
    version: spec.version,
    size,
    modules: bestModules,
  };
}

export function colabOneRenderQrSvg(
  text: string,
  opts?: { margin?: number; fg?: string; bg?: string },
): string {
  const { size, modules } = colabOneEncodeQrMatrix(text);
  const rawMargin = opts?.margin;
  const margin =
    typeof rawMargin === "number" && Number.isFinite(rawMargin)
      ? Math.max(2, Math.min(16, Math.floor(rawMargin)))
      : 4;
  const fg = sanitizeQrColor(opts?.fg, "#0f172a");
  const bg = sanitizeQrColor(opts?.bg, "#ffffff");
  const viewSize = size + margin * 2;
  const parts: string[] = [];
  for (let r = 0; r < size; r += 1) {
    for (let c = 0; c < size; c += 1) {
      if (modules[r][c]) {
        parts.push(`M${c + margin},${r + margin}h1v1h-1z`);
      }
    }
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${viewSize} ${viewSize}" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="${bg}"/><path fill="${fg}" d="${parts.join("")}"/></svg>`;
}

export function colabOneRenderQrSvgDataUrl(
  text: string,
  opts?: { margin?: number; fg?: string; bg?: string },
): string {
  const svg = colabOneRenderQrSvg(text, opts);
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}
