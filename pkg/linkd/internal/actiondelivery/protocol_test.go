// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store/storetest"
)

func actionAlert(tenant string, revision int64) domain.Alert {
	a := storetest.Alert(tenant, "alert", "opening", "fp", "warning")
	a.Revision = revision
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 4, RequiredRevision: revision}}}
	return a
}

func actionTask(t *testing.T, tenant string, revision int64) Task {
	t.Helper()
	a := actionAlert(tenant, revision)
	q, e := NewTask(a, "kac", Cause{Type: "source_event", ID: a.LatestEventID}, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	return q
}

func confirmed(q Request) Receipt {
	return Receipt{SchemaVersion: SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, ActionID: q.ActionID, RequestHash: q.Hash(), Outcome: "queued", TaskID: "celery-" + q.ActionID}
}

func visible(q Request) projection.Receipt {
	status := domain.AlertStatusActive
	if q.Action == "resolved" {
		status = domain.AlertStatusRecovered
	}
	if q.Action == "close" {
		status = domain.AlertStatusClosed
	}
	return projection.Receipt{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, AppliedRevision: q.Revision, ContentHash: q.ContentHash, AppliedStatus: status, SearchVisible: true, DocumentRef: "owned-index/document"}
}

func TestActionRequestPreservesFrozenAdmissionAndStableIdentity(t *testing.T) {
	a := actionAlert("tenant", 1)
	cause := Cause{Type: "source_event", ID: a.LatestEventID}
	a.ExtraData = domain.JSONObject{"complex": json.RawMessage(`{"n":9007199254740993,"ok":false,"zero":0}`)}
	first, e := BuildRequest(a, "kac", cause)
	if e != nil {
		t.Fatal(e)
	}
	acked := a.Clone()
	at := a.UpdateAt
	acked.Projection.Targets["kac"] = domain.ProjectionTargetState{SourceVersion: 4, RequiredRevision: 1, SyncedRevision: 1, SyncedAt: &at}
	again, e := BuildRequest(acked, "kac", cause)
	if e != nil || first.Hash() != again.Hash() || first.ActionID != again.ActionID {
		t.Fatal("projection ACK changed action", e)
	}
	changed := a.Clone()
	changed.Title = "later"
	q, e := BuildRequest(changed, "kac", cause)
	if e != nil || q.ActionID != first.ActionID || q.Hash() == first.Hash() {
		t.Fatal("identity/hash boundary", e)
	}
	for name, change := range map[string]func(*domain.Alert){"never admitted": func(a *domain.Alert) { a.Admission = domain.AlertAdmission{} }, "wrong cause": func(a *domain.Alert) { a.Admission.CauseID = "different" }, "old severity": func(a *domain.Alert) { a.Admission.Severity = "critical" }, "old admission time": func(a *domain.Alert) { a.UpdateAt = a.UpdateAt.Add(time.Second) }} {
		t.Run(name, func(t *testing.T) {
			b := a.Clone()
			change(&b)
			if _, e := BuildRequest(b, "kac", cause); e == nil {
				t.Fatal("unqualified firing accepted")
			}
		})
	}
	task := actionTask(t, "tenant", 1)
	other := actionTask(t, "another", 1)
	if task.ID == other.ID || task.Request.ActionID == other.Request.ActionID {
		t.Fatal("tenant identity collision")
	}
	noncanonical := first.Clone()
	noncanonical.Alert = append([]byte(" "), noncanonical.Alert...)
	if noncanonical.Validate() != nil {
		t.Fatal("JSON formatting rejected during task validation")
	}
	r := confirmed(first)
	if r.ValidateFor(first) != nil {
		t.Fatal("valid receipt rejected")
	}
	r.TaskID = "another-celery-task"
	if r.ValidateFor(first) != nil {
		t.Fatal("retry task reference rejected")
	}
	r.TaskID = ""
	if r.ValidateFor(first) == nil {
		t.Fatal("missing task reference accepted")
	}
	r = confirmed(first)
	r.RequestHash = q.Hash()
	if r.ValidateFor(first) == nil {
		t.Fatal("different payload receipt accepted")
	}
}

func TestStoredActionReceiptChecksIdentityAndNewReceiptChecksDigest(t *testing.T) {
	task := actionTask(t, "tenant", 1)
	task.Progress.State = "succeeded"
	task.Progress.Attempts, task.Progress.TotalAttempts = 1, 1
	task.Progress.DueAt = nil
	proof, receipt := visible(task.Request), confirmed(task.Request)
	task.Progress.Projection, task.Progress.Receipt = &proof, &receipt
	receipt.RequestHash = strings.Repeat("0", 64)
	if err := task.Validate(); err != nil {
		t.Fatal("stored receipt recomputed request digest", err)
	}
	if receipt.ValidateFor(task.Request) == nil {
		t.Fatal("new receipt accepted a different request digest")
	}
	sending := task.Clone()
	sending.Progress.State = "sending"
	sending.Progress.Receipt = nil
	until := sending.Progress.UpdatedAt.Add(30 * time.Second)
	sending.Progress.LeaseUntil = &until
	if ValidateWrite(&sending, task) == nil {
		t.Fatal("new receipt persisted without checking request digest")
	}
	receipt.RequestHash = task.Request.Hash()
	if err := ValidateWrite(&sending, task); err != nil {
		t.Fatal("valid new receipt rejected", err)
	}
	receipt.TenantID = "other"
	if task.Validate() == nil {
		t.Fatal("cross-tenant stored receipt accepted")
	}
}

func TestActionRequestValidatesDynamicPayloadOnlyAtConstruction(t *testing.T) {
	for _, name := range []string{"invalid JSON", "invalid enrich result"} {
		t.Run(name, func(t *testing.T) {
			a := actionAlert("tenant", 1)
			if name == "invalid JSON" {
				a.ExtraData = domain.JSONObject{"value": json.RawMessage(`{`)}
			} else {
				a.Enrich = domain.JSONObject{"processors": json.RawMessage(`"not an array"`)}
			}
			if _, err := BuildRequest(a, "kac", Cause{Type: "source_event", ID: a.LatestEventID}); err == nil {
				t.Fatal("invalid new payload accepted")
			}
		})
	}

	task := actionTask(t, "tenant", 1)
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(task.Request.Alert, &snapshot); err != nil {
		t.Fatal(err)
	}
	// 已保存的动态结果仅作为 JSON 携带，动作读取不再次解释处理器结果结构。
	snapshot["enrich"] = json.RawMessage(`{"processors":"opaque stored result"}`)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	task.Request.Alert = raw
	if err := task.Validate(); err != nil {
		t.Fatal("stored dynamic payload revalidated", err)
	}
	snapshot["admission"] = json.RawMessage(`{}`)
	raw, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	task.Request.Alert = raw
	if task.Validate() == nil {
		t.Fatal("missing action admission accepted")
	}
}
