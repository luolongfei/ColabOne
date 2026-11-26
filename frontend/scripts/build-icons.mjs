import { mkdir, readFile, writeFile } from "node:fs/promises";
import { Resvg } from "@resvg/resvg-js";

// SVG is the editable source of truth. MyGo makes ICNS/ICO from icon.png,
// while webconsole, oauthpage, and frontend/index.html consume the synced
// brandicon assets derived here.
const resourcesDir = new URL("../../console/resources/", import.meta.url);
const brandiconDir = new URL("../../console/internal/brandicon/", import.meta.url);

const rawViewBox = 'viewBox="0 0 1254 1254"';
const trimmedViewBox = 'viewBox="120 120 1014 1014"';

function trimDesktopIconGutter(svgText) {
  if (!svgText.includes(rawViewBox)) {
    throw new Error(
      `Expected ${rawViewBox} in console/resources/icon.svg so inline/web icons can trim the desktop gutter`,
    );
  }
  return svgText.replace(rawViewBox, trimmedViewBox);
}

function renderPng(svgText, width, background) {
  const opts = {
    fitTo: { mode: "width", value: width },
    font: { loadSystemFonts: false },
  };
  if (background) {
    opts.background = background;
  }
  return new Resvg(svgText, opts).render().asPng();
}

function encodePngIco(entries) {
  // Standard ICO container embedding PNG frames (supported by all modern browsers).
  const headerSize = 6;
  const dirEntrySize = 16;
  const header = Buffer.alloc(headerSize + entries.length * dirEntrySize);
  header.writeUInt16LE(0, 0); // reserved
  header.writeUInt16LE(1, 2); // type: 1 = icon
  header.writeUInt16LE(entries.length, 4);

  let offset = header.length;
  const chunks = [header];
  for (let i = 0; i < entries.length; i++) {
    const { size, png } = entries[i];
    const entryOffset = headerSize + i * dirEntrySize;
    const dimByte = size >= 256 ? 0 : size;
    header.writeUInt8(dimByte, entryOffset + 0); // width
    header.writeUInt8(dimByte, entryOffset + 1); // height
    header.writeUInt8(0, entryOffset + 2); // color palette count
    header.writeUInt8(0, entryOffset + 3); // reserved
    header.writeUInt16LE(1, entryOffset + 4); // color planes
    header.writeUInt16LE(32, entryOffset + 6); // bits per pixel
    header.writeUInt32LE(png.length, entryOffset + 8); // image data length
    header.writeUInt32LE(offset, entryOffset + 12); // image data offset
    offset += png.length;
    chunks.push(Buffer.from(png));
  }
  return Buffer.concat(chunks);
}

const rawSvgBuf = await readFile(new URL("icon.svg", resourcesDir));
const rawSvg = rawSvgBuf.toString("utf8");
const trimmedSvg = trimDesktopIconGutter(rawSvg);

const desktopPng = renderPng(rawSvg, 1024);
await writeFile(new URL("icon.png", resourcesDir), desktopPng);

await mkdir(brandiconDir, { recursive: true });
await writeFile(new URL("icon.svg", brandiconDir), rawSvgBuf);
await writeFile(new URL("favicon.svg", brandiconDir), trimmedSvg, "utf8");

// iOS fills transparent areas of apple-touch-icon with black, so render on solid white.
const appleTouchPng = renderPng(trimmedSvg, 180, "#ffffff");
await writeFile(new URL("apple-touch-icon.png", brandiconDir), appleTouchPng);

const faviconIco = encodePngIco([
  { size: 16, png: renderPng(trimmedSvg, 16) },
  { size: 32, png: renderPng(trimmedSvg, 32) },
  { size: 48, png: renderPng(trimmedSvg, 48) },
]);
await writeFile(new URL("favicon.ico", brandiconDir), faviconIco);

console.log(
  "Generated desktop icon.png and synced brandicon web/favicon assets from icon.svg",
);
