import assert from "node:assert/strict";
import test from "node:test";

import { assetName, binaryName, defaultInstallDir, normalizeVersion, resolveTarget } from "../scripts/platform.mjs";

test("maps supported Node targets to Go release targets", () => {
  assert.deepEqual(resolveTarget("win32", "x64"), { goos: "windows", goarch: "amd64", extension: ".exe" });
  assert.deepEqual(resolveTarget("darwin", "arm64"), { goos: "darwin", goarch: "arm64", extension: "" });
  assert.deepEqual(resolveTarget("linux", "x64"), { goos: "linux", goarch: "amd64", extension: "" });
});

test("rejects unsupported targets", () => {
  assert.throws(() => resolveTarget("freebsd", "x64"), /unsupported platform/);
  assert.throws(() => resolveTarget("linux", "ia32"), /unsupported platform/);
});

test("builds exact GoReleaser asset names", () => {
  assert.equal(assetName("v1.2.3", "win32", "x64"), "mys_1.2.3_windows_amd64.exe");
  assert.equal(assetName("1.2.3-rc.1", "linux", "arm64"), "mys_1.2.3-rc.1_linux_arm64");
  assert.equal(binaryName("win32"), "mys.exe");
  assert.equal(binaryName("linux"), "mys");
});

test("validates versions before constructing URLs", () => {
  assert.equal(normalizeVersion("v1.2.3"), "1.2.3");
  assert.throws(() => normalizeVersion("../latest"), /invalid release version/);
});

test("uses per-user install directories", () => {
  assert.equal(
    defaultInstallDir("win32", { LOCALAPPDATA: "C:\\Users\\u\\AppData\\Local" }, "ignored"),
    "C:\\Users\\u\\AppData\\Local\\miyoushe-cli\\bin",
  );
  assert.equal(defaultInstallDir("linux", {}, "/home/u"), "/home/u/.local/bin");
});
