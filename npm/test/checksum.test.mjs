import assert from "node:assert/strict";
import test from "node:test";

import { isDirectoryOnPath, parseChecksumFile, sha256 } from "../scripts/install.mjs";

test("selects the checksum for the exact asset name", () => {
  const wanted = "mys_1.2.3_linux_amd64";
  const hash = "a".repeat(64);
  const text = `${"b".repeat(64)}  mys_1.2.3_darwin_amd64\n${hash}  ${wanted}\n`;
  assert.equal(parseChecksumFile(text, wanted), hash);
  assert.throws(() => parseChecksumFile(text, `${wanted}.zip`), /checksum not found/);
});

test("computes SHA-256", () => {
  assert.equal(sha256(Buffer.from("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
});

test("detects whether the install directory is already on PATH", () => {
  assert.equal(isDirectoryOnPath("/home/u/.local/bin", "/usr/bin:/home/u/.local/bin", "linux"), true);
  assert.equal(isDirectoryOnPath("/home/u/.local/bin", "/usr/bin", "linux"), false);
  assert.equal(
    isDirectoryOnPath("C:\\Users\\u\\bin", "C:\\Windows;C:\\USERS\\U\\BIN", "win32"),
    true,
  );
});
