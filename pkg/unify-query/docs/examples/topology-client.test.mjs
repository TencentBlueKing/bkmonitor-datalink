import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { alignFrame, entityKey, parseTopologyJSON, queryRelations, readTopologyItem, RelationHTTPError } from "./topology-client.mjs";

const catalog = JSON.parse(readFileSync(new URL("../api/timegraph-schema.json", import.meta.url)));
const scenarioFixture = JSON.parse(readFileSync(new URL("topology-scenarios.json", import.meta.url)));
const scenarios = scenarioFixture.cases;

test("snapshot uint64 tokens remain exact; identities ignore request IDs and optional properties", () => {
  const frame = parseTopologyJSON('{"timestamp":1700000000000,"partial":false,"nodes":[{"id":18446744073709551615,"resource_type":"container","dimensions":{"bcs_cluster_id":"example-cluster","namespace":"demo","pod":"pod-a","container":"app-a","version":"v1"}},{"id":9007199254740993,"resource_type":"pod","dimensions":{"bcs_cluster_id":"example-cluster","namespace":"demo","pod":"pod-a"}}],"edges":[{"source":18446744073709551615,"target":9007199254740993,"relation_type":"one","metric_name":"m1","category":"static","direction":"outbound"},{"source":18446744073709551615,"target":9007199254740993,"relation_type":"two","metric_name":"m2","category":"static","direction":"outbound"}]}');
  assert.equal(frame.nodes[0].id, "18446744073709551615");
  assert.equal(frame.nodes[1].id, "9007199254740993");
  const changed = structuredClone(frame);
  changed.nodes[0].id = "42"; changed.nodes[0].dimensions.version = "v2";
  changed.nodes[1].id = "7"; changed.nodes.reverse();
  for (const edge of changed.edges) { edge.source = "42"; edge.target = "7"; }
  changed.edges.reverse();
  const a = alignFrame(frame, catalog), b = alignFrame(changed, catalog);
  assert.deepEqual(new Set(a.nodes.keys()), new Set(b.nodes.keys()));
  assert.deepEqual(a.edges, b.edges); assert.equal(a.edges.size, 2);
  assert.notDeepEqual(a.nodes, b.nodes); // Optional attributes may legitimately change.
  assert.throws(() => entityKey({ resource_type: "container", dimensions: { pod: "pod-a" } }, catalog), /Missing primary/);
  assert.throws(() => parseTopologyJSON('{"id":18446744073709551616}'), /uint64/);
  assert.throws(() => readTopologyItem({ code: 200, response_format: "compact-v1", compact: null }), /compact/);
  assert.equal(readTopologyItem({ code: 400, compact: null, message: "failed" }).ok, false);
});

