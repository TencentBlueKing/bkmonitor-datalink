import { createHash, randomUUID } from "node:crypto";
import type { ConsoleConfig } from "./config.js";
import type {
  StrategyAudit,
  StrategyAuditRequest,
  StrategyTarget,
} from "../shared/strategy-index.js";
import { strategyLabel, type StrategyAlertReader } from "./strategy-alerts.js";
import { readStrategyPage } from "./strategy-browse.js";
import { withStrategyRedis } from "./strategy-redis.js";

export interface AuditTarget {
  target: StrategyTarget;
  redis: NonNullable<ConsoleConfig["redis"]>;
  identity: string;
}
type Scope = {
  tenantId: string;
  strategyId: string;
  key: string;
  expected: Set<string>;
};
type Job = { view: StrategyAudit; abort: AbortController; done: Promise<void> };
const limits = {
  alerts: 500000,
  strategies: 10000,
  bytes: 128 * 1024 * 1024,
  samples: 500,
  members: 100000,
  milliseconds: 10 * 60 * 1000,
};

// 一次只运行一个只读对账任务，最多保留四份结果。进程重启后结果失效，不持久化业务数据。
export class StrategyAuditJobs {
  private jobs = new Map<string, Job>();
  constructor(
    private readonly resolve: (
      query: StrategyAuditRequest,
      signal?: AbortSignal,
    ) => Promise<AuditTarget>,
    private readonly alerts: StrategyAlertReader | undefined,
    private readonly timeout: number,
    private readonly readPage = readStrategyPage,
    private readonly readMembers = readAuditMembers,
  ) {}

  async start(query: StrategyAuditRequest): Promise<StrategyAudit> {
    if ([...this.jobs.values()].some((j) => j.view.status === "running"))
      throw new Error("an audit is running; must wait or cancel it");
    const scope = await this.resolve(query);
    // 解析配置期间可能有另一个请求创建任务，必须再次检查。
    if ([...this.jobs.values()].some((j) => j.view.status === "running"))
      throw new Error("an audit is running; must wait or cancel it");
    while (this.jobs.size >= 4)
      this.jobs.delete(this.jobs.keys().next().value!);
    const id = randomUUID(),
      abort = new AbortController();
    const view: StrategyAudit = {
      id,
      target: scope.target,
      status: "running",
      phase: "alerts",
      startedAt: new Date().toISOString(),
      finishedAt: null,
      scannedAlerts: 0,
      skippedAlerts: 0,
      invalidAlerts: 0,
      checkedStrategies: 0,
      discoveredStrategies: 0,
      incompleteStrategies: 0,
      redisMembers: 0,
      matched: 0,
      missing: 0,
      extra: 0,
      differences: [],
      samplesTruncated: false,
      warnings: [
        "两侧扫描不是同一时刻的事务快照；并发告警变更和 ES refresh 延迟可能产生暂时差异。",
      ],
    };
    const job: Job = { view, abort, done: Promise.resolve() };
    this.jobs.set(id, job);
    job.done = this.run(job, scope, query);
    return structuredClone(view);
  }
  get(id: string): StrategyAudit {
    const job = this.jobs.get(id);
    if (!job) throw new Error("audit not found; must restart audit");
    return structuredClone(job.view);
  }
  latest(): StrategyAudit | null {
    const job = [...this.jobs.values()].at(-1);
    return job ? structuredClone(job.view) : null;
  }
  cancel(id: string): StrategyAudit {
    const job = this.jobs.get(id);
    if (!job) throw new Error("audit not found; must restart audit");
    job.abort.abort();
    return structuredClone(job.view);
  }
  async close() {
    for (const job of this.jobs.values()) job.abort.abort();
    await Promise.all([...this.jobs.values()].map((j) => j.done));
  }

