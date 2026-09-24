// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ownership

import (
	"context"
	"testing"
	"time"
)

// The record's word on the Query Group's timeline revision travels with the
// renewal, the way its content scope does; a decision that does not name it
// leaves it; one that does replaces it; and none of that moves the record's
// own revision, which is the decision's, not the timeline's.
func TestTheTimelineRevisionTravelsWithTheRenewalAndOutlivesDecisions(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placed, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", PlacementReason: PlacementRendezvous, DecidedAt: now,
		ContentScope: "view-a", TimelineRecordRevision: 7,
	})
	if err != nil || placed.TimelineRecordRevision != 7 {
		t.Fatalf("PublishAssignment() = (%+v, %v), want the timeline revision on the reply", placed, err)
	}
	read, err := store.ReadAssignment(ctx, "query-group-1")
	if err != nil || read.TimelineRecordRevision != 7 {
		t.Fatalf("ReadAssignment() = (%+v, %v), want timeline revision 7", read, err)
	}
	lease, err := store.Acquire(ctx, "query-group-1", "worker-1", now, time.Minute)
	if err != nil || lease.TimelineRecordRevision != 7 {
		t.Fatalf("Acquire() = (%+v, %v), want timeline revision 7 from the first lease, not only from a renewal", lease, err)
	}
	renewed, err := store.Renew(ctx, lease.Fence, now.Add(time.Second), time.Minute)
	if err != nil || renewed.TimelineRecordRevision != 7 || renewed.ContentScope != "view-a" {
		t.Fatalf("Renew() = (%+v, %v), want timeline revision 7 beside content scope view-a", renewed, err)
	}

	// A decision that says nothing about the timeline leaves the number.
	same, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: placed.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now.Add(2 * time.Second), ContentScope: "view-a",
	})
	if err != nil || same.TimelineRecordRevision != 7 {
		t.Fatalf("a decision naming no timeline revision left %+v (%v), want 7 kept", same, err)
	}
	// One that names a newer number replaces it without moving the record's
	// own revision: the number is the Query Group's, and a reader compares it
	// to the view, not to the decision.
	moved, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: same.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now.Add(3 * time.Second), ContentScope: "view-a", TimelineRecordRevision: 8,
	})
	if err != nil || moved.TimelineRecordRevision != 8 || moved.RecordRevision != same.RecordRevision {
		t.Fatalf("a decision naming timeline revision 8 gave %+v (%v), want 8 with the record revision unmoved", moved, err)
	}
	renewed, err = store.Renew(ctx, lease.Fence, now.Add(4*time.Second), time.Minute)
	if err != nil || renewed.TimelineRecordRevision != 8 {
		t.Fatalf("Renew() after the move = (%+v, %v), want timeline revision 8", renewed, err)
	}
	// A decision carrying an older number than the record holds - a copy read
	// from the catalog before a cutover stamped the record - does not put the
	// record behind the timeline.
	behind, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-1", DesiredWorkerID: "worker-1", ExpectedRecordRevision: moved.RecordRevision,
		PlacementReason: PlacementRendezvous, DecidedAt: now.Add(5 * time.Second), ContentScope: "view-a", TimelineRecordRevision: 7,
	})
	if err != nil || behind.TimelineRecordRevision != 8 {
		t.Fatalf("a decision naming timeline revision 7 against a record at 8 gave %+v (%v), want 8 kept", behind, err)
	}
	// A record no leader wrote the number to says so with zero, and the
	// renewal carries the zero: zero is "not said", which no timeline is.
	unsaid, err := store.PublishAssignment(ctx, authority, AssignmentDecision{
		QueryGroup: "query-group-2", DesiredWorkerID: "worker-1", PlacementReason: PlacementRendezvous, DecidedAt: now,
	})
	if err != nil || unsaid.TimelineRecordRevision != 0 {
		t.Fatalf("a placement naming no timeline revision gave %+v (%v), want zero", unsaid, err)
	}
}
