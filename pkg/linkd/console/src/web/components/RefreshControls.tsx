import type { ReactNode } from "react";

import { formatTime, useTimeMode } from "../time";
import { StatusBadge } from "./StatusBadge";

interface RefreshControlsProps {
  status?: unknown;
  lastSuccessfulAt?: number;
  isFetching: boolean;
  autoRefresh: boolean;
  intervalSeconds: number;
  onRefresh: () => void;
  onToggleAutoRefresh: () => void;
  children?: ReactNode;
}

export function RefreshControls({
  status,
  lastSuccessfulAt,
  isFetching,
  autoRefresh,
  intervalSeconds,
  onRefresh,
  onToggleAutoRefresh,
  children,
}: RefreshControlsProps) {
  const timeMode = useTimeMode();
  const lastSuccess = lastSuccessfulAt
    ? formatTime(new Date(lastSuccessfulAt).toISOString(), timeMode)
    : "尚未成功";

  return (
    <div className="refresh-controls">
      <div className="refresh-controls-summary">
        {status !== undefined && status !== null && (
          <StatusBadge value={status} />
        )}
        <span>最后成功：{lastSuccess}</span>
      </div>
      <div className="refresh-controls-actions">
        <button type="button" disabled={isFetching} onClick={onRefresh}>
          {isFetching ? "刷新中…" : "立即刷新"}
        </button>
        <button
          className={autoRefresh ? "selected" : ""}
          type="button"
          aria-pressed={autoRefresh}
          onClick={onToggleAutoRefresh}
        >
          {autoRefresh ? `自动 ${intervalSeconds}s` : "已暂停"}
        </button>
        {children}
      </div>
    </div>
  );
}
