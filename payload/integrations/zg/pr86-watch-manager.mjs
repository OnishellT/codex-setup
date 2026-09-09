#!/usr/bin/env node
import { createHash } from "node:crypto";
import { constants, copyFileSync, existsSync, lstatSync, readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const PACKAGE = "@zvec/zvec-grep";
const VERSION = "0.2.1";
const FILE = "dist/daemon/watch-manager.js";
const BACKUP = "watch-manager.js.codex-setup-pr86.original";
const ORIGINAL_HASH = "1f21664fc7e35b5beff88cf165c0dbc9940ef9fddf302dcba5e9d3c87afbc869";
const PATCHED_HASH = "1f0117b13a81c6126a0a0d84df3bdd31885076645dd4cef856f9ef119ff10efa";
const ORIGINAL = `function requiresDirectoryWatchers(platform, nodeVersion) {
    if (platform !== "linux") {
        return false;
    }
    const [major, minor] = nodeVersion.split(".", 2).map(Number);
    return major === 22 && minor === 0;
}`;
const PATCHED = `function requiresDirectoryWatchers(platform, nodeVersion) {
    return platform === "linux";
}`;

const hash = (text) => createHash("sha256").update(text).digest("hex");

function fail(message) {
  throw new Error(message);
}

function packageRootFromNpm() {
  return join(execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim(), ...PACKAGE.split("/"));
}

function paths(packageRoot) {
  const root = resolve(packageRoot);
  const target = join(root, FILE);
  return { root, target, backup: join(dirname(target), BACKUP) };
}

function readRegular(path) {
  if (!existsSync(path) || !lstatSync(path).isFile()) fail(`refusing non-file or missing path: ${path}`);
  return readFileSync(path, "utf8");
}

function validatePackage(root) {
  const manifest = JSON.parse(readRegular(join(root, "package.json")));
  if (manifest.name !== PACKAGE || manifest.version !== VERSION) {
    fail(`expected ${PACKAGE}@${VERSION}, found ${manifest.name ?? "unknown"}@${manifest.version ?? "unknown"}`);
  }
}

function state(target, expected) {
  const value = readRegular(target);
  const digest = hash(value);
  if (digest === expected.originalHash) return { kind: "original", value };
  if (digest === expected.patchedHash) return { kind: "patched", value };
  fail(`unrecognized ${FILE} hash: ${digest}`);
}

function validBackup(path, expected) {
  return existsSync(path) && lstatSync(path).isFile() && hash(readFileSync(path, "utf8")) === expected.originalHash;
}

export function run(command, packageRoot, expected = {}) {
  const values = {
    originalHash: ORIGINAL_HASH,
    patchedHash: PATCHED_HASH,
    original: ORIGINAL,
    patched: PATCHED,
    platform: process.platform,
    ...expected,
  };
  const { root, target, backup } = paths(packageRoot);
  validatePackage(root);
  const current = state(target, values);

  if (command === "check") {
    if (current.kind === "patched" && !validBackup(backup, values)) fail("patched file lacks a valid codex-setup backup");
    return current.kind;
  }
  if (command === "apply") {
    if (values.platform !== "linux") fail("apply is supported only on Linux");
    if (current.kind === "patched") {
      if (!validBackup(backup, values)) fail("refusing patched file without a valid codex-setup backup");
      return "patched";
    }
    const first = current.value.indexOf(values.original);
    if (first < 0 || first !== current.value.lastIndexOf(values.original)) fail("known source function is missing or ambiguous");
    const replaced = current.value.replace(values.original, values.patched);
    const patched = replaced.endsWith("\n") ? replaced : `${replaced}\n`;
    if (hash(patched) !== values.patchedHash) fail("known source content does not produce the expected patched hash");
    if (existsSync(backup)) {
      if (!validBackup(backup, values)) fail("refusing to replace an invalid or foreign backup");
    } else {
      copyFileSync(target, backup, constants.COPYFILE_EXCL);
    }
    writeFileSync(target, patched);
    return "patched";
  }
  if (command === "restore") {
    if (!validBackup(backup, values)) fail("refusing restore without a valid codex-setup backup");
    if (current.kind === "patched") writeFileSync(target, readFileSync(backup, "utf8"));
    return "original";
  }
  fail("usage: pr86-watch-manager.mjs <apply|check|restore> [--package-root PATH]");
}

function cli(argv) {
  const [command, ...rest] = argv;
  let packageRoot;
  if (rest.length === 2 && rest[0] === "--package-root") packageRoot = rest[1];
  else if (rest.length !== 0) fail("usage: pr86-watch-manager.mjs <apply|check|restore> [--package-root PATH]");
  return run(command, packageRoot ?? packageRootFromNpm());
}

try {
  if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) console.log(cli(process.argv.slice(2)));
} catch (error) {
  console.error(`pr86 patch: ${error.message}`);
  process.exitCode = 1;
}
