import { readFileSync } from "node:fs";
import { alignFrame, parseTopologyJSON, queryRelations, readTopologyItem } from "./topology-client.mjs";
const catalog = JSON.parse(readFileSync(new URL("../api/timegraph-schema.json", import.meta.url)));
let response;
if (process.argv[2] === "--live") {
  if (!process.env.UQ_URL || !process.env.UQ_SPACE_UID || !process.argv[3] || !process.argv[4]) throw new Error("Set UQ_URL/UQ_SPACE_UID; use --live request.json endpoint");
  const request = JSON.parse(readFileSync(process.argv[3], "utf8"));
  response = await queryRelations({ baseURL: process.env.UQ_URL, spaceUID: process.env.UQ_SPACE_UID,
    path: process.argv[4], queryList: request.query_list,
    headers: process.env.UQ_JWT ? { "X-Bkapi-Jwt": process.env.UQ_JWT } : {},
  });
} else {
  const cases = JSON.parse(readFileSync(new URL("topology-scenarios.json", import.meta.url))).cases;
  const scenario = cases.find((c) => c.name === (process.argv[2] ?? "known-node"));
  if (!scenario) throw new Error(`Choose a scenario: ${cases.map((c) => c.name).join(", ")}`);
  const records = parseTopologyJSON(readFileSync(new URL("topology-responses.json", import.meta.url), "utf8"));
  const record = records.find((r) => r.space === "example-space" && r.path.endsWith(`/${scenario.path}`) &&
    Object.entries(scenario.query).every(([key, value]) => JSON.stringify(r.request.query_list[0]?.[key]) === JSON.stringify(value)));
  if (!record) throw new Error("Scenario response fixture is missing");
  response = record.response;
}
for (const item of response.data) {
  if (item.code !== 200) { console.error(JSON.stringify(item)); process.exitCode = 1; continue; }
  if (!Object.hasOwn(item,"snapshots") && item.response_format !== "compact-v1") { console.log(JSON.stringify(item)); continue; }
  const reader = readTopologyItem(item);
  for (let i = 0; i < reader.pointCount; i++) {
    const aligned = alignFrame(reader.frame(i), catalog);
    console.log(JSON.stringify({ ...aligned, nodes: [...aligned.nodes], edges: [...aligned.edges] }));
  }
}
