#!/usr/bin/env python3
"""Rebuild bundled Sarasa UI SC WOFF2 subsets from the pinned upstream release.

This tool is only run manually when upgrading or refreshing the bundled UI font
assets in ``frontend/src/assets/fonts/``. Normal builds and tests are 100%
offline and consume the committed WOFF2 files directly.

Example:
    uv run --no-project --with py7zr --with "fonttools[woff]" python tools/build_fonts.py
"""

from __future__ import annotations

import argparse
import hashlib
import tempfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUTPUT_DIR = ROOT / "frontend/src/assets/fonts"

SARASA_VERSION = "1.0.43"
SARASA_ARCHIVE = f"SarasaUiSC-TTF-{SARASA_VERSION}.7z"
SARASA_URL = (
    f"https://github.com/be5invis/Sarasa-Gothic/releases/download/"
    f"v{SARASA_VERSION}/{SARASA_ARCHIVE}"
)
SARASA_SHA256 = "3dc0f534b2cfa5f98b58a574948d3e3f9eb140600bd738f259c3d077acb2de66"

WEIGHT_TARGETS: tuple[tuple[str, str], ...] = (
    ("SarasaUiSC-Regular.ttf", "sarasa-ui-sc-regular.woff2"),
    ("SarasaUiSC-SemiBold.ttf", "sarasa-ui-sc-semibold.woff2"),
    ("SarasaUiSC-Bold.ttf", "sarasa-ui-sc-bold.woff2"),
)

# Common modern Simplified Chinese Hanzi outside GB2312.
EXTRA_MODERN_HANZI = "〇啰昇旻晞镕喆犇燚囧吒牠妳"

# Core Unicode blocks for bilingual Simplified Chinese / Latin desktop UI.
UI_UNICODE_RANGES: tuple[tuple[int, int], ...] = (
    (0x0020, 0x007E),  # Basic Latin
    (0x00A0, 0x00FF),  # Latin-1 Supplement
    (0x0100, 0x017F),  # Latin Extended-A
    (0x2000, 0x206F),  # General Punctuation
    (0x20A0, 0x20CF),  # Currency Symbols
    (0x2100, 0x218F),  # Letterlike & Number Forms
    (0x2190, 0x21FF),  # Arrows
    (0x2200, 0x22FF),  # Mathematical Operators
    (0x2500, 0x25FF),  # Box Drawing & Geometric Shapes
    (0x2600, 0x26FF),  # Miscellaneous Symbols
    (0x2700, 0x27BF),  # Dingbats
    (0x3000, 0x303F),  # CJK Symbols and Punctuation
    (0xFE10, 0xFE6F),  # Vertical & Small Form Variants
    (0xFF00, 0xFFEF),  # Halfwidth and Fullwidth Forms
)

LAYOUT_FEATURES = [
    "kern",
    "calt",
    "ccmp",
    "locl",
    "tnum",
    "pnum",
    "palt",
    "halt",
    "mark",
    "mkmk",
    "zero",
]

SCAN_DIRS = ("frontend/src", "bridge", "server", "common", "catalog", "console")
SCAN_SUFFIXES = {".vue", ".ts", ".js", ".css", ".html", ".py", ".go", ".json"}


def collect_codepoints() -> set[int]:
    """Build the deterministic Simplified Chinese + UI Unicode subset."""
    codepoints: set[int] = set()

    for lo, hi in UI_UNICODE_RANGES:
        codepoints.update(range(lo, hi + 1))

    # Full GB2312 Simplified Chinese Hanzi (Level 1 + Level 2: 6,763 Hanzi).
    for b1 in range(0xB0, 0xF8):
        for b2 in range(0xA1, 0xFF):
            try:
                ch = bytes([b1, b2]).decode("gb2312")
            except UnicodeDecodeError:
                continue
            codepoints.add(ord(ch))

    for ch in EXTRA_MODERN_HANZI:
        codepoints.add(ord(ch))

    # Guarantee every character appearing in ColabOne sources is included.
    for rel in SCAN_DIRS:
        base = ROOT / rel
        if not base.is_dir():
            continue
        for path in base.rglob("*"):
            if not path.is_file() or path.suffix not in SCAN_SUFFIXES:
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for ch in text:
                cp = ord(ch)
                if cp >= 0x20:
                    codepoints.add(cp)

    return codepoints


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def ensure_archive(archive_path: Path) -> Path:
    if not archive_path.is_file():
        archive_path.parent.mkdir(parents=True, exist_ok=True)
        print(f"Downloading {SARASA_URL} ...")
        urllib.request.urlretrieve(SARASA_URL, archive_path)
    digest = sha256_file(archive_path)
    if digest != SARASA_SHA256:
        raise RuntimeError(
            f"SHA-256 mismatch for {archive_path}: expected {SARASA_SHA256}, got {digest}"
        )
    return archive_path


def build_fonts(archive_path: Path, output_dir: Path) -> None:
    import py7zr
    from fontTools import subset
    from fontTools.ttLib import TTFont

    ensure_archive(archive_path)
    codepoints = collect_codepoints()
    output_dir.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix="colabone-sarasa-") as tmp:
        tmp_dir = Path(tmp)
        ttf_names = [src for src, _ in WEIGHT_TARGETS]
        with py7zr.SevenZipFile(archive_path, mode="r") as zf:
            zf.extract(path=tmp_dir, targets=ttf_names)

        for src_name, dst_name in WEIGHT_TARGETS:
            ttf_path = tmp_dir / src_name
            out_path = output_dir / dst_name
            opts = subset.Options()
            opts.hinting = True
            opts.name_IDs = ["*"]
            opts.layout_features = list(LAYOUT_FEATURES)

            font = TTFont(ttf_path)
            sub = subset.Subsetter(options=opts)
            sub.populate(unicodes=codepoints)
            sub.subset(font)
            font.flavor = "woff2"
            font.save(out_path)

            magic = out_path.read_bytes()[:4]
            if magic != b"wOF2":
                raise RuntimeError(f"Expected WOFF2 header for {out_path}, got {magic!r}")
            size_mb = out_path.stat().st_size / (1024 * 1024)
            print(
                f"Built {out_path.relative_to(ROOT)} "
                f"({size_mb:.2f} MiB, {len(font.getGlyphOrder())} glyphs)"
            )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--archive",
        type=Path,
        default=ROOT / "work/font-cache" / SARASA_ARCHIVE,
        help="Path to the pinned SarasaUiSC-TTF-1.0.43.7z archive",
    )
    parser.add_argument(
        "--out",
        type=Path,
        default=OUTPUT_DIR,
        help="Destination directory for WOFF2 font files",
    )
    args = parser.parse_args()
    build_fonts(args.archive, args.out)


if __name__ == "__main__":
    main()
