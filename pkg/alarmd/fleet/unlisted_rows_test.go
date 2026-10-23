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
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// unlistedFixture is two replicas' snapshots with complete owned lists: pod-a
// owns a healthy object and a listed one, pod-b owns a third; and pod-a's
// tracker, which has decided and sent no-data for a Plan on the healthy one.
func unlistedFixture(t *testing.T) (*Tracker, []Snapshot, Expectation) {
	t.Helper()
	tracker := newTracker(t, &clock{at: now})
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-healthy"})
	absenceDecided(ctx, tracker, "s-sent", 600, observability.NoDataAbsenceFacts{RosterSource: "HISTORY", Expected: 4, Present: 1, Absent: 3})
	written(ctx, tracker, "s-sent", observability.NoDataEmissionFacts{AbnormalSent: 2,
		LastAbnormal: &observability.NoDataEmittedEvent{EvaluationTime: 600, AlertKey: "key-600", Group: map[string]string{"bk_target_ip": "192.0.2.10"}}})
	listed := anomaly("qg-listed")
	listed.Replica = "pod-a"
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 2, Determined: 2, OwnedObjects: []string{"qg-healthy", "qg-listed"},
			Anomalies: []Anomaly{listed}, TotalAnomalies: 1},
		{Replica: "pod-b", TakenAt: now.Add(-20 * time.Second), Owned: 1, Determined: 1, OwnedObjects: []string{"qg-other"}},
	}
	return tracker, snapshots, Expectation{QueryGroups: 3, Known: true, IDs: []string{"qg-healthy", "qg-listed", "qg-other"}}
}

func routeOn(t *testing.T, snapshots []Snapshot, expectation Expectation, rows func(string) (Anomaly, bool)) http.Handler {
	t.Helper()
	service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	service.SetLocalRows(rows)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// A healthy object's row is in no snapshot. Read through the object route on
// the replica that tracks it, it comes back as that replica's live row --
// the listed rows' shape, listed false -- with the Plan's no-data tracking
// (Q3) and what it sent (Q8). On another replica the route names the holder
// as of its snapshot. A listed object is answered from the view as before,
// not as a tracked row.
func TestAHealthyObjectsRowIsReadThroughTheRouteFromTheReplicaTrackingIt(t *testing.T) {
	tracker, snapshots, expectation := unlistedFixture(t)
	owner := routeOn(t, snapshots, expectation, tracker.TrackedRow)
	_, body := get(t, owner, "/api/objects/qg-healthy")
	tracked, _ := body["tracked"].(map[string]any)
	if tracked == nil || body["anomaly"] != nil || tracked["listed"] != false || tracked["replica"] != "pod-a" {
		t.Fatalf("owner body = %+v, want pod-a's live row, listed false, and no anomaly", body)
	}
	entries, _ := tracked["no_data_tracking"].([]any)
	if len(entries) != 1 {
		t.Fatalf("tracked no_data_tracking = %+v, want the one Plan", tracked["no_data_tracking"])
	}
	entry := entries[0].(map[string]any)
	emitted, _ := entry["emitted"].(map[string]any)
	if entry["expected"] != float64(4) || entry["present"] != float64(1) || entry["absent"] != float64(3) ||
		emitted == nil || emitted["abnormal_sent"] != float64(2) || emitted["last_abnormal"].(map[string]any)["alert_key"] != "key-600" {
		t.Fatalf("tracked entry = %+v, want Q3's counts and Q8's sends", entry)
	}

	elsewhere := routeOn(t, snapshots, expectation, func(string) (Anomaly, bool) { return Anomaly{}, false })
	_, body = get(t, elsewhere, "/api/objects/qg-healthy")
	asOf, _ := time.Parse(time.RFC3339Nano, body["tracked_by_as_of"].(string))
	if body["tracked"] != nil || body["tracked_by"] != "pod-a" || !asOf.Equal(now.Add(-10*time.Second)) {
		t.Fatalf("other replica body = %+v, want pod-a named as of its snapshot", body)
	}

	_, body = get(t, owner, "/api/objects/qg-listed")
	if body["anomaly"] == nil || body["tracked"] != nil || body["tracked_by"] != nil {
		t.Fatalf("listed object body = %+v, want the view's row only", body)
	}
	// Read under another check's filter, a listed object has no facts in
	// the answer and is still listed: no live row stands in for it.
	tracker.Observe(observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-listed"}),
		observability.Observation{Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
			Result: observability.ResultSuccess, Trace: observability.TraceFields{StrategyID: "s-listed", BusinessID: "2"},
			NoDataAbsence: &observability.NoDataAbsenceFacts{Outcome: "EVALUATED", Expected: 1, Present: 1}})
	_, body = get(t, owner, "/api/objects/qg-listed?check="+string(CheckReadHeld))
	if facts, _ := body["facts"].([]any); len(facts) != 0 || body["tracked"] != nil {
		t.Fatalf("listed object under another check = %+v, want no facts and no live row", body)
	}
}

// The live row is built while the observe path keeps writing the maps it
// reads; run under -race, a row built outside the tracker's lock fails here.
func TestATrackedRowIsBuiltUnderTheTrackersLockWhileObservationsArrive(t *testing.T) {
	tracker, snapshots, expectation := unlistedFixture(t)
	owner := routeOn(t, snapshots, expectation, tracker.TrackedRow)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-healthy"})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for round := int64(0); round < 200; round++ {
			absenceDecided(ctx, tracker, "s-sent", 660+round*60, observability.NoDataAbsenceFacts{RosterSource: "HISTORY", Expected: 4, Absent: 4})
			written(ctx, tracker, "s-sent", observability.NoDataEmissionFacts{AbnormalSent: 1,
				LastAbnormal: &observability.NoDataEmittedEvent{EvaluationTime: 660 + round*60}})
		}
	}()
	for read := 0; read < 50; read++ {
		if _, body := get(t, owner, "/api/objects/qg-healthy"); body["tracked"] == nil {
			t.Fatalf("read %d: no tracked row", read)
		}
	}
	wait.Wait()
}

// The strategy route attaches the live row to a Plan whose object lists none
// and that this replica tracks, and leaves a Plan with a listed row alone.
func TestTheStrategyRouteAttachesTheLiveRowOnlyWhereNoRowIsListed(t *testing.T) {
	tracker, snapshots, expectation := unlistedFixture(t)
	// The listed object is tracked here too, so only its listed row keeps
	// the live one off it.
	listed := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-listed"})
	absenceDecided(listed, tracker, "s-listed", 600, observability.NoDataAbsenceFacts{RosterSource: "HISTORY", Expected: 1, Present: 1})
	service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	service.SetLocalRows(tracker.TrackedRow)
	standing := StrategyStanding{Plans: []StrategyPlanStanding{
		{StrategyPlanRef: StrategyPlanRef{QueryGroup: "qg-healthy"}},
		{StrategyPlanRef: StrategyPlanRef{QueryGroup: "qg-listed"}, Rows: []Anomaly{anomaly("qg-listed")}},
		{StrategyPlanRef: StrategyPlanRef{QueryGroup: "qg-other"}},
	}}
	attachTrackedRows(&standing, service)
	if standing.Plans[0].Tracked == nil || standing.Plans[0].Tracked.Listed == nil || *standing.Plans[0].Tracked.Listed {
		t.Fatalf("healthy plan = %+v, want the live row", standing.Plans[0])
	}
	if standing.Plans[1].Tracked != nil || standing.Plans[2].Tracked != nil {
		t.Fatalf("plans = %+v, want no live row on a listed object or on one this replica does not track", standing.Plans)
	}
}
