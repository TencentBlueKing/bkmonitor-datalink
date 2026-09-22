import { expect, it, vi } from "vitest";
import { StrategyAuditJobs, type AuditTarget } from "./strategy-audit.js";
import type {
  StrategyAlertReader,
  StrategyAlertRow,
} from "./strategy-alerts.js";

const target: AuditTarget = {
  target: {
    eventSourceId: "a",
    hookName: "active",
    keyPrefix: "open",
    address: "redis:6379",
    database: 8,
    sources: ["a", "b"],
  },
  redis: { mode: "standalone", address: "redis:6379", database: 8 },
  identity: "identity",
};
const query = { event_source_id: "a", hook_name: "active" };
const row = (strategy: string, fp: string, source = "a"): StrategyAlertRow => ({
  bk_tenant_id: "tenant",
  alert_id: fp,
  event_source_id: source,
  fingerprint: fp,
  status: "active",
  labels: { strategy_id: strategy },
});
const page = async () => ({
  rows: [
    {
      tenantId: "tenant",
      strategyId: "123",
      key: "open:tenant:123",
      members: 2,
      pending: false,
      lastSuccess: null,
      lastAttempt: null,
      error: null,
    },
    {
      tenantId: "tenant",
      strategyId: "orphan",
      key: "open:tenant:orphan",
      members: 1,
      pending: false,
      lastSuccess: null,
      lastAttempt: null,
      error: null,
    },
  ],
  phase: "sets" as const,
  nextCursor: null,
  health: {
    lastSuccess: null,
    lastAttempt: null,
    error: null,
    pendingCount: 0,
    oldestDueAt: null,
  },
  warnings: [],
});
async function finish(jobs: StrategyAuditJobs, id: string) {
  for (let i = 0; i < 100; i++) {
    const job = jobs.get(id);
    if (job.status !== "running") return job;
    await new Promise((r) => setTimeout(r, 1));
  }
  throw new Error("audit did not finish");
}

it("compares the union in both directions, includes entirely missing Redis strategies, and deduplicates shared sources", async () => {
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts() {
      yield [row("123", "both"), row("123", "both", "b")];
      yield [row("123", "missing"), row("db-only", "lost")];
    },
  };
  const members = vi.fn(
    async (_redis: AuditTarget["redis"], _prefix: string, key: string) => ({
      members: new Set(
        key.endsWith(":123")
          ? ["both", "extra"]
          : key.endsWith(":orphan")
            ? ["orphan"]
            : [],
      ),
      complete: true,
    }),
  );
  const jobs = new StrategyAuditJobs(
    async () => target,
    reader,
    1000,
    page,
    members,
  );
  const started = await jobs.start(query),
    result = await finish(jobs, started.id);
  expect(result).toMatchObject({
    status: "completed",
    scannedAlerts: 4,
    checkedStrategies: 3,
    matched: 1,
    missing: 2,
    extra: 2,
    incompleteStrategies: 0,
  });
  expect(result.differences).toContainEqual({
    tenantId: "tenant",
    strategyId: "db-only",
    fingerprint: "lost",
    status: "missing_redis",
  });
  expect(members).toHaveBeenCalledTimes(3);
  await jobs.close();
});

it("never calls a partial database scan consistent or starts Redis absence decisions", async () => {
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts() {
      yield [row("123", "fp")];
      throw new Error("private database detail");
    },
  };
  const redis = vi.fn(page),
    jobs = new StrategyAuditJobs(async () => target, reader, 1000, redis);
  const result = await finish(jobs, (await jobs.start(query)).id);
  expect(result.status).toBe("incomplete");
  expect(redis).not.toHaveBeenCalled();
  expect(JSON.stringify(result)).not.toContain("private database detail");
  await jobs.close();
});

it("isolates a failed strategy and continues other comparisons", async () => {
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts() {
      yield [row("123", "both")];
    },
  };
  const jobs = new StrategyAuditJobs(
    async () => target,
    reader,
    1000,
    page,
    async (_r, _p, key) => {
      if (key.endsWith(":123")) throw new Error("private redis error");
      return { members: new Set(["orphan"]), complete: true };
    },
  );
  const result = await finish(jobs, (await jobs.start(query)).id);
  expect(result).toMatchObject({
    status: "incomplete",
    checkedStrategies: 2,
    incompleteStrategies: 1,
    extra: 1,
    missing: 0,
  });
  expect(result.differences).toContainEqual({
    tenantId: "tenant",
    strategyId: "123",
    fingerprint: null,
    status: "unknown",
  });
  await jobs.close();
});

it("supports cancellation and permits only one running audit", async () => {
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts(_sources, signal) {
      yield [];
      await new Promise<void>((resolve) => {
        signal.addEventListener("abort", () => resolve(), { once: true });
        if (signal.aborted) resolve();
      });
      signal.throwIfAborted();
    },
  };
  const jobs = new StrategyAuditJobs(async () => target, reader, 1000, page);
  const started = await jobs.start(query);
  await expect(jobs.start(query)).rejects.toThrow("running");
  jobs.cancel(started.id);
  expect((await finish(jobs, started.id)).status).toBe("canceled");
  await jobs.close();
});

it("rejects a source-scope change before reporting completed", async () => {
  let calls = 0;
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts() {
      yield [];
    },
  };
  const jobs = new StrategyAuditJobs(
    async () => ({ ...target, identity: ++calls === 1 ? "old" : "new" }),
    reader,
    1000,
    async () => ({ ...(await page()), rows: [] }),
  );
  expect((await finish(jobs, (await jobs.start(query)).id)).status).toBe(
    "incomplete",
  );
  await jobs.close();
});

it("caps difference samples without truncating completed comparison counts", async () => {
  const reader: StrategyAlertReader = {
    readStrategyAlerts: async () => [],
    async *scanActiveStrategyAlerts() {
      yield [];
    },
  };
  const jobs = new StrategyAuditJobs(
    async () => target,
    reader,
    1000,
    page,
    async () => ({
      members: new Set(Array.from({ length: 600 }, (_, i) => `fp-${i}`)),
      complete: true,
    }),
  );
  const result = await finish(jobs, (await jobs.start(query)).id);
  expect(result).toMatchObject({
    status: "completed",
    extra: 1200,
    samplesTruncated: true,
  });
  expect(result.differences).toHaveLength(500);
  await jobs.close();
});
