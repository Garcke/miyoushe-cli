#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

import { install } from "./install.mjs";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const packageJSON = JSON.parse(await readFile(path.join(scriptDir, "..", "package.json"), "utf8"));

function usage() {
  console.log(`miyoushe-cli installer ${packageJSON.version}

Usage:
  npx @garcke/miyoushe-cli@latest install [--install-dir <path>]

The installer downloads the matching native binary from GitHub Releases,
verifies its SHA-256 checksum, and installs it for the current user.`);
}

function parseInstallArgs(args) {
  let installDir;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--install-dir") {
      if (!args[i + 1]) {
        throw new Error("--install-dir requires a path");
      }
      installDir = path.resolve(args[++i]);
      continue;
    }
    throw new Error(`unknown argument: ${args[i]}`);
  }
  return { installDir };
}

const [command, ...args] = process.argv.slice(2);
try {
  if (command === "install") {
    const options = parseInstallArgs(args);
    const result = await install({ version: packageJSON.version, ...options });
    console.log(`miyoushe-cli ${result.version} installed to ${result.destination}`);
    if (!result.pathConfigured) {
      console.log(`Add ${result.installDir} to PATH, then open a new terminal.`);
    }
  } else if (command === "--version" || command === "-v") {
    console.log(packageJSON.version);
  } else if (!command || command === "--help" || command === "-h" || command === "help") {
    usage();
  } else {
    throw new Error(`unknown command: ${command}`);
  }
} catch (error) {
  console.error(`Installation failed: ${error.message}`);
  process.exitCode = 1;
}
