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
	"reflect"
	"strconv"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func v3Facts(t *testing.T, params domain.JSONObject, key string) domain.JSONObject {
	t.Helper()
	if string(params["display_version"]) != "3" || string(params["content_key"]) != `"`+key+`"` {
		t.Fatalf("wrong envelope: %v", params)
	}
	var facts domain.JSONObject
	if err := json.Unmarshal(params["content_params"], &facts); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"reason_code", "policy", "window_id", "relation_id", "bindings", "before_bindings", "after_bindings", "change_kind", "merge_role"} {
		if _, ok := params[field]; ok {
			t.Errorf("duplicated root fact %s", field)
		}
	}
	if params["content"] != nil || params["content_format"] != nil || facts["content"] != nil || facts["content_format"] != nil {
		t.Fatal("stored rendered snapshot")
	}
	return facts
}

func TestPolicyFlowV3ShieldAndSuppressionKeepCompleteFacts(t *testing.T) {
	a := releaseAlert()
	b := domain.ShieldBinding{BindingID: "binding", Policy: a.Merge.Pending[0].Policy, Origin: "manual", OperatorID: "alice", OperatorDisplay: "甲"}
	for _, action := range []struct {
		before, after []domain.ShieldBinding
		key           string
	}{
		{nil, []domain.ShieldBinding{b}, "linkd_shield.v1"},
		{[]domain.ShieldBinding{b}, nil, "linkd_unshield.v1"},
	} {
		logs, err := eventShieldFlowLogs(domain.Event{EventID: "event", BKTenantID: a.BKTenantID}, a, action.before, action.after)
		if err != nil || len(logs) != 1 {
			t.Fatal(logs, err)
		}
		facts := v3Facts(t, logs[0].Params, action.key)
		var bindings []domain.ShieldBinding
		if json.Unmarshal(facts["bindings"], &bindings) != nil || len(bindings) != 1 || bindings[0].Policy != b.Policy || bindings[0].OperatorID != "alice" {
			t.Fatal("lost binding facts")
		}
		if string(logs[0].Params["event_id"]) != `"event"` {
			t.Fatal("lost root event identity")
		}
	}
	event := testEvent("event", "warning")
	ref := suppressionTestSnapshot().Releases[0]
	step := store.SuppressionStep{Policy: ref, Scheme: "aggregation", Outcome: "suppressed", Window: &store.SuppressionWindow{OwnerAlertID: "owner"}}
	result := store.SuppressionEvaluation{Severity: "warning", RelatedAlertID: "owner", ReasonCode: "aggregation_suppressed", Steps: []store.SuppressionStep{step}}
	logs, err := policySuppressionLogs(event, result)
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
	facts := v3Facts(t, logs[0].Params, "linkd_policy_suppress.v1")
	var saved store.SuppressionStep
	if json.Unmarshal(facts["suppression"], &saved) != nil || saved.Policy != ref || !reflect.DeepEqual(saved.Window, step.Window) {
		t.Fatal("lost suppression decision")
	}
	second, err := policySuppressionLogs(event, result)
	if err != nil || !reflect.DeepEqual(logs, second) {
		t.Fatal("v3 identity/body is not deterministic")
	}
	clip := result
	clip.RelatedAlertID = ""
	clip.Steps = []store.SuppressionStep{{Policy: ref, Scheme: "clip", Outcome: "suppressed"}}
	if noAlert, err := policySuppressionLogs(event, clip); err != nil || len(noAlert) != 0 {
		t.Fatal("created fake Alert log")
	}
}

