import { expect, it } from "vitest";
import { mergeDecision, mergeRelation } from "./merge-runtime.js";
import { mergeControlPoint } from "./merge-retries.js";
import {
  runtimeDecision,
  runtimeRelation,
} from "../test-fixtures/merge-runtime.js";
import { mergePointFixture } from "../test-fixtures/merge-retries.js";

it.each([5000, 10000])(
  "accepts %i members and progress without a count cap",
  (count) => {
    const ids = Array.from({ length: count }, (_, i) => `member-${i}`);
    const decision = {
      ...runtimeDecision(),
      member_count: count,
      wait_member_count: count,
      capture_offset: count,
      member_offset: count,
    };
    expect(mergeDecision.parse(decision).member_count).toBe(count);
    const relation = {
      ...runtimeRelation(),
      wait_member_ids: ids,
      members: ids.map((alert_id) => ({ alert_id, state: "terminal" })),
      index_offset: count + 1,
      end_offset: count,
    };
    expect(mergeRelation.parse(relation).members).toHaveLength(count);
    const point = {
      ...mergePointFixture("relations", true),
      index_offset: count + 1,
      end_offset: count,
    };
    expect(mergeControlPoint.parse(point).index_offset).toBe(count + 1);
  },
);
