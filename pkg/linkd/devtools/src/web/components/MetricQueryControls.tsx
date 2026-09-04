import { metricCalculationWindows, metricRanges } from "../metricRange";

export function MetricQueryControls({
  rangeSeconds,
  calculationWindowSeconds,
  onRangeChange,
  onCalculationWindowChange,
}: {
  rangeSeconds: number;
  calculationWindowSeconds: number;
  onRangeChange: (seconds: number) => void;
  onCalculationWindowChange: (seconds: number) => void;
}) {
  return (
    <div className="metric-query-controls">
      <label className="metric-calculation-window-control">
        <span>计算窗口</span>
        <select
          aria-label="指标计算窗口"
          value={calculationWindowSeconds}
          onChange={(event) =>
            onCalculationWindowChange(Number(event.target.value))
          }
        >
          {metricCalculationWindows.map((window) => (
            <option key={window.seconds} value={window.seconds}>
              {window.label}
            </option>
          ))}
        </select>
      </label>
      <div className="range-controls" role="group" aria-label="图表时间范围">
        {metricRanges.map((range) => (
          <button
            key={range.seconds}
            className={range.seconds === rangeSeconds ? "selected" : ""}
            type="button"
            aria-pressed={range.seconds === rangeSeconds}
            onClick={() => onRangeChange(range.seconds)}
          >
            {range.label}
          </button>
        ))}
      </div>
    </div>
  );
}
