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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runPolicyContextContract(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("policy context frozen before effects and retained after replay", func(t *testing.T) {
		repo := factory(t)
		ctx := t.Context()
		event := Event("tenant-policy", "event-policy", "policy-fp", "warning")
		created, err := repo.CreateEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := &store.PolicyContext{EvaluatedAt: event.CreateAt.Add(time.Minute), Releases: []store.PolicyReleaseRef{{Kind: "suppression", ID: "policy", Version: 1, Digest: strings.Repeat("a", 64)}}}
		if _, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, created.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: snapshot}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("policy context saved before enrich: %v", err)
		}
		enriched, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, Enrichment(event))
		if err != nil {
			t.Fatal(err)
		}
		frozen, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, enriched.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		changed := snapshot.Clone()
		changed.Releases[0].Version = 2
		if _, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, frozen.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: changed}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("context changed on retry: %v", err)
		}
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, frozen.Version, Enrichment(event)); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("enrich changed after policy snapshot: %v", err)
		}
		evaluation := store.EvaluationResult{Severity: "warning", Action: domain.EventActionTriggered, State: domain.EventProcessStateSuppressed, Outcome: "event_suppressed", ReasonCode: "clip_below_threshold"}
		decision := &store.PolicyDecision{Suppression: &store.SuppressionDecision{Evaluations: []store.SuppressionEvaluation{{Severity: "warning", Suppressed: true, ReasonCode: "clip_below_threshold", Steps: []store.SuppressionStep{{Policy: snapshot.Releases[0], Scheme: "clip", Outcome: "suppressed", ReasonCode: "clip_below_threshold", Count: 1, Threshold: 3, DurationSeconds: 60, CounterID: strings.Repeat("b", 64), Epoch: event.EventID, EvaluatedAtMillis: snapshot.EvaluatedAt.UnixMilli()}}}}}}
		plan := &store.EventPlan{PolicyDecision: decision, UpgradePolicy: "close_and_create", State: domain.EventProcessStateSuppressed, Outcome: evaluation.Outcome, ReasonCode: evaluation.ReasonCode, Evaluations: []store.EvaluationResult{evaluation}}
		planned, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, frozen.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		changedPlan := plan.Clone()
		changedPlan.PolicyDecision.Suppression.Evaluations[0].Steps[0].Count = 2
		if _, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, planned.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: changedPlan}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("saved count overwritten: %v", err)
		}
		if _, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, planned.Version, store.EventResult{State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, Evaluations: plan.Evaluations, ProcessedAt: event.CreateAt.Add(2 * time.Minute)}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("final CAS erased policy decision: %v", err)
		}
		cleared, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, planned.Version, store.EventResult{State: domain.EventProcessStateUnprocessed})
		if err != nil || !reflect.DeepEqual(cleared.Processing.PolicyContext, snapshot) {
			t.Fatalf("plan reset cleared policy time/version: %+v %v", cleared.Processing.PolicyContext, err)
		}
		final, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, cleared.Version, store.EventResult{PolicyDecision: decision, State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, Evaluations: plan.Evaluations, ProcessedAt: event.CreateAt.Add(2 * time.Minute)})
		if err != nil || !reflect.DeepEqual(final.Processing.PolicyContext, snapshot) || !reflect.DeepEqual(final.Processing.PolicyDecision, decision) {
			t.Fatalf("final lost context: %+v %v", final.Processing.PolicyContext, err)
		}
		replay, err := repo.CreateEvent(ctx, event)
		if err != nil || replay.Created || !reflect.DeepEqual(replay.Processing.PolicyContext, snapshot) || !reflect.DeepEqual(replay.Processing.PolicyDecision, decision) {
			t.Fatalf("redelivery lost context: %+v %v", replay.Processing.PolicyContext, err)
		}
		read, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
		if err != nil || !reflect.DeepEqual(read.Processing.PolicyContext, snapshot) || !reflect.DeepEqual(read.Processing.PolicyDecision, decision) {
			t.Fatalf("read lost context: %+v %v", read.Processing.PolicyContext, err)
		}
	})
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
		snapshot := &store.PolicyContext{EvaluatedAt: event.CreateAt, Releases: []store.PolicyReleaseRef{ref}}
		frozen, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, enriched.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: snapshot})
		if err != nil {
			t.Fatal(err)
		}
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
		planned, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, frozen.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		final, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, planned.Version, store.EventResult{PolicyDecision: decision, State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, RelatedAlertIDs: plan.RelatedAlertIDs, Evaluations: plan.Evaluations, ProcessedAt: event.CreateAt.Add(time.Minute)})
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
