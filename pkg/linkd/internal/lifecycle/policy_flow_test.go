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
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func TestMergeFlowFreezesRolesAndBoundedFacts(t *testing.T) {
	before := releaseAlert().Merge
	relation := domain.MergeRelation{ParentAlertID: "parent", ParentTitle: "业务合并告警", Members: []domain.MergeRelationMember{{AlertID: "one"}, {AlertID: "two"}}, Policy: before.Pending[0].Policy, EndReason: "parent_ended"}
	for _, tc := range []struct{ kind, role string }{{"member_link", "member"}, {"parent_ready", "parent"}, {"member_unlink", "member"}, {"parent_recover", "parent"}, {"release", "member"}} {
		t.Run(tc.kind, func(t *testing.T) {
			change := &domain.AlertMergeChange{Kind: tc.kind, WindowID: before.Pending[0].WindowID, Before: before.Clone()}
			if err := freezeMergeFlow(change, &relation); err != nil {
				t.Fatal(err)
			}
			var role string
			if json.Unmarshal(change.LogParams["merge_role"], &role) != nil || role != tc.role {
				t.Fatal("lost merge role")
			}
			clone := change.Clone()
			change.LogParams["merge_role"][0] = 'x'
			if json.Unmarshal(clone.LogParams["merge_role"], &role) != nil || role != tc.role {
				t.Fatal("retry payload shared bytes")
			}
			if _, exists := clone.LogParams["members"]; exists {
				t.Fatal("unbounded member list copied to timeline")
			}
		})
	}
}

func TestOldPendingMergeIntentKeepsOriginalLogPayload(t *testing.T) {
	repo := memory.New()
	a := releaseAlert()
	before := a.Merge.Clone()
	a.Merge, _ = a.Merge.ReleaseWindow(before.Pending[0].WindowID)
	a.UpdateAt = a.UpdateAt.Add(time.Second)
	a.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: "legacy-release", WindowID: before.Pending[0].WindowID, EffectiveAt: a.UpdateAt, Before: before, After: a.Merge.Clone()}
	current, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	p := newTestProcessor(t, repo, NoopFinalHook{})
	if _, err = p.finishMergeChange(t.Context(), current.StoredAlert); err != nil {
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
			if len(log.Params) != 3 || log.LogID != digestStrings("merge-change-log", "release", "legacy-release") {
				t.Fatal("old intent changed immutable payload or identity", log)
			}
		}
	}
	if !found {
		t.Fatal("missing old pending log")
	}
}

func TestEventShieldFlowReportsOnlyChangedBindings(t *testing.T) {
	a := releaseAlert()
	old := domain.ShieldBinding{BindingID: "old", Policy: a.Merge.Pending[0].Policy}
	retained := old
	retained.BindingID = "retained"
	added := old
	added.BindingID = "added"
	event := domain.Event{EventID: "event", BKTenantID: a.BKTenantID}
	logs, err := eventShieldFlowLogs(event, a, []domain.ShieldBinding{old, retained}, []domain.ShieldBinding{retained, added})
	if err != nil || len(logs) != 2 {
		t.Fatal(logs, err)
	}
	for i, kind := range []domain.OperationKind{domain.OperationKindUnshield, domain.OperationKindShield} {
		if logs[i].OperationKind != kind {
			t.Fatal("lost transition kind")
		}
		var bindings []domain.ShieldBinding
		if json.Unmarshal(logs[i].Params["bindings"], &bindings) != nil || len(bindings) != 1 || bindings[0].BindingID == "retained" {
			t.Fatal("retained binding repeated")
		}
	}
	second, err := eventShieldFlowLogs(event, a, []domain.ShieldBinding{old, retained}, []domain.ShieldBinding{retained, added})
	if err != nil || !reflect.DeepEqual(logs, second) {
		t.Fatal("non-deterministic event logs")
	}
}

