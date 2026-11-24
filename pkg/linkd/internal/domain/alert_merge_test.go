// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain_test

import (
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func mergeWaitFixture(a domain.Alert) domain.MergeWait {
	return domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0, 1}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}
}

func TestMergeStateIndependentImmutableWaitAndAdmission(t *testing.T) {
	a := validAlert()
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.TriggerEventID}
	if !a.AdmittedActiveMain() {
		t.Fatal("fixture not admitted")
	}
	a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{mergeWaitFixture(a)}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if !a.Merge.Blocking() || a.AdmittedActiveMain() {
		t.Fatal("pending alert was an admitted main")
	}
	changed := a.Clone()
	changed.Merge.Pending[0].Groups[0] = 2
	if a.Merge.Pending[0].Groups[0] != 0 {
		t.Fatal("merge clone shares groups")
	}
	changed = a.Clone()
	changed.Revision++
	changed.UpdateAt = changed.UpdateAt.Add(time.Second)
	changed.Merge.Pending[0].Deadline = changed.Merge.Pending[0].Deadline.Add(time.Second)
	if err := domain.ValidateAlertReplacement(a, changed); err == nil {
		t.Fatal("same window extended deadline")
	}
	ended := a.Clone()
	ended.Revision++
	ended.UpdateAt = ended.UpdateAt.Add(time.Second)
	ended.Status = domain.AlertStatusRecovered
	ended.EndAt = &ended.UpdateAt
	ended.EndType = domain.AlertEndTypeSource
	ended.EndReason = "resolved"
	if err := ended.Validate(); err == nil {
		t.Fatal("terminal waiting accepted")
	}
	ended.Merge = ended.Merge.EndWaiting()
	if err := domain.ValidateAlertReplacement(a, ended); err != nil {
		t.Fatal(err)
	}
	if ended.Merge.State != "released" || ended.Merge.Blocking() {
		t.Fatal("terminal merge wait retained")
	}
}

func TestAggregateRelationReadinessDoesNotReuseLifecycleStatus(t *testing.T) {
	a := validAlert()
	a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: strings.Repeat("d", 64)}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if !a.Merge.Blocking() {
		t.Fatal("aggregate dispatched before relations")
	}
	ready := a.Clone()
	ready.Revision++
	ready.UpdateAt = ready.UpdateAt.Add(time.Second)
	ready.Merge.RelationsReady = true
	ready.Merge.State = "merged"
	ready.Merge.RelationIDs = []string{strings.Repeat("e", 64)}
	if err := domain.ValidateAlertReplacement(a, ready); err != nil {
		t.Fatal(err)
	}
	if ready.Merge.Blocking() {
		t.Fatal("ready aggregate remained blocked")
	}
	erased := ready.Clone()
	erased.Revision++
	erased.UpdateAt = erased.UpdateAt.Add(time.Second)
	erased.Merge = nil
	if err := domain.ValidateAlertReplacement(ready, erased); err == nil {
		t.Fatal("merge origin erased")
	}
}

func TestMergeReleaseIntentCannotAlterUnrelatedBusinessState(t *testing.T) {
	current := validAlert()
	w := mergeWaitFixture(current)
	current.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{w}}
	released := current.Clone()
	released.Merge, _ = released.Merge.ReleaseWindow(w.WindowID)
	released.Revision++
	released.UpdateAt = released.UpdateAt.Add(time.Second)
	released.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: "release-operation", WindowID: w.WindowID, EffectiveAt: released.UpdateAt, Before: current.Merge.Clone(), After: released.Merge.Clone()}
	if err := domain.ValidateAlertReplacement(current, released); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*domain.Alert){
		func(a *domain.Alert) { a.Severity = "critical" },
		func(a *domain.Alert) { a.LatestEventID = "unrelated-event" },
		func(a *domain.Alert) { a.PolicyTags = []int64{3} },
		func(a *domain.Alert) { a.MergeChange.WindowID = strings.Repeat("f", 64) },
		func(a *domain.Alert) { a.MergeChange.After.Pending = []domain.MergeWait{w} },
		func(a *domain.Alert) {
			at := a.UpdateAt
			a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "system_operation", CauseID: "another"}
		},
		func(a *domain.Alert) {
			a.Status = domain.AlertStatusRecovered
			a.EndAt = &a.UpdateAt
			a.EndType = domain.AlertEndTypeSystem
			a.EndReason = "ended"
		},
	} {
		bad := released.Clone()
		edit(&bad)
		if err := domain.ValidateAlertReplacement(current, bad); err == nil {
			t.Fatal("merge release altered unrelated facts")
		}
	}
	if current.Merge.Pending[0].WindowID != w.WindowID {
		t.Fatal("intent clone changed original wait")
	}
}

func TestOnlyBuiltinAggregateCanUseSystemMergeRecovery(t *testing.T) {
	a := validAlert()
	a.EventSourceID = domain.BuiltinMergeEventSourceID
	a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: strings.Repeat("a", 64)}
	a.Status = domain.AlertStatusRecovered
	a.EndAt = &a.UpdateAt
	a.EndType = domain.AlertEndTypeSystem
	a.EndReason = "merge_members_ended"
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*domain.Alert){
		func(v *domain.Alert) { v.EventSourceID = "source" },
		func(v *domain.Alert) { v.Merge = nil },
		func(v *domain.Alert) { v.Merge = &domain.AlertMerge{Role: "original", State: "none"} },
		func(v *domain.Alert) { v.EndReason = "other" },
		func(v *domain.Alert) { v.EndType = domain.AlertEndTypeUser },
	} {
		invalid := a.Clone()
		change(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("arbitrary recovery accepted")
		}
	}
}
