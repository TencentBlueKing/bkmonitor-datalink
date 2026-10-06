import { useQuery } from "@tanstack/react-query";

import { getKafkaInfrastructure, getRedisInfrastructure } from "../api";
import { JsonViewer } from "../components/JsonViewer";
import { StatusBadge } from "../components/StatusBadge";

export function InfrastructurePage({ kind }: { kind: "kafka" | "redis" }) {
  const result = useQuery({
    queryKey: ["infrastructure", kind],
    queryFn: async (): Promise<Record<string, unknown>> =>
      (kind === "kafka"
        ? await getKafkaInfrastructure()
        : await getRedisInfrastructure()) as unknown as Record<string, unknown>,
    refetchInterval: 30_000,
  });
  return (
    <section>
      <div className="page-heading">
        <div>
          <p className="eyebrow">INFRASTRUCTURE</p>
          <h1>{kind === "kafka" ? "Kafka" : "Redis"}</h1>
          <p>只读展示当前配置对应的实时基础设施状态。</p>
        </div>
        {Boolean(result.data?.status) && (
          <StatusBadge value={String(result.data?.status)} />
        )}
      </div>
      {result.isError ? (
        <div className="error-banner">查询失败：{result.error.message}</div>
      ) : (
        <article className="runtime-config-panel">
          <JsonViewer
            value={result.data ?? {}}
            description="基础设施连接层返回的只读快照；字段保持来源系统语义。"
          />
        </article>
      )}
    </section>
  );
}
