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
	"testing"
	"time"

	"linkd/internal/domain"
)

func projectionAlert() domain.Alert {
	a := validAlert()
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{
		"kac":   {SourceVersion: 3, RequiredRevision: 1},
		"audit": {SourceVersion: 3, RequiredRevision: 1},
	}}
	return a
}

func TestBusinessRevisionAndProjectionRequirementsAdvanceTogether(t *testing.T) {
	current := projectionAlert()
	next := current.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.Severity = "critical"
	prepared, err := domain.PrepareAlertReplacement(current, next)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Revision != 2 || prepared.Projection.Targets["kac"].RequiredRevision != 2 || prepared.Projection.Targets["audit"].RequiredRevision != 2 || prepared.Projection.Targets["kac"].SyncedRevision != 0 {
		t.Fatal("revision or requirements did not advance together")
	}
	if current.Revision != 1 || next.Revision != 1 || current.Projection.Targets["kac"].RequiredRevision != 1 {
		t.Fatal("preparation mutated input snapshots")
	}
	if err := domain.ValidateAlertReplacement(current, prepared); err != nil {
		t.Fatal(err)
	}
	bad := prepared.Clone()
	bad.Revision = 4
	if _, err := domain.PrepareAlertReplacement(current, bad); err == nil {
		t.Fatal("arbitrary revision accepted")
	}
	missing := prepared.Clone()
	target := missing.Projection.Targets["kac"]
	target.RequiredRevision = 1
	missing.Projection.Targets["kac"] = target
	if err := domain.ValidateAlertReplacement(current, missing); err == nil {
		t.Fatal("business write omitted required watermark")
	}
}

func TestProjectionACKIsPerTargetMonotonicAndDoesNotCreateBusinessRevision(t *testing.T) {
	current := projectionAlert()
	next := current.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	current, err := domain.PrepareAlertReplacement(current, next)
	if err != nil {
		t.Fatal(err)
	}
	at := current.UpdateAt.Add(time.Second)
	ack, changed, err := current.Projection.Acknowledge("kac", 3, 1, at)
	if err != nil || !changed {
		t.Fatal(err)
	}
	next = current.Clone()
	next.Projection = ack
	saved, err := domain.PrepareAlertReplacement(current, next)
	if err != nil || saved.Revision != current.Revision || !saved.UpdateAt.Equal(current.UpdateAt) || saved.Projection.Targets["audit"].SyncedRevision != 0 || !saved.Projection.Pending() {
		t.Fatal("ACK changed business state or another target", err)
	}
	if !domain.SameAlertBusinessSnapshot(saved, current) {
		t.Fatal("ACK invalidated source plan retry")
	}
	same, changed, err := saved.Projection.Acknowledge("kac", 3, 1, at.Add(time.Hour))
	if err != nil || changed || !reflect.DeepEqual(same, saved.Projection) {
		t.Fatal("duplicate ACK moved metadata", err)
	}
	stale, changed, err := saved.Projection.Acknowledge("kac", 2, 2, at)
	if err != nil || changed || !reflect.DeepEqual(stale, saved.Projection) {
		t.Fatal("old target reference acknowledged new target", err)
	}
	if _, _, err := saved.Projection.Acknowledge("kac", 3, 3, at); err == nil {
		t.Fatal("future ACK accepted")
	}
	final, changed, err := saved.Projection.Acknowledge("kac", 3, 2, at.Add(-time.Second))
	if err != nil || !changed || final.Targets["kac"].SyncedAt.Before(at) {
		t.Fatal("clock drift regressed ACK metadata", err)
	}
	if final.Targets["audit"].SyncedRevision != 0 {
		t.Fatal("one target confirmed another")
	}
}

