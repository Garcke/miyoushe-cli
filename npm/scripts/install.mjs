import { createHash } from "node:crypto";
import { constants as fsConstants } from "node:fs";
import { access, chmod, mkdir, rename, rm, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";

import { assetName, binaryName, defaultInstallDir, normalizeVersion, pathForPlatform } from "./platform.mjs";

const releaseRoot = "https://github.com/Garcke/miyoushe-cli/releases/download";
const allowedDownloadHosts = new Set([
  "github.com",
  "objects.githubusercontent.com",
  "release-assets.githubusercontent.com",
]);

export function parseChecksumFile(text, wantedName) {
  for (const rawLine of String(text).split(/\r?\n/)) {
    const match = rawLine.trim().match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    if (match && match[2] === wantedName) {
      return match[1].toLowerCase();
    }
  }
  throw new Error(`checksum not found for ${wantedName}`);
}

export function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

function validateDownloadURL(value) {
  const url = new URL(value);
  if (url.protocol !== "https:" || !allowedDownloadHosts.has(url.hostname)) {
    throw new Error(`unexpected download location: ${url.origin}`);
  }
}

async function download(url, label) {
  validateDownloadURL(url);
  const response = await fetch(url, {
    redirect: "follow",
    headers: { "user-agent": "miyoushe-cli-installer" },
    signal: AbortSignal.timeout(300_000),
  });
  if (!response.ok) {
    throw new Error(`failed to download ${label}: HTTP ${response.status}`);
  }
  validateDownloadURL(response.url);
  return Buffer.from(await response.arrayBuffer());
}

async function exists(file) {
  try {
    await access(file, fsConstants.F_OK);
    return true;
  } catch {
    return false;
  }
}

function verifyBinary(file, version) {
  const result = spawnSync(file, ["--version"], {
    encoding: "utf8",
    timeout: 15_000,
    windowsHide: true,
  });
  if (result.error) {
    throw new Error(`installed binary could not start: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error(`installed binary returned exit code ${result.status}: ${String(result.stderr).trim()}`);
  }
  if (!String(result.stdout).trim().startsWith(`mys version ${version}`)) {
    throw new Error(`installed binary version mismatch: ${String(result.stdout).trim()}`);
  }
}

async function replaceAtomically(tempFile, destination) {
  const backup = `${destination}.backup-${process.pid}`;
  const hadExisting = await exists(destination);
  try {
    if (hadExisting) {
      await rm(backup, { force: true });
      await rename(destination, backup);
    }
    await rename(tempFile, destination);
    if (hadExisting) {
      // Installation has already succeeded. A stale backup is preferable to
      // reporting failure or rolling back a working binary if cleanup fails.
      await rm(backup, { force: true }).catch(() => {});
    }
  } catch (error) {
    await rm(tempFile, { force: true }).catch(() => {});
    if (hadExisting && await exists(backup) && !await exists(destination)) {
      await rename(backup, destination).catch(() => {});
    }
    throw error;
  }
}

export function isDirectoryOnPath(directory, envPath = process.env.PATH || "", platform = process.platform) {
  const platformPath = pathForPlatform(platform);
  const normalize = (value) => {
    const resolved = platformPath.resolve(value);
    return platform === "win32" ? resolved.toLowerCase() : resolved;
  };
  const target = normalize(directory);
  return envPath.split(platformPath.delimiter).some((entry) => entry && normalize(entry) === target);
}

export async function install({
  version,
  installDir = process.env.MIYOUSHE_CLI_INSTALL_DIR || defaultInstallDir(),
  platform = process.platform,
  arch = process.arch,
} = {}) {
  const normalizedVersion = normalizeVersion(version);
  const platformPath = pathForPlatform(platform);
  const asset = assetName(normalizedVersion, platform, arch);
  const tag = `v${normalizedVersion}`;
  const checksumsURL = `${releaseRoot}/${tag}/checksums.txt`;
  const binaryURL = `${releaseRoot}/${tag}/${asset}`;

  const [checksums, binary] = await Promise.all([
    download(checksumsURL, "checksums.txt"),
    download(binaryURL, asset),
  ]);
  const expected = parseChecksumFile(checksums.toString("utf8"), asset);
  const actual = sha256(binary);
  if (actual !== expected) {
    throw new Error(`checksum verification failed for ${asset}`);
  }

  await mkdir(installDir, { recursive: true, mode: 0o755 });
  const destination = platformPath.join(installDir, binaryName(platform));
  const suffix = platform === "win32" ? ".exe" : "";
  const tempFile = platformPath.join(installDir, `.mys-${process.pid}-${Date.now()}.tmp${suffix}`);
  await writeFile(tempFile, binary, { mode: 0o755, flag: "wx" });
  if (platform !== "win32") {
    await chmod(tempFile, 0o755);
  }

  try {
    verifyBinary(tempFile, normalizedVersion);
    await replaceAtomically(tempFile, destination);
  } catch (error) {
    await rm(tempFile, { force: true }).catch(() => {});
    throw error;
  }

  return {
    destination,
    installDir,
    version: normalizedVersion,
    pathConfigured: isDirectoryOnPath(installDir, process.env.PATH || "", platform),
  };
}
