import { describe, expect, it } from "vitest";

import { formatMetricNumber, formatMetricValue } from "./metricFormat";

describe("metric value formatting", () => {
  it("rounds chart values to at most four decimal places", () => {
    expect(formatMetricNumber(1.328818063790047)).toBe("1.3288");
    expect(formatMetricNumber(12_345.67894)).toBe("12,345.6789");
    expect(formatMetricNumber(2)).toBe("2");
  });

  it("adds the panel unit to scalar and ECharts tuple values", () => {
    expect(formatMetricValue(1.328818063790047, "operation/s")).toBe(
      "1.3288 operation/s",
    );
    expect(formatMetricValue([1_788_000_000_000, 0.123456], "s")).toBe(
      "0.1235 s",
    );
    expect(formatMetricValue(null, "message")).toBe("—");
  });
});
