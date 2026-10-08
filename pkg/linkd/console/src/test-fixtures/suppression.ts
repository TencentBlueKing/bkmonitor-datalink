export const suppressionFixture = {
  evaluations: [
    {
      severity: "critical",
      suppressed: true,
      reason_code: "clip_below_threshold",
      steps: [
        {
          policy: {
            kind: "suppression",
            id: "clip-policy",
            version: 2,
            digest: "a".repeat(64),
          },
          scheme: "clip",
          outcome: "suppressed",
          reason_code: "clip_below_threshold",
          count: 2,
          threshold: 3,
          duration_seconds: 60,
          evaluated_at_ms: 1791151200000,
          counter_id: "b".repeat(64),
          epoch: "opening-event",
        },
      ],
    },
    {
      severity: "warning",
      suppressed: true,
      reason_code: "aggregation_suppressed",
      related_alert_id: "main-alert",
      steps: [
        {
          policy: {
            kind: "suppression",
            id: "group-policy",
            version: 4,
            digest: "c".repeat(64),
          },
          scheme: "aggregation",
          outcome: "suppressed",
          reason_code: "aggregation_suppressed",
          window: {
            window_id: "d".repeat(64),
            group_key: "e".repeat(64),
            epoch: "main-event",
            owner_event_id: "main-event",
            owner_alert_id: "main-alert",
            owner_source_id: "source-b",
            owner_fingerprint: "host-group",
            started_at_ms: 1791151200000,
            expires_at_ms: 1791151260000,
          },
        },
      ],
    },
  ],
};
