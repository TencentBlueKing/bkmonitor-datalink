import type { MetricPanel } from "../../shared/contracts";
import { MetricPanelCard } from "./MetricPanelCard";

export function MetricSection({
  title,
  description,
  ids,
  panels,
}: {
  title: string;
  description: string;
  ids: string[];
  panels: MetricPanel[];
}) {
  return (
    <section className="diagnostic-section" aria-label={title}>
      <header>
        <h2>{title}</h2>
        <p>{description}</p>
      </header>
      <div className="chart-grid">
        {ids
          .map((id) => panels.find((p) => p.id === id))
          .filter((p): p is MetricPanel => Boolean(p))
          .map((panel) => (
            <MetricPanelCard key={panel.id} panel={panel} />
          ))}
      </div>
    </section>
  );
}
