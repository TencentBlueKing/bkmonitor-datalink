// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func seedLegacyIdentity(t *testing.T, client *Client, alert domain.Alert) string {
	t.Helper()
	id := "linkd-677f0584-6f1c-5d50-969e-4bcf8c2deff3"
	if alert.AlertID == "parent" {
		id = "linkd-0148a4cd-4d45-5fe0-8959-f50c22d361bc"
	}
	fields, _, err := client.fields(t.Context(), alert, true)
	if err != nil {
		t.Fatal(err)
	}
	fields["alarm_id"], fields["event_id"] = id, "linkd-"+alert.AlertID
	fields["status"], fields["conductor"] = "executing", []string{"existing-user"}
	fields["storage_time"] = "2026-10-21 12:00:00"
	if err := client.request(t.Context(), http.MethodPut, "/"+client.config.AlarmEventIndex+"/_doc/"+id, url.Values{"refresh": {"true"}}, fields, nil); err != nil {
		t.Fatal(err)
	}
	return id
}

func readIdentityDocument(t *testing.T, client *Client, index, id string) map[string]any {
	t.Helper()
	var result struct {
		Source map[string]any `json:"_source"`
	}
	if err := client.request(t.Context(), http.MethodGet, "/"+index+"/_doc/"+id, nil, nil, &result); err != nil {
		t.Fatal(err)
	}
	return result.Source
}

func TestElasticsearchNativeIdentityLeavesLegacyUntouchedAcrossRestartAndRollover(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			client := integrationClient(t)
			alert := storetest.Alert("tenant-a", "alert-20261022081224-cdefghijklmab", "event-20261022081223-efghijklmabcd", "fp-a", "warning")
			id, eventID := alert.AlertID, alert.TriggerEventID
			legacyID := ""
			var legacyBefore map[string]any
			if legacy {
				legacyID = seedLegacyIdentity(t, client, alert)
				legacyBefore = readIdentityDocument(t, client, client.config.AlarmEventIndex, legacyID)
			}
			out := client.WriteBatch(t.Context(), []domain.Alert{alert}, true)
			if out[0].Err != nil {
				t.Fatal(out[0].Err)
			}
			index := out[0].Index
			if err := client.request(t.Context(), http.MethodPost, "/"+client.config.AlarmEventIndex+"/_rollover", nil, map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
			// 新进程没有位置缓存，仍需定位原生文档的原物理索引。
			restarted, err := Open(client.config, client.level, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			for _, state := range []domain.AlertStatus{domain.AlertStatusActive, domain.AlertStatusRecovered, domain.AlertStatusClosed} {
				alert.LatestEventID = "event-20261022081324-fghijklmabcde"
				alert.UpdateAt = alert.UpdateAt.Add(time.Minute)
				alert.Status = state
				if state.Terminal() {
					alert.EndAt = &alert.UpdateAt
					alert.EndType = domain.AlertEndTypeSource
				}
				for range 2 {
					out = restarted.WriteBatch(t.Context(), []domain.Alert{alert}, true)
					if out[0].Err != nil || out[0].Index != index {
						t.Fatal("document moved or failed", out)
					}
				}
				doc := readIdentityDocument(t, restarted, index, id)
				if doc["alarm_id"] != id || doc["event_id"] != eventID || doc["fingerprint"] != "fp-a" {
					t.Fatal("identity changed", doc)
				}
				if legacy {
					after := readIdentityDocument(t, restarted, index, legacyID)
					if !reflect.DeepEqual(after, legacyBefore) {
						t.Fatal("legacy document changed", after)
					}
				}
			}
			var count struct {
				Count int `json:"count"`
			}
			wantCount := 1
			if legacy {
				wantCount = 2
			}
			if err := client.request(t.Context(), http.MethodGet, "/"+client.config.AlarmEventIndex+"/_count", nil, nil, &count); err != nil || count.Count != wantCount {
				t.Fatal("duplicate KAC document", count.Count, err)
			}
		})
	}
}

func TestElasticsearchParentChildReferencesIgnoreLegacyDocuments(t *testing.T) {
	for _, oldParent := range []bool{false, true} {
		for _, oldChild := range []bool{false, true} {
			t.Run(fmt.Sprintf("parent=%t child=%t", oldParent, oldChild), func(t *testing.T) {
				client := integrationClient(t)
				parent := storetest.Alert("tenant-a", "parent", "parent-event", "parent-fp", "warning")
				child := storetest.Alert("tenant-a", "child", "child-event", "child-fp", "warning")
				parentID, childID := parent.AlertID, child.AlertID
				if oldParent {
					seedLegacyIdentity(t, client, parent)
				}
				if oldChild {
					seedLegacyIdentity(t, client, child)
				}
				client.relations = relationFunc(func(_ context.Context, tenant, id string) (domain.MergeRelation, error) {
					return domain.MergeRelation{TenantID: tenant, ID: id, ParentAlertID: parent.AlertID, Members: []domain.MergeRelationMember{{AlertID: child.AlertID}}}, nil
				})
				child.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{strings.Repeat("a", 64)}}
				// 新父尚未投影时，引用其原生身份；父落库后保持同一个引用。
				for range 2 {
					out := client.WriteBatch(t.Context(), []domain.Alert{child}, true)
					if out[0].Err != nil {
						t.Fatal(out[0].Err)
					}
					doc := readIdentityDocument(t, client, out[0].Index, childID)
					refs, _ := json.Marshal(doc["associate_alarm_id"])
					want, _ := json.Marshal([]string{parentID})
					if string(refs) != string(want) {
						t.Fatal("wrong parent reference", string(refs), string(want))
					}
					if out = client.WriteBatch(t.Context(), []domain.Alert{parent}, true); out[0].Err != nil {
						t.Fatal(out[0].Err)
					}
				}
			})
		}
	}
}
