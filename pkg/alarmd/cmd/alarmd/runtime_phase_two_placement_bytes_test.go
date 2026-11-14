// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The Leader places a new Query Group under the byte constraint's
// projection: when its rendezvous choice is full by the peaks its workers
// reported, it goes to the worker it fits on. A placement blind to bytes put
// it on the full worker for the byte constraint to move off again.
func TestTheLeaderPlacesANewQueryGroupWhereItFits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const pool = uint64(1 << 30)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
			Load: &ownership.WorkerLoad{RetainedPoolBytes: pool},
		}
	}
	workers := []ownership.WorkerRegistration{worker("worker-1"), worker("worker-2")}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	top, err := scheduler.NewRouter(eligibility).Select("query-group-new", workers, now)
	if err != nil {
		t.Fatal(err)
	}
	other := "worker-1"
	if top.WorkerID == "worker-1" {
		other = "worker-2"
	}
	groups := []execution.QueryGroupIdentity{"query-group-full", "query-group-light", "query-group-new"}
	store := newIndexStore(now, groups[:2])
	store.workers = workers
	store.assignments["query-group-full"] = ownership.AssignmentRecord{QueryGroup: "query-group-full", DesiredWorkerID: top.WorkerID,
		AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now}
	store.assignments["query-group-light"] = ownership.AssignmentRecord{QueryGroup: "query-group-light", DesiredWorkerID: other,
		AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now}
	costs := scheduler.NewCostLedger(func() time.Time { return now })
	production, _ := newIndexOwnershipHarnessWithCosts(t, now, store, costs)
	sink := costLedgerSink{ledger: costs}
	// The rendezvous choice holds 75% of its pool; the new Query Group,
	// unread, counts at the estimate (the larger of the two read peaks), so
	// it does not fit there and does beside the light one.
	sink.RecordCosts(top.WorkerID, []viewstream.QueryGroupCost{{QueryGroup: "query-group-full", RetainedBytesPeak: pool / 100 * 75}})
	sink.RecordCosts(other, []viewstream.QueryGroupCost{{QueryGroup: "query-group-light", RetainedBytesPeak: pool / 100 * 5}})
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
		t.Fatalf("PublishAssignments() error = %v", err)
	}
	if placed := store.assignments["query-group-new"].DesiredWorkerID; placed != other {
		t.Fatalf("query-group-new placed on %q, want %s: its rendezvous choice %s is full", placed, other, top.WorkerID)
	}
}
