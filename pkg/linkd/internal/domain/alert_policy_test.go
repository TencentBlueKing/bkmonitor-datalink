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
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestAlertPolicyStatesAreIndependentAndBindingsImmutable(t *testing.T) {
	a := validAlert()
	next := a.UpdateAt.Add(time.Minute)
	binding := domain.ShieldBinding{BindingID: strings.Repeat("a", 64), ActivationID: strings.Repeat("b", 64), Policy: domain.PolicyVersion{ID: "policy", Version: 1, Digest: strings.Repeat("c", 64)}, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: a.UpdateAt}
	a.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{binding}, NextCheckAt: &next}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	changed := a.Clone()
	changed.Revision++
	changed.UpdateAt = changed.UpdateAt.Add(time.Second)
	changed.Shield.Bindings[0].Policy.Version = 2
	if err := domain.ValidateAlertReplacement(a, changed); err == nil {
		t.Fatal("binding version changed under same ID")
	}
	changed = a.Clone()
	changed.Revision++
	changed.UpdateAt = changed.UpdateAt.Add(time.Second)
	changed.Shield = domain.AlertShield{}
	if err := domain.ValidateAlertReplacement(a, changed); err != nil {
		t.Fatal(err)
	}
	if changed.Admission.AdmittedAt != nil || changed.Status != domain.AlertStatusActive {
		t.Fatal("unshield changed lifecycle/admission")
	}
	admitted := changed.Clone()
	at := admitted.UpdateAt.Add(time.Second)
	admitted.Revision++
	admitted.UpdateAt = at
	admitted.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: admitted.Severity, CauseType: "source_event", CauseID: "later-event"}
	if err := domain.ValidateAlertReplacement(changed, admitted); err != nil {
		t.Fatal(err)
	}
	lost := admitted.Clone()
	lost.Revision++
	lost.UpdateAt = lost.UpdateAt.Add(time.Second)
	lost.Admission = domain.AlertAdmission{}
	if err := domain.ValidateAlertReplacement(admitted, lost); err == nil {
		t.Fatal("forgot admitted lifecycle")
	}
	copy := a.Clone()
	copy.Shield.Bindings[0].Reason = "changed"
	*copy.Shield.NextCheckAt = copy.Shield.NextCheckAt.Add(time.Hour)
	if reflect.DeepEqual(copy.Shield, a.Shield) || a.Shield.Bindings[0].Reason != "" || !a.Shield.NextCheckAt.Equal(next) {
		t.Fatal("policy clone shares state")
	}
	ended := a.Clone()
	ended.Status = domain.AlertStatusRecovered
	ended.EndType = domain.AlertEndTypeSource
	end := a.UpdateAt
	ended.EndAt = &end
	if err := ended.Validate(); err == nil {
		t.Fatal("terminal shield remained active")
	}
}
