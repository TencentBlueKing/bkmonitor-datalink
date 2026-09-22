import { beforeEach, expect, it, vi } from "vitest";

const fakes = vi.hoisted(() => {
  const client = {
    isOpen: false,
    on: vi.fn(),
    connect: vi.fn(),
    sendCommand: vi.fn(),
    destroy: vi.fn(),
  };
  return {
    client,
    createClient: vi.fn(() => client),
    createSentinel: vi.fn(() => client),
  };
});
vi.mock("redis", () => ({
  createClient: fakes.createClient,
  createSentinel: fakes.createSentinel,
}));
import { readStrategyMembers } from "./strategy-redis.js";

beforeEach(() => {
  vi.clearAllMocks();
  fakes.client.isOpen = false;
  fakes.client.connect.mockImplementation(async () => {
    fakes.client.isOpen = true;
  });
  fakes.client.destroy.mockImplementation(() => {
    fakes.client.isOpen = false;
  });
});
it("reads only the selected key with separate Sentinel and data-node credentials", async () => {
  fakes.client.sendCommand
    .mockResolvedValueOnce(1)
    .mockResolvedValueOnce(["0", ["fp"]])
    .mockResolvedValueOnce(1);
  const result = await readStrategyMembers(
    {
      mode: "sentinel",
      database: 8,
      username: "reader",
      password: "data-private",
      sentinel: {
        masterName: "main",
        addresses: ["sentinel:26379"],
        username: "sentinel-user",
        password: "sentinel-private",
      },
    },
    "prefix:tenant:123",
    AbortSignal.timeout(1000),
    1000,
  );
  expect(result.complete).toBe(true);
  expect(fakes.createSentinel).toHaveBeenCalledWith(
    expect.objectContaining({
      nodeClientOptions: expect.objectContaining({
        database: 8,
        password: "data-private",
      }),
      sentinelClientOptions: expect.objectContaining({
        password: "sentinel-private",
      }),
    }),
  );
  expect(fakes.client.sendCommand.mock.calls).toEqual([
    [true, ["SCARD", "prefix:tenant:123"]],
    [true, ["SSCAN", "prefix:tenant:123", "0", "COUNT", "200"]],
    [true, ["SCARD", "prefix:tenant:123"]],
  ]);
  expect(fakes.client.destroy).toHaveBeenCalledOnce();
});
it("destroys a blocked connection on deadline and does not retry", async () => {
  fakes.client.sendCommand.mockImplementation(
    () => new Promise(() => undefined),
  );
  await expect(
    readStrategyMembers(
      { mode: "standalone", address: "redis:6379", database: 8 },
      "prefix:tenant:123",
      AbortSignal.timeout(10),
      10,
    ),
  ).rejects.toThrow("canceled");
  expect(fakes.client.destroy).toHaveBeenCalledOnce();
  expect(fakes.createClient).toHaveBeenCalledWith(
    expect.objectContaining({
      socket: expect.objectContaining({ reconnectStrategy: false }),
    }),
  );
});

it("detects same-cardinality replacement during scan and reports projection failure", async () => {
  fakes.client.sendCommand.mockReset();
  fakes.client.sendCommand
    .mockResolvedValueOnce([
      "2026-09-22T01:00:00Z",
      "2026-09-22T01:00:00Z",
      "",
      "1",
    ])
    .mockResolvedValueOnce(1)
    .mockResolvedValueOnce(["0", ["fp"]])
    .mockResolvedValueOnce(1)
    .mockResolvedValueOnce([
      "2026-09-22T01:00:01Z",
      "2026-09-22T01:00:02Z",
      "read_failed",
      "1",
    ])
    .mockResolvedValueOnce(["2026-09-22T01:00:00Z", "discovery_failed"])
    .mockResolvedValueOnce("123");
  const result = await readStrategyMembers(
    { mode: "standalone", address: "redis:6379", database: 8 },
    "prefix:tenant:123",
    AbortSignal.timeout(1000),
    1000,
    "prefix",
  );
  expect(result.complete).toBe(false);
  expect(result.projection).toMatchObject({
    error: "read_failed",
    discoveryError: "discovery_failed",
    pending: true,
  });
});

it("distinguishes an unbuilt empty index from a confirmed empty snapshot", async () => {
  fakes.client.sendCommand.mockReset();
  fakes.client.sendCommand
    .mockResolvedValueOnce([null, null, null, null])
    .mockResolvedValueOnce(0)
    .mockResolvedValueOnce(["0", []])
    .mockResolvedValueOnce(0)
    .mockResolvedValueOnce([null, null, null, null])
    .mockResolvedValueOnce([null, null])
    .mockResolvedValueOnce(null);
  const result = await readStrategyMembers(
    { mode: "standalone", address: "redis:6379", database: 8 },
    "prefix:tenant:123",
    AbortSignal.timeout(1000),
    1000,
    "prefix",
  );
  expect(result.projection?.lastSuccess).toBeNull();
  expect(result.members).toEqual([]);
});
