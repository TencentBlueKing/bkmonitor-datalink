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
	"reflect"
	"testing"
	"time"
)

// A replica's declines - the Query Groups whose execution hung there, each
// holding one of its execution slots until it returns - ride that replica's
// own row of the view as it published them, with how many slots it has; a
// replica that declines nothing carries none. Copied, so a later change to
// the snapshot does not reach the row.
func TestAReplicasDeclinesRideItsOwnRow(t *testing.T) {
	snapshots := idleSnapshots()
	since := now.Add(-3 * time.Minute).UTC()
	snapshots[0].Declines = &DeclineFacts{Fanout: 4, Total: 1,
		QueryGroups: []DeclinedQueryGroup{{QueryGroup: "qg-hung", Stage: "query", Since: since}}}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	want := DeclineFacts{Fanout: 4, Total: 1, QueryGroups: []DeclinedQueryGroup{{QueryGroup: "qg-hung", Stage: "query", Since: since}}}
	if got := rows["pod-a"].Declines; got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("pod-a's row declines %+v, want %+v", got, want)
	}
	if got := rows["pod-b"].Declines; got != nil {
		t.Fatalf("pod-b declines nothing, its row says %+v", got)
	}
	snapshots[0].Declines.QueryGroups[0].Stage = "evaluate"
	if got := rows["pod-a"].Declines.QueryGroups[0].Stage; got != "query" {
		t.Fatalf("the row aliases the snapshot: stage now %q", got)
	}
}
