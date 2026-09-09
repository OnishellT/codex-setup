import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { run } from "./pr86-watch-manager.mjs";

const target = (root) => join(root, "dist/daemon/watch-manager.js");
const backup = (root) => join(dirname(target(root)), "watch-manager.js.codex-setup-pr86.original");
const digest = (value) => createHash("sha256").update(value).digest("hex");
const original = "function requiresDirectoryWatchers(platform, nodeVersion) { return nodeVersion === '22.0.0'; }";
const patched = "function requiresDirectoryWatchers(platform, nodeVersion) { return platform === 'linux'; }";
const prefix = "export class WatchManager {\n";
const suffix = "}\n//# sourceMappingURL=watch-manager.js.map";
const source = prefix + original + suffix;
const patchedSource = prefix + patched + suffix + "\n";
const expected = { originalHash: digest(source), patchedHash: digest(patchedSource), original, patched, platform: "linux" };

function fixture(version = "0.2.1") {
  const root = mkdtempSync(join(tmpdir(), "zg-pr86-"));
  mkdirSync(dirname(target(root)), { recursive: true });
  writeFileSync(join(root, "package.json"), JSON.stringify({ name: "@zvec/zvec-grep", version }));
  writeFileSync(target(root), source);
  return root;
}

test("apply, check, reapply, and restore are reversible", () => {
  const root = fixture();
  assert.equal(run("check", root, expected), "original");
  assert.equal(run("apply", root, expected), "patched");
  assert.equal(run("check", root, expected), "patched");
  assert.equal(run("apply", root, expected), "patched");
  assert.equal(run("restore", root, expected), "original");
  assert.equal(readFileSync(target(root), "utf8"), source);
  assert.equal(readFileSync(backup(root), "utf8"), source);
});

test("fails closed for wrong version, unknown content, and corrupt backup", () => {
  const wrong = fixture("0.2.2");
  assert.throws(() => run("apply", wrong, expected), /expected @zvec\/zvec-grep@0.2.1/);
  assert.equal(readFileSync(target(wrong), "utf8"), source);

  const unknown = fixture();
  writeFileSync(target(unknown), "unknown\n");
  assert.throws(() => run("apply", unknown, expected), /unrecognized/);
  assert.equal(readFileSync(target(unknown), "utf8"), "unknown\n");

  const nonLinux = fixture();
  assert.throws(() => run("apply", nonLinux, { ...expected, platform: "darwin" }), /only on Linux/);
  assert.equal(readFileSync(target(nonLinux), "utf8"), source);

  const preexistingBackup = fixture();
  writeFileSync(backup(preexistingBackup), "foreign\n");
  assert.throws(() => run("apply", preexistingBackup, expected), /invalid or foreign backup/);
  assert.equal(readFileSync(target(preexistingBackup), "utf8"), source);

  const corrupt = fixture();
  run("apply", corrupt, expected);
  writeFileSync(backup(corrupt), "foreign\n");
  assert.throws(() => run("restore", corrupt, expected), /valid codex-setup backup/);
  assert.equal(readFileSync(target(corrupt), "utf8"), patchedSource);
});
