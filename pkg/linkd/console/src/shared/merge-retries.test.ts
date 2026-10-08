import { expect, it } from "vitest";
import { mergeControlPoint, mergeRetryRequest } from "./merge-retries.js";
import {
  mergePointFixture,
  mergeRetryFixture,
} from "../test-fixtures/merge-retries.js";
it("keeps completed attempt separate from still pending business progress and rejects inconsistent scopes", () => {
  const r = mergeRetryFixture(true);
  expect(mergeRetryRequest.safeParse(r).success).toBe(true);
  expect(r.result!.after!.complete).toBe(false);
  for (const bad of [
    { ...r, state: "pending" },
    { ...r, command: { ...r.command, expected_token: "b".repeat(64) } },
    {
      ...r,
      result: {
        ...r.result,
        after: { ...r.result!.after, bk_tenant_id: "other" },
      },
    },
    { ...r, result: { ...r.result, step_attempted: false } },
    { ...r, result: { ...r.result, reason: "target_changed" } },
    { ...r, started_at: undefined },
  ])
    expect(mergeRetryRequest.safeParse(bad).success).toBe(false);
  const p = mergePointFixture();
  expect(mergeControlPoint.safeParse({ ...p, complete: true }).success).toBe(
    false,
  );
  expect(mergeControlPoint.safeParse({ ...p, index_offset: 1 }).success).toBe(
    false,
  );
  expect(
    mergeControlPoint.safeParse(mergePointFixture("relations", true)).success,
  ).toBe(true);
});
