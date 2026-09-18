// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func TestMultipleEvaluationsAndUpgradePolicies(t *testing.T) {
	trigger := func(level string) domain.EventEvaluation {
		return domain.EventEvaluation{Severity: level, Action: domain.EventActionTriggered}
	}
	recoverLevel := func(level string) domain.EventEvaluation {
		return domain.EventEvaluation{Severity: level, Action: domain.EventActionResolved}
	}
	closeLevel := func(level string) domain.EventEvaluation {
		return domain.EventEvaluation{Severity: level, Action: domain.EventActionClosed}
	}
	cases := []struct {
		name, opening, policy, wantLevel string
		evaluations                      []domain.EventEvaluation
		sameID                           bool
		oldStatus                        domain.AlertStatus
		hooks                            int
		linked                           int
	}{
		{name: "highest only", policy: "close_and_create", evaluations: []domain.EventEvaluation{trigger("warning"), trigger("critical"), trigger("info")}, wantLevel: "critical", hooks: 1, linked: 1},
		{name: "update before recovery", opening: "warning", policy: "update_current", evaluations: []domain.EventEvaluation{recoverLevel("warning"), trigger("critical")}, wantLevel: "critical", sameID: true, hooks: 1, linked: 1},
		{name: "update before close", opening: "warning", policy: "update_current", evaluations: []domain.EventEvaluation{closeLevel("warning"), trigger("critical")}, wantLevel: "critical", sameID: true, hooks: 1, linked: 1},
		{name: "rotate before recovery", opening: "warning", policy: "close_and_create", evaluations: []domain.EventEvaluation{recoverLevel("warning"), trigger("critical")}, wantLevel: "critical", oldStatus: domain.AlertStatusClosed, hooks: 2, linked: 2},
		{name: "recover and create lower", opening: "critical", policy: "update_current", evaluations: []domain.EventEvaluation{recoverLevel("critical"), trigger("warning")}, wantLevel: "warning", oldStatus: domain.AlertStatusRecovered, hooks: 2, linked: 2},
		{name: "wrong level recovery", opening: "critical", policy: "update_current", evaluations: []domain.EventEvaluation{recoverLevel("warning"), trigger("critical")}, wantLevel: "critical", sameID: true, hooks: 1, linked: 1},
		{name: "missing high level", opening: "critical", policy: "update_current", evaluations: []domain.EventEvaluation{trigger("warning"), trigger("info")}, wantLevel: "critical", sameID: true, hooks: 0, linked: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				ctx := context.Background()
				repo := memory.New()
				hook := &recordingHook{}
				processor := newTestProcessor(t, repo, hook)
				processor.upgradePolicy = tc.policy
				var opening domain.Alert
				if tc.opening != "" {
					first := testEvent("opening", tc.opening)
					result := persistAndProcess(t, repo, processor, first)
					saved, err := repo.GetAlert(ctx, first.BKTenantID, result.AlertIDs[0])
					if err != nil {
						t.Fatal(err)
					}
					opening = saved.Alert
				}
				hook.inputs = nil
				event := testEvent("multiple", "warning")
				event.Evaluations = slices.Clone(tc.evaluations)
				if reverse {
					slices.Reverse(event.Evaluations)
				}
				event.Values = domain.EventValues{"cpu": 92.5}
				result := persistAndProcess(t, repo, processor, event)
				active, err := repo.FindActiveAlert(ctx, activeKeyForTest(event))
				if err != nil {
					t.Fatal(err)
				}
				if active.Alert.Severity != tc.wantLevel || len(hook.inputs) != tc.hooks || len(result.AlertIDs) != tc.linked {
					t.Fatalf("active=%+v result=%+v hooks=%d", active.Alert, result, len(hook.inputs))
				}
				if opening.AlertID != "" {
					if (active.Alert.AlertID == opening.AlertID) != tc.sameID {
						t.Fatal("unexpected alert identity")
					}
					if tc.sameID && (!active.Alert.BeginAt.Equal(opening.BeginAt) || active.Alert.TriggerEventID != opening.TriggerEventID || active.Alert.Title != opening.Title) {
						t.Fatal("upgrade replaced opening facts")
					}
					if tc.oldStatus != "" {
						old, err := repo.GetAlert(ctx, event.BKTenantID, opening.AlertID)
						if err != nil || old.Alert.Status != tc.oldStatus {
							t.Fatalf("old=%+v err=%v", old, err)
						}
					}
				}
				stored, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.Processing.Plan != nil || len(stored.Processing.Evaluations) != len(event.Evaluations) || stored.Event.Values["cpu"] != 92.5 {
					t.Fatalf("stored=%+v", stored)
				}
				for _, id := range stored.Event.RelatedAlertIDs {
					page, err := repo.ListEventsByAlert(ctx, event.BKTenantID, id, store.EventByAlertRequest{})
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, item := range page.Events {
						found = found || item.Event.EventID == event.EventID
					}
					if !found {
						t.Fatal("multi-alert association not queryable")
					}
				}
				beforeHooks := len(hook.inputs)
				if _, err := processor.ProcessEvent(ctx, stored); err != nil {
					t.Fatal(err)
				}
				if len(hook.inputs) != beforeHooks {
					t.Fatal("terminal replay repeated hooks")
				}
			}
		})
	}
}