const endpoint = process.env.TG_EXAMPLE_URL;
test("consumer serializer to real UQ HTTP handler/model and back", { skip: !endpoint }, async () => {
  const call = (path, queryList) => queryRelations({ baseURL: endpoint, spaceUID: "example-space", path, queryList });
  for (const scenario of scenarios) {
    if (!scenario.path.includes("topology")) {
      const { data } = await call(scenario.path, [scenario.query]);
      assert.equal(data[0].code, 200, JSON.stringify(data));
      if (scenario.name === "legacy-target") {
        assert.deepEqual(data[0].path, ["node", "pod"]);
        assert.equal(data[0].target_list.length, 3);
        for (const frame of data[0].target_list) assert.equal(frame.items[0].pod, "pod-a");
      } else {
        assert.equal(data[0].results.length, 3);
        for (const result of data[0].results) assert.equal(result.path.at(-1).dimensions.pod, "pod-a");
      }
      continue;
    }
    const queryList = ["snapshots", "compact-v1"].map((response_format) => ({ ...scenario.query, response_format }));
    const before = structuredClone(queryList);
    const { data } = await call(scenario.path, queryList);
    assert.deepEqual(queryList, before, "wrapper must not mutate input");
    const readers = data.map(readTopologyItem);
    assert.ok(readers.every((r) => r.ok), `${scenario.name}: ${JSON.stringify(data)}`);
    assert.equal(readers[0].pointCount, 3);
    for (let i = 0; i < 3; i++) {
      const left = readers[0].frame(i), right = readers[1].frame(i);
      assert.equal(left.nodes.length, scenario.expected_node_counts[i], scenario.name);
      assert.equal(left.timestamp, 1700000000000 + i * 60000);
      assert.equal(left.edges.length, scenario.expected_edge_counts[i], scenario.name);
      assert.deepEqual(new Set(left.nodes.map((node) => entityKey(node, catalog))), new Set(scenario.expected_entities_by_frame[i].map((key) => entityKey(scenarioFixture.entities[key], catalog))), scenario.name);
      assert.deepEqual(alignFrame(left, catalog), alignFrame(right, catalog), scenario.name);
      assert.equal(left.partial, false, scenario.name);
      if (scenario.name === "attribute-roots") assert.ok(left.nodes.filter((n) => n.resource_type === "container").every((n) => n.dimensions.version === "v2"));
      if (scenario.name === "historical-detail") assert.equal(left.nodes.find((n) => n.resource_type === "container").dimensions.version, i === 0 ? "v1" : "v2");
      if (scenario.name === "output-filter") assert.equal(left.edges.length, 0);
    }
    const instant = { ...scenario.query, timestamp: scenario.query.start_time };
    delete instant.start_time; delete instant.end_time; delete instant.step;
    const single = await call("v1beta3/topology", [instant]);
    const reader = readTopologyItem(single.data[0]);
    assert.ok(reader.ok); assert.equal(reader.pointCount, 1);
    assert.deepEqual(alignFrame(reader.frame(0), catalog), alignFrame(readers[0].frame(0), catalog));
  }
  const good = scenarios[0].query;
  const mixed = await call("v1beta3/topology_range", [good, { ...good, source_expand_info: { unknown: "value" }, response_format: "compact-v1" }, { ...good, max_hops: -1 }]);
  assert.deepEqual(mixed.data.map((item) => item.code), [200, 400, 400]);
  assert.equal(mixed.data[1].compact, null);
  assert.deepEqual(mixed.data[2].snapshots, []);
  assert.equal(readTopologyItem(mixed.data[1]).ok, false);
  for (const spaceUID of ["example-partial", "example-empty-info", "example-no-info", "example-no-route", "example-backend-error"]) {
    const q = ["example-empty-info", "example-no-info"].includes(spaceUID) ? scenarios.find((s) => s.name === "attribute-roots").query : good;
    const { data } = await queryRelations({ baseURL: endpoint, spaceUID, path: "v1beta3/topology_range", queryList: [q] });
    if (["example-no-route", "example-backend-error"].includes(spaceUID)) {
      assert.equal(data[0].code, 400, JSON.stringify(data)); assert.deepEqual(data[0].snapshots, []);
    } else {
      const reader = readTopologyItem(data[0]); assert.ok(reader.ok);
      for (let i = 0; i < reader.pointCount; i++) {
        const frame = reader.frame(i);
        assert.equal(frame.partial, ["example-partial", "example-no-info"].includes(spaceUID));
        if (spaceUID === "example-no-info") assert.match(frame.partial_reason, /source_info_no_route/);
        if (spaceUID === "example-partial") { assert.match(frame.partial_reason, /backend_partial/); assert.equal(frame.nodes.length, 2); }
        else { assert.equal(frame.nodes.length, 0); }
      }
    }
  }
  await assert.rejects(() => call("v1beta3/topology_range", [42]), (error) => error instanceof RelationHTTPError && error.status === 400 && typeof error.body.error === "string");
  const raw = await fetch(`${endpoint}/api/v1/relation/v1beta3/topology_range`, { method: "POST", body: "{" });
  assert.equal(raw.status, 400); assert.equal(typeof (await raw.json()).error, "string");
  const empty = await call("v1beta3/topology_range", []); assert.deepEqual(empty.data, []);
});

test("published synthetic response fixtures decode without a running service", () => {
  const records = parseTopologyJSON(readFileSync(new URL("topology-responses.json", import.meta.url), "utf8"));
  assert.ok(records.length >= 23);
  let frames = 0, partial = 0, failed = 0;
  for (const record of records) {
    if (!record.path.includes("topology") || record.http_status !== 200) continue;
    assert.equal(record.response.data.length, record.request.query_list.length);
    for (const item of record.response.data) {
      const reader = readTopologyItem(item);
      if (!reader.ok) { failed++; continue; }
      for (let i = 0; i < reader.pointCount; i++) {
        const frame = reader.frame(i);
        alignFrame(frame, catalog); frames++; if (frame.partial) partial++;
      }
    }
  }
  assert.ok(frames > 40 && partial > 0 && failed >= 4);
});
