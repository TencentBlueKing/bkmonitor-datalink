import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createTopologyReader } from "./topology-compact.mjs";

const largeID = "18446744073709551615";
const fixture = () => ({ version: 1, timestamps: Array.from({length: 129}, (_,i) => i * 1000), nodes: [
  {id: largeID, resource_type: "node", dimensions: {name:"old"}, mask:["8000000000000001"]},
  {id: largeID, resource_type: "node", dimensions: {name:"new"}, mask:["0000000000000000","8000000000000001","0000000000000001"]},
], edges: [{source:largeID,target:largeID,relation_type:"self",metric_name:"flow",category:"dynamic",direction:"outbound",mask:["0000000000000001"]}], partial:[{index:64,reason:"backend_partial"}] });

test("exact IDs, extended masks, versions, partial and lazy ownership", () => {
  const input = fixture();
  const reader = createTopologyReader(input);
  assert.equal(reader.frame(0).nodes[0].id, largeID);
  assert.equal(reader.frame(63).nodes[0].dimensions.name, "old");
  for (const i of [64,127,128]) assert.equal(reader.frame(i).nodes[0].dimensions.name, "new");
  assert.deepEqual(reader.frame(1), {timestamp:1000,nodes:[],edges:[],partial:false});
  assert.equal(reader.frame(64).partial_reason, "backend_partial");
  reader.frame(0).nodes[0].dimensions.name = "changed";
  input.nodes[0].dimensions.name = "changed";
  assert.equal(reader.frame(0).nodes[0].dimensions.name, "old");
  assert.throws(() => reader.frame(129), RangeError);
});

test("reject malformed masks, IDs, overlaps and dangling edges", () => {
  for (const change of [
    x => { x.version = 2; },
    x => { x.nodes[0].id = Number(largeID); },
    x => { x.nodes[0].mask = ["fffffffffffffffff"]; },
    x => { x.nodes[0].mask = ["0000000000000000","0000000000000000","0000000000000002"]; },
    x => { x.nodes[1].mask = ["0000000000000001"]; },
    x => { x.edges[0].source = "12"; },
  ]) { const input = fixture(); change(input); assert.throws(() => createTopologyReader(input)); }
});

test("compact validation covers each grid, mask, field and partial-state boundary", () => {
  const cases = [
    [x => { x.timestamps = []; }, /Unsupported compact/],
    [x => { x.timestamps = null; }, /Unsupported compact/],
    [x => { x.timestamps[1] = 0; }, /Invalid topology grid/],
    [x => { x.timestamps[1] = -1; }, /Invalid topology grid/],
    [x => { x.timestamps[0] = 0.5; }, /Invalid topology grid/],
    [x => { x.timestamps[128] = Number.MAX_SAFE_INTEGER + 1; }, /Invalid topology grid/],
    [x => { x.nodes[0].mask = []; }, /Invalid time mask/],
    [x => { x.nodes[0].mask = null; }, /Invalid time mask/],
    [x => { x.nodes[0].mask = ["0000000000000001", "0000000000000000", "0000000000000000", "0000000000000000"]; }, /Invalid time mask/],
    [x => { x.nodes[0].mask = [1]; }, /Invalid mask word/],
    [x => { x.nodes[0].mask = ["FFFFFFFFFFFFFFFF"]; }, /Invalid mask word/],
    [x => { x.nodes[0].mask = ["fffffffffffffffff"]; }, /Invalid mask word/],
    [x => { x.nodes[0].mask = ["0000000000000001", "0000000000000000", "0000000000000002"]; }, /Mask exceeds grid/],
    [x => { x.nodes[0].mask = ["0000000000000000"]; x.edges = []; }, /Empty version mask/],
    [x => { x.partial[0].index = -1; }, /Invalid partial state/],
    [x => { x.partial[0].index = 0.5; }, /Invalid partial state/],
    [x => { x.partial[0].index = x.timestamps.length; }, /Invalid partial state/],
    [x => { x.partial.push({ ...x.partial[0] }); }, /Invalid partial state/],
    [x => { x.partial[0].reason = 42; }, /Invalid partial state/],
  ];
  for (const [change, message] of cases) {
    const input = fixture(); change(input);
    assert.throws(() => createTopologyReader(input), message);
  }
  for (const field of ["id", "source", "target"]) {
    for (const value of [42, "-1", "01", "18446744073709551616"]) {
      const input = fixture();
      (field === "id" ? input.nodes[0] : input.edges[0])[field] = value;
      assert.throws(() => createTopologyReader(input), /Invalid uint64/);
    }
  }
  for (const field of ["resource_type", "relation_type", "metric_name", "category", "direction"]) {
    for (const value of [undefined, null, 42]) {
      const input = fixture();
      (field === "resource_type" ? input.nodes[0] : input.edges[0])[field] = value;
      assert.throws(() => createTopologyReader(input), new RegExp(`Invalid ${field}`));
    }
  }
  for (const dimensions of [undefined, [], "invalid", 42, { name: 42 }]) {
    const input = fixture(); input.nodes[0].dimensions = dimensions;
    assert.throws(() => createTopologyReader(input), /Invalid dimensions/);
  }
  for (const value of [undefined, null]) assert.throws(() => createTopologyReader(value), /Unsupported compact/);
});

test("compact accepts zero IDs, empty details and partial reasons without sharing frames", () => {
  const input = fixture();
  input.timestamps = input.timestamps.map((value) => value - 1000);
  for (const node of input.nodes) node.id = "0";
  input.nodes[0].dimensions = null;
  input.nodes[1].dimensions = {};
  input.edges[0].source = "0"; input.edges[0].target = "0";
  input.partial[0].reason = "";
  const reader = createTopologyReader(input);
  assert.equal(reader.frame(0).timestamp, -1000);
  assert.equal(reader.frame(0).nodes[0].id, "0");
  assert.equal(reader.frame(0).nodes[0].dimensions, null);
  assert.deepEqual(reader.frame(64).nodes[0].dimensions, {});
  assert.equal(reader.frame(64).partial, true);
  assert.equal(Object.hasOwn(reader.frame(64), "partial_reason"), false);
  for (const index of [-1, 0.5, reader.pointCount]) assert.throws(() => reader.frame(index), RangeError);
  input.nodes = []; input.edges = []; input.partial = [];
  const empty = createTopologyReader(input).frame(0);
  assert.deepEqual(empty, { timestamp: -1000, nodes: [], edges: [], partial: false });
});

test("Go shared-graph differential fixtures", {skip: !process.env.TG_COMPACT_FIXTURE}, () => {
  const cases = JSON.parse(readFileSync(process.env.TG_COMPACT_FIXTURE, "utf8"));
  for (const {compact, snapshots} of cases) {
    const reader = createTopologyReader(compact);
    snapshots.forEach((expected, index) => assert.deepEqual(reader.frame(index), expected));
  }
});
