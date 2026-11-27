import {
  access,
  cp,
  lstat,
  mkdir,
  mkdtemp,
  readdir,
  rename,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.resolve(scriptDir, "..", "dist");
const targetRootDir = path.resolve(scriptDir, "..", "..", "console", "frontend");
const targetStageParentDir = path.dirname(targetRootDir);
const targetDistDir = path.join(targetRootDir, "dist");
const distIndex = path.join(distDir, "index.html");
const targetGitkeep = path.join(targetRootDir, ".gitkeep");

const RM_OPTIONS = { recursive: true, force: true, maxRetries: 5, retryDelay: 100 };
const WIN_RETRYABLE_RENAME_CODES = new Set(["EPERM", "EACCES", "EBUSY", "ENOTEMPTY"]);

async function renameWithRetry(src, dst) {
  const maxAttempts = process.platform === "win32" ? 5 : 1;
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    try {
      await rename(src, dst);
      return;
    } catch (err) {
      if (
        attempt + 1 >= maxAttempts ||
        process.platform !== "win32" ||
        !WIN_RETRYABLE_RENAME_CODES.has(err?.code)
      ) {
        throw err;
      }
      await new Promise((resolve) => setTimeout(resolve, 60 * (attempt + 1)));
    }
  }
}

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

// Stage strictly outside console/frontend so `//go:embed all:frontend` can never
// pick up an in-progress or leftover staging directory.
const tmpRoot = await mkdtemp(path.join(targetStageParentDir, ".frontend-dist-stage-"));
const stagedDistDir = path.join(tmpRoot, "dist");
const previousDistDir = path.join(tmpRoot, "previous");
let hadPrevious = false;
let installed = false;

try {
  await mkdir(stagedDistDir, { recursive: true });

  const distEntries = await readdir(distDir);
  for (const entry of distEntries) {
    if (entry === ".gitkeep") continue;
    await cp(path.join(distDir, entry), path.join(stagedDistDir, entry), {
      recursive: true,
    });
  }

  try {
    await lstat(targetDistDir);
    await renameWithRetry(targetDistDir, previousDistDir);
    hadPrevious = true;
  } catch (err) {
    if (err?.code !== "ENOENT") {
      throw err;
    }
  }

  try {
    await renameWithRetry(stagedDistDir, targetDistDir);
    installed = true;
  } catch (err) {
    if (hadPrevious) {
      try {
        await renameWithRetry(previousDistDir, targetDistDir);
        hadPrevious = false;
      } catch {
        // Preserve original error; retain tmpRoot for recovery.
      }
    }
    throw err;
  }
} finally {
  if (installed || !hadPrevious) {
    try {
      await rm(tmpRoot, RM_OPTIONS);
    } catch {
      // Best-effort cleanup outside console/frontend so transient locks do not fail the build.
    }
  }
}

const existingEntries = await readdir(targetRootDir);
for (const entry of existingEntries) {
  if (entry === ".gitkeep" || entry === "dist") {
    continue;
  }
  await rm(path.join(targetRootDir, entry), RM_OPTIONS);
}

try {
  await access(targetGitkeep);
} catch {
  await writeFile(targetGitkeep, "");
}

console.log("Synced frontend/dist into console/frontend/dist");
