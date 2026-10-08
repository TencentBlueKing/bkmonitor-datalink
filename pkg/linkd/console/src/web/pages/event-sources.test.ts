import { describe, expect, it } from "vitest";
import {
  editableSpec,
  formToSpec,
  hasMetadataSuccess,
  parseSourceJSON,
  sourceRecordSchema,
  specToForm,
  validateSource,
  type SourceSpec,
} from "./event-sources";

function kafkaSpec(
  spec: SourceSpec,
): SourceSpec & { storage: Extract<SourceSpec["storage"], { type: "kafka" }> } {
  if (spec.storage.type !== "kafka") throw new Error("Kafka fixture required");
  return { ...spec, storage: spec.storage };
}

function record() {
  const parsed = sourceRecordSchema.parse({
    id: "source-a",
    revision: 3,
    published: 3,
    deleted: false,
    spec: {
      ...editableSpec(),
      event_source_id: "source-a",
      cleaner: { type: "standard", runtime: { concurrency: 10 } },
      fingerprint_mode: "field",
      fingerprint_field: "source_alert_id",
      hooks: [
        {
          name: "output",
          type: "kafka",
          config: { security: { sasl: { password: "******" } } },
        },
      ],
      enrich: {
        processors: [{ type: "fields", config: { rules: [] } }],
      },
      storage: {
        type: "kafka",
        kafka: {
          brokers: ["kafka:9092"],
          topic: "events",
          consumer_group: "cleaner",
          fetch_max_wait_milliseconds: 500,
          security: {
            protocol: "sasl_plaintext",
            sasl: { password: "******" },
          },
        },
      },
    },
  });
  return { ...parsed, spec: kafkaSpec(parsed.spec) };
}

