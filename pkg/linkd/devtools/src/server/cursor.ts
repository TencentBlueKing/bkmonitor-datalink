import { createHash } from "node:crypto";

import { z } from "zod";

const cursorSchema = z.object({
  version: z.literal(1),
  kind: z.enum(["mysql", "elasticsearch"]),
  entity: z.enum(["events", "alerts", "alert-logs"]),
  queryHash: z.string(),
  values: z.array(z.union([z.string(), z.number()])),
  pitId: z.string().optional(),
  from: z.string().datetime().optional(),
  to: z.string().datetime().optional(),
});

export type Cursor = z.infer<typeof cursorSchema>;

export function encodeCursor(cursor: Cursor): string {
  return Buffer.from(JSON.stringify(cursor)).toString("base64url");
}

export function decodeCursor(
  value: string,
  entity: Cursor["entity"],
  query: unknown,
): Cursor {
  const cursor = decodeValue(value);
  if (cursor.entity !== entity || cursor.queryHash !== queryHash(query))
    throw new Error("cursor does not match query");
  return cursor;
}

export function cursorTimeRange(value: string): {
  from?: string;
  to?: string;
} {
  const cursor = decodeValue(value);
  return { from: cursor.from, to: cursor.to };
}

export function queryHash(query: unknown): string {
  return createHash("sha256")
    .update(stableStringify(query))
    .digest("base64url");
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.entries(value as Record<string, unknown>)
      .filter(([, item]) => item !== undefined)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`)
      .join(",")}}`;
  }
  return JSON.stringify(value);
}

function decodeValue(value: string): Cursor {
  try {
    return cursorSchema.parse(
      JSON.parse(Buffer.from(value, "base64url").toString("utf8")) as unknown,
    );
  } catch {
    throw new Error("invalid cursor");
  }
}
