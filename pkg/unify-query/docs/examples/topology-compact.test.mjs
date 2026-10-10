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

test("Go shared-graph differential fixtures", {skip: !process.env.TG_COMPACT_FIXTURE}, () => {
  const cases = JSON.parse(readFileSync(process.env.TG_COMPACT_FIXTURE, "utf8"));
  for (const {compact, snapshots} of cases) {
    const reader = createTopologyReader(compact);
    snapshots.forEach((expected, index) => assert.deepEqual(reader.frame(index), expected));
  }
});
