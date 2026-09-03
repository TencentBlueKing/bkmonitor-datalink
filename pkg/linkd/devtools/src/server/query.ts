import { z } from "zod";

import type { DevtoolsConfig } from "./config.js";
import type { EntityKind, SearchParams } from "../shared/contracts.js";
import { cursorTimeRange } from "./cursor.js";

const rawQuerySchema = z.object({
  bk_tenant_id: z.string().min(1).max(1024).optional(),
  id: z.string().min(1).max(1024).optional(),
  from: z.string().datetime().optional(),
  to: z.string().datetime().optional(),
  state: z.string().max(64).optional(),
  status: z.string().max(64).optional(),
  event_source_id: z.string().max(1024).optional(),
  related_alert_id: z.string().max(1024).optional(),
  fingerprint: z.string().max(1024).optional(),
  severity: z.string().max(255).optional(),
  alert_id: z.string().max(1024).optional(),
  operation_kind: z.string().max(64).optional(),
  operator_kind: z.string().max(64).optional(),
  limit: z.coerce.number().int().positive().optional(),
  cursor: z.string().max(16384).optional(),
});

export function parseSearchQuery(
  raw: unknown,
  _entity: EntityKind,
  config: DevtoolsConfig,
): SearchParams {
  const parsed = rawQuerySchema.parse(raw);
  const now = new Date();
  const cursorRange = parsed.cursor
    ? cursorTimeRange(parsed.cursor)
    : { from: undefined, to: undefined };
  const fromValue = parsed.from ?? cursorRange.from;
  const toValue = parsed.to ?? cursorRange.to;
  const exactIDWithoutRange = Boolean(parsed.id && !fromValue && !toValue);
  const to = toValue ? new Date(toValue) : now;
  const from = fromValue
    ? new Date(fromValue)
    : exactIDWithoutRange
      ? undefined
      : new Date(to.getTime() - config.query.defaultRangeSeconds * 1000);
  if (from && to.getTime() < from.getTime())
    throw new Error("to must not be earlier than from");
  if (
    from &&
    to.getTime() - from.getTime() > config.query.maxRangeSeconds * 1000
  ) {
    throw new Error(
      `query range must not exceed ${config.query.maxRangeSeconds} seconds`,
    );
  }
  const limit = parsed.limit ?? config.query.defaultLimit;
  if (limit > config.query.maxLimit)
    throw new Error(`limit must not exceed ${config.query.maxLimit}`);
  return {
    tenantId: parsed.bk_tenant_id,
    id: parsed.id,
    from: from?.toISOString(),
    to: exactIDWithoutRange && !parsed.to ? undefined : to.toISOString(),
    state: parsed.state,
    status: parsed.status,
    eventSourceId: parsed.event_source_id,
    relatedAlertId: parsed.related_alert_id,
    fingerprint: parsed.fingerprint,
    severity: parsed.severity,
    alertId: parsed.alert_id,
    operationKind: parsed.operation_kind,
    operatorKind: parsed.operator_kind,
    limit,
    cursor: parsed.cursor,
  };
}
