import { z } from "zod";
import { validPolicyURL } from "./policy-links.js";

export const kacAlertLinkQuery = z
  .object({
    bk_tenant_id: z.string().regex(/^[a-zA-Z0-9_-]{1,64}$/),
    alarm_id: z
      .string()
      .regex(
        /^linkd-[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
      ),
  })
  .strict();
export const kacAlertLinkResponse = kacAlertLinkQuery.extend({
  url: z.string().refine(validPolicyURL).nullable(),
});