func TestNewMergeFlowV3FreezesEveryActionBeforeOutput(t *testing.T) {
	before := releaseAlert().Merge
	relation := domain.MergeRelation{ParentAlertID: "parent", ParentTitle: "合并告警", Members: []domain.MergeRelationMember{{AlertID: "one"}}, Policy: before.Pending[0].Policy, EndReason: "parent_ended"}
	for _, kind := range []string{"member_link", "parent_ready", "member_unlink", "parent_recover", "release"} {
		t.Run(kind, func(t *testing.T) {
			change := &domain.AlertMergeChange{Kind: kind, OperationID: "operation", WindowID: before.Pending[0].WindowID, RelationID: "relation", Before: before.Clone()}
			if err := freezeMergeFlow(change, &relation); err != nil {
				t.Fatal(err)
			}
			facts := v3Facts(t, change.LogParams, "linkd_merge_"+kind+".v1")
			if string(change.LogParams["operation_id"]) != `"operation"` || string(facts["relation_id"]) != `"relation"` {
				t.Fatal("lost operation/relationship identity")
			}
			if string(facts["change_kind"]) != `"`+kind+`"` {
				t.Fatal("lost action")
			}
			clone := change.Clone()
			change.LogParams["content_params"][0] = 'x'
			_ = v3Facts(t, clone.LogParams, "linkd_merge_"+kind+".v1")
		})
	}
}

func TestMergeV3ReplayRetainsFrozenBodyAfterLostAppendResult(t *testing.T) {
	repo := &trackingRepository{Repository: memory.New(), failBatchIndex: 0}
	a := releaseAlert()
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	_, err := p.ReleaseMergeWindowWithReason(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID, "conditions_not_met")
	if err == nil {
		t.Fatal("expected lost append result")
	}
	pending, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || pending.Alert.MergeChange == nil {
		t.Fatal("missing durable intent", err)
	}
	facts := v3Facts(t, pending.Alert.MergeChange.LogParams, "linkd_merge_release.v1")
	if string(facts["reason_code"]) != `"conditions_not_met"` {
		t.Fatal("lost actual release reason")
	}
	repo.failBatchIndex = -1
	if _, err = p.finishMergeChange(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	_, batches := repo.snapshot()
	if len(batches) != 2 || !reflect.DeepEqual(batches[0], batches[1]) {
		t.Fatal("retry rebuilt frozen output", batches)
	}
}

func TestOldMergeV2IntentRemainsV2UnderV3Writer(t *testing.T) {
	repo := memory.New()
	a := releaseAlert()
	before := a.Merge.Clone()
	a.Merge, _ = a.Merge.ReleaseWindow(before.Pending[0].WindowID)
	a.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: "old-op", WindowID: before.Pending[0].WindowID, EffectiveAt: a.UpdateAt, Before: before, After: a.Merge.Clone()}
	a.MergeChange.LogParams = domain.JSONObject{
		"display_version":  json.RawMessage("2"),
		"change_kind":      json.RawMessage(`"release"`),
		"merge_role":       json.RawMessage(`"member"`),
		"reason_code":      json.RawMessage(`"window_released"`),
		"duration_seconds": json.RawMessage("60"),
		"policy":           json.RawMessage(`{"id":"merge","version":1,"digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`),
	}
	saved, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	if _, err = p.finishMergeChange(t.Context(), saved.StoredAlert); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListAlertLogs(t.Context(), a.BKTenantID, a.AlertID, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, log := range page.Logs {
		if log.OperationKind == domain.OperationKindMergeRelease {
			found = true
			if string(log.Params["display_version"]) != "2" || log.Params["content_key"] != nil || log.LogID != digestStrings("policy-flow-log-v2", digestStrings("merge-change-log", "release", "old-op")) {
				t.Fatal("rewrote old intent", log)
			}
		}
	}
	if !found {
		t.Fatal("missing log")
	}
}

func TestShieldIntentUsesItsPersistedVersion(t *testing.T) {
	for _, version := range []int{2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			repo := memory.New()
			a := releaseAlert()
			b := domain.ShieldBinding{BindingID: strings.Repeat("d", 64), ActivationID: strings.Repeat("e", 64), Policy: a.Merge.Pending[0].Policy, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: a.UpdateAt}
			a.PolicyChange = &domain.AlertPolicyChange{LogVersion: version, OperationID: "shield-op", EffectiveAt: a.UpdateAt, Before: []domain.ShieldBinding{b}}
			saved, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			if _, err = p.finishPendingChanges(t.Context(), saved.StoredAlert); err != nil {
				t.Fatal(err)
			}
			page, err := repo.ListAlertLogs(t.Context(), a.BKTenantID, a.AlertID, store.PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, log := range page.Logs {
				if log.OperationKind == domain.OperationKindUnshield {
					found = true
					if version == 3 {
						_ = v3Facts(t, log.Params, "linkd_unshield.v1")
					} else if string(log.Params["display_version"]) != "2" || log.Params["content_key"] != nil {
						t.Fatal("old shield upgraded during replay")
					}
				}
			}
			if !found {
				t.Fatal("missing shield log")
			}
		})
	}
}

