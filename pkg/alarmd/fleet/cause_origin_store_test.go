// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A snapshot a replica on a build before cause_origin published, read through
// the store's own loop: its row with a reason comes back with origin
// unknown, a filed cause word with origin derived, and its row without a
// reason with none - and the summary counts the first under unknown.
func TestASnapshotFromAnOlderBuildLoadsWithItsOriginsSettled(t *testing.T) {
	store, client, _, _, _ := pageRig(t)
	key := store.snapshotKey("pod-b")
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(client.values[key]), &snapshot); err != nil {
		t.Fatal(err)
	}
	// The rows as that build wrote them: no cause_origin key anywhere.
	var rows []any
	for _, raw := range []string{
		`{"query_group":"qg-319","kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","cause":"LEVEL_OUTCOME_UNKNOWN","cause_reason":"QUERY_TIMEOUT"}`,
		`{"query_group":"qg-320","kind":"DEGRADED_RUN","reason_code":"COMPLETED_WITH_UNAVAILABLE","cause":"GAP_GUARD_WARMING","cause_reason":"GAP_GUARD_WARMING"}`,
		`{"query_group":"qg-321","kind":"STALLED"}`,
	} {
		var row any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	snapshot["anomalies"], snapshot["total_anomalies"] = rows, len(rows)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "cause_origin") {
		t.Fatalf("fixture names cause_origin: %s", encoded)
	}
	client.values[key] = string(encoded)

	loaded, err := store.loadUnadmitted(context.Background(), []string{"pod-a", "pod-b"})
	if err != nil {
		t.Fatal(err)
	}
	byGroup := map[string]Anomaly{}
	var all []Anomaly
	for _, item := range loaded {
		for _, row := range item.Anomalies {
			byGroup[row.QueryGroup] = row
			all = append(all, row)
		}
	}
	if row := byGroup["qg-319"]; row.CauseOrigin == nil || row.CauseOrigin.Origin != CauseOriginUnknown {
		t.Fatalf("older row with a reason: origin %+v, want unknown", row.CauseOrigin)
	}
	if row := byGroup["qg-320"]; row.CauseOrigin == nil || row.CauseOrigin.Origin != CauseOriginDerived {
		t.Fatalf("older row filed under its cause: origin %+v, want derived", row.CauseOrigin)
	}
	if row, found := byGroup["qg-321"]; !found || row.CauseOrigin != nil {
		t.Fatalf("older row without a reason: found %t origin %+v, want no origin", found, row.CauseOrigin)
	}
	unknown := 0
	for _, entry := range summarize(all, now).ByCauseReasonUnknown.Top {
		unknown += entry.Count
	}
	if unknown != 1 {
		t.Fatalf("by_cause_reason_unknown = %d, want the one row that never learned its origin", unknown)
	}
}

// A restored row whose reason is a cause word a round is filed under is
// derived on every build: not unknown.
func TestARestoredFiledCauseIsDerived(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	if !tracker.Restore("qg-322", RestoredState{LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: at.at,
		LastRound: &RestoredRound{Slot: at.at, CompletedAt: at.at, Kind: "COMPLETED_WITH_UNAVAILABLE",
			ReasonCode: "GAP_GUARD_WARMING"}}, at.at, 0) {
		t.Fatal("not restored")
	}
	row, found := tracker.TrackedRow("qg-322")
	if !found || row.CauseOrigin == nil || row.CauseOrigin.Origin != CauseOriginDerived {
		t.Fatalf("restored filed cause: found %t origin %+v, want derived", found, row.CauseOrigin)
	}
}
