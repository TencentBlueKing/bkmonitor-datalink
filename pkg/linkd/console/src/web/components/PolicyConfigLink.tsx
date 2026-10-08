import { useQuery } from "@tanstack/react-query";
import { getPolicyLink } from "../api";
import type { PolicyRecord } from "../../shared/policies";

export function PolicyConfigLink({ record }: { record: PolicyRecord }) {
  // 使用当前配置上下文，查看历史 Release 不应让编辑入口暗示可修改历史快照。
  const space = record.spec?.space_code ?? record.pending?.spec.space_code;
  const query = {
    bk_tenant_id: record.bk_tenant_id,
    type: record.type,
    id: record.id,
    ...(typeof space === "string" ? { space_code: space } : {}),
  };
  const link = useQuery({
    queryKey: [
      "policy-config-link",
      record.bk_tenant_id,
      record.type,
      record.id,
      space,
    ],
    queryFn: ({ signal }) => getPolicyLink(query, signal),
    retry: false,
  });
  return (
    <div className="policy-config-link" aria-label="KAC 配置入口">
      {link.isFetching ? (
        <p role="status">正在读取 KAC 配置入口…</p>
      ) : link.isError ? (
        <p className="policy-note">
          KAC 配置入口读取失败。
          <button onClick={() => void link.refetch()}>重试配置入口</button>
        </p>
      ) : link.data?.url ? (
        <>
          <a
            href={link.data.url}
            target="_blank"
            rel="noopener noreferrer"
            referrerPolicy="no-referrer"
          >
            在 KAC 管理此类策略 ↗
          </a>
          <p className="policy-note">
            打开 KAC 当前配置，使用 KAC
            的登录租户与权限。修改后返回刷新策略，核对发布版本。
          </p>
        </>
      ) : (
        <p className="policy-note">
          {link.data?.reason === "missing_context"
            ? "缺少配置入口所需的策略或业务范围信息。"
            : "当前租户尚未配置此类策略的 KAC 入口。"}
        </p>
      )}
    </div>
  );
}
