import { expect, it } from "vitest";
import {
  actionDelivery,
  actionOrder,
  actionQuery,
  actionSnapshot,
} from "./action-deliveries.js";
import {
  actionFixture,
  actionOrderFixture,
  actionSnapshotFixture,
  recoveredAction,
} from "../test-fixtures/action-deliveries.js";
it("accepts every actual progress state and distinguishes local skip from receiver receipt", () => {
  for (const state of [
    "pending",
    "waiting_projection",
    "sending",
    "retry",
    "failed",
    "succeeded",
    "skipped",
  ] as const)
    expect(actionDelivery.safeParse(actionFixture(state)).success, state).toBe(
      true,
    );
  expect(actionDelivery.safeParse(recoveredAction()).success).toBe(true);
  const skip = actionFixture("skipped");
  skip.progress.previous_unconfirmed = true;
  expect(actionDelivery.safeParse(skip).success).toBe(true);
  const remote = actionFixture("succeeded");
  remote.progress.state = "skipped";
  remote.progress.error_code = "superseded_by_terminal";
  remote.progress.receipt = {
    ...remote.progress.receipt!,
    outcome: "skipped",
    reason: "superseded_by_terminal",
    applied_revision: 3,
    applied_status: "closed",
  };
  expect(actionDelivery.safeParse(remote).success).toBe(true);
});
it("rejects false success, wrong proof/action identity and illegal ordering state", () => {
  const good = actionFixture("succeeded");
  for (const change of [
    (v: typeof good) => {
      delete v.progress.receipt;
    },
    (v: typeof good) => {
      delete v.progress.projection;
    },
    (v: typeof good) => {
      v.progress.receipt!.request_hash = "f".repeat(64);
    },
    (v: typeof good) => {
      v.progress.receipt!.action_id = "f".repeat(64);
    },
    (v: typeof good) => {
      v.progress.receipt!.bk_tenant_id = "other";
    },
    (v: typeof good) => {
      v.progress.projection!.applied_revision = 1;
    },
    (v: typeof good) => {
      v.progress.projection!.content_hash = "f".repeat(64);
    },
    (v: typeof good) => {
      v.action = "close";
    },
    (v: typeof good) => {
      v.progress.total_attempts = 0;
    },
  ]) {
    const v = structuredClone(good);
    change(v);
    expect(actionDelivery.safeParse(v).success).toBe(false);
  }
  const skip = actionFixture("skipped");
  skip.progress.projection!.applied_status = "active";
  expect(actionDelivery.safeParse(skip).success).toBe(false);
  const sending = actionFixture("sending");
  sending.progress.lease_until = sending.progress.updated_at;
  expect(actionDelivery.safeParse(sending).success).toBe(false);
  const order = actionOrderFixture(good, good);
  expect(actionOrder.safeParse(order).success).toBe(false);
});
it("binds frozen requests and filters to explicit scope", () => {
  const s = actionSnapshotFixture();
  expect(actionSnapshot.safeParse(s).success).toBe(true);
  s.request.alert.bk_tenant_id = "other";
  expect(actionSnapshot.safeParse(s).success).toBe(false);
  expect(
    actionQuery.safeParse({
      bk_tenant_id: "tenant-a",
      action: "resolved",
      state: "waiting_projection",
    }).success,
  ).toBe(true);
  for (const input of [
    { bk_tenant_id: "tenant-a", action: "triggered" },
    { bk_tenant_id: "tenant-a", state: "delivered" },
    { bk_tenant_id: "tenant-a", limit: 5 },
    { bk_tenant_id: "tenant-a", force: true },
  ])
    expect(actionQuery.safeParse(input).success).toBe(false);
});
