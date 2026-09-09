import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it } from "vitest";
import { formatBuildInfo, readBuildInfo } from "./version.js";

const directories: string[] = [];
afterEach(() => {
  for (const directory of directories.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

function fixture(text?: string) {
  const directory = mkdtempSync(join(tmpdir(), "linkd-console-version-"));
  directories.push(directory);
  const file = join(directory, "build-info.json");
  if (text !== undefined) writeFileSync(file, text);
  return file;
}

it("reports the baked version and full source commit", () => {
  const info = { version: "0.1.0", git_commit: "a".repeat(40) };
  expect(readBuildInfo(fixture(JSON.stringify(info)))).toEqual(info);
  expect(formatBuildInfo(info)).toBe(
    `version: 0.1.0\ngit_commit: ${"a".repeat(40)}\n`,
  );
});

it("reports unknown metadata for an unpackaged development checkout", () => {
  expect(readBuildInfo(fixture())).toEqual({
    version: "dev",
    git_commit: "unknown",
  });
});

it.each(["invalid json", "{}", '{"version":"0.1.0","git_commit":""}'])(
  "rejects corrupt metadata instead of reporting a made-up version: %s",
  (text) => expect(() => readBuildInfo(fixture(text))).toThrow(),
);