type policyFlowTestMerger struct{ wait domain.MergeWait }

func (m policyFlowTestMerger) Check(_ context.Context, _ domain.Event, evaluation domain.EventEvaluation, _ *store.PolicyContext, _ domain.Alert, _ func(string) (string, error)) (MergeEvaluation, error) {
	return MergeEvaluation{Merge: &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{m.wait}}, Decision: &store.MergeDecision{Severity: evaluation.Severity, Steps: []store.MergeStep{{Policy: store.PolicyReleaseRef{Kind: "merge", ID: m.wait.Policy.ID, Version: m.wait.Policy.Version, Digest: m.wait.Policy.Digest}, Outcome: "joined", Wait: &m.wait}}}}, nil
}
func (policyFlowTestMerger) Confirm(context.Context, domain.Event, *store.PolicyDecision, domain.Alert) error {
	return nil
}

func TestMergeWaitV3KeepsWindowDeadlineAndEventIdentity(t *testing.T) {
	a := releaseAlert()
	wait := a.Merge.Pending[0].Clone()
	a.Merge = nil
	event := testEvent("wait-event", "warning")
	wait.MemberEventID = event.EventID
	p := newTestProcessor(t, memory.New(), NoopFinalHook{})
	p.merger = policyFlowTestMerger{wait: wait}
	plan := &store.EventPlan{}
	if err := p.planMerge(t.Context(), event, event.Evaluations[0], nil, plan, &a); err != nil {
		t.Fatal(err)
	}
	if len(plan.Logs) != 1 {
		t.Fatal("missing waiting log")
	}
	facts := v3Facts(t, plan.Logs[0].Params, "linkd_merge_wait.v1")
	if string(facts["duration_seconds"]) != "60" || string(facts["window_id"]) != `"`+wait.WindowID+`"` || facts["deadline"] == nil || string(plan.Logs[0].Params["event_id"]) != `"wait-event"` {
		t.Fatal("waiting facts changed", facts)
	}
}

func TestSourceTerminalUnshieldUsesV3Facts(t *testing.T) {
	repo := memory.New()
	a := releaseAlert()
	ref := a.Merge.Pending[0].Policy
	a.Merge = nil
	binding := domain.ShieldBinding{BindingID: strings.Repeat("d", 64), ActivationID: strings.Repeat("e", 64), Policy: ref, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: a.UpdateAt, Reason: "旧配置原因"}
	a.Shield = domain.AlertShield{Active: true, NextCheckAt: &a.UpdateAt, Bindings: []domain.ShieldBinding{binding}}
	stored, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	event := testEvent("source-resolved", a.Severity)
	event.BKTenantID, event.EventSourceID, event.Fingerprint = a.BKTenantID, a.EventSourceID, a.Fingerprint
	event.Evaluations[0].Action = domain.EventActionResolved
	p := newTestProcessor(t, repo, NoopFinalHook{})
	plan, err := p.preparePlan(t.Context(), event, nil, stored.StoredAlert)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, log := range plan.Logs {
		if log.OperationKind != domain.OperationKindUnshield {
			continue
		}
		found = true
		facts := v3Facts(t, log.Params, "linkd_unshield.v1")
		if string(facts["reason_code"]) != `"alert_ended"` || string(facts["after_bindings"]) != `[]` {
			t.Fatal("lost terminal transition", facts)
		}
		var bindings []domain.JSONObject
		if json.Unmarshal(facts["bindings"], &bindings) != nil || len(bindings) != 1 || bindings[0]["reason"] != nil || bindings[0]["policy"] == nil {
			t.Fatal("lost binding or copied static reason")
		}
	}
	if !found {
		t.Fatal("missing terminal unshield log")
	}
}
