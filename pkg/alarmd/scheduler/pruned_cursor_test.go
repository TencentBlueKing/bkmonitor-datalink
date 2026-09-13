// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type advancingProgressReader struct {
	*fakeProgressReader
	requests []execution.ProgressSkipPrunedRequest
	result   execution.ProgressSkipResult
	err      error
}

func (reader *advancingProgressReader) SkipPrunedRange(_ context.Context, request execution.ProgressSkipPrunedRequest) (execution.ProgressSkipResult, error) {
	reader.requests = append(reader.requests, request)
	return reader.result, reader.err
}

func prunedCursorSource(t *testing.T, catalog *fakeSlotCatalog, reader ScheduleProgressReader, at time.Time, observer observability.Observer) *ProductionSlotSource {
	t.Helper()
	source, err := NewProductionSlotSource("query-group-1", "worker-1",
		&fakeAssignmentReader{records: []ownership.AssignmentRecord{testAssignment("worker-1", 3)}},
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog, reader,
		func() time.Time { return at }, WithRecoveryLimits(testRecoveryLimits()), WithPostRecoveryTerminalDelay(time.Minute),
		WithQueryDeadlineReserve(5*time.Second), WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// A cursor that points before the earliest segment the timeline still holds
// can never be navigated from. On that positive fact, read from the
// timeline itself, the source moves the cursor to the earliest retained
// Slot under the owner's fence, reports the advance, and continues from
// there in the same call. Without the fact, without the write side, or when
// the store refuses, the cursor stays where it is and the call stays
// blocked as before.
func TestProductionSlotSourceAdvancesACursorTheTimelineNoLongerHolds(t *testing.T) {
	at := time.Unix(661, 0)
	pruned := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	advances := func(observations []observability.Observation) []*observability.CursorAdvanceFacts {
		var found []*observability.CursorAdvanceFacts
		for _, observation := range observations {
			if observation.Stage == observability.StageScheduleCursorAdvanced {
				found = append(found, observation.CursorAdvance)
			}
		}
		return found
	}
	wantRequest := execution.ProgressSkipPrunedRequest{Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"},
		OwnerFence: testFence(7), ExpectedNextSlot: 120, ResumeAt: 600}

	t.Run("the cursor is moved to the earliest retained Slot and navigation continues", func(t *testing.T) {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressCommitted}}
		var observations []observability.Observation
		source := prunedCursorSource(t, catalog, reader, at, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}))
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 600 {
			t.Fatalf("Next() = (slot at %d, due %v, error %v), want the Slot at 600", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if !reflect.DeepEqual(reader.requests, []execution.ProgressSkipPrunedRequest{wantRequest}) {
			t.Fatalf("skip requests = %+v, want %+v", reader.requests, wantRequest)
		}
		facts := advances(observations)
		if len(facts) != 1 || facts[0].From != 120 || facts[0].To != 600 || facts[0].Status != observability.CursorAdvanceApplied {
			t.Fatalf("cursor advance observations = %+v, want one applied advance from 120 to 600", facts)
		}
	})
	t.Run("the resume point is the first due Slot, not the segment start", func(t *testing.T) {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 630, nil, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressCommitted}}
		source := prunedCursorSource(t, catalog, reader, time.Unix(721, 0), nil)
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 660 {
			t.Fatalf("Next() = (slot at %d, due %v, error %v), want the Slot at 660", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if len(reader.requests) != 1 || reader.requests[0].ResumeAt != 660 {
			t.Fatalf("skip requests = %+v, want a resume at 660", reader.requests)
		}
	})
	t.Run("a reader without the write side leaves the cursor blocked", func(t *testing.T) {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
		var observations []observability.Observation
		source := prunedCursorSource(t, catalog, &fakeProgressReader{result: pruned, catalog: catalog}, at,
			observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observations = append(observations, observation)
			}))
		var blocked *SourceBlockedError
		if _, due, _, err := source.Next(context.Background(), "query-group-1"); due || !errors.As(err, &blocked) {
			t.Fatalf("Next() = (due %v, error %v), want blocked", due, err)
		}
		if len(advances(observations)) != 0 {
			t.Fatal("an advance was reported without a write side")
		}
	})
	t.Run("a timeline that still starts at or before the cursor proves nothing", func(t *testing.T) {
		end := execution.EvaluationTime(90)
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 60, &end, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressCommitted}}
		source := prunedCursorSource(t, catalog, reader, at, nil)
		var blocked *SourceBlockedError
		if _, due, _, err := source.Next(context.Background(), "query-group-1"); due || !errors.As(err, &blocked) {
			t.Fatalf("Next() = (due %v, error %v), want blocked", due, err)
		}
		if len(reader.requests) != 0 {
			t.Fatalf("skip requested although the timeline starts before the cursor: %+v", reader.requests)
		}
	})
	t.Run("a store that refuses leaves the cursor for the next attempt", func(t *testing.T) {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressConflict, Refusal: execution.SkipRefusalCASConflict, InFlightSlot: 120}}
		var observations []observability.Observation
		source := prunedCursorSource(t, catalog, reader, at, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}))
		var blocked *SourceBlockedError
		if _, due, _, err := source.Next(context.Background(), "query-group-1"); due || !errors.As(err, &blocked) {
			t.Fatalf("Next() = (due %v, error %v), want blocked", due, err)
		}
		// The refusal and the Slot it names travel with the report, so a
		// conflict that keeps happening says which premise fails.
		if facts := advances(observations); len(reader.requests) != 1 || len(facts) != 1 || facts[0].Status != observability.CursorAdvanceConflict ||
			facts[0].Refusal != observability.CursorRefusalCASConflict || facts[0].InFlightSlot != 120 {
			t.Fatalf("requests %+v, advance observations %+v; want one refused advance reported as conflict with its refusal and Slot", reader.requests, facts)
		}
	})
	t.Run("a skip that discarded a Slot in flight reports which one", func(t *testing.T) {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: pruned, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressCommitted, InFlightSlot: 120}}
		var observations []observability.Observation
		source := prunedCursorSource(t, catalog, reader, at, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}))
		if _, _, _, err := source.Next(context.Background(), "query-group-1"); err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if facts := advances(observations); len(facts) != 1 || facts[0].Status != observability.CursorAdvanceApplied || facts[0].Refusal != "" || facts[0].InFlightSlot != 120 {
			t.Fatalf("advance observations %+v; want one applied advance naming the discarded Slot 120", facts)
		}
	})
	t.Run("a cursor moved onto the retired boundary drains", func(t *testing.T) {
		retiredAt := execution.EvaluationTime(600)
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}, retiredAt: &retiredAt}
		// The fake resolves the next Slot after any completion from the
		// retirement it was given, so this case starts from a cursor without
		// a completion behind it, which is the other navigation path.
		unanchored := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120,
		}}
		reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: unanchored, catalog: catalog},
			result: execution.ProgressSkipResult{Status: execution.ProgressCommitted}}
		source := prunedCursorSource(t, catalog, reader, at, nil)
		_, due, facts, err := source.Next(context.Background(), "query-group-1")
		if err != nil || due || !facts.Retired || len(reader.requests) != 1 {
			t.Fatalf("Next() = (due %v, facts %+v, error %v, requests %d), want retired after the advance", due, facts, err, len(reader.requests))
		}
	})
}
