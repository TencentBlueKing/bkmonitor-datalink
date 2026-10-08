import { EventEmitter } from "node:events";
import type { FastifyReply, FastifyRequest } from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import { createPolicyProxy } from "./policies.js";
import type { ConsoleConfig } from "./config.js";

const config = {
  dispatch: {
    url: "http://control",
    jwt: { secretKey: "test-only", username: "admin" },
  },
  query: { timeoutMilliseconds: 10000 },
} as ConsoleConfig;
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function call(
  proxy: ReturnType<typeof createPolicyProxy>,
  parse: (v: unknown) => unknown = (v) => v,
) {
  const raw = new EventEmitter(),
    responseRaw = Object.assign(new EventEmitter(), { writableEnded: false });
  const result: { status: number; body?: unknown } = { status: 200 };
  const reply = {
    raw: responseRaw,
    header() {
      return reply;
    },
    code(status: number) {
      result.status = status;
      return reply;
    },
    send(body: unknown) {
      result.body = body;
      responseRaw.writableEnded = true;
      return result;
    },
  };
  const done = proxy(
    { raw } as unknown as FastifyRequest,
    reply as unknown as FastifyReply,
    "/read",
    undefined,
    parse,
  );
  return { raw, responseRaw, result, done };
}
function upstream() {
  const pending: Array<{ resolve: () => void; signal: AbortSignal }> = [];
  let active = 0,
    maximum = 0;
  const fetcher = vi.fn(
    (_url: string, options: RequestInit) =>
      new Promise<Response>((resolve, reject) => {
        const signal = options.signal! as AbortSignal;
        active++;
        maximum = Math.max(maximum, active);
        const abort = () => {
          active--;
          reject(signal.reason);
        };
        signal.addEventListener("abort", abort, { once: true });
        pending.push({
          signal,
          resolve: () => {
            signal.removeEventListener("abort", abort);
            active--;
            resolve(Response.json({ ok: true }));
          },
        });
      }),
  );
  vi.stubGlobal("fetch", fetcher);
  return { pending, fetcher, maximum: () => maximum };
}
it("limits upstream to two, bounds sixteen queued requests and frees canceled waiters", async () => {
  const u = upstream(),
    proxy = createPolicyProxy(config, 16);
  const calls = Array.from({ length: 18 }, () => call(proxy));
  await vi.waitFor(() => expect(u.fetcher).toHaveBeenCalledTimes(2));
  const overflow = call(proxy);
  await overflow.done;
  expect(overflow.result.status).toBe(429);
  calls[2].raw.emit("aborted");
  await calls[2].done;
  expect(calls[2].result.status).toBe(502);
  const replacement = call(proxy);
  expect(u.fetcher).toHaveBeenCalledTimes(2);
  for (let i = 0; i < 18; i++) {
    await vi.waitFor(() => expect(u.pending.length).toBeGreaterThan(i));
    u.pending[i].resolve();
  }
  await Promise.all([...calls.map((c) => c.done), replacement.done]);
  expect(u.maximum()).toBe(2);
  expect(replacement.result.status).toBe(200);
  for (const [i, c] of calls.entries()) {
    expect(c.result.status).toBe(i === 2 ? 502 : 200);
    expect(c.raw.listenerCount("aborted")).toBe(0);
    expect(c.responseRaw.listenerCount("close")).toBe(0);
  }
});
it("starts the total deadline before waiting and releases slots after parse failures", async () => {
  const deadlines: AbortController[] = [];
  vi.spyOn(AbortSignal, "timeout").mockImplementation(() => {
    const c = new AbortController();
    deadlines.push(c);
    return c.signal;
  });
  const u = upstream(),
    proxy = createPolicyProxy(config, 1);
  const first = call(proxy, () => {
      throw new Error("private response");
    }),
    second = call(proxy),
    queued = call(proxy);
  await vi.waitFor(() => expect(u.fetcher).toHaveBeenCalledTimes(2));
  expect(deadlines).toHaveLength(3);
  deadlines[2].abort(new Error("private timeout"));
  await queued.done;
  expect(queued.result.status).toBe(502);
  expect(JSON.stringify(queued.result.body)).not.toContain("private");
  const next = call(proxy);
  u.pending[0].resolve();
  await first.done;
  expect(first.result.status).toBe(502);
  await vi.waitFor(() => expect(u.fetcher).toHaveBeenCalledTimes(3));
  u.pending[1].resolve();
  u.pending[2].resolve();
  await Promise.all([second.done, next.done]);
  expect(next.result.status).toBe(200);
  expect(u.maximum()).toBe(2);
});
