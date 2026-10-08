import { expect, it } from "vitest";
import { shieldCheck } from "./shield-checks.js";
import { runtimeCheck } from "../test-fixtures/shield-checks.js";
it("accepts existing-binding and candidate-rule steps while rejecting new dependency reservations", () => {
  const c = runtimeCheck();
  c.trigger = "hint";
  const steps = c.report.decision!.steps,
    ref = steps[0].policy;
  steps.push({
    policy: ref,
    outcome: "not_matched",
    reason_code: "timer_does_not_rebind",
  });
  expect(shieldCheck.safeParse(c).success).toBe(true);
  for (let i = 0; i < 255; i++)
    steps.push({
      policy: ref,
      outcome: "skipped",
      reason_code: "release_unavailable",
    });
  expect(shieldCheck.safeParse(c).success).toBe(true);
  steps.push({ policy: ref, outcome: "not_matched" });
  expect(shieldCheck.safeParse(c).success).toBe(false);
  for (const step of [
    { policy: ref, outcome: "released" },
    { policy: ref, outcome: "bound" },
    {
      policy: ref,
      outcome: "bound",
      from_binding: true,
      binding_id: "a".repeat(64),
    },
    { policy: ref, outcome: "main_reserved", main: { alert_id: "x" } },
    { policy: ref, outcome: "skipped", main: { alert_id: "x" } },
  ]) {
    const bad = runtimeCheck();
    bad.report.decision!.steps = [step as (typeof steps)[number]];
    expect(shieldCheck.safeParse(bad).success).toBe(false);
  }
});
