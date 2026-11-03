// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A timeline the Control Leader rewrote because it stopped decoding keeps
// none of its Segments: the new one opens a single Segment at the rewrite.
// The cursor of the Query Group stood inside a Segment that is gone, and the
// catalog continues it at the new Segment's first Slot - the same answer it
// gives a retirement's hole and a pruned prefix, which is right for those and
// keeps their behaviour. For the rewrite the Slots in between were lost and
// nothing said so: no gap, no skip, the cursor simply jumped. These pin that
// the jump into a Segment carrying the rewrite's mark is recorded, once, under
// its own word, and that an unmarked Segment is continued into as before.

func repairedCursorFixture(t *testing.T, repairs map[execution.EvaluationTime]execution.SegmentRepair,
	result execution.ProgressSkipResult) (*fakeSlotCatalog, *advancingProgressReader, *[]observability.Observation, *ProductionSlotSource) {
	t.Helper()
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)},
		repairs: repairs}
	// Anchored on the completion at 60, with the cursor at 120 inside the
	// Segment the rewrite did not keep.
	inside := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull, LastDataSlot: 60,
	}}
	reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: inside, catalog: catalog}, result: result}
	var observations []observability.Observation
	source := prunedCursorSource(t, catalog, reader, time.Unix(661, 0), observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}))
	return catalog, reader, &observations, source
}

func cursorAdvances(observations []observability.Observation) []observability.CursorAdvanceFacts {
	var found []observability.CursorAdvanceFacts
	for _, observation := range observations {
		if observation.Stage == observability.StageScheduleCursorAdvanced && observation.CursorAdvance != nil {
			found = append(found, *observation.CursorAdvance)
		}
	}
	return found
}

