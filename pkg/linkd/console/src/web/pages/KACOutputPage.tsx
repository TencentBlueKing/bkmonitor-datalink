import { DeliveryMetricsPanel } from "./DeliveryMetricsPanel";
export function KACOutputPage() {
  return (
    <div>
      <h1>KAC 输出</h1>
      <DeliveryMetricsPanel kind="projection" />
      <DeliveryMetricsPanel kind="action" />
    </div>
  );
}
