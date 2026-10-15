import { createTopologyReader } from "./topology-compact.mjs";

// Snapshot IDs are JSON uint64 numbers. Recover their original tokens before a
// JavaScript Number can round them; ordinary timestamps and string dimensions stay unchanged.
export function parseTopologyJSON(text) {
  return JSON.parse(text, (key, value, context) => {
    if (["id", "source", "target"].includes(key) && typeof value === "number") {
      const exact = context?.source ?? (Number.isSafeInteger(value) ? String(value) : "");
      if (!/^(0|[1-9][0-9]*)$/.test(exact) || BigInt(exact) > 0xffffffffffffffffn) {
        throw new TypeError("Snapshot uint64 requires a lossless JSON parser (Node.js 22+)");
      }
      return exact;
    }
    return value;
  });
}

export class RelationHTTPError extends Error {
  constructor(status, body) {
    super(`Relation HTTP ${status}: ${JSON.stringify(body)}`);
    this.status = status;
    this.body = body;
  }
}

// The wrapper preserves every query field, including source_expand_info. It
// neither clamps hops/time nor retries a large request automatically.
export async function queryRelations({ baseURL, spaceUID, path, queryList, headers = {}, signal }) {
  if (!spaceUID || !Array.isArray(queryList)) throw new TypeError("spaceUID and queryList are required");
  const allowed = new Set([
    "multi_resource", "multi_resource_range", "path_resources", "path_resources_range",
    "v1beta3/multi_resource", "v1beta3/multi_resource_range", "v1beta3/topology", "v1beta3/topology_range",
  ]);
  if (!allowed.has(path)) throw new TypeError("Unknown relation endpoint");
  const requestHeaders = new Headers(headers);
  requestHeaders.set("Content-Type", "application/json");
  requestHeaders.set("X-Bk-Scope-Space-Uid", spaceUID);
  const response = await fetch(new URL(`/api/v1/relation/${path}`, baseURL), {
    method: "POST", headers: requestHeaders,
    body: JSON.stringify({ query_list: queryList }), signal,
  });
  const body = parseTopologyJSON(await response.text());
  if (!response.ok) throw new RelationHTTPError(response.status, body);
  if (!Array.isArray(body.data) || body.data.length !== queryList.length) {
    throw new TypeError("Relation response does not match the ordered batch");
  }
  return body;
}

export function readTopologyItem(item) {
  if (item.code !== 200) return { ok: false, code: item.code, message: item.message, pointCount: 0 };
  if (item.response_format === "compact-v1") {
    const reader = createTopologyReader(item.compact);
    if (reader.pointCount !== item.point_count) throw new TypeError("Inconsistent compact point_count");
    return { ok: true, ...reader };
  }
  if (item.response_format && item.response_format !== "snapshots") throw new TypeError("Unsupported response format");
  if (!Array.isArray(item.snapshots) || item.snapshots.length !== item.point_count) {
    throw new TypeError("Inconsistent snapshots point_count");
  }
  return {
    ok: true, pointCount: item.snapshots.length,
    frame(index) {
      if (!Number.isInteger(index) || index < 0 || index >= item.snapshots.length) throw new RangeError("Frame outside grid");
      return structuredClone(item.snapshots[index]);
    },
  };
}

// Pass the schema for this space/version, not an assumed global field list.
export function entityKey(node, catalog) {
  const definition = catalog.resources.find((r) => r.name === node.resource_type);
  if (!definition) throw new TypeError(`Unknown resource type: ${node.resource_type}`);
  const keys = definition.fields.filter((f) => f.required).map((f) => f.name).sort();
  if (!keys.length) throw new TypeError("Resource has no primary keys");
  const identity = keys.map((key) => {
    const value = node.dimensions?.[key];
    if (typeof value !== "string" || value === "") throw new TypeError(`Missing primary key: ${key}`);
    return [key, value];
  });
  // Include space/schema scope in the caller's cache key when mixing contexts.
  return JSON.stringify([node.resource_type, identity]);
}

export function alignFrame(frame, catalog) {
  const identities = new Map();
  const nodes = new Map();
  for (const node of frame.nodes) {
    if (typeof node.id !== "string") throw new TypeError("Parse snapshot IDs losslessly before alignment");
    if (identities.has(node.id)) throw new TypeError("Duplicate node ID");
    const key = entityKey(node, catalog);
    if (nodes.has(key)) throw new TypeError("Duplicate full entity identity");
    identities.set(node.id, key);
    nodes.set(key, structuredClone(node.dimensions));
  }
  const edges = new Set();
  for (const edge of frame.edges) {
    for (const key of ["relation_type", "metric_name", "category", "direction"]) {
      if (typeof edge[key] !== "string") throw new TypeError(`Invalid edge ${key}`);
    }
    const source = identities.get(edge.source), target = identities.get(edge.target);
    if (!source || !target) throw new TypeError("Dangling edge");
    edges.add(JSON.stringify([source, target, edge.relation_type, edge.metric_name, edge.category, edge.direction]));
  }
  return { timestamp: frame.timestamp, nodes, edges, partial: frame.partial, partial_reason: frame.partial_reason };
}
