import type { KafkaResource } from "../../shared/contracts";

export type ResourceStatusFilter = "all" | KafkaResource["status"];

export function filterKafkaResources(
  resources: KafkaResource[],
  filters: {
    keyword: string;
    status: ResourceStatusFilter;
  },
): KafkaResource[] {
  const keyword = filters.keyword.trim().toLowerCase();
  return resources.filter((resource) => {
    if (filters.status !== "all" && resource.status !== filters.status)
      return false;
    if (!keyword) return true;
    return [
      resource.eventSourceId,
      resource.topic,
      resource.clientId,
      resource.consumerGroup,
      resource.kind === "output" ? "lifecycle finalhook output" : "input",
    ].some((value) => value?.toLowerCase().includes(keyword));
  });
}