func TestProjectionACKCannotRewriteTerminalBusinessFacts(t *testing.T) {
	current := projectionAlert()
	terminal := current.Clone()
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Second)
	terminal.Status = domain.AlertStatusRecovered
	terminal.EndAt = &terminal.UpdateAt
	terminal.EndType = domain.AlertEndTypeSource
	terminal.EndReason = "resolved"
	terminal, err := domain.PrepareAlertReplacement(current, terminal)
	if err != nil {
		t.Fatal(err)
	}
	ack, _, err := terminal.Projection.Acknowledge("kac", 3, terminal.Revision, terminal.UpdateAt)
	if err != nil {
		t.Fatal(err)
	}
	next := terminal.Clone()
	next.Projection = ack
	if _, err := domain.PrepareAlertReplacement(terminal, next); err != nil {
		t.Fatal("terminal ACK rejected", err)
	}
	for _, edit := range []func(*domain.Alert){
		func(a *domain.Alert) { a.Revision++ },
		func(a *domain.Alert) { a.UpdateAt = a.UpdateAt.Add(time.Second) },
		func(a *domain.Alert) { a.EndReason = "other" },
		func(a *domain.Alert) {
			a.Status = domain.AlertStatusActive
			a.EndAt = nil
			a.EndType = ""
			a.EndReason = ""
		},
		func(a *domain.Alert) {
			v := a.Projection.Targets["kac"]
			v.SourceVersion = 4
			a.Projection.Targets["kac"] = v
		},
	} {
		bad := next.Clone()
		edit(&bad)
		if _, err := domain.PrepareAlertReplacement(terminal, bad); err == nil {
			t.Fatal("ACK rewrote terminal facts")
		}
	}
}

func TestProjectionValidationCloneAndRevisionBounds(t *testing.T) {
	current := projectionAlert()
	if err := current.Validate(); err != nil {
		t.Fatal(err)
	}
	copy := current.Clone()
	v := copy.Projection.Targets["kac"]
	v.SourceVersion = 9
	copy.Projection.Targets["kac"] = v
	if current.Projection.Targets["kac"].SourceVersion != 3 {
		t.Fatal("projection clone shared targets")
	}
	for _, revision := range []int64{0, -1, 1 << 53} {
		bad := current.Clone()
		bad.Revision = revision
		if err := bad.Validate(); err == nil {
			t.Fatal("invalid revision accepted")
		}
	}
	full := current.Clone()
	full.Revision = 1<<53 - 1
	full.Projection = domain.AlertProjection{}
	next := full.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	if _, err := domain.PrepareAlertReplacement(full, next); err == nil {
		t.Fatal("revision overflow accepted")
	}
	malformed := current.Clone()
	v = malformed.Projection.Targets["kac"]
	v.RequiredRevision = 2
	malformed.Projection.Targets["kac"] = v
	if err := malformed.Validate(); err == nil {
		t.Fatal("future required revision accepted")
	}
	missing := current.Clone()
	delete(missing.Projection.Targets, "kac")
	missing.UpdateAt = missing.UpdateAt.Add(time.Second)
	if _, err := domain.PrepareAlertReplacement(current, missing); err == nil {
		t.Fatal("binding silently discarded")
	}
}

func TestProjectionArchiveMetadataMergePreservesIndependentACKs(t *testing.T) {
	a := projectionAlert()
	a.Revision = 3
	var err error
	a.Projection, err = a.Projection.RequireRevision(3)
	if err != nil {
		t.Fatal(err)
	}
	left, _, err := a.Projection.Acknowledge("kac", 3, 2, a.UpdateAt)
	if err != nil {
		t.Fatal(err)
	}
	right, _, err := a.Projection.Acknowledge("audit", 3, 3, a.UpdateAt)
	if err != nil {
		t.Fatal(err)
	}
	merged, changed, err := left.MergeAcknowledgments(right)
	if err != nil || !changed || merged.Targets["kac"].SyncedRevision != 2 || merged.Targets["audit"].SyncedRevision != 3 {
		t.Fatal("archive merge lost an ACK", err)
	}
	if left.Targets["audit"].SyncedRevision != 0 || right.Targets["kac"].SyncedRevision != 0 {
		t.Fatal("merge changed input snapshots")
	}
	changedSource := right.Clone()
	v := changedSource.Targets["kac"]
	v.SourceVersion++
	changedSource.Targets["kac"] = v
	if _, _, err := left.MergeAcknowledgments(changedSource); err == nil {
		t.Fatal("different target bindings merged")
	}
}

func TestProjectionCASRejectsRegressingConfirmationTime(t *testing.T) {
	current := projectionAlert()
	next := current.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	current, err := domain.PrepareAlertReplacement(current, next)
	if err != nil {
		t.Fatal(err)
	}
	at := current.UpdateAt
	current.Projection, _, err = current.Projection.Acknowledge("kac", 3, 1, at)
	if err != nil {
		t.Fatal(err)
	}
	next = current.Clone()
	value := next.Projection.Targets["kac"]
	earlier := at.Add(-time.Second)
	value.SyncedRevision, value.SyncedAt = current.Revision, &earlier
	next.Projection.Targets["kac"] = value
	if _, err := domain.PrepareAlertReplacement(current, next); err == nil {
		t.Fatal("direct CAS regressed confirmed time")
	}
}