  private async run(job: Job, scope: AuditTarget, query: StrategyAuditRequest) {
    const v = job.view,
      signal = AbortSignal.any([
        job.abort.signal,
        AbortSignal.timeout(limits.milliseconds),
      ]);
    const scopes = new Map<string, Scope>(),
      checked = new Set<string>();
    let memory = 0;
    const ensure = (tenantId: string, strategyId: string): Scope => {
      if (
        !/^[a-zA-Z0-9_-]{1,64}$/.test(tenantId) ||
        !strategyId ||
        Buffer.byteLength(strategyId) > 1024
      )
        throw new Error("scope limit");
      const key = `${scope.target.keyPrefix}:${tenantId}:${strategyId}`;
      let s = scopes.get(key);
      if (!s) {
        s = { tenantId, strategyId, key, expected: new Set() };
        scopes.set(key, s);
        memory += Buffer.byteLength(key) * 2 + 256;
        v.discoveredStrategies = scopes.size;
      }
      if (scopes.size > limits.strategies || memory > limits.bytes)
        throw new Error("scope limit");
      return s;
    };
    const sample = (
      s: Scope,
      fingerprint: string | null,
      status: StrategyAudit["differences"][number]["status"],
    ) => {
      if (v.differences.length >= limits.samples) {
        v.samplesTruncated = true;
        return;
      }
      v.differences.push({
        tenantId: s.tenantId,
        strategyId: s.strategyId,
        fingerprint,
        status,
      });
    };
    const compare = async (s: Scope) => {
      signal.throwIfAborted();
      if (checked.has(s.key)) return;
      checked.add(s.key);
      try {
        const result = await this.readMembers(
          scope.redis,
          scope.target.keyPrefix,
          s.key,
          signal,
          this.timeout,
        );
        signal.throwIfAborted();
        if (!result.complete) throw new Error("unstable set");
        v.redisMembers += result.members.size;
        for (const fp of result.members) {
          if (s.expected.has(fp)) {
            v.matched++;
          } else {
            v.extra++;
            sample(s, fp, "redis_only");
          }
        }
        for (const fp of s.expected) {
          if (!result.members.has(fp)) {
            v.missing++;
            sample(s, fp, "missing_redis");
          }
        }
      } catch {
        signal.throwIfAborted();
        v.incompleteStrategies++;
        sample(s, null, "unknown");
      } finally {
        v.checkedStrategies++;
        for (const fp of s.expected) memory -= Buffer.byteLength(fp) * 2 + 96;
        s.expected.clear();
      }
    };
    try {
      if (!this.alerts?.scanActiveStrategyAlerts)
        throw new Error("reader unavailable");
      // 必须先完成数据库扫描，才能把 Redis 中额外成员判断为没有对应 active。
      for await (const page of this.alerts.scanActiveStrategyAlerts(
        scope.target.sources,
        signal,
      )) {
        signal.throwIfAborted();
        for (const row of page) {
          if (++v.scannedAlerts > limits.alerts) throw new Error("alert limit");
          if (
            row.status !== "active" ||
            !scope.target.sources.includes(row.event_source_id)
          )
            throw new Error("invalid source scope");
          const raw = row.labels?.strategy_id;
          if (raw === undefined || raw === "") {
            v.skippedAlerts++;
            continue;
          }
          const strategy = strategyLabel(raw);
          if (
            strategy === undefined ||
            typeof row.fingerprint !== "string" ||
            !row.fingerprint ||
            Buffer.byteLength(row.fingerprint) > 128
          ) {
            v.invalidAlerts++;
            continue;
          }
          const s = ensure(row.bk_tenant_id, strategy);
          if (!s.expected.has(row.fingerprint)) {
            s.expected.add(row.fingerprint);
            memory += Buffer.byteLength(row.fingerprint) * 2 + 96;
          }
          if (memory > limits.bytes) throw new Error("memory limit");
        }
      }
      v.phase = "redis";
      let cursor: string | undefined;
      let pages = 0;
      do {
        signal.throwIfAborted();
        if (++pages > 100000) throw new Error("scan limit");
        const page = await this.readPage(
          scope.redis,
          scope.target.keyPrefix,
          scope.identity,
          cursor,
          100,
          signal,
          this.timeout,
          true,
        );
        for (const row of page.rows)
          await compare(ensure(row.tenantId, row.strategyId));
        cursor = page.nextCursor ?? undefined;
      } while (cursor);
      v.phase = "remaining";
      // 数据库有、Redis 没有整个集合时也必须逐个核验，不能只检查 SCAN 返回的 key。
      for (const s of scopes.values()) await compare(s);
      const current = await this.resolve(query, signal);
      if (current.identity !== scope.identity)
        throw new Error("configuration changed");
      v.status =
        v.incompleteStrategies || v.invalidAlerts ? "incomplete" : "completed";
      if (v.invalidAlerts)
        v.warnings.push(
          "部分 Active Alert 的策略标签或身份非法，无法完成全范围判断。",
        );
      if (v.incompleteStrategies)
        v.warnings.push(
          "部分策略读取失败、超限或扫描期间发生变化，详见无法确认的策略。",
        );
    } catch {
      v.status = job.abort.signal.aborted ? "canceled" : "incomplete";
      v.warnings.push(
        job.abort.signal.aborted
          ? "对账已取消，当前数据只是部分结果。"
          : `对账未完整完成（阶段：${v.phase}）；可能是数据源异常、配置变化、十分钟时限或扫描资源上限，不得判定全量一致。`,
      );
    } finally {
      v.phase = "done";
      v.finishedAt = new Date().toISOString();
    }
  }
}

// 整体对账只读取成员，不受交互详情页 5000 条展示上限影响。单策略超过硬上限仍明确拒绝。
export async function readAuditMembers(
  redis: AuditTarget["redis"],
  prefix: string,
  key: string,
  signal: AbortSignal,
  timeout: number,
): Promise<{ members: Set<string>; complete: boolean }> {
  const bounded = AbortSignal.any([signal, AbortSignal.timeout(timeout)]);
  return withStrategyRedis(redis, bounded, timeout, async (command) => {
    const root = `linkd:active-index:${createHash("sha256").update(prefix).digest("hex")}`;
    const statusKey = `${root}:status:${key.slice(prefix.length + 1)}`;
    const before = await command(["HGET", statusKey, "last_success"]),
      cardinality = Number(await command(["SCARD", key]));
    if (
      !Number.isSafeInteger(cardinality) ||
      cardinality < 0 ||
      cardinality > limits.members
    )
      throw new Error("member limit");
    const members = new Set<string>();
    let cursor = "0",
      bytes = 0;
    for (let round = 0; round < 10000; round++) {
      bounded.throwIfAborted();
      const raw = await command(["SSCAN", key, cursor, "COUNT", "500"]);
      if (
        !Array.isArray(raw) ||
        !Array.isArray(raw[1]) ||
        !/^\d+$/.test(String(raw[0]))
      )
        throw new Error("invalid member scan");
      cursor = String(raw[0]);
      for (const value of raw[1]) {
        if (
          typeof value !== "string" ||
          !value ||
          Buffer.byteLength(value) > 128
        )
          throw new Error("invalid member");
        members.add(value);
        bytes += Buffer.byteLength(value);
        if (members.size > limits.members || bytes > 32 * 1024 * 1024)
          throw new Error("member limit");
      }
      if (cursor === "0") {
        const after = await command(["HGET", statusKey, "last_success"]),
          end = Number(await command(["SCARD", key]));
        return {
          members,
          complete:
            before === after && cardinality === end && members.size === end,
        };
      }
    }
    throw new Error("scan limit");
  });
}
