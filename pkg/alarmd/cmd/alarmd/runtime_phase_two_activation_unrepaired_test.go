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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A rebuild of the activation goes on without a Query Group whose timeline is
// absent or does not decode, and the Leader's repair then reopens or rewrites
// it. When the repair keeps failing, the Query Group stays unread round after
// round: it keeps its place and executes none of its Plans, every activation
// round succeeds, and ACTIVATION_FAILING, which said so while the rebuild was
// refused, does not fire. Past the activation bound the fleet verdict leaves
// HEALTHY with ACTIVATION_TIMELINE_UNREPAIRED naming it. Repaired within the
// bound it never does, and a timeline another schema wrote, left alone by
// design, is never counted.
func TestAnUnreadTimelineWhoseRepairKeepsFailingDegradesTheFleet(t *testing.T) {
	published := controlplane.SnapshotPublicationRef{SnapshotRevision: "e7a1b2c30000", PublicationEpoch: 8}
	type step struct {
		after   time.Duration
		skipped []controlplane.SkippedTimeline
	}
	unread := func(reason controlplane.SkippedTimelineReason) []controlplane.SkippedTimeline {
		return []controlplane.SkippedTimeline{{QueryGroup: "query-group-1", Reason: reason}}
	}
	bound := controlplane.SourceStalenessBound
	behindNewer := func() []controlplane.SkippedTimeline {
		skipped := make([]controlplane.SkippedTimeline, 0, controlplane.MaxNamedSkippedTimelines+2)
		for index := range controlplane.MaxNamedSkippedTimelines + 1 {
			skipped = append(skipped, controlplane.SkippedTimeline{
				QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("query-group-0-%03d", index)), Reason: controlplane.SkippedTimelineNewerFormat})
		}
		return append(skipped, controlplane.SkippedTimeline{QueryGroup: "query-group-1", Reason: controlplane.SkippedTimelineAbsent})
	}
	for _, tc := range []struct {
		name     string
		steps    []step
		degraded bool
	}{
		{name: "absent past the bound", degraded: true, steps: []step{
			{0, unread(controlplane.SkippedTimelineAbsent)}, {bound / 2, unread(controlplane.SkippedTimelineAbsent)},
			{bound/2 + time.Minute, unread(controlplane.SkippedTimelineAbsent)}}},
		{name: "undecodable past the bound", degraded: true, steps: []step{
			{0, unread(controlplane.SkippedTimelineUndecodable)}, {bound + time.Minute, unread(controlplane.SkippedTimelineUndecodable)}}},
		{name: "repaired within the bound", steps: []step{
			{0, unread(controlplane.SkippedTimelineAbsent)}, {bound / 2, nil}, {bound, unread(controlplane.SkippedTimelineAbsent)},
			{bound / 2, unread(controlplane.SkippedTimelineAbsent)}}},
		// A rollout: more newer-format Query Groups than a reading names,
		// all sorting before the one whose timeline is gone.
		{name: "absent behind a full list of newer formats", degraded: true, steps: []step{
			{0, behindNewer()}, {bound + time.Minute, behindNewer()}}},
		{name: "newer format", steps: []step{
			{0, unread(controlplane.SkippedTimelineNewerFormat)}, {bound + time.Minute, unread(controlplane.SkippedTimelineNewerFormat)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := time.Unix(1_700_000_000, 0)
			var reading controlplane.SkippedTimelinesReading
			results := make([]phaseTwoControlRefreshResult, 0, len(tc.steps)+1)
			for range len(tc.steps) + 1 {
				result := healthyResult()
				result.Activation = &phaseTwoActivationOutcome{Published: published, Applied: published}
				results = append(results, result)
			}
			control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
				initialResult: results[0], refreshResults: results[1:]}
			owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
			bundle, err := newPhaseTwoWorkerBundle(phaseTwoWorkerBundleDependencies{
				Config: validGoAccessRuntimeConfig(), Health: newPhaseTwoApplicationHealth(), Control: control, Ownership: owner,
				Observer: observability.NopObserver{}, Now: func() time.Time { return clock },
				ActivationSkipped: func() controlplane.SkippedTimelinesReading { return reading },
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := bundle.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
			for _, s := range tc.steps {
				clock = clock.Add(s.after)
				reading = controlplane.ReadSkippedTimelines(s.skipped, nil)
				if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
					t.Fatal(err)
				}
			}
			facts := bundle.activationFleetFacts()
			view := fleet.Aggregate(fleet.Expectation{Known: true, QueryGroups: 1},
				[]fleet.Snapshot{{Replica: "pod-a", TakenAt: clock, Owned: 1, Determined: 1, Activation: facts}},
				[]string{"pod-a"}, clock, time.Hour)
			var raised *fleet.Degradation
			for index := range view.Degradations {
				if view.Degradations[index].Kind == fleet.DegradationActivationTimelineUnrepaired {
					raised = &view.Degradations[index]
				}
			}
			if !tc.degraded {
				if raised != nil || view.Health != fleet.HealthHealthy {
					t.Fatalf("health %s degradations %+v, want HEALTHY and nothing raised", view.Health, view.Degradations)
				}
				return
			}
			if raised == nil || view.Health == fleet.HealthHealthy || !strings.Contains(raised.Text, "query-group-1") ||
				raised.AgeSeconds == nil || *raised.AgeSeconds <= bound.Seconds() {
				t.Fatalf("health %s degradations %+v facts %+v, want the fleet degraded with the Query Group named",
					view.Health, view.Degradations, facts)
			}
		})
	}
}
