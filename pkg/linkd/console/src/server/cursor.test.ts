import { describe, expect, it } from "vitest";

import { decodeCursor, encodeCursor, queryHash } from "./cursor.js";

describe("cursor", () => {
  it("binds an opaque cursor to the complete query", () => {
    const query = { tenantId: "tenant-a", limit: 50 };
    const encoded = encodeCursor({
      version: 1,
      kind: "mysql",
      entity: "events",
      queryHash: queryHash(query),
      values: ["123", "tenant-a", "event-a"],
    });
    expect(decodeCursor(encoded, "events", query).values).toEqual([
      "123",
      "tenant-a",
      "event-a",
    ]);
    expect(() =>
      decodeCursor(encoded, "events", { ...query, tenantId: "tenant-b" }),
    ).toThrow("cursor does not match query");
  });

  it("rejects malformed cursors", () => {
    expect(() => decodeCursor("not-base64-json", "alerts", {})).toThrow(
      "invalid cursor",
    );
  });
});
