import { z } from "zod";

// 指标只按进程观察，不接受租户、来源或 Alert 作为筛选标签。
export const deliveryMetricQuery = z
  .object({
    from: z.string().datetime(),
    to: z.string().datetime(),
    step: z.coerce.number().int().min(1).max(3600),
    calculation_window_seconds: z.coerce
      .number()
      .int()
      .min(15)
      .max(3600)
      .default(60),
    instance: z.string().min(1).max(512).optional(),
  })
  .strict()
  .superRefine((v, ctx) => {
    const seconds = (Date.parse(v.to) - Date.parse(v.from)) / 1000;
    if (
      seconds <= 0 ||
      seconds > 604800 ||
      Math.floor(seconds / v.step) + 1 > 481
    ) {
      ctx.addIssue({
        code: "custom",
        message: "查询范围不合法或超过 481 个采样点",
      });
    }
  });
export type DeliveryMetricQuery = z.infer<typeof deliveryMetricQuery>;
export const deliveryMetricIDs = {
  action: [
    "action-runners",
    "action-rounds",
    "action-duration",
    "action-work",
    "action-page-items",
    "action-page-age",
    "action-page-freshness",
    "action-unconfirmed",
  ] as const,
  projection: [
    "projection-runners",
    "projection-rounds",
    "projection-duration",
    "projection-work",
    "projection-page-items",
    "projection-page-age",
    "projection-page-freshness",
  ] as const,
};
export const deliveryMetricKindSchema = z.enum(["action", "projection"]);
export type DeliveryMetricKind = z.infer<typeof deliveryMetricKindSchema>;
