import { readFile, writeFile } from "node:fs/promises";
import { Resvg } from "@resvg/resvg-js";

// SVG is the editable source of truth. MyGo makes ICNS/ICO from this PNG.
const resources = new URL("../../console/resources/", import.meta.url);
const svg = await readFile(new URL("icon.svg", resources), "utf8");
const png = new Resvg(svg, {
  fitTo: { mode: "width", value: 1024 },
  font: { loadSystemFonts: false },
})
  .render()
  .asPng();
await writeFile(new URL("icon.png", resources), png);
console.log("Generated transparent desktop icon from icon.svg");
