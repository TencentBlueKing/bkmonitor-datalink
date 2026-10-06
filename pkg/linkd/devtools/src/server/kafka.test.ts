import { describe, expect, it } from "vitest";

import type {
  KafkaGroup,
  KafkaPartition,
  KafkaResource,
} from "../shared/contracts.js";
import {
  analyzeKafkaResource,
  kafkaInspectionStatus,
  offsetLag,
} from "./kafka.js";

describe("Kafka infrastructure snapshot", () => {
  it("keeps offsets lossless and treats missing values as unknown", () => {
    expect(offsetLag("900719925474099312345", "900719925474099300000")).toBe(
      "12345",
    );
    expect(offsetLag("42", "-1")).toBeUndefined();
    expect(offsetLag(undefined, "12")).toBeUndefined();
    expect(offsetLag("invalid", "12")).toBeUndefined();
  });

  it("aggregates empty, all-partial and all-unavailable resources correctly", () => {
    expect(kafkaInspectionStatus([])).toBe("unavailable");
    expect(kafkaInspectionStatus([status("partial"), status("partial")])).toBe(
      "partial",
    );
    expect(
      kafkaInspectionStatus([status("unavailable"), status("unavailable")]),
    ).toBe("unavailable");
    expect(
      kafkaInspectionStatus([status("available"), status("partial")]),
    ).toBe("partial");
  });

  it("reports input group and partition issues without applying them to output", () => {
    const input = analyzeKafkaResource("input", group("Stable"), [
      partition({ committedOffset: undefined, members: [] }),
    ]);
    expect(input.status).toBe("partial");
    expect(input.issues.map((issue) => issue.code)).toEqual([
      "owner_missing",
      "committed_missing",
    ]);

    const output = analyzeKafkaResource("output", undefined, [partition()]);
    expect(output.status).toBe("available");
    expect(output.issues).toEqual([]);
    expect(output.partitions[0].members).toBeUndefined();
    expect(output.partitions[0].lag).toBeUndefined();
  });

  it.each([
    ["Empty", "group_empty"],
    ["Dead", "group_dead"],
    ["PreparingRebalance", "group_rebalancing"],
    ["CompletingRebalance", "group_rebalancing"],
  ])("maps %s consumer group state to %s", (state, code) => {
    const result = analyzeKafkaResource("input", group(state), [
      partition({ committedOffset: "8", members: ["member-a"] }),
    ]);
    expect(result.issues).toContainEqual(expect.objectContaining({ code }));
  });

  it("reports missing leaders and incomplete ISR as partition issues", () => {
    const result = analyzeKafkaResource("output", undefined, [
      partition({ leader: null, replicas: [1, 2], isr: [1] }),
    ]);
    expect(result.status).toBe("partial");
    expect(result.partitions[0].issues.map((issue) => issue.code)).toEqual([
      "leader_missing",
      "isr_incomplete",
    ]);
  });
});

function status(value: KafkaResource["status"]): Pick<KafkaResource, "status"> {
  return { status: value };
}

function group(state: string): KafkaGroup {
  return { state, protocol: "range", members: [] };
}

function partition(override: Partial<KafkaPartition> = {}): KafkaPartition {
  return {
    partition: 0,
    leader: 1,
    replicas: [1],
    isr: [1],
    lowOffset: "0",
    highOffset: "10",
    committedOffset: "8",
    lag: "2",
    members: ["member-a"],
    status: "available",
    issues: [],
    ...override,
  };
}
