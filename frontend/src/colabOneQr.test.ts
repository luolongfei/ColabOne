import { describe, expect, it } from "vitest";
import {
  colabOneEncodeQrMatrix,
  colabOneRenderQrSvg,
  colabOneRenderQrSvgDataUrl,
  colabOneScoreQrPenalty,
} from "./colabOneQr";
import { colabOneIsMissingAudioDeviceError } from "./colabOneLiveAudio";

describe("colabOneQr offline ISO/IEC 18004 QR generator", () => {
  it("encodes short and realistic HTTPS Live Studio companion URLs across versions", () => {
    const shortMat = colabOneEncodeQrMatrix("hello");
    expect(shortMat.version).toBe(1);
    expect(shortMat.size).toBe(21);
    expect(shortMat.modules.length).toBe(21);

    const url =
      "https://live-studio.example.com/_colabone_live/remote#code=AbCdEfGhIjKlMnOpQrStUvWxYz012345";
    const mat = colabOneEncodeQrMatrix(url);
    expect(mat.version).toBeGreaterThanOrEqual(5);
    expect(mat.version).toBeLessThanOrEqual(10);
    expect(mat.size).toBe(mat.version * 4 + 17);

    // Top-left 7x7 finder outer border must be dark, inner ring light, 3x3 center dark
    for (let i = 0; i < 7; i += 1) {
      expect(mat.modules[0][i]).toBe(true);
      expect(mat.modules[6][i]).toBe(true);
      expect(mat.modules[i][0]).toBe(true);
      expect(mat.modules[i][6]).toBe(true);
    }
    expect(mat.modules[1][1]).toBe(false);
    expect(mat.modules[3][3]).toBe(true);
    // Fixed dark module at (size - 8, 8)
    expect(mat.modules[mat.size - 8][8]).toBe(true);
  });

  it("renders valid crisp SVG and data URL without external dependencies", () => {
    const url = "https://studio.example.com/_colabone_live/remote#code=token123";
    const svg = colabOneRenderQrSvg(url);
    expect(svg.startsWith("<svg ")).toBe(true);
    expect(svg.endsWith("</svg>")).toBe(true);
    expect(svg).toContain('shape-rendering="crispEdges"');

    const dataUrl = colabOneRenderQrSvgDataUrl(url);
    expect(dataUrl.startsWith("data:image/svg+xml;charset=utf-8,")).toBe(true);
  });

  it("supports long custom-domain companion URLs up to Version 15 ECC-M (412 bytes) and rejects overflow", () => {
    const longHost = `${"a".repeat(60)}.${"b".repeat(60)}.${"c".repeat(60)}.${"d".repeat(55)}.example.com`;
    const longUrl = `https://${longHost}/_colabone_live/remote#code=${"AbCdEfGhIj012345".repeat(3)}`;
    expect(longUrl.length).toBeGreaterThan(250);
    expect(longUrl.length).toBeLessThanOrEqual(412);

    const mat = colabOneEncodeQrMatrix(longUrl);
    expect(mat.version).toBeGreaterThanOrEqual(11);
    expect(mat.version).toBeLessThanOrEqual(15);
    expect(mat.size).toBe(mat.version * 4 + 17);

    const maxMat = colabOneEncodeQrMatrix("x".repeat(412));
    expect(maxMat.version).toBe(15);
    expect(maxMat.size).toBe(77);

    expect(() => colabOneEncodeQrMatrix("x".repeat(413))).toThrow();
  });

  it("round-trips format/version BCH, Reed-Solomon zero syndromes, and payload bytes across Versions 1, 7, 10, 14, and 15", () => {
    // Minimal independent ISO/IEC 18004 verifier (unmask + de-interleave + GF(256) RS syndrome check + byte decode)
    const gfExp = new Uint8Array(512);
    const gfLog = new Uint8Array(256);
    let x = 1;
    for (let i = 0; i < 255; i += 1) {
      gfExp[i] = x;
      gfLog[x] = i;
      x <<= 1;
      if (x & 0x100) x ^= 0x11d;
    }
    for (let i = 255; i < 512; i += 1) gfExp[i] = gfExp[i - 255];
    const gfMul = (a: number, b: number) =>
      a === 0 || b === 0 ? 0 : gfExp[gfLog[a] + gfLog[b]];

    const blockSpecs: Record<
      number,
      { ec: number; g1B: number; g1D: number; g2B: number; g2D: number; align: number[] }
    > = {
      1: { ec: 10, g1B: 1, g1D: 16, g2B: 0, g2D: 0, align: [] },
      7: { ec: 18, g1B: 4, g1D: 31, g2B: 0, g2D: 0, align: [6, 22, 38] },
      10: { ec: 26, g1B: 4, g1D: 43, g2B: 1, g2D: 44, align: [6, 28, 50] },
      14: { ec: 24, g1B: 4, g1D: 40, g2B: 5, g2D: 41, align: [6, 26, 46, 66] },
      15: { ec: 24, g1B: 5, g1D: 41, g2B: 5, g2D: 42, align: [6, 26, 48, 70] },
    };

    const maskPred = (mask: number, r: number, c: number): boolean => {
      switch (mask) {
        case 0: return (r + c) % 2 === 0;
        case 1: return r % 2 === 0;
        case 2: return c % 3 === 0;
        case 3: return (r + c) % 3 === 0;
        case 4: return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0;
        case 5: return ((r * c) % 2) + ((r * c) % 3) === 0;
        case 6: return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0;
        default: return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0;
      }
    };

    const testCases = [
      { payload: "ColabOne!", expectedVersion: 1 },
      { payload: "https://live.example.com/_colabone_live/remote#code=" + "A".repeat(65), expectedVersion: 7 },
      { payload: "https://live.example.com/_colabone_live/remote#code=" + "B".repeat(150), expectedVersion: 10 },
      { payload: "https://live.example.com/_colabone_live/remote#code=" + "C".repeat(300), expectedVersion: 14 },
      { payload: "https://live.example.com/_colabone_live/remote#code=" + "D".repeat(350), expectedVersion: 15 },
    ];

    for (const tc of testCases) {
      const mat = colabOneEncodeQrMatrix(tc.payload);
      expect(mat.version).toBe(tc.expectedVersion);
      const spec = blockSpecs[mat.version];
      const n = mat.size;

      // Read format bits from top-left strip and unmask with 0x5412
      let fmt = 0;
      for (let i = 0; i <= 5; i += 1) if (mat.modules[i][8]) fmt |= 1 << i;
      if (mat.modules[7][8]) fmt |= 1 << 6;
      if (mat.modules[8][8]) fmt |= 1 << 7;
      if (mat.modules[8][7]) fmt |= 1 << 8;
      for (let i = 9; i < 15; i += 1) if (mat.modules[8][14 - i]) fmt |= 1 << i;
      const unmaskedFmt = fmt ^ 0x5412;
      const eccBits = (unmaskedFmt >> 13) & 0x3;
      const mask = (unmaskedFmt >> 10) & 0x7;
      expect(eccBits).toBe(0b00); // Level M

      // Verify second format copy matches identically
      let fmt2 = 0;
      for (let i = 0; i < 8; i += 1) if (mat.modules[8][n - 1 - i]) fmt2 |= 1 << i;
      for (let i = 8; i < 15; i += 1) if (mat.modules[n - 15 + i][8]) fmt2 |= 1 << i;
      expect(fmt2).toBe(fmt);

      // Verify version info blocks match for V >= 7
      if (mat.version >= 7) {
        let v1 = 0;
        let v2 = 0;
        for (let i = 0; i < 18; i += 1) {
          const a = Math.floor(i / 3);
          const b = n - 11 + (i % 3);
          if (mat.modules[a][b]) v1 |= 1 << i;
          if (mat.modules[b][a]) v2 |= 1 << i;
        }
        expect(v1).toBe(v2);
        expect(v1 >> 12).toBe(mat.version);
      }

      // Mark function modules to extract data+EC bits
      const isFunc: boolean[][] = Array.from({ length: n }, () => Array<boolean>(n).fill(false));
      const markFinder = (top: number, left: number) => {
        for (let dr = -1; dr <= 7; dr += 1) {
          for (let dc = -1; dc <= 7; dc += 1) {
            const r = top + dr;
            const c = left + dc;
            if (r >= 0 && r < n && c >= 0 && c < n) isFunc[r][c] = true;
          }
        }
      };
      markFinder(0, 0);
      markFinder(0, n - 7);
      markFinder(n - 7, 0);
      for (let i = 8; i < n - 8; i += 1) {
        isFunc[6][i] = true;
        isFunc[i][6] = true;
      }
      for (let i = 0; i < spec.align.length; i += 1) {
        for (let j = 0; j < spec.align.length; j += 1) {
          if (
            (i === 0 && j === 0) ||
            (i === 0 && j === spec.align.length - 1) ||
            (i === spec.align.length - 1 && j === 0)
          ) {
            continue;
          }
          for (let dr = -2; dr <= 2; dr += 1) {
            for (let dc = -2; dc <= 2; dc += 1) {
              isFunc[spec.align[i] + dr][spec.align[j] + dc] = true;
            }
          }
        }
      }
      for (let i = 0; i <= 8; i += 1) {
        isFunc[8][i] = true;
        isFunc[i][8] = true;
      }
      for (let i = 0; i < 8; i += 1) isFunc[8][n - 1 - i] = true;
      for (let i = 0; i < 7; i += 1) isFunc[n - 1 - i][8] = true;
      isFunc[n - 8][8] = true;
      if (mat.version >= 7) {
        for (let i = 0; i < 18; i += 1) {
          const a = Math.floor(i / 3);
          const b = n - 11 + (i % 3);
          isFunc[a][b] = true;
          isFunc[b][a] = true;
        }
      }

      const rawBits: number[] = [];
      for (let right = n - 1; right >= 1; right -= 2) {
        if (right === 6) right = 5;
        const upward = ((right + 1) & 2) === 0;
        for (let vert = 0; vert < n; vert += 1) {
          const r = upward ? n - 1 - vert : vert;
          for (let j = 0; j < 2; j += 1) {
            const c = right - j;
            if (isFunc[r][c]) continue;
            const unmasked = maskPred(mask, r, c) ? !mat.modules[r][c] : mat.modules[r][c];
            rawBits.push(unmasked ? 1 : 0);
          }
        }
      }

      const totalBlocks = spec.g1B + spec.g2B;
      const totalDataCw = spec.g1B * spec.g1D + spec.g2B * spec.g2D;
      const totalCw = totalDataCw + totalBlocks * spec.ec;
      expect(Math.floor(rawBits.length / 8)).toBe(totalCw);

      const cw = new Uint8Array(totalCw);
      for (let i = 0; i < totalCw; i += 1) {
        let b = 0;
        for (let k = 0; k < 8; k += 1) b = (b << 1) | rawBits[i * 8 + k];
        cw[i] = b;
      }

      // De-interleave data and EC blocks
      const dataBlocks: Uint8Array[] = [];
      for (let i = 0; i < spec.g1B; i += 1) dataBlocks.push(new Uint8Array(spec.g1D));
      for (let i = 0; i < spec.g2B; i += 1) dataBlocks.push(new Uint8Array(spec.g2D));
      const ecBlocks = Array.from({ length: totalBlocks }, () => new Uint8Array(spec.ec));
      let pos = 0;
      const maxD = Math.max(spec.g1D, spec.g2D);
      for (let col = 0; col < maxD; col += 1) {
        for (let b = 0; b < totalBlocks; b += 1) {
          if (col < dataBlocks[b].length) dataBlocks[b][col] = cw[pos++];
        }
      }
      for (let col = 0; col < spec.ec; col += 1) {
        for (let b = 0; b < totalBlocks; b += 1) {
          ecBlocks[b][col] = cw[pos++];
        }
      }

      // Verify every RS syndrome S_k = poly(alpha^k) == 0 for k = 0..ec-1
      for (let b = 0; b < totalBlocks; b += 1) {
        const full = new Uint8Array(dataBlocks[b].length + spec.ec);
        full.set(dataBlocks[b], 0);
        full.set(ecBlocks[b], dataBlocks[b].length);
        for (let k = 0; k < spec.ec; k += 1) {
          const root = gfExp[k];
          let evalVal = 0;
          for (let i = 0; i < full.length; i += 1) {
            evalVal = gfMul(evalVal, root) ^ full[i];
          }
          expect(evalVal).toBe(0);
        }
      }

      // Concatenate data blocks and decode Byte-mode payload
      const streamBits: number[] = [];
      for (const blk of dataBlocks) {
        for (const byte of blk) {
          for (let k = 7; k >= 0; k -= 1) streamBits.push((byte >> k) & 1);
        }
      }
      let cursor = 0;
      const readBits = (len: number) => {
        let val = 0;
        for (let i = 0; i < len; i += 1) val = (val << 1) | streamBits[cursor++];
        return val;
      };
      expect(readBits(4)).toBe(0b0100); // Byte mode
      const countLen = mat.version <= 9 ? 8 : 16;
      const byteLen = readBits(countLen);
      const outBytes = new Uint8Array(byteLen);
      for (let i = 0; i < byteLen; i += 1) outBytes[i] = readBits(8);
      expect(new TextDecoder().decode(outBytes)).toBe(tc.payload);
    }
  });

  it("sanitizes unsafe SVG color attributes and clamps non-finite or out-of-range margins", () => {
    const svg = colabOneRenderQrSvg("hello", {
      fg: '#000" onload="alert(1)',
      bg: "white</rect><script>alert(1)</script>",
      margin: Number.NaN,
    });
    expect(svg).not.toContain("onload");
    expect(svg).not.toContain("<script>");
    expect(svg).toContain('fill="#ffffff"');
    expect(svg).toContain('fill="#0f172a"');
    // Version 1 size = 21, default fallback margin = 4 -> viewBox 29
    expect(svg).toContain('viewBox="0 0 29 29"');

    const clampedMin = colabOneRenderQrSvg("hello", { margin: -10 });
    expect(clampedMin).toContain('viewBox="0 0 25 25"');

    const clampedMax = colabOneRenderQrSvg("hello", { margin: 999 });
    expect(clampedMax).toContain('viewBox="0 0 53 53"');

    const nonFiniteInf = colabOneRenderQrSvg("hello", {
      margin: Number.POSITIVE_INFINITY,
      fg: "rgb(15, 23, 42)",
      bg: "#fafafa",
    });
    expect(nonFiniteInf).toContain('viewBox="0 0 29 29"');
    expect(nonFiniteInf).toContain('fill="#fafafa"');
    expect(nonFiniteInf).toContain('fill="rgb(15, 23, 42)"');
  });

  it("applies ISO/IEC 18004 Penalty Rule 3 (+40 per 1:1:3:1:1 finder-like row/col pattern)", () => {
    const n = 15;
    // Start with alternating checkerboard (0 runs >=5, 0 2x2 blocks, 0 rule-3 hits)
    const makeChecker = (): boolean[][] =>
      Array.from({ length: n }, (_, r) =>
        Array.from({ length: n }, (_, c) => (r + c) % 2 === 0),
      );

    // Baseline grid vs grid with an explicit 10111010000 row pattern at row 0
    const rowGrid = makeChecker();
    const rowPattern = [true, false, true, true, true, false, true, false, false, false, false];
    for (let c = 0; c < rowPattern.length; c += 1) {
      rowGrid[0][c] = rowPattern[c];
    }
    const rowWithoutRule3 = rowGrid.map((r) => r.slice());
    // Flip final light module of the 4-light quiet zone (index 10) to break Rule 3
    // while keeping counts comparable (difference must include the +40 Rule 3 penalty).
    rowWithoutRule3[0][10] = true;
    expect(
      colabOneScoreQrPenalty(rowGrid) - colabOneScoreQrPenalty(rowWithoutRule3),
    ).toBeGreaterThanOrEqual(30);

    // Column reverse pattern 00001011101 at col 0
    const colGrid = makeChecker();
    const colPattern = [false, false, false, false, true, false, true, true, true, false, true];
    for (let r = 0; r < colPattern.length; r += 1) {
      colGrid[r][0] = colPattern[r];
    }
    const colWithoutRule3 = colGrid.map((r) => r.slice());
    colWithoutRule3[0][0] = true;
    expect(
      colabOneScoreQrPenalty(colGrid) - colabOneScoreQrPenalty(colWithoutRule3),
    ).toBeGreaterThanOrEqual(30);
  });
});

describe("colabOneIsMissingAudioDeviceError", () => {
  it("recognizes headless Windows server missing-mic errors and ignores permission errors", () => {
    expect(
      colabOneIsMissingAudioDeviceError(new Error("Requested device not found")),
    ).toBe(true);
    const domErr = new Error("No input device");
    domErr.name = "NotFoundError";
    expect(colabOneIsMissingAudioDeviceError(domErr)).toBe(true);

    const denied = new Error("Permission denied");
    denied.name = "NotAllowedError";
    expect(colabOneIsMissingAudioDeviceError(denied)).toBe(false);
  });
});
