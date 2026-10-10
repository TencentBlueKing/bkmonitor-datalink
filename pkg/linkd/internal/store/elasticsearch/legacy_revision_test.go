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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestDecodeLegacyAlertRevision(t *testing.T) {
	for _, status := range []domain.AlertStatus{domain.AlertStatusActive, domain.AlertStatusRecovered, domain.AlertStatusClosed} {
		t.Run(string(status), func(t *testing.T) {
			a := storetest.Alert("tenant-legacy", "alert-legacy", "event-legacy", "fp", "warning")
			a.Status = status
			if status.Terminal() {
				at := a.UpdateAt.Add(time.Minute)
				a.UpdateAt = at
				a.EndAt = &at
				a.EndType = domain.AlertEndTypeSource
			}
			for _, tc := range []struct {
				name     string
				revision json.RawMessage
				modify   func(map[string]json.RawMessage)
				valid    bool
			}{
				{name: "missing", valid: true},
				{name: "current", revision: json.RawMessage(`1`), valid: true},
				{name: "zero", revision: json.RawMessage(`0`)},
				{name: "null", revision: json.RawMessage(`null`)},
				{name: "negative", revision: json.RawMessage(`-1`)},
				{name: "overflow", revision: json.RawMessage(`9007199254740992`)},
				{name: "projection_without_revision", modify: func(f map[string]json.RawMessage) {
					f["projection"] = json.RawMessage(`{"targets":{"kac":{"source_version":1,"required_revision":1}}}`)
				}},
				{name: "action_without_revision", modify: func(f map[string]json.RawMessage) {
					f["action_pending"] = json.RawMessage(`{"revision":1}`)
					f["action_work"] = json.RawMessage(`true`)
				}},
				{name: "projection_work_without_revision", modify: func(f map[string]json.RawMessage) { f["projection_work"] = json.RawMessage(`true`) }},
				{name: "merge_work_without_revision", modify: func(f map[string]json.RawMessage) { f["merge_work"] = json.RawMessage(`true`) }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw, err := encodeAlertDocument(a)
					if err != nil {
						t.Fatal(err)
					}
					var fields map[string]json.RawMessage
					if err = json.Unmarshal(raw, &fields); err != nil {
						t.Fatal(err)
					}
					delete(fields, "revision")
					if tc.revision != nil {
						fields["revision"] = tc.revision
					}
					if tc.modify != nil {
						tc.modify(fields)
					}
					raw, err = json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					hit := searchHit{Index: "linkd-test-alerts", ID: alertDocumentID(a), SeqNo: 7, PrimaryTerm: 2, Source: raw}
					decoded, err := decodeAlertHit(hit)
					if !tc.valid {
						if err == nil {
							t.Fatal("invalid version accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(decoded.Alert, a) {
						t.Fatalf("legacy snapshot changed: %#v", decoded.Alert)
					}
					version, ok := decodeVersion(decoded.Version)
					if !ok || version.SeqNo != 7 || version.PrimaryTerm != 2 {
						t.Fatal("CAS token changed")
					}
					encoded, err := encodeAlertDocument(decoded.Alert)
					if err != nil {
						t.Fatal(err)
					}
					var stored map[string]json.RawMessage
					if err = json.Unmarshal(encoded, &stored); err != nil || string(stored["revision"]) != "1" {
						t.Fatalf("version not persisted: %v", err)
					}
					// 领域规则仍严格，下一次正常业务变更必须推进版本。
					if status == domain.AlertStatusActive {
						next := decoded.Alert.Clone()
						next.Revision++
						next.UpdateAt = next.UpdateAt.Add(time.Second)
						if err = domain.ValidateAlertReplacement(decoded.Alert, next); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func legacyAlertHit(t *testing.T, a domain.Alert) searchHit {
	t.Helper()
	raw, err := encodeAlertDocument(a)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "revision")
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return searchHit{Index: "linkd-test-alerts-active-000001", ID: alertDocumentID(a), SeqNo: 7, PrimaryTerm: 2, Source: raw}
}

func TestLegacyAlertNormalCASPersistsRevisionAndRejectsConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "conflict"}[conflict], func(t *testing.T) {
			a := storetest.Alert("tenant-legacy", "alert-legacy", "event-legacy", "fp", "warning")
			hit := legacyAlertHit(t, a)
			current, err := decodeAlertHit(hit)
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			transport := transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/"+hit.Index+"/_doc/"+hit.ID {
					t.Fatalf("unexpected path: %s", req.URL.Path)
				}
				if req.Method == http.MethodGet {
					raw, _ := json.Marshal(map[string]any{"_index": hit.Index, "_id": hit.ID, "_seq_no": 7, "_primary_term": 2, "found": true, "_source": hit.Source})
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				}
				writes++
				if req.Method != http.MethodPut || req.URL.Query().Get("if_seq_no") != "7" || req.URL.Query().Get("if_primary_term") != "2" {
					t.Fatal("CAS condition missing")
				}
				var written domain.Alert
				if err := json.NewDecoder(req.Body).Decode(&written); err != nil {
					t.Fatal(err)
				}
				if written.Revision != 2 || written.Status != domain.AlertStatusRecovered {
					t.Fatal("legacy business update did not advance revision")
				}
				status := 200
				if conflict {
					status = 409
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"_index":"` + hit.Index + `","_id":"` + hit.ID + `","_seq_no":8,"_primary_term":2}`))}, nil
			})
			router, err := NewStaticRouter("linkd-test")
			if err != nil {
				t.Fatal(err)
			}
			repo, err := New(transport, router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			next := current.Alert.Clone()
			next.Status = domain.AlertStatusRecovered
			end := next.UpdateAt.Add(time.Minute)
			next.EndAt = &end
			next.UpdateAt = end
			next.EndType = domain.AlertEndTypeSource
			result, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, current.Version, next)
			if writes != 1 {
				t.Fatal("unexpected write count")
			}
			if conflict {
				if !errors.Is(err, store.ErrVersionConflict) {
					t.Fatalf("conflict lost: %v", err)
				}
				return
			}
			if err != nil || result.Alert.Revision != 2 {
				t.Fatalf("CAS: %v revision=%d", err, result.Alert.Revision)
			}
		})
	}
}

func TestLegacyTerminalAlertArchivesWithRevision(t *testing.T) {
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	original := archiveStoredAlert(t, "legacy-terminal", now)
	hit := legacyAlertHit(t, original.Alert)
	legacy, err := decodeAlertHit(hit)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost || req.URL.Path != "/_bulk" {
			t.Fatal("unexpected archive request")
		}
		if calls == 1 {
			if !strings.Contains(string(body), `"revision":1`) {
				t.Fatal("history lacks baseline revision")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[{"create":{"_index":"linkd-test-alert-history-20260831","_id":"` + hit.ID + `","_seq_no":0,"_primary_term":1,"status":201}}]}`))}, nil
		}
		if calls != 2 || !strings.Contains(string(body), `"if_seq_no":7`) || !strings.Contains(string(body), `"if_primary_term":2`) {
			t.Fatal("archive delete lacks original CAS")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[{"delete":{"_index":"` + hit.Index + `","_id":"` + hit.ID + `","status":200}}]}`))}, nil
	})
	router, err := newBucketRouter("linkd-test", BucketConfig{EventBucketDays: 7, AlertHistoryBucketDays: 7, AlertLogBucketDays: 7, MaxFutureSkew: time.Minute, ActiveAlertRefreshInterval: 5 * time.Second}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repo, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results := repo.archiveTerminalAlertsBulk(t.Context(), []store.StoredAlert{legacy})
	if len(results) != 1 || !results[0].archived || results[0].err != nil || calls != 2 {
		t.Fatalf("archive: %#v calls=%d", results, calls)
	}
}
