import type { ConsoleConfig, EventSourceConfig } from "./config.js";
import type {
  StrategyQuery,
  StrategyResult,
  StrategyTarget,
} from "../shared/strategy-index.js";
import { loadRuntimeSources } from "./source-runtime.js";
import { strategyLabel, type StrategyAlertReader } from "./strategy-alerts.js";
import { readStrategyMembers } from "./strategy-redis.js";

type Binding = NonNullable<EventSourceConfig["strategyHooks"]>[number] & {
  eventSourceId: string;
};

// 配置身份不包含密码，轮换凭据不会拆分同一个共享索引。
function destination(b: Binding): string {
  return JSON.stringify([
    b.redis.mode,
    b.redis.address?.toLowerCase(),
    b.redis.sentinel?.masterName,
    b.redis.sentinel?.addresses.map((a) => a.toLowerCase()).sort(),
    b.redis.database,
    b.keyPrefix,
  ]);
}
function bindings(sources: EventSourceConfig[]): Binding[] {
  return sources.flatMap((s) =>
    (s.strategyHooks ?? []).map((h) => ({
      ...h,
      eventSourceId: s.eventSourceId,
    })),
  );
}
function target(selected: Binding, all: Binding[]): StrategyTarget {
  return {
    eventSourceId: selected.eventSourceId,
    hookName: selected.name,
    notifyChannel: selected.notifyChannel,
    keyPrefix: selected.keyPrefix,
    address:
      selected.redis.address ??
      `sentinel:${selected.redis.sentinel!.masterName} (${selected.redis.sentinel!.addresses.join(", ")})`,
    database: selected.redis.database,
    sources: [
      ...new Set(
        all
          .filter((b) => destination(b) === destination(selected))
          .map((b) => b.eventSourceId),
      ),
    ].sort(),
  };
}

// 仅诊断，不修复 Redis；同时最多四次读取，来源与两侧存储故障不会被解释为“空”。
export class StrategyIndexConnector {
  private active = 0;
  constructor(
    private readonly config: ConsoleConfig,
    private readonly alerts: StrategyAlertReader | undefined,
    private readonly readRedis = readStrategyMembers,
    private readonly loadSources = loadRuntimeSources,
  ) {}

  private async sources(signal: AbortSignal) {
    const result = bindings(
      this.config.dispatch?.apiToken
        ? await this.loadSources(this.config, signal)
        : (this.config.eventSources ?? []),
    );
    if (result.length > 512)
      throw new Error("strategy hooks must not exceed 512");
    return result;
  }
  private async limited<T>(
    run: (signal: AbortSignal) => Promise<T>,
    caller?: AbortSignal,
  ): Promise<T> {
    if (this.active >= 4) throw new Error("对账查询繁忙，请稍后重试");
    this.active++;
    const timer = AbortSignal.timeout(
      Math.min(15000, this.config.query.timeoutMilliseconds),
    );
    try {
      return await run(caller ? AbortSignal.any([timer, caller]) : timer);
    } finally {
      this.active--;
    }
  }
  targets(signal?: AbortSignal): Promise<StrategyTarget[]> {
    return this.limited(async (signal) => {
      const all = await this.sources(signal);
      return all.map((b) => target(b, all));
    }, signal);
  }
  inspect(query: StrategyQuery, signal?: AbortSignal): Promise<StrategyResult> {
    return this.limited(async (signal) => {
      const startedAt = new Date().toISOString();
      const all = await this.sources(signal);
      const selected = all.find(
        (b) =>
          b.eventSourceId === query.event_source_id &&
          b.name === query.hook_name,
      );
      if (!selected)
        throw new Error("Hook must exist in current source configuration");
      const scope = target(selected, all);
      if (scope.sources.length > 64)
        throw new Error("shared index must not exceed 64 sources");
      signal.throwIfAborted();
      const key = `${scope.keyPrefix}:${query.bk_tenant_id}:${query.strategy_id}`;
      const [redis, alerts] = await Promise.allSettled([
        this.readRedis(
          selected.redis,
          key,
          signal,
          Math.min(15000, this.config.query.timeoutMilliseconds),
        ),
        this.alerts
          ? this.alerts.readStrategyAlerts(
              query.bk_tenant_id,
              scope.sources,
              query.strategy_id,
              5001,
            )
          : Promise.reject(new Error("alert store unavailable")),
      ]);
      const warnings = [
        "两侧读取不是原子快照；告警并发变更、ES 刷新延迟和来源发布切换可能造成短暂差异，请复查。",
        "仅对账当前配置中共享目标的来源；已删除来源、旧前缀或外部写入的成员需要另行确认。",
      ];
      const rows = new Map<string, StrategyResult["rows"][number]>();
      const redisOK = redis.status === "fulfilled" && redis.value.complete;
      const alertOK =
        alerts.status === "fulfilled" && alerts.value.length <= 5000;
      if (redis.status === "rejected")
        warnings.push(
          "Redis 读取失败（连接、认证、类型或超时），不能判断成员缺失。",
        );
      else if (!redisOK)
        warnings.push(
          "Redis 扫描达到成员/字节/轮数上限，或读取期间集合数量发生变化，结果不完整。",
        );
      if (alerts.status === "rejected")
        warnings.push("Active Alert 读取失败，不能判断 Redis 独有成员。");
      else if (!alertOK)
        warnings.push(
          "该租户、策略及共享来源的 Active Alert 候选超过 5000 条，扫描不完整；未按时间窗口排除旧告警。",
        );
      if (scope.sources.length > 1)
        warnings.push(
          "这些来源共用 Redis 成员且没有引用计数；某个来源关闭告警可能删除其他来源仍活跃的 fingerprint。",
        );
      if (redis.status === "fulfilled")
        for (const fingerprint of redis.value.members)
          rows.set(fingerprint, {
            fingerprint,
            alerts: [],
            status: alertOK ? "redis_only" : "unknown",
          });
      let matched = 0;
      if (alerts.status === "fulfilled")
        for (const alert of alerts.value.slice(0, 5000)) {
          if (
            alert.bk_tenant_id !== query.bk_tenant_id ||
            !scope.sources.includes(alert.event_source_id) ||
            alert.status !== "active"
          )
            throw new Error("invalid alert scope response");
          if (strategyLabel(alert.labels?.strategy_id) !== query.strategy_id)
            continue;
          matched++;
          const row = rows.get(alert.fingerprint) ?? {
            fingerprint: alert.fingerprint,
            alerts: [],
            status: redisOK ? "missing_redis" : "unknown",
          };
          if (rows.has(alert.fingerprint) && row.alerts.length === 0)
            row.status = "matched";
          row.alerts.push({
            alertId: alert.alert_id,
            eventSourceId: alert.event_source_id,
            fingerprint: alert.fingerprint,
          });
          rows.set(alert.fingerprint, row);
        }
      return {
        target: scope,
        tenantId: query.bk_tenant_id,
        strategyId: query.strategy_id,
        key,
        startedAt,
        finishedAt: new Date().toISOString(),
        complete: redisOK && alertOK,
        warnings,
        redis: {
          complete: redisOK,
          total: redis.status === "fulfilled" ? redis.value.total : null,
          scanned:
            redis.status === "fulfilled" ? redis.value.members.length : 0,
        },
        alerts: {
          complete: alertOK,
          scanned:
            alerts.status === "fulfilled"
              ? Math.min(5000, alerts.value.length)
              : 0,
          matched,
        },
        rows: [...rows.values()].sort((a, b) =>
          a.fingerprint.localeCompare(b.fingerprint),
        ),
      };
    }, signal);
  }
}
