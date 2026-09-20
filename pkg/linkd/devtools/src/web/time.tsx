import { createContext, useContext } from "react";

export type TimeMode = "local" | "utc";

export const TimeContext = createContext<TimeMode>("local");

export function useTimeMode(): TimeMode {
  return useContext(TimeContext);
}

export function formatTime(value: string, mode: TimeMode): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  if (mode === "utc")
    return date.toISOString().replace("T", " ").replace("Z", " UTC");
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date);
}