type failNextCreateRepository struct {
	store.Repository
	fail bool
}

func (r *failNextCreateRepository) CreateAlert(ctx context.Context, alert domain.Alert) (store.CreateAlertResult, error) {
	if r.fail {
		r.fail = false
		return store.CreateAlertResult{}, errors.New("injected create failure")
	}
	return r.Repository.CreateAlert(ctx, alert)
}

func TestPersistedRotationResumesWithChangedPolicy(t *testing.T) {
	ctx := context.Background()
	base := memory.New()
	repository := &failNextCreateRepository{Repository: base}
	processor := newTestProcessor(t, repository, NoopFinalHook{})
	opening := testEvent("opening", "warning")
	old := persistAndProcess(t, repository, processor, opening)
	event := testEvent("upgrade", "critical")
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionResolved})
	created, err := base.CreateEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	repository.fail = true
	if _, err := processor.ProcessEvent(ctx, created.StoredEvent); err == nil {
		t.Fatal("expected interrupted creation")
	}
	pending, err := base.GetEvent(ctx, event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Processing.Plan == nil || pending.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatal("plan not persisted")
	}
	encoded, err := json.Marshal(pending.Processing.Plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded store.EventPlan
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	pending.Processing.Plan = &decoded
	retry := newTestProcessor(t, repository, NoopFinalHook{})
	retry.upgradePolicy = "update_current"
	result, err := retry.ProcessEvent(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertRotated || len(result.AlertIDs) != 2 {
		t.Fatalf("result=%+v", result)
	}
	ended, err := base.GetAlert(ctx, event.BKTenantID, old.AlertIDs[0])
	if err != nil || ended.Alert.EndType != domain.AlertEndTypeSeverityUpgrade {
		t.Fatalf("old=%+v err=%v", ended, err)
	}
	active, err := base.FindActiveAlert(ctx, activeKeyForTest(event))
	if err != nil || active.Alert.Severity != "critical" || active.Alert.AlertID == ended.Alert.AlertID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestPlanTenantIsolationAndLaterClose(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	hook := &recordingHook{}
	processor := newTestProcessor(t, repo, hook)
	event := testEvent("planned-create", "warning")
	created, err := repo.CreateEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := processor.preparePlan(ctx, created.Event)
	if err != nil {
		t.Fatal(err)
	}
	wrong := plan.Clone()
	wrong.Mutations[0].Alert.BKTenantID = "other-tenant"
	if _, err := processor.writeEventResult(ctx, created.StoredEvent, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: wrong}); err == nil {
		t.Fatal("cross-tenant plan accepted")
	}
	saved, err := processor.writeEventResult(ctx, created.StoredEvent, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	target := plan.Mutations[0].Alert
	if _, err := repo.CreateAlert(ctx, target); err != nil {
		t.Fatal(err)
	}
	_, err = processor.CloseAlert(ctx, CloseAlertCommand{OperationID: "manual", BKTenantID: event.BKTenantID, AlertID: target.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "user", Reason: "manual close", EffectiveAt: target.UpdateAt.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	hook.inputs = nil
	if _, err := processor.ProcessEvent(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if len(hook.inputs) != 0 {
		t.Fatal("replayed an active hook after a later close")
	}
	current, err := repo.GetAlert(ctx, event.BKTenantID, target.AlertID)
	if err != nil || current.Alert.Status != domain.AlertStatusClosed {
		t.Fatalf("alert reopened: %+v %v", current, err)
	}
}
