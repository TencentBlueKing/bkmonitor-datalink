const assert = require("node:assert/strict");
const { spawnSync } = require("node:child_process");
const { mkdtempSync, readFileSync, existsSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { resolve, join } = require("node:path");
const { test } = require("node:test");
const { parse } = require("yaml");

const root = resolve(__dirname, "../../..");
const workflow = parse(readFileSync(join(root, ".github/workflows/linkd-images.yml"), "utf8"));
const inputs = workflow.on.workflow_dispatch.inputs;
const metadata = workflow.jobs.prepare.steps.find(step => step.id === "metadata");
const context = workflow.jobs.build.steps.find(step => step.id === "context");

function execute(script, env, cwd = root) {
  const directory = mkdtempSync(join(tmpdir(), "linkd-workflow-"));
  const output = join(directory, "output");
  try {
    const result = spawnSync("bash", ["-c", script], {
      cwd, encoding: "utf8", timeout: 5000,
      env: {...process.env, GITHUB_OUTPUT: output, GITHUB_SHA: "abcdef0123456789abcdef0123456789abcdef0123", GITHUB_RUN_ID: "123", GITHUB_RUN_ATTEMPT: "1", GITHUB_REPOSITORY: "Example/Linkd", ...env},
    });
    const values = Object.fromEntries((existsSync(output) ? readFileSync(output, "utf8") : "")
      .trim().split("\n").filter(Boolean).map(line => {
        const index = line.indexOf("=");
        return [line.slice(0, index), line.slice(index + 1)];
      }));
    return {...result, values};
  } finally {
    rmSync(directory, {recursive: true, force: true});
  }
}

test("workflow remains manual and default all excludes eventgen", () => {
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.equal(inputs.component.default, "all");
  assert.ok(inputs.component.options.includes("linkd-eventgen"));
  assert.equal(inputs.eventgen_version.default, "0.1.0");
  const result = execute(metadata.run, {COMPONENT: inputs.component.default, VERSION: "0.1.1", EVENTGEN_VERSION: "0.1.0"});
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.values.matrix).component, ["linkd", "linkd-console"]);
  assert.equal(result.values.version, "0.1.1");
});

test("eventgen uses only its own version, including the unique-tag fallback", () => {
  assert.equal(metadata.env.EVENTGEN_VERSION, "${{ inputs.eventgen_version }}");
  for (const version of ["0.1.0", "0.2.0-rc.1", ""]) {
    const result = execute(metadata.run, {COMPONENT: "linkd-eventgen", VERSION: "ignored/shared", EVENTGEN_VERSION: version});
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.values.matrix).component, ["linkd-eventgen"]);
    assert.equal(result.values.version, version || "sha-abcdef012345-123.1");
  }
  const normal = execute(metadata.run, {COMPONENT: "linkd", VERSION: "0.1.1", EVENTGEN_VERSION: "ignored/eventgen"});
  assert.equal(normal.status, 0, normal.stderr);
  assert.equal(normal.values.version, "0.1.1");
});

test("invalid selected versions and unknown components fail before building", () => {
  for (const version of ["bad/tag", "bad tag", ".invalid", "a".repeat(129)]) {
    const result = execute(metadata.run, {COMPONENT: "linkd-eventgen", VERSION: "0.1.1", EVENTGEN_VERSION: version});
    assert.notEqual(result.status, 0);
    assert.equal(result.values.matrix, undefined);
  }
  assert.notEqual(execute(metadata.run, {COMPONENT: "unexpected", VERSION: "", EVENTGEN_VERSION: "0.1.0"}).status, 0);
  const helm = execute(metadata.run, {COMPONENT: "helm", VERSION: "", EVENTGEN_VERSION: "0.1.0"});
  assert.equal(helm.status, 0, helm.stderr);
  assert.deepEqual(JSON.parse(helm.values.matrix).component, []);
});

test("each optional image resolves its real build context and Dockerfile", () => {
  for (const [component, directory, file] of [
    ["linkd", "pkg/linkd", "Dockerfile"],
    ["linkd-console", "pkg/linkd/console", "Dockerfile"],
    ["linkd-eventgen", "pkg/linkd", "Dockerfile.eventgen"],
  ]) {
    const result = execute(context.run, {COMPONENT: component});
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.values.path, directory);
    assert.equal(result.values.dockerfile, directory + "/" + file);
  }
  const missing = execute(context.run, {COMPONENT: "linkd-eventgen"}, tmpdir());
  assert.notEqual(missing.status, 0);
});
