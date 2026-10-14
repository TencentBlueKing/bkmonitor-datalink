// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type snapshotEventFixture struct {
	source       map[string]json.RawMessage
	id           string
	seq          int64
	gets, writes int
}

func newSnapshotEventFixture(t *testing.T) (*Repository, *snapshotEventFixture, domain.Event) {
	t.Helper()
	event := storetest.Event("tenant", "snapshot-event", "fingerprint", "warning")
	event.SourceRawData = domain.JSONObject{"original": json.RawMessage(`{"kept":true}`)}
	body, err := encodeEventDocument(event, store.NewUnprocessedEventProcessing())
	if err != nil {
		t.Fatal(err)
	}
	f := &snapshotEventFixture{id: documentID(event.BKTenantID, event.EventID), seq: 10}
	if err := json.Unmarshal(body, &f.source); err != nil {
		t.Fatal(err)
	}
	repo, err := New(transportFunc(f.perform), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return repo, f, event
}

func (f *snapshotEventFixture) perform(req *http.Request) (*http.Response, error) {
	response := map[string]any{"_index": "linkd-test-events", "_id": f.id, "_seq_no": f.seq, "_primary_term": 7}
	if req.Method == http.MethodGet {
		f.gets++
		response["found"] = true
		response["_source"] = f.source
	} else {
		f.writes++
		if req.URL.Query().Get("if_seq_no") != strconv.FormatInt(f.seq, 10) || req.URL.Query().Get("if_primary_term") != "7" {
			return managerJSONResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
		}
		if req.Method != http.MethodPost || !strings.Contains(req.URL.Path, "/_update/") || req.URL.Query().Get("refresh") != "false" {
			return nil, fmt.Errorf("unexpected snapshot write %s %s", req.Method, req.URL)
		}
		var body struct {
			Doc    map[string]json.RawMessage `json:"doc"`
			Script struct {
				Source string                     `json:"source"`
				Params map[string]json.RawMessage `json:"params"`
			} `json:"script"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		for key, value := range body.Doc {
			f.source[key] = value
		}
		for key, value := range body.Script.Params {
			f.source[key] = value
		}
		f.seq++
		response["_seq_no"] = f.seq
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return managerJSONResponse(http.StatusOK, string(raw)), nil
}

func TestSnapshotEventCommitsReadOnceAndKeepAllCheckpoints(t *testing.T) {
	repo, f, event := newSnapshotEventFixture(t)
	current, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	original := current.Event.Clone()
	current, err = repo.CompareAndSetEventEnrichmentFromSnapshot(t.Context(), current, storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	contextResult := store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: &store.PolicyContext{EvaluatedAt: event.ReceivedAt}}
	current, err = repo.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), current, contextResult)
	if err != nil || current.Processing.PolicyContext == nil || current.Processing.Plan != nil {
		t.Fatalf("context checkpoint: %v", err)
	}
	plan := &store.EventPlan{UpgradePolicy: "update_current", State: domain.EventProcessStateSuppressed, Outcome: "suppressed", Evaluations: []store.EvaluationResult{{Severity: "warning", Action: domain.EventActionTriggered, State: domain.EventProcessStateSuppressed, Outcome: "suppressed"}}}
	current, err = repo.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), current, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
	if err != nil || current.Processing.Plan == nil {
		t.Fatalf("plan checkpoint: %v", err)
	}
	old := current
	current, err = repo.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), current, store.EventResult{State: plan.State, Outcome: plan.Outcome, Evaluations: plan.Evaluations, ProcessedAt: event.ReceivedAt.Add(time.Second)})
	if err != nil || current.Processing.Plan != nil || current.Processing.PolicyContext == nil {
		t.Fatalf("final checkpoint: %v", err)
	}
	if old.Processing.Plan == nil || !reflect.DeepEqual(current.Event.SourceRawData, original.SourceRawData) || f.gets != 1 || f.writes != 4 {
		t.Fatalf("snapshot lost source / mutated old plan / requests: reads=%d writes=%d", f.gets, f.writes)
	}
	// 旧 owner 迟到提交不能把完成结果覆盖回 unprocessed；只有上层显式重读才增加 GET。
	if _, err := repo.CompareAndSetLifecycleEventResultFromSnapshot(t.Context(), old, store.EventResult{State: domain.EventProcessStateUnprocessed}); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("stale owner accepted: %v", err)
	}
	if f.gets != 1 {
		t.Fatal("conflict hid an implicit GET")
	}
	reloaded, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil || reloaded.Processing.State != plan.State || reloaded.Version != current.Version || f.gets != 2 {
		t.Fatalf("conflict reload: %v", err)
	}
}

func TestSnapshotEventRejectsInvalidIdentityTransitionBudgetAndCancellation(t *testing.T) {
	for _, name := range []string{"tenant", "entity", "token", "state", "budget", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			repo, f, event := newSnapshotEventFixture(t)
			current, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			result := storetest.Enrichment(event)
			ctx := t.Context()
			switch name {
			case "tenant":
				current.Event.BKTenantID = "other"
			case "entity":
				current.Event.EventID = "other"
			case "token":
				current.Version = store.NewVersionToken("mysql:10")
			case "state":
				current.Processing.State = domain.EventProcessStateRejected
			case "budget":
				repo.config.MaxDocumentBytes = 256
				current.Event.SourceRawData = domain.JSONObject{"large": json.RawMessage(strconv.Quote(strings.Repeat("x", 1024)))}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before := current.Event.Clone()
			if _, err := repo.CompareAndSetEventEnrichmentFromSnapshot(ctx, current, result); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
			if f.writes != 0 || f.gets != 1 || !reflect.DeepEqual(current.Event, before) {
				t.Fatal("validation performed IO or mutated snapshot")
			}
		})
	}
}

func TestSnapshotAlertCASProtectsConcurrentBusinessRevision(t *testing.T) {
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	token, err := encodeVersion(versionPayload{Index: "linkd-test-alerts", DocumentID: alertDocumentID(a), SeqNo: 4, PrimaryTerm: 2})
	if err != nil {
		t.Fatal(err)
	}
	current := store.StoredAlert{Alert: a, Version: token}
	seq := int64(4)
	requests := 0
	repo, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodPut || req.URL.Query().Get("if_primary_term") != "2" || req.URL.Query().Get("refresh") != "false" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		}
		if req.URL.Query().Get("if_seq_no") != strconv.FormatInt(seq, 10) {
			return managerJSONResponse(http.StatusConflict, `{"error":{"type":"version_conflict_engine_exception"}}`), nil
		}
		seq++
		return managerJSONResponse(http.StatusOK, fmt.Sprintf(`{"_index":"linkd-test-alerts","_id":%q,"_seq_no":%d,"_primary_term":2}`, alertDocumentID(a), seq)), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	next := a.Clone()
	next.LatestEventID = "next"
	next.LastOccurredAt = next.LastOccurredAt.Add(time.Second)
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	saved, err := repo.CompareAndSetAlertFromSnapshot(t.Context(), current, next)
	if err != nil || saved.Alert.Revision != 2 || current.Alert.Revision != 1 {
		t.Fatalf("business CAS: %v", err)
	}
	concurrent := saved.Alert.Clone()
	concurrent.UpdateAt = concurrent.UpdateAt.Add(time.Second)
	confirmed, err := repo.CompareAndSetAlertFromSnapshot(t.Context(), saved, concurrent)
	if err != nil {
		t.Fatal(err)
	}
	late := saved.Alert.Clone()
	late.LatestEventID = "late"
	late.LastOccurredAt = late.LastOccurredAt.Add(time.Second)
	late.UpdateAt = late.UpdateAt.Add(time.Second)
	if _, err := repo.CompareAndSetAlertFromSnapshot(t.Context(), saved, late); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("stale write ignored ACK: %v", err)
	}
	other := saved
	other.Alert.BKTenantID = "other"
	if _, err := repo.CompareAndSetAlertFromSnapshot(t.Context(), other, next); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("foreign token accepted: %v", err)
	}
	invalid := confirmed.Alert.Clone()
	invalid.TriggerEventID = "rewritten"
	if _, err := repo.CompareAndSetAlertFromSnapshot(t.Context(), confirmed, invalid); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("immutable creation fact accepted: %v", err)
	}
	if requests != 3 {
		t.Fatalf("unexpected reads or rejected writes: %d", requests)
	}
}