describe("EventSource editing contract", () => {
  it("preserves advanced fields and secret retention semantics through form and JSON", () => {
    const original = record();
    const editable = kafkaSpec(editableSpec(original));
    const form = specToForm(editable);
    expect(formToSpec(form, editable)).toEqual(editable);
    form.cleaner.replicas = "0";
    const updated = kafkaSpec(
      parseSourceJSON(JSON.stringify(formToSpec(form, editable))),
    );
    validateSource(updated, original);
    expect(updated.scheduling.cleaner.replicas).toBe(0);
    expect(updated.storage.kafka.security).toBeUndefined();
    expect(updated.storage.kafka.fetch_max_wait_milliseconds).toBe(500);
    expect(updated.cleaner).toEqual(original.spec.cleaner);
    expect(updated.enrich).toEqual(original.spec.enrich);
    expect(updated.hooks).toEqual(original.spec.hooks);
    expect(original.spec.storage.kafka.security).toBeDefined();
  });
  it.each(["-1", "1.5", "10001", "", "two", "1e2"])(
    "rejects invalid form replicas %s",
    (value) => {
      const spec = kafkaSpec(editableSpec(record()));
      const form = specToForm(spec);
      form.cleaner.replicas = value;
      expect(() => formToSpec(form, spec)).toThrow(/副本数/);
    },
  );
  it.each(["all", "0", "10000"])("accepts replicas %s", (value) => {
    const original = record(),
      spec = kafkaSpec(editableSpec(original)),
      form = specToForm(spec);
    form.cleaner.replicas = value;
    expect(() =>
      validateSource(formToSpec(form, spec), original),
    ).not.toThrow();
  });
  it("rejects duplicate selectors and byte/count limits without silently overwriting labels", () => {
    const spec = kafkaSpec(editableSpec(record())),
      form = specToForm(spec);
    form.cleaner.selector = [
      { key: "pool", value: "a" },
      { key: "pool", value: "b" },
    ];
    expect(() => formToSpec(form, spec)).toThrow("标签键不能重复");
    for (const selector of [
      { "": "x" },
      { ["中".repeat(43)]: "x" },
      { key: "中".repeat(86) },
      Object.fromEntries(
        Array.from({ length: 33 }, (_, i) => [String(i), "v"]),
      ),
    ]) {
      spec.scheduling.cleaner.selector = selector;
      expect(() => validateSource(spec)).toThrow(/最多 32 个标签/);
    }
    spec.scheduling.cleaner.selector = {
      pool: "",
      ["中".repeat(42)]: "中".repeat(85),
    };
    expect(() => validateSource(spec)).not.toThrow();
  });
  it.each([
    (s: ReturnType<typeof kafkaSpec>) => {
      s.event_source_id = "other";
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.related_tenant_id = "other";
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.storage.kafka.brokers = ["other:9092"];
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.storage.kafka.topic = "other";
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.storage.kafka.consumer_group = "other";
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.cleaner = { type: "other" };
    },
    (s: ReturnType<typeof kafkaSpec>) => {
      s.fingerprint_field = "subject_id";
    },
  ])("rejects immutable identity changes", (change) => {
    const original = record(),
      spec = kafkaSpec(editableSpec(original));
    change(spec);
    expect(() => validateSource(spec, original)).toThrow(/不可修改/);
  });
  it("checks source ID, tenant, Kafka and numeric JSON boundaries", () => {
    const valid = kafkaSpec(editableSpec(record()));
    for (const spec of [
      { ...valid, event_source_id: "bad/id" },
      { ...valid, related_tenant_id: "x".repeat(65) },
      {
        ...valid,
        storage: {
          ...valid.storage,
          kafka: { ...valid.storage.kafka, brokers: [] },
        },
      },
      {
        ...valid,
        storage: {
          ...valid.storage,
          kafka: { ...valid.storage.kafka, topic: ".." },
        },
      },
      {
        ...valid,
        storage: {
          ...valid.storage,
          kafka: { ...valid.storage.kafka, consumer_group: "group\n" },
        },
      },
      {
        ...valid,
        scheduling: {
          ...valid.scheduling,
          cleaner: { replicas: -1, selector: {} },
        },
      },
    ])
      expect(() => validateSource(spec)).toThrow();
  });
  it("reports JSON location without reflecting sensitive input", () => {
    expect(() =>
      parseSourceJSON('{\n  "password": "private-value"\n broken }'),
    ).toThrow(/第 \d+ 行，第 \d+ 列/);
    try {
      parseSourceJSON('{"password": "private-value" broken}');
    } catch (error) {
      expect(String(error)).not.toContain("private-value");
    }
    expect(() => parseSourceJSON("{}")).toThrow(/event_source_id/);
  });
  it("validates broker ports and canonical duplicates while preserving valid IPv6", () => {
    const spec = kafkaSpec(editableSpec(record()));
    for (const brokers of [
      ["kafka"],
      ["kafka:0"],
      ["kafka:65536"],
      ["kafka:9092", "KAFKA:09092"],
      ["[::1]:9092", "[0:0:0:0:0:0:0:1]:9092"],
    ]) {
      spec.storage.kafka.brokers = brokers;
      expect(() => validateSource(spec)).toThrow(/Broker/);
    }
    spec.storage.kafka.brokers = ["[::1]:9092", "kafka:65535"];
    expect(() => validateSource(spec)).not.toThrow();
  });
  it("does not treat a zero timestamp as successful Kafka metadata", () => {
    expect(hasMetadataSuccess("0001-01-01T00:00:00Z")).toBe(false);
    expect(hasMetadataSuccess("not a date")).toBe(false);
    expect(hasMetadataSuccess("2026-09-22T00:00:00Z")).toBe(true);
  });
});

it("edits internal merge source without inventing Kafka or Cleaner input", () => {
  const spec = parseSourceJSON(
    JSON.stringify({
      event_source_id: "builtin_alarm_merge",
      enabled: true,
      scheduling: {
        cleaner: { replicas: 0, selector: {} },
        lifecycle: { replicas: "all", selector: {} },
      },
      storage: { type: "internal_merge" },
      enrich: { processors: [] },
      hooks: [],
    }),
  );
  expect(() => validateSource(spec)).not.toThrow();
  const form = specToForm(spec);
  form.lifecycle.replicas = "1";
  const updated = formToSpec(form, spec);
  expect(updated.storage).toEqual({ type: "internal_merge" });
  expect(updated.cleaner).toBeUndefined();
  expect(updated.enrich).toEqual(spec.enrich);
  expect(() =>
    validateSource({ ...updated, event_source_id: "external" }),
  ).toThrow(/保留 ID/);
  expect(() =>
    validateSource({ ...updated, cleaner: { type: "standard" } }),
  ).toThrow(/不配置 Cleaner/);
});
