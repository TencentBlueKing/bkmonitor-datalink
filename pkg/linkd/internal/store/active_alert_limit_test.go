// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestActiveLimitDiagnosisPreservesEnvelopeAndIsolation(t *testing.T) {
	current := sourceSnapshot(t)
	decision := &store.ActiveAlertLimitDecision{Rule: "strategy", Group: map[string]string{"labels.strategy_id": "123"}, Count: 5000, MaxActive: 5000}
	result := store.EventResult{State: domain.EventProcessStateSuppressed, Outcome: "alert_limited", ReasonCode: "active_alert_limit_exceeded", ProcessedAt: time.Now().UTC(), ActiveAlertLimit: decision}
	event, processing, err := store.ApplyEventResultFromSnapshot(current, result)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventID != current.Event.EventID || len(event.RelatedAlertIDs) != 0 || processing.ActiveAlertLimit == nil {
		t.Fatal("diagnosis changed source identity")
	}
	decision.Group["labels.strategy_id"] = "changed"
	copy := processing.Clone()
	copy.ActiveAlertLimit.Group["labels.strategy_id"] = "changed"
	if processing.ActiveAlertLimit.Group["labels.strategy_id"] != "123" {
		t.Fatal("shared diagnosis")
	}
	raw, err := json.Marshal(processing)
	if err != nil {
		t.Fatal(err)
	}
	var restored store.EventProcessing
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ActiveAlertLimit.MaxActive != 5000 || restored.ReasonCode != "active_alert_limit_exceeded" {
		t.Fatal("lost JSON diagnosis")
	}
}
