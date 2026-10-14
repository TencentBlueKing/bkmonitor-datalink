// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runPolicyDecisionContract(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("aggregation retains distinct main references for each evaluation", func(t *testing.T) {
		repo := factory(t)
		ctx := t.Context()
		event := Event("tenant-aggregation", "event-aggregation", "multi-owner", "critical")
		event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "info", Action: domain.EventActionTriggered}, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered})
		created, err := repo.CreateEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		enriched, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, Enrichment(event))
		if err != nil {
			t.Fatal(err)
		}
		ref := store.PolicyReleaseRef{Kind: "suppression", ID: "aggregation", Version: 1, Digest: strings.Repeat("a", 64)}
		frozen := enriched
		decision := &store.PolicyDecision{Suppression: &store.SuppressionDecision{}}
		plan := &store.EventPlan{PolicyDecision: decision, UpgradePolicy: "close_and_create", State: domain.EventProcessStateSuppressed, Outcome: "alert_suppressed", ReasonCode: "aggregation_suppressed"}
		for i, evaluation := range event.Evaluations {
			id := "owner-" + evaluation.Severity
			ownerEvent := "opening-" + evaluation.Severity
			window := &store.SuppressionWindow{WindowID: strings.Repeat(string("bcd"[i]), 64), GroupKey: strings.Repeat(string("def"[i]), 64), Epoch: ownerEvent, OwnerEventID: ownerEvent, OwnerAlertID: id, OwnerSourceID: "other-source", OwnerFingerprint: "fp-" + evaluation.Severity, StartedAtMillis: event.CreateAt.UnixMilli(), ExpiresAtMillis: event.CreateAt.Add(time.Minute).UnixMilli()}
			decision.Suppression.Evaluations = append(decision.Suppression.Evaluations, store.SuppressionEvaluation{Severity: evaluation.Severity, Suppressed: true, ReasonCode: plan.ReasonCode, RelatedAlertID: id, Steps: []store.SuppressionStep{{Policy: ref, Scheme: "aggregation", Outcome: "suppressed", ReasonCode: plan.ReasonCode, Window: window}}})
			plan.Evaluations = append(plan.Evaluations, store.EvaluationResult{Severity: evaluation.Severity, Action: evaluation.Action, State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, RelatedAlertIDs: []string{id}})
			plan.RelatedAlertIDs = append(plan.RelatedAlertIDs, id)
		}
		final, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, frozen.Version, store.EventResult{PolicyDecision: decision, State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, RelatedAlertIDs: plan.RelatedAlertIDs, Evaluations: plan.Evaluations, ProcessedAt: event.CreateAt.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if len(final.Event.RelatedAlertIDs) != 3 || !reflect.DeepEqual(final.Processing.PolicyDecision, decision) {
			t.Fatal("lost per-evaluation aggregation references")
		}
		final.Processing.PolicyDecision.Suppression.Evaluations[0].Steps[0].Window.OwnerFingerprint = "mutated"
		read, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
		if err != nil || !reflect.DeepEqual(read.Processing.PolicyDecision, decision) {
			t.Fatal("window pointer aliases storage", err)
		}
	})

}