func TestTheJumpIntoARewrittenTimelineIsRecorded(t *testing.T) {
	rewrittenAt := int64(590_000)
	marked := map[execution.EvaluationTime]execution.SegmentRepair{
		600: {Kind: execution.SegmentRepairUnreadable, AtUnixMilli: rewrittenAt},
	}

	t.Run("the skip is recorded from the cursor to the new Segment and the Slot runs there", func(t *testing.T) {
		_, reader, observations, source := repairedCursorFixture(t, marked, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 600 || slot.ExpectedNextSlot != 600 {
			t.Fatalf("Next() = (slot %d expecting %d, due %v, error %v), want the new Segment's first Slot 600",
				slot.Contract.Slot.EvaluationTime, slot.ExpectedNextSlot, due, err)
		}
		if len(reader.requests) != 1 {
			t.Fatalf("skip requests = %+v, want exactly one", reader.requests)
		}
		request := reader.requests[0]
		if request.ExpectedNextSlot != 120 || request.ResumeAt != 600 || request.Reason != "SCHEDULE_REPAIRED" ||
			request.OwnerFence != testFence(7) {
			t.Fatalf("skip request = %+v, want from the cursor 120 to 600 under SCHEDULE_REPAIRED with the owner's fence", request)
		}
		// The gap the store writes from it: the cursor is the first lost Slot,
		// where it resumed is carried apart, and the count is not invented.
		gap := request.SkipGap()
		if gap.FirstSlot != 120 || gap.ResumedAt != 600 || !gap.Uncounted || gap.Count != 0 || gap.Kind != execution.CompletionGapSkipped {
			t.Fatalf("recorded gap = %+v, want an uncounted skip from 120 resumed at 600", gap)
		}
		facts := cursorAdvances(*observations)
		if len(facts) != 1 || facts[0].Status != observability.CursorAdvanceApplied || facts[0].Reason != "SCHEDULE_REPAIRED" ||
			facts[0].From != 120 || facts[0].To != 600 || facts[0].RepairedAtUnixMilli != rewrittenAt {
			t.Fatalf("cursor advance reports = %+v, want one applied repaired skip naming when the timeline was rewritten", facts)
		}
	})

	t.Run("a Segment opened for a key that was gone is recorded under its own word", func(t *testing.T) {
		reopened := map[execution.EvaluationTime]execution.SegmentRepair{
			600: {Kind: execution.SegmentRepairAbsent, AtUnixMilli: rewrittenAt},
		}
		_, reader, observations, source := repairedCursorFixture(t, reopened, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 600 {
			t.Fatalf("Next() = (slot %d, due %v, error %v), want the Slot at 600", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if len(reader.requests) != 1 || reader.requests[0].Reason != "SCHEDULE_REOPENED" || reader.requests[0].ExpectedNextSlot != 120 ||
			reader.requests[0].ResumeAt != 600 {
			t.Fatalf("skip requests = %+v, want one from 120 to 600 under SCHEDULE_REOPENED", reader.requests)
		}
		if facts := cursorAdvances(*observations); len(facts) != 1 || facts[0].Reason != "SCHEDULE_REOPENED" || facts[0].RepairedAtUnixMilli != rewrittenAt {
			t.Fatalf("cursor advance reports = %+v, want the reopen named", facts)
		}
	})

	t.Run("an unmarked Segment is continued into as before", func(t *testing.T) {
		// The retirement's hole and the pruned prefix: the same shape, no mark.
		_, reader, observations, source := repairedCursorFixture(t, nil, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 600 {
			t.Fatalf("Next() = (slot %d, due %v, error %v), want the Slot at 600", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if len(reader.requests) != 0 || len(cursorAdvances(*observations)) != 0 {
			t.Fatalf("an unmarked Segment recorded a skip: requests %+v, reports %+v", reader.requests, cursorAdvances(*observations))
		}
	})

	t.Run("a mark this build does not know reads as no mark", func(t *testing.T) {
		unknown := map[execution.EvaluationTime]execution.SegmentRepair{600: {Kind: "from-a-later-build", AtUnixMilli: rewrittenAt}}
		_, reader, _, source := repairedCursorFixture(t, unknown, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		if _, due, _, err := source.Next(context.Background(), "query-group-1"); err != nil || !due {
			t.Fatalf("Next() = (due %v, error %v), want the Slot run as before", due, err)
		}
		if len(reader.requests) != 0 {
			t.Fatalf("an unknown mark recorded a skip: %+v", reader.requests)
		}
	})

	t.Run("a skip the store refuses is retried, never run past", func(t *testing.T) {
		catalog, reader, observations, source := repairedCursorFixture(t, marked,
			execution.ProgressSkipResult{Status: execution.ProgressConflict, Refusal: execution.SkipRefusalCursorMoved})
		_, due, _, err := source.Next(context.Background(), "query-group-1")
		var retry *SourceRetryError
		if due || !errors.As(err, &retry) || !errors.Is(err, ErrRepairSkipUnrecorded) {
			t.Fatalf("Next() = (due %v, error %T %v), want a retry naming the unrecorded skip", due, err, err)
		}
		if len(catalog.requests) != 0 {
			t.Fatalf("the Slot was frozen past an unrecorded skip: %+v", catalog.requests)
		}
		if facts := cursorAdvances(*observations); len(reader.requests) != 1 || len(facts) != 1 ||
			facts[0].Status != observability.CursorAdvanceConflict || facts[0].Reason != "SCHEDULE_REPAIRED" {
			t.Fatalf("requests %+v, reports %+v; want one refused repaired skip reported", reader.requests, facts)
		}
	})

	t.Run("a mark that cannot be read fails the round closed", func(t *testing.T) {
		catalog, reader, _, source := repairedCursorFixture(t, marked, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		catalog.repairErr = errors.New("timeline read failed")
		if _, due, _, err := source.Next(context.Background(), "query-group-1"); due || err == nil {
			t.Fatalf("Next() = (due %v, error %v), want the round refused", due, err)
		}
		if len(reader.requests) != 0 || len(catalog.requests) != 0 {
			t.Fatalf("an unread mark still skipped (%+v) or froze (%+v)", reader.requests, catalog.requests)
		}
	})

	t.Run("a cursor inside the marked Segment that no Plan is due at is continued, not skipped", func(t *testing.T) {
		// Anchored on 600 with the cursor off the grid at 630, inside the
		// Segment the rewrite opened: navigation continues at 660 within the
		// same Segment, which lost nothing.
		_, reader, _, source := repairedCursorFixture(t, marked, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		reader.fakeProgressReader.result = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 630, LastFullSlot: 600,
			LastCompletionKind: execution.CompletionFull,
		}}
		source.now = func() time.Time { return time.Unix(721, 0) }
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 660 {
			t.Fatalf("Next() = (slot %d, due %v, error %v), want the Slot at 660", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if len(reader.requests) != 0 {
			t.Fatalf("a cursor inside the rewritten Segment was recorded as a loss: %+v", reader.requests)
		}
	})

	t.Run("a cursor already inside the marked Segment is not skipped again", func(t *testing.T) {
		catalog, reader, _, source := repairedCursorFixture(t, marked, execution.ProgressSkipResult{Status: execution.ProgressCommitted})
		reader.result = execution.ProgressSkipResult{}
		reader.fakeProgressReader.result = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 660, LastFullSlot: 600,
			LastCompletionKind: execution.CompletionFull,
		}}
		source.now = func() time.Time { return time.Unix(721, 0) }
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		if err != nil || !due || slot.Contract.Slot.EvaluationTime != 660 {
			t.Fatalf("Next() = (slot %d, due %v, error %v), want the Slot at 660", slot.Contract.Slot.EvaluationTime, due, err)
		}
		if len(reader.requests) != 0 {
			t.Fatalf("a cursor inside the rewritten Segment was skipped again: %+v", reader.requests)
		}
		_ = catalog
	})
}

// A cursor with no completion behind it reaches the pruned advance instead,
// and lands in the same marked Segment: the advance records the rewrite's
// word, not retention's.
func TestTheAdvanceIntoARewrittenTimelineNamesTheRewrite(t *testing.T) {
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)},
		repairs: map[execution.EvaluationTime]execution.SegmentRepair{600: {Kind: execution.SegmentRepairUnreadable, AtUnixMilli: 590_000}}}
	unanchored := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120,
	}}
	reader := &advancingProgressReader{fakeProgressReader: &fakeProgressReader{result: unanchored, catalog: catalog},
		result: execution.ProgressSkipResult{Status: execution.ProgressCommitted}}
	var observations []observability.Observation
	source := prunedCursorSource(t, catalog, reader, time.Unix(661, 0), observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}))
	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.Contract.Slot.EvaluationTime != 600 {
		t.Fatalf("Next() = (slot %d, due %v, error %v), want the Slot at 600", slot.Contract.Slot.EvaluationTime, due, err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Reason != "SCHEDULE_REPAIRED" || reader.requests[0].ExpectedNextSlot != 120 ||
		reader.requests[0].ResumeAt != 600 {
		t.Fatalf("skip requests = %+v, want one from 120 to 600 under SCHEDULE_REPAIRED", reader.requests)
	}
	if facts := cursorAdvances(observations); len(facts) != 1 || facts[0].Reason != "SCHEDULE_REPAIRED" || facts[0].RepairedAtUnixMilli != 590_000 {
		t.Fatalf("cursor advance reports = %+v, want the rewrite named", facts)
	}
}

// A timeline whose bytes do not decode is named SCHEDULE_UNREADABLE whichever
// read of it meets them first. For a Query Group with Progress that is the
// retirement check, which returned the decode failure raw: the round failed
// as an unclassified error, the word was never said, and the Worker had
// nothing to name on its registration - for nearly every Query Group, since
// nearly every one has Progress. A Query Group without Progress meets it on
// its first Segment's read, raw as well.
func TestATimelineThatDoesNotDecodeBlocksTheSourceByName(t *testing.T) {
	undecodable := &controlplane.DeterministicScheduleError{Err: errors.New("decode Schedule timeline: invalid character")}
	for name, load := range map[string]execution.ProgressLoadResult{
		"with Progress": {Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120, LastFullSlot: 60,
			LastCompletionKind: execution.CompletionFull,
		}},
		"without Progress": {Status: execution.ProgressMissing},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)},
				timelineErr: undecodable}
			source := newProductionSlotSourceForTest(t, catalog, load, time.Unix(661, 0))
			_, due, _, err := source.Next(context.Background(), "query-group-1")
			var blocked *SourceBlockedError
			if due || !errors.As(err, &blocked) || blocked.ReasonCode() != "SCHEDULE_UNREADABLE" {
				t.Fatalf("Next() = (due %v, error %T %v), want the source blocked under SCHEDULE_UNREADABLE", due, err, err)
			}
		})
	}
}
