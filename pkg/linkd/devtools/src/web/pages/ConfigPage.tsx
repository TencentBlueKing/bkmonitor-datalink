import { useQuery } from "@tanstack/react-query";

import { getConfigSummary } from "../api";
import { JsonViewer } from "../components/JsonViewer";

export function ConfigPage() {
  const config = useQuery({
    queryKey: ["config-summary"],
    queryFn: getConfigSummary,
  });
  return (
    <section>
      <div className="page-heading">
        <div>
          <p className="eyebrow">EFFECTIVE CONFIG</p>
          <h1>Linkd Configuration</h1>
          <p>直接读取 Linkd YAML，并隐藏全部凭据和私钥。</p>
        </div>
      </div>
      {config.isError ? (
        <div className="error-banner">配置读取失败：{config.error.message}</div>
      ) : (
        <article className="runtime-config-panel">
          <JsonViewer
            value={config.data ?? {}}
            description="Linkd 当前生效的脱敏配置；凭据和私钥不会下发到浏览器。"
          />
        </article>
      )}
    </section>
  );
}
