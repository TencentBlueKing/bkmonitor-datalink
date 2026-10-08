import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadConfig } from "./config.js";
import Fastify from "fastify";
import { afterEach, expect, it, vi } from "vitest";
import {
  loadKACAlertLink,
  registerKACAlertLinkRoute,
} from "./kac-alert-link.js";
import type { ConsoleConfig } from "./config.js";
const alarm = "linkd-550e8400-e29b-51d4-a716-446655440000";
it("omits optional links and renders only encoded identity from a global template", async () => {
  for (const template of [
    undefined,
    "",
    "https://kac.example/#/kac/alarmDetail?type=all&id={alarm_id}&tenant={bk_tenant_id}",
  ]) {
    const app = Fastify();
    registerKACAlertLinkRoute(app, {
      kacAlertURLTemplate: loadKACAlertLink(template),
    } as ConsoleConfig);
    try {
      for (const tenant of ["tenant-a", "tenant-b"]) {
        const r = await app.inject(
          `/local-api/kac-alert-link?bk_tenant_id=${tenant}&alarm_id=${alarm}`,
        );
        expect(r.statusCode).toBe(200);
        expect(r.headers["cache-control"]).toBe("no-store");
        expect(r.json().url).toBe(
          template
            ? `https://kac.example/#/kac/alarmDetail?type=all&id=${alarm}&tenant=${tenant}`
            : null,
        );
      }
      for (const suffix of [
        "",
        `?bk_tenant_id=a&alarm_id=${alarm}&url=https://evil.example`,
        `?bk_tenant_id=a&alarm_id=x`,
        `?bk_tenant_id=a&bk_tenant_id=b&alarm_id=${alarm}`,
      ])
        expect(
          (await app.inject("/local-api/kac-alert-link" + suffix)).statusCode,
        ).toBe(400);
    } finally {
      await app.close();
    }
  }
});
it("rejects malformed or credential-bearing templates with a safe error", () => {
  for (const raw of [
    "javascript:{alarm_id}",
    "https://{bk_tenant_id}/?id={alarm_id}",
    "https://kac.example/{unknown}?id={alarm_id}",
    "https://user:private@kac.example/{alarm_id}",
    "https://kac.example/?secret=private&id={alarm_id}",
    "https://kac.example/#/?token=private&id={alarm_id}",
    "https://kac.example/",
    "https://kac.example/" + "a".repeat(2048) + "{alarm_id}",
  ])
    expect(() => loadKACAlertLink(raw)).toThrow(
      /^invalid LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE$/,
    );
});

afterEach(() => vi.unstubAllEnvs());
it("loads the optional global URL once without requiring it for other features", async () => {
  const dir = await mkdtemp(join(tmpdir(), "linkd-alert-link-"));
  const path = join(dir, "linkd.yaml");
  try {
    await writeFile(
      path,
      "storage:\n  repository: mysql\n  mysql:\n    address: localhost:3306\n    database: linkd\n    username: linkd\n",
    );
    vi.stubEnv("LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE", "");
    expect((await loadConfig(path)).kacAlertURLTemplate).toBeUndefined();
    vi.stubEnv(
      "LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE",
      "https://kac.example/?id={alarm_id}",
    );
    expect((await loadConfig(path)).kacAlertURLTemplate).toBe(
      "https://kac.example/?id={alarm_id}",
    );
    vi.stubEnv(
      "LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE",
      "https://private:password@kac.example/{alarm_id}",
    );
    await expect(loadConfig(path)).rejects.toThrow(
      /^invalid LINKD_CONSOLE_KAC_ALERT_URL_TEMPLATE$/,
    );
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
