// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const cli = fileURLToPath(new URL("topology-run.mjs", import.meta.url));
const scenarios = JSON.parse(readFileSync(new URL("topology-scenarios.json", import.meta.url))).cases;
const run = (args = [], env = {}) => spawnSync(process.execPath, [cli, ...args], {
  encoding: "utf8", timeout: 5000,
  env: { ...process.env, UQ_URL: "", UQ_SPACE_UID: "", UQ_JWT: "", ...env },
});
const lines = (text) => text.trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));

test("CLI offline scenarios preserve frames and legacy results", () => {
  for (const scenario of scenarios) {
    const result = run([scenario.name]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stderr, "");
    const output = lines(result.stdout);
    if (scenario.expected_node_counts) {
      assert.deepEqual(output.map((frame) => frame.nodes.length), [...scenario.expected_node_counts, ...scenario.expected_node_counts]);
      assert.deepEqual(output.map((frame) => frame.edges.length), [...scenario.expected_edge_counts, ...scenario.expected_edge_counts]);
      assert.deepEqual(output.map((frame) => frame.timestamp), [1700000000000, 1700000060000, 1700000120000, 1700000000000, 1700000060000, 1700000120000]);
    } else {
      assert.equal(output.length, 1);
      assert.equal(output[0].code, 200);
      if (scenario.name === "legacy-target") assert.equal(output[0].target_list.length, 3);
      else assert.equal(output[0].results.length, 3);
    }
  }
  const defaultRun = run();
  assert.equal(defaultRun.status, 0, defaultRun.stderr);
  assert.equal(defaultRun.stdout, run(["known-node"]).stdout);
  for (const name of ["unknown", ""]) {
    const result = run([name]);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Choose a scenario:/);
  }
});

test("CLI live options require each input", () => {
  const env = { UQ_URL: "http://127.0.0.1:1", UQ_SPACE_UID: "example-space" };
  const args = ["--live", "unused.json", "v1beta3/topology_range"];
  for (const [argv, settings] of [
    [args, { ...env, UQ_URL: "" }], [args, { ...env, UQ_SPACE_UID: "" }],
    [["--live", "", args[2]], env], [["--live", args[1]], env],
  ]) {
    const result = run(argv, settings);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /Set UQ_URL\/UQ_SPACE_UID/);
  }
});

test("CLI live HTTP handles formats, item failures and optional JWT", { skip: !process.env.TG_EXAMPLE_URL }, () => {
  const dir = mkdtempSync(join(tmpdir(), "topology-cli-"));
  try {
    const request = join(dir, "request.json");
    const env = { UQ_URL: process.env.TG_EXAMPLE_URL, UQ_SPACE_UID: "example-space" };
    for (const response_format of ["snapshots", "compact-v1"]) {
      writeFileSync(request, JSON.stringify({ query_list: [{ ...scenarios[0].query, response_format }] }));
      const result = run(["--live", request, scenarios[0].path], env);
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(lines(result.stdout).map((frame) => frame.nodes.length), [2, 2, 2]);
    }
    writeFileSync(request, JSON.stringify({ query_list: [{ ...scenarios[0].query, max_hops: -1 }, scenarios[0].query] }));
    const failed = run(["--live", request, scenarios[0].path], env);
    assert.equal(failed.status, 1);
    assert.equal(lines(failed.stderr)[0].code, 400);
    assert.deepEqual(lines(failed.stdout).map((frame) => frame.nodes.length), [2, 2, 2]);
    writeFileSync(request, JSON.stringify({ query_list: [] }));
    for (const jwt of ["", "example-cli-jwt"]) {
      const result = run(["--live", request, scenarios[0].path], {
        ...env, UQ_JWT: jwt, UQ_SPACE_UID: jwt ? "example-cli-with-jwt" : "example-cli-without-jwt",
      });
      assert.equal(result.status, 0, result.stderr);
      assert.equal(result.stdout, "");
      assert.equal(result.stderr, "");
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
