import { afterEach, expect, it, vi } from "vitest";
import { getPolicyLink } from "./api";
afterEach(() => vi.unstubAllGlobals());

it("checks response tenant/type and navigation URL before exposing a link", async () => {
  const query = { bk_tenant_id: "tenant-a", type: "merge" as const, id: "23" };
  const result = {
    bk_tenant_id: "tenant-a",
    type: "merge",
    url: "https://kac.example/#/kac/alarmMerge/edit?id=23",
  };
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const signal = new AbortController().signal;
  fetcher.mockResolvedValueOnce(Response.json(result));
  expect((await getPolicyLink(query, signal)).url).toContain("id=23");
  expect(fetcher.mock.calls[0][1].signal).toBe(signal);
  for (const invalid of [
    { ...result, bk_tenant_id: "tenant-b" },
    { ...result, type: "shield" },
    { ...result, url: "javascript:alert(1)" },
    { ...result, url: null },
  ]) {
    fetcher.mockResolvedValueOnce(Response.json(invalid));
    await expect(getPolicyLink(query)).rejects.toThrow();
  }
});
