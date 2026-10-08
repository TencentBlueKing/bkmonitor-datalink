import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import type { ConsoleConfig } from "./config.js";
import { loadConfig, publicConfig, redactedConfig } from "./config.js";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  loadPolicyLinks,
  registerPolicyLinkRoutes,
  resolvePolicyLink,
} from "./policy-links.js";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});
const links = loadPolicyLinks(
  JSON.stringify({
    "tenant-a": {
      suppression:
        "https://kac.example/kingeye/#/kac/editAlarmRestrain?mode=edit&id={policy_id}",
      shield:
        "https://kac.example/kingeye/#/kac/editAlarmShield?mode=edit&id={policy_id}&space_code={space_code}&tenant={bk_tenant_id}",
      merge: "https://kac.example/kingeye/#/kac/alarmMerge/edit?id={policy_id}",
    },
    "tenant-b": { shield: "https://other.example/#/kac/alarmShield" },
  }),
);

it("resolves only explicitly mapped tenant/type, encodes context and does not contact KAC", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const app = Fastify();
  registerPolicyLinkRoutes(app, { policyLinks: links } as ConsoleConfig);
  try {
    const query = {
      bk_tenant_id: "tenant-a",
      type: "shield" as const,
      id: "23",
      space_code: "bkcc__2&mode=delete#x",
    };
    const response = await app.inject(
      "/local-api/policy-links?" + new URLSearchParams(query),
    );
    expect(response.statusCode).toBe(200);
    expect(response.headers["cache-control"]).toBe("no-store");
    expect(response.json()).toEqual({
      bk_tenant_id: "tenant-a",
      type: "shield",
      url: "https://kac.example/kingeye/#/kac/editAlarmShield?mode=edit&id=23&space_code=bkcc__2%26mode%3Ddelete%23x&tenant=tenant-a",
    });
    for (const type of ["suppression", "merge"] as const) {
      const result = resolvePolicyLink(links, { ...query, type });
      expect(result.url).toContain(
        type === "merge"
          ? "/alarmMerge/edit?id=23"
          : "/editAlarmRestrain?mode=edit&id=23",
      );
    }
    for (const tenant of ["tenant-c", "constructor", "toString"]) {
      const result = resolvePolicyLink(links, {
        ...query,
        bk_tenant_id: tenant,
      });
      expect(result).toMatchObject({ url: null, reason: "not_configured" });
    }
    expect(
      resolvePolicyLink(links, { ...query, bk_tenant_id: "tenant-b" }).url,
    ).toBe("https://other.example/#/kac/alarmShield");
    expect(
      resolvePolicyLink(links, { bk_tenant_id: "tenant-a", type: "shield" }),
    ).toMatchObject({ url: null, reason: "missing_context" });
    for (const suffix of [
      "",
      "?bk_tenant_id=tenant-a&type=other",
      "?bk_tenant_id=tenant-a&type=shield&id=a/b",
      "?bk_tenant_id=tenant-a&type=shield&url=https://evil.example",
      "?bk_tenant_id=tenant-a&bk_tenant_id=tenant-b&type=shield",
    ]) {
      expect(
        (await app.inject("/local-api/policy-links" + suffix)).statusCode,
      ).toBe(400);
    }
    expect(fetcher).not.toHaveBeenCalled();
  } finally {
    await app.close();
  }
});

it("rejects credentials, unknown variables and authority substitution without echoing configuration", () => {
  expect(loadPolicyLinks(undefined)).toEqual({});
  for (const url of [
    "javascript:alert(1)",
    "//kac.example/",
    "https:kac.example",
    "https://user:private@kac.example/",
    "https://kac.example/?token=private",
    "https://kac.example/#/kac?api_key=private",
    "https://kac.example/#/kac?%74oken=private",
    "https://{bk_tenant_id}.example/",
    "HTTPS://{bk_tenant_id}.example/",
    "https://kac.example/{unknown}",
    "https://kac.example/\n",
    "https://kac.example/" + "a".repeat(2048),
  ]) {
    expect(() =>
      loadPolicyLinks(JSON.stringify({ tenant: { shield: url } })),
    ).toThrow(/^invalid LINKD_CONSOLE_KAC_POLICY_LINKS$/);
  }
  for (const raw of [
    "",
    "null",
    "[]",
    '{"tenant":{"other":"https://kac.example"}}',
    JSON.stringify(
      Object.fromEntries(Array.from({ length: 65 }, (_, i) => ["t" + i, {}])),
    ),
    " ".repeat(65537),
  ]) {
    expect(() => loadPolicyLinks(raw)).toThrow(
      /^invalid LINKD_CONSOLE_KAC_POLICY_LINKS$/,
    );
  }
});

it("loads deployment mappings once and keeps all-tenant destinations out of public config", async () => {
  const root = await mkdtemp(join(tmpdir(), "linkd-policy-links-"));
  try {
    const path = join(root, "linkd.yaml");
    await writeFile(
      path,
      "storage:\n  repository: mysql\n  mysql:\n    address: localhost:3306\n    database: linkd\n    username: linkd\n",
    );
    vi.stubEnv("LINKD_CONSOLE_KAC_POLICY_LINKS", JSON.stringify(links));
    const loaded = await loadConfig(path);
    expect(loaded.policyLinks).toEqual(links);
    expect(JSON.stringify(publicConfig(loaded))).not.toContain("kac.example");
    expect(JSON.stringify(redactedConfig(loaded))).not.toContain("kac.example");
    vi.stubEnv("LINKD_CONSOLE_KAC_POLICY_LINKS", "invalid");
    await expect(loadConfig(path)).rejects.toThrow(
      "invalid LINKD_CONSOLE_KAC_POLICY_LINKS",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
