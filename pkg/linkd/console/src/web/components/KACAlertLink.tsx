import { useQuery } from "@tanstack/react-query";
import { getKACAlertLink } from "../api";

export function KACAlertLink({
  tenant,
  alarmID,
}: {
  tenant: string;
  alarmID: string;
}) {
  const link = useQuery({
    queryKey: ["kac-alert-link", tenant, alarmID],
    queryFn: ({ signal }) =>
      getKACAlertLink({ bk_tenant_id: tenant, alarm_id: alarmID }, signal),
    retry: false,
  });
  if (!link.data?.url || link.isError) return null;
  return (
    <a
      href={link.data.url}
      target="_blank"
      rel="noopener noreferrer"
      referrerPolicy="no-referrer"
      title="使用 KAC 当前登录租户及权限"
    >
      查看 KAC 告警 ↗
    </a>
  );
}
