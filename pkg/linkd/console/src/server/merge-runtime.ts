import type { FastifyInstance } from "fastify";
import { z } from "zod";
import {
  mergePage,
  mergeQuery,
  mergeResource,
  mergeRow,
  mergeSnapshotPage,
  frozenMergeSnapshot,
} from "../shared/merge-runtime.js";
import type { ConsoleConfig } from "./config.js";
import { createPolicyProxy } from "./policies.js";

// 本组路由只读；显式操作使用独立命令入口，不通过查询触发副作用。
export function registerMergeRuntimeRoutes(
  app: FastifyInstance,
  config: ConsoleConfig,
  proxy = createPolicyProxy(config),
) {
  const key = z.object({
    resource: mergeResource,
    id: z.string().regex(/^[a-f0-9]{64}$/),
  });
  const tenant = z
    .object({ bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/) })
    .strict();
  app.get(
    "/local-api/policy-runtime/merge/decisions/:id/members/:alert",
    async (request, reply) => {
      const k = z
          .object({
            id: z.string().regex(/^[a-f0-9]{64}$/),
            alert: z.string().min(1).max(160),
          })
          .safeParse(request.params),
        q = tenant.safeParse(request.query);
      if (!k.success || !q.success)
        return reply
          .code(400)
          .send({ error: { message: "冻结快照参数不合法" } });
      return proxy(
        request,
        reply,
        "/api/v1/policy-runtime/merge/decisions/" +
          k.data.id +
          "/members/" +
          encodeURIComponent(k.data.alert) +
          "?" +
          new URLSearchParams(q.data),
        undefined,
        (raw) => {
          const row = frozenMergeSnapshot.parse(raw);
          if (
            row.bk_tenant_id !== q.data.bk_tenant_id ||
            row.decision_id !== k.data.id ||
            row.alert.alert_id !== k.data.alert
          )
            throw new Error("scope");
          return row;
        },
        8 << 20,
      );
    },
  );
  app.get("/local-api/policy-runtime/merge", async (request, reply) => {
    const parsed = mergeQuery.safeParse(request.query);
    if (!parsed.success)
      return reply
        .code(400)
        .send({ error: { message: "运行态查询参数不合法" } });
    const { resource, ...query } = parsed.data;
    const params = new URLSearchParams(
      Object.entries(query)
        .filter(([, v]) => v !== undefined)
        .map(([k, v]) => [k, String(v)]),
    );
    return proxy(
      request,
      reply,
      "/api/v1/policy-runtime/merge/" + resource + "?" + params,
      undefined,
      (raw) => {
        const page = mergePage(resource).parse(raw);
        if (
          page.bk_tenant_id !== query.bk_tenant_id ||
          page.items.length > query.limit ||
          (page.next && page.next === query.after)
        )
          throw new Error("scope");
        let last = "";
        for (const row of page.items) {
          if (
            row.bk_tenant_id !== query.bk_tenant_id ||
            row.id <= last ||
            (query.policy_id && row.policy.id !== query.policy_id) ||
            (query.phase && (!("phase" in row) || row.phase !== query.phase))
          )
            throw new Error("scope");
          if (
            query.alert_id &&
            (!("members" in row) ||
              !("parent_alert_id" in row) ||
              (row.parent_alert_id !== query.alert_id &&
                !row.members?.some(
                  (m) => "alert_id" in m && m.alert_id === query.alert_id,
                )))
          )
            throw new Error("alert scope");
          last = row.id;
        }
        return page;
      },
      1 << 20,
    );
  });
  app.get(
    "/local-api/policy-runtime/merge/:resource/:id",
    async (request, reply) => {
      const k = key.safeParse(request.params),
        q = tenant.safeParse(request.query);
      if (!k.success || !q.success)
        return reply
          .code(400)
          .send({ error: { message: "运行态详情参数不合法" } });
      return proxy(
        request,
        reply,
        "/api/v1/policy-runtime/merge/" +
          k.data.resource +
          "/" +
          k.data.id +
          "?" +
          new URLSearchParams(q.data),
        undefined,
        (raw) => {
          const row = mergeRow(k.data.resource).parse(raw);
          if (row.bk_tenant_id !== q.data.bk_tenant_id || row.id !== k.data.id)
            throw new Error("scope");
          return row;
        },
        1 << 20,
      );
    },
  );
  app.get(
    "/local-api/policy-runtime/merge/decisions/:id/members",
    async (request, reply) => {
      const k = z
        .object({ id: z.string().regex(/^[a-f0-9]{64}$/) })
        .safeParse(request.params);
      const q = tenant
        .extend({
          after: z.string().max(1024).default(""),
          limit: z.coerce.number().int().min(1).max(4).default(4),
        })
        .safeParse(request.query);
      if (!k.success || !q.success)
        return reply
          .code(400)
          .send({ error: { message: "成员查询参数不合法" } });
      const params = new URLSearchParams({
        ...q.data,
        limit: String(q.data.limit),
      });
      return proxy(
        request,
        reply,
        "/api/v1/policy-runtime/merge/decisions/" +
          k.data.id +
          "/members?" +
          params,
        undefined,
        (raw) => {
          const page = mergeSnapshotPage.parse(raw);
          if (
            page.bk_tenant_id !== q.data.bk_tenant_id ||
            page.items.length > q.data.limit ||
            (page.next && page.next === q.data.after) ||
            page.items.some(
              (row) =>
                row.bk_tenant_id !== q.data.bk_tenant_id ||
                row.decision_id !== k.data.id,
            )
          )
            throw new Error("scope");
          return page;
        },
        1 << 20,
      );
    },
  );
}
