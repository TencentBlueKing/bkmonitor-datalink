// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A draining Query Group whose Progress cursor lies before the earliest Slot
// its timeline still holds can never find the Slot it asks for: it blocks on
// every attempt, never drains, and a retirement that keeps returning it
// holds it for as long as the projection lives. The draining view now says
// so per Query Group, from the timeline itself, before anything acts on it:
// the cursor's position, the earliest retained Slot, and a count. A cursor
// inside the retained range makes no such claim.
func TestProductionPhaseTwoDrainingViewReportsACursorTheTimelineNoLongerHolds(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-new", PublicationEpoch: 2}
	boundary := execution.EvaluationTime(900)
	retired := execution.QueryGroupIdentity("query-group-old")
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 2, Current: publication,
			Draining: []controlplane.DrainingQueryGroup{{QueryGroup: retired, RetiredBoundary: boundary}}},
		snapshot: controlplane.PublishedSnapshot{Publication: publication,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-new"}}},
	}
	// The timeline's earliest retained Segment starts at 600; everything
	// before it has been pruned.
	schedules := &fakeScheduleProjection{
		initial: map[execution.QueryGroupIdentity]execution.FrozenQueryGroupSchedule{
			retired: schedulerScheduleForProductionControl(t, retired, 600, 60, &boundary),
		},
		retired: map[execution.QueryGroupIdentity]execution.EvaluationTime{retired: boundary},
	}
	cursorAt := func(slot execution.EvaluationTime, inFlight *execution.UnfinishedSlotProjection) *fakeProductionProgressReader {
		return &fakeProductionProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
			retired: {Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: retired}, NextSlot: slot, UnfinishedSlot: inFlight,
			}},
		}}
	}
	inFlightAt := func(slot execution.EvaluationTime) *execution.UnfinishedSlotProjection {
		contract := execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: retired, EvaluationTime: slot}, SnapshotRevision: "s", QueryRevision: "query",
			ScheduleRevision: "r", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans",
		}
		return &execution.UnfinishedSlotProjection{
			Contract: contract,
			DuePlanTargets: execution.FrozenDuePlanTargets{DuePlanSetDigest: contract.DuePlanSetDigest,
				Plans: []execution.PlanIdentity{{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}}},
			EarliestQueryDeadlineUnixMilli: int64(slot)*1000 + 1_000, KeepUntilUnixMilli: int64(slot)*1000 + 601_000,
		}
	}
	for _, test := range []struct {
		name         string
		cursor       execution.EvaluationTime
		inFlight     *execution.UnfinishedSlotProjection
		wantPruned   bool
		wantEarliest int64
		wantInFlight string
	}{
		{name: "a cursor before the retained range is reported pruned", cursor: 60, wantPruned: true, wantEarliest: 600},
		{name: "a cursor inside the retained range is not", cursor: 660, wantPruned: false, wantEarliest: 600},
		// A pruned cursor with a Slot in flight is the record a skip refuses
		// to move past; the sample says so instead of leaving it to be
		// inferred from a refusal that keeps happening.
		{name: "a slot in flight is reported with the pruned cursor", cursor: 60, inFlight: inFlightAt(60), wantPruned: true, wantEarliest: 600, wantInFlight: observability.DrainingInFlightSlot},
	} {
		t.Run(test.name, func(t *testing.T) {
			var observations []observability.Observation
			control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
				Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: &fakeSourceReconciler{},
				Activator: &fakeInitialScheduleActivator{}, Repository: repository, Schedules: schedules, Progress: cursorAt(test.cursor, test.inFlight),
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					observations = append(observations, observation)
				}),
				RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.LoadActive(context.Background()); err != nil {
				t.Fatal(err)
			}
			var facts *observability.DrainingQGFacts
			for _, observation := range observations {
				if observation.DrainingQG != nil {
					facts = observation.DrainingQG
				}
			}
			if facts == nil || facts.Undrained != 1 || len(facts.Samples) != 1 {
				t.Fatalf("draining facts = %+v, want one undrained sample", facts)
			}
			wantCount := 0
			if test.wantPruned {
				wantCount = 1
			}
			sample := facts.Samples[0]
			if facts.CursorPruned != wantCount || sample.CursorPruned != test.wantPruned || sample.EarliestRetainedSlot != test.wantEarliest ||
				sample.NextSlot != int64(test.cursor) || sample.InFlight != test.wantInFlight {
				t.Fatalf("draining view = %+v (sample %+v), want cursor_pruned=%v earliest=%d next_slot=%d in_flight=%q",
					facts, sample, test.wantPruned, test.wantEarliest, test.cursor, test.wantInFlight)
			}
		})
	}
}
