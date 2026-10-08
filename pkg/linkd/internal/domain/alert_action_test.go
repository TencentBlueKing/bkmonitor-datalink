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
	"testing"
	"time"

	"linkd/internal/domain"
)

func actionIntentAlert(t *testing.T) domain.Alert {
	t.Helper()
	a := projectionAlert()
	v := a.Projection.Targets["kac"]
	v.ActionEnabled = true
	a.Projection.Targets["kac"] = v
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	var err error
	a.ActionPending, err = domain.NewAlertActionIntent(a, "source_event", a.LatestEventID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestActionIntentAtomicTransitionAndMetadataBoundary(t *testing.T) {
	a := actionIntentAlert(t)
	if err := domain.ValidateAlertCreation(a); err != nil {
		t.Fatal(err)
	}
	missing := a.Clone()
	missing.ActionPending = nil
	if err := domain.ValidateAlertCreation(missing); err == nil {
		t.Fatal("created admitted action without durable intent")
	}
	if len(a.ActionPending.Targets) != 1 || a.ActionPending.Targets["kac"] != 3 {
		t.Fatal("pure projection became action")
	}
	next := a.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.ActionPending = nil
	if _, err := domain.PrepareAlertReplacement(a, next); err == nil {
		t.Fatal("business mutation bypassed unrecorded action")
	}
	next = a.Clone()
	next.ActionPending.CauseID = "other"
	if _, err := domain.PrepareAlertReplacement(a, next); err == nil {
		t.Fatal("rewrote action cause")
	}
	next = a.Clone()
	next.ActionPending = nil
	done, err := domain.PrepareAlertReplacement(a, next)
	if err != nil || done.Revision != a.Revision || !done.UpdateAt.Equal(a.UpdateAt) {
		t.Fatal("intent completion changed business version", err)
	}
	ack := a.Clone()
	ack.Projection, _, err = ack.Projection.Acknowledge("kac", 3, 1, a.UpdateAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = domain.PrepareAlertReplacement(a, ack); err != nil {
		t.Fatal("pending action blocked projection ACK", err)
	}
	changed := a.Clone()
	v := changed.Projection.Targets["kac"]
	v.ActionEnabled = false
	changed.Projection.Targets["kac"] = v
	changed.ActionPending = nil
	if _, err = domain.PrepareAlertReplacement(a, changed); err == nil {
		t.Fatal("ACK path changed action binding")
	}
	terminal := done.Clone()
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Second)
	terminal.Status = domain.AlertStatusClosed
	terminal.EndAt = &terminal.UpdateAt
	terminal.EndType = domain.AlertEndTypeUser
	terminal.EndReason = "manual"
	if _, err = domain.PrepareAlertReplacement(done, terminal); err == nil {
		t.Fatal("terminal action lost")
	}
	terminal.Revision++
	terminal.Projection, err = terminal.Projection.RequireRevision(terminal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	terminal.ActionPending, err = domain.NewAlertActionIntent(terminal, "user_operation", "close-once")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = domain.PrepareAlertReplacement(done, terminal); err != nil {
		t.Fatal(err)
	}
	clone := terminal.Clone()
	clone.ActionPending.Targets["kac"] = 9
	if terminal.ActionPending.Targets["kac"] != 3 {
		t.Fatal("intent clone shares map")
	}
}

func TestActionIntentRejectsInvalidEligibilityAndTargets(t *testing.T) {
	a := actionIntentAlert(t)
	for name, change := range map[string]func(*domain.Alert){
		"old admission":        func(v *domain.Alert) { v.UpdateAt = v.UpdateAt.Add(time.Second) },
		"wrong action":         func(v *domain.Alert) { v.ActionPending.Action = "close" },
		"wrong version":        func(v *domain.Alert) { v.ActionPending.Revision++ },
		"wrong target release": func(v *domain.Alert) { v.ActionPending.Targets["kac"]++ },
		"omitted target":       func(v *domain.Alert) { delete(v.ActionPending.Targets, "kac") },
		"unknown cause":        func(v *domain.Alert) { v.ActionPending.CauseType = "unknown" },
		"wrong event":          func(v *domain.Alert) { v.LatestEventID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			v := a.Clone()
			change(&v)
			if v.Validate() == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
}
