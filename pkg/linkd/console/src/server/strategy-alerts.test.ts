import { expect, it } from "vitest";
import {
  effectiveStrategyRow,
  readEffectiveStrategyAlerts,
  type StrategyAlertRow,
} from "./strategy-alerts.js";
it("replays processor order and finds enriched strategies outside the raw filter", async () => {
  const original: StrategyAlertRow = {
    bk_tenant_id: "t",
    alert_id: "a",
    event_source_id: "host",
    fingerprint: "fp",
    status: "active",
    labels: { strategy_id: "old" },
    enrich: {
      processors: [
        {
          fields: {
            status: "succeeded",
            patches: [
              { op: "set", path: '$["labels"]["strategy_id"]', value: 9001 },
            ],
          },
        },
      ],
    },
  };
  expect(effectiveStrategyRow(original).labels?.strategy_id).toBe(9001);
  expect(original.labels?.strategy_id).toBe("old");
  async function* pages() {
    yield [original];
  }
  expect(
    await readEffectiveStrategyAlerts(pages(), "t", "9001", 10),
  ).toHaveLength(1);
  expect(
    await readEffectiveStrategyAlerts(pages(), "other", "9001", 10),
  ).toHaveLength(0);
});