func TestReleaseFlowRecordsConfirmedReasonAndRetryUsesFrozenFacts(t *testing.T) {
	for _, reason := range []string{"conditions_not_met", "window_missing", "member_not_selected"} {
		t.Run(reason, func(t *testing.T) {
			repo := memory.New()
			a := releaseAlert()
			if _, err := repo.CreateAlert(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			done, err := p.ReleaseMergeWindowWithReason(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID, reason)
			if err != nil {
				t.Fatal(err)
			}
			if done.Alert.Status != domain.AlertStatusActive {
				t.Fatal("release terminated member")
			}
			page, err := repo.ListAlertLogs(t.Context(), a.BKTenantID, a.AlertID, store.PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, log := range page.Logs {
				if log.OperationKind == domain.OperationKindMergeRelease {
					found = true
					var actual string
					if err := json.Unmarshal(log.Params["reason_code"], &actual); err != nil {
						t.Fatal(err)
					}
					if actual != reason || log.Params["policy_display"] != nil || string(log.Params["display_version"]) != "2" || string(log.Params["duration_seconds"]) != "60" || log.Params["policy"] == nil {
						t.Fatal("release history lost confirmed facts")
					}
				}
			}
			if !found {
				t.Fatal("missing release log")
			}
		})
	}
}

func TestShieldFlowV2PreservesPeriodsAndCollectionsWithoutStaticDisplay(t *testing.T) {
	a := releaseAlert()
	period := domain.PolicyPeriod{From: a.UpdateAt, Until: a.UpdateAt.Add(time.Hour)}
	b := domain.ShieldBinding{BindingID: "binding", Policy: a.Merge.Pending[0].Policy, Origin: "manual", OperatorID: "user", OperatorDisplay: "张三", Reason: "配置原因", Periods: []domain.PolicyPeriod{period}}
	logs, err := eventShieldFlowLogs(domain.Event{EventID: "event", BKTenantID: a.BKTenantID}, a, nil, []domain.ShieldBinding{b})
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
	for _, key := range []string{"bindings", "after_bindings"} {
		var bindings []domain.JSONObject
		if json.Unmarshal(logs[0].Params[key], &bindings) != nil || len(bindings) != 1 {
			t.Fatal("binding collection changed")
		}
		var periods []domain.PolicyPeriod
		if json.Unmarshal(bindings[0]["periods"], &periods) != nil || !reflect.DeepEqual(periods, []domain.PolicyPeriod{period}) || bindings[0]["display"] != nil || bindings[0]["reason"] != nil || bindings[0]["operator_display"] == nil || bindings[0]["policy"] == nil {
			t.Fatal("execution facts lost or static metadata copied", bindings)
		}
	}
	if !reflect.DeepEqual(b.Periods, []domain.PolicyPeriod{period}) || b.Reason != "配置原因" {
		t.Fatal("serialization mutated business state")
	}
	old, err := marshalShieldFlowBindings([]domain.ShieldBinding{b}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal([]domain.ShieldBinding{b})
	if !reflect.DeepEqual([]byte(old), want) {
		t.Fatal("old intent payload changed")
	}
}

func TestSuppressionFlowV2KeepsRepeatedReferencesAndWindow(t *testing.T) {
	event := testEvent("event", "warning")
	ref := suppressionTestSnapshot().Releases[0]
	step := store.SuppressionStep{Policy: ref, Scheme: "aggregation", Outcome: "suppressed", Window: &store.SuppressionWindow{OwnerAlertID: "real-owner"}}
	result := store.SuppressionEvaluation{Severity: "warning", RelatedAlertID: "real-owner", ReasonCode: "aggregation_suppressed", Steps: []store.SuppressionStep{step}}
	logs, err := policySuppressionLogs(event, result)
	if err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
	var saved store.SuppressionStep
	if json.Unmarshal(logs[0].Params["suppression"], &saved) != nil || saved.Policy != ref || !reflect.DeepEqual(saved.Window, step.Window) || logs[0].Params["policy"] == nil || logs[0].Params["policy_display"] != nil || string(logs[0].Params["display_version"]) != "2" {
		t.Fatal("suppression structure changed or display retained")
	}
	if !reflect.DeepEqual(result.Steps[0], step) {
		t.Fatal("frozen evaluation mutated")
	}
}
