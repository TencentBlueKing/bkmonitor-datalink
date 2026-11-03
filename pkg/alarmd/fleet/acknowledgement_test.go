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
	"testing"
	"time"
)

// The view partitions the replicas it counted by the Activation they report
// executing by, against the one the control plane published. Both are
// persisted record revisions, so lag is a difference of versions and never of
// times. A replica that reported none, or a published version that could not
// be read, is unknown on its own: never lagging, never acked.
func TestAggregateCountsReplicasByTheActivationTheyApplied(t *testing.T) {
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, AppliedActivationRecordRevision: 7},
		{Replica: "pod-b", TakenAt: now, AppliedActivationRecordRevision: 5},
		{Replica: "pod-c", TakenAt: now},
	}
	all := []string{"pod-a", "pod-b", "pod-c"}
	view := Aggregate(Expectation{Known: true, ActivationRecordRevision: 7}, snapshots, all, now, freshness)
	if view.PublishedVersion != 7 || view.Workers != (WorkerAcknowledgement{Ready: 3, Acked: 1, Lagging: 1, Unknown: 1}) {
		t.Fatalf("published=%d workers=%+v, want 7 and 1 acked, 1 lagging, 1 unknown of 3", view.PublishedVersion, view.Workers)
	}
	acked := map[string]*uint64{}
	for _, replica := range view.PerReplica {
		acked[replica.Replica] = replica.AckedVersion
	}
	if acked["pod-a"] == nil || *acked["pod-a"] != 7 || acked["pod-b"] == nil || *acked["pod-b"] != 5 || acked["pod-c"] != nil {
		t.Fatalf("per-replica acked versions = %v, want a=7 b=5 c=absent", acked)
	}

	// The published version could not be read: nobody is behind, and nobody
	// is ahead either.
	unread := Aggregate(Expectation{Known: true}, snapshots, all, now, freshness)
	if unread.PublishedVersion != 0 || unread.Workers != (WorkerAcknowledgement{Ready: 3, Unknown: 3}) {
		t.Fatalf("with no published version: published=%d workers=%+v, want all unknown", unread.PublishedVersion, unread.Workers)
	}

	// A replica past the published version straddled an activation; it is
	// not behind.
	ahead := Aggregate(Expectation{Known: true, ActivationRecordRevision: 6}, snapshots, all, now, freshness)
	if ahead.Workers != (WorkerAcknowledgement{Ready: 3, Acked: 1, Lagging: 1, Unknown: 1}) {
		t.Fatalf("with the published version behind pod-a: workers=%+v", ahead.Workers)
	}

	// A replica whose snapshot is stale is not counted, so it is in none of
	// the three; Ready says how many the partition covers.
	stale := append([]Snapshot(nil), snapshots...)
	stale[1].TakenAt = now.Add(-2 * freshness)
	partial := Aggregate(Expectation{Known: true, ActivationRecordRevision: 7}, stale, all, now, freshness)
	if partial.Workers != (WorkerAcknowledgement{Ready: 2, Acked: 1, Unknown: 1}) {
		t.Fatalf("with one stale replica: workers=%+v, want it left out of the partition", partial.Workers)
	}
	_ = time.Second
}
