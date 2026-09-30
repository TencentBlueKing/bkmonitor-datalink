// IDs remain decimal strings. Convert to BigInt, never Number, when needed.
export function createTopologyReader(topology) {
  if (topology?.version !== 1 || !Array.isArray(topology.timestamps) || !topology.timestamps.length) {
    throw new TypeError("Unsupported compact topology");
  }
  const times = [...topology.timestamps];
  if (times.some((t, i) => !Number.isSafeInteger(t) || (i && t <= times[i - 1]))) {
    throw new TypeError("Invalid topology grid");
  }
  const id = (value) => {
    if (typeof value !== "string" || !/^(0|[1-9][0-9]*)$/.test(value) || BigInt(value) > 0xffffffffffffffffn) {
      throw new TypeError("Invalid uint64 identity");
    }
    return value;
  };
  const mask = (words) => {
    if (!Array.isArray(words) || !words.length || words.length > Math.ceil(times.length / 64)) {
      throw new TypeError("Invalid time mask");
    }
    const result = words.map((word, index) => {
      if (typeof word !== "string" || !/^[0-9a-f]{16}$/.test(word)) throw new TypeError("Invalid mask word");
      const bits = BigInt(`0x${word}`);
      const available = Math.min(64, times.length - index * 64);
      if ((bits >> BigInt(available)) !== 0n) throw new TypeError("Mask exceeds grid");
      return bits;
    });
    if (!result.some((word) => word !== 0n)) throw new TypeError("Empty version mask");
    return result;
  };
  const strings = (object, keys) => {
    for (const key of keys) if (typeof object[key] !== "string") throw new TypeError(`Invalid ${key}`);
  };
  const nodeMasks = new Map();
  const nodes = topology.nodes.map(({ mask: words, ...value }) => {
    id(value.id);
    strings(value, ["resource_type"]);
    if (value.dimensions !== null && (typeof value.dimensions !== "object" || Array.isArray(value.dimensions) || Object.values(value.dimensions).some((v) => typeof v !== "string"))) {
      throw new TypeError("Invalid dimensions");
    }
    const bits = mask(words);
    const active = nodeMasks.get(value.id) ?? [];
    bits.forEach((word, index) => {
      if (((active[index] ?? 0n) & word) !== 0n) throw new TypeError("Overlapping node versions");
      active[index] = (active[index] ?? 0n) | word;
    });
    nodeMasks.set(value.id, active);
    return { value: { ...value, dimensions: value.dimensions === null ? null : { ...value.dimensions } }, bits };
  });
  const edges = topology.edges.map(({ mask: words, ...value }) => {
    id(value.source); id(value.target);
    strings(value, ["relation_type", "metric_name", "category", "direction"]);
    const bits = mask(words);
    bits.forEach((word, index) => {
      const source = nodeMasks.get(value.source)?.[index] ?? 0n;
      const target = nodeMasks.get(value.target)?.[index] ?? 0n;
      if ((word & source & target) !== word) throw new TypeError("Dangling edge version");
    });
    return { value, bits };
  });
  const partial = new Map();
  for (const { index, reason } of topology.partial) {
    if (!Number.isInteger(index) || index < 0 || index >= times.length || partial.has(index) || typeof reason !== "string") {
      throw new TypeError("Invalid partial state");
    }
    partial.set(index, reason);
  }
  return {
    pointCount: times.length,
    frame(index) {
      if (!Number.isInteger(index) || index < 0 || index >= times.length) throw new RangeError("Frame outside grid");
      const active = ({ bits }) => ((bits[Math.floor(index / 64)] ?? 0n) & (1n << BigInt(index % 64))) !== 0n;
      const result = {
        timestamp: times[index],
        nodes: nodes.filter(active).map(({ value }) => ({ ...value, dimensions: value.dimensions === null ? null : { ...value.dimensions } })),
        edges: edges.filter(active).map(({ value }) => ({ ...value })),
        partial: partial.has(index),
      };
      if (partial.get(index)) result.partial_reason = partial.get(index);
      return result;
    },
  };
}
