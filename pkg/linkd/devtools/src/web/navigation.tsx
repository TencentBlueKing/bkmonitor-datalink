import { createContext, useContext, useEffect } from "react";

export const PageQueryFailureContext = createContext<(failed: boolean) => void>(
  () => undefined,
);

// useReportPageQueryFailure 让固定侧边栏只在当前查询页失败时退回原生导航。
// 原生导航会重新建立整棵 React 树，避免失败请求留下的并发过渡保留旧页面。
export function useReportPageQueryFailure(failed: boolean): void {
  const report = useContext(PageQueryFailureContext);
  useEffect(() => {
    report(failed);
    return () => report(false);
  }, [failed, report]);
}
