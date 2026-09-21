import os from "node:os";
import path from "node:path";

const platforms = new Map([
  ["win32", "windows"],
  ["darwin", "darwin"],
  ["linux", "linux"],
]);

const architectures = new Map([
  ["x64", "amd64"],
  ["arm64", "arm64"],
]);

export function resolveTarget(platform = process.platform, arch = process.arch) {
  const goos = platforms.get(platform);
  const goarch = architectures.get(arch);
  if (!goos || !goarch) {
    throw new Error(`unsupported platform: ${platform}/${arch}`);
  }
  return { goos, goarch, extension: goos === "windows" ? ".exe" : "" };
}

export function normalizeVersion(version) {
  const normalized = String(version).replace(/^v/, "");
  if (!/^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/.test(normalized)) {
    throw new Error(`invalid release version: ${version}`);
  }
  return normalized;
}

export function assetName(version, platform = process.platform, arch = process.arch) {
  const normalized = normalizeVersion(version);
  const target = resolveTarget(platform, arch);
  return `mys_${normalized}_${target.goos}_${target.goarch}${target.extension}`;
}

export function binaryName(platform = process.platform) {
  return platform === "win32" ? "mys.exe" : "mys";
}

export function pathForPlatform(platform = process.platform) {
  return platform === "win32" ? path.win32 : path.posix;
}

export function defaultInstallDir(platform = process.platform, env = process.env, home = os.homedir()) {
  const platformPath = pathForPlatform(platform);
  if (platform === "win32") {
    const base = env.LOCALAPPDATA || (env.USERPROFILE && platformPath.join(env.USERPROFILE, "AppData", "Local"));
    if (!base) {
      throw new Error("cannot determine LOCALAPPDATA for installation");
    }
    return platformPath.join(base, "miyoushe-cli", "bin");
  }
  return platformPath.join(home, ".local", "bin");
}
