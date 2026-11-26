import { access, cp, mkdir, readdir, rm, stat, writeFile } from "node:fs/promises";

const distDir = new URL("../dist/", import.meta.url);
const targetRootDir = new URL("../../console/frontend/", import.meta.url);
const targetDistDir = new URL("../../console/frontend/dist/", import.meta.url);
const distIndex = new URL("index.html", distDir);
const targetGitkeep = new URL(".gitkeep", targetRootDir);

let indexStat;
try {
  indexStat = await stat(distIndex);
} catch {
  console.error(
    "Missing frontend/dist/index.html. Run `vite build` before syncing console/frontend/dist.",
  );
  process.exit(1);
}

if (!indexStat.isFile()) {
  console.error("frontend/dist/index.html is not a regular file.");
  process.exit(1);
}

await mkdir(targetRootDir, { recursive: true });

const existingEntries = await readdir(targetRootDir);
for (const entry of existingEntries) {
  if (entry === ".gitkeep") continue;
  await rm(new URL(entry, targetRootDir), { recursive: true, force: true });
}

await mkdir(targetDistDir, { recursive: true });

const distEntries = await readdir(distDir);
for (const entry of distEntries) {
  if (entry === ".gitkeep") continue;
  await cp(new URL(entry, distDir), new URL(entry, targetDistDir), {
    recursive: true,
  });
}

try {
  await access(targetGitkeep);
} catch {
  await writeFile(targetGitkeep, "");
}

console.log("Synced frontend/dist into console/frontend/dist");
