// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A new process becomes the Control Leader while one Query Group's timeline
// does not decode, through the production wiring against Redis. Its
// activation read used to fail as a whole on that one key: nothing was cut
// over for anyone, and the repair of the bad timeline was never reached. Now
// the read leaves the Query Group out and names it, the Leader rebuilds its
// records from the publication it runs and rewrites the timeline, and the
// Worker records the Slots lost with the old timeline and detects again.
//
// What the alert on that Query Group sees: the rebuilt records carry the
// generation the lost ones did - the publication stores it and compiling
// refuses any other - so the State on file is read on, and the events carry
// the same generation (in the event identity) and the same alert dedupe. A
// series that was ABNORMAL is still ABNORMAL into the same alert - each
// round's event names its own record, as every round's does - and recovers
// on the first evaluation that finds it normal, with nothing warming in
// between.
func TestANewLeaderRebuildsAnUndecodableTimelineAndTheAlertGoesOn(t *testing.T) {
	fixture := startLifecycleFixture(t, lifecycleStrategy{revision: 7, noDataOff: true,
		targets: []string{lifecycleHostA, lifecycleHostB}}, nil)
	fixture.serve(false, 95, lifecycleHostA)
	fixture.mustAttempt(1)
	raised := fixture.thresholdEventsFor(lifecycleHostA)
	if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host A's events %+v, want one ABNORMAL", raised)
	}

	ctx := context.Background()
	queryGroup := fixture.queryGroup
	key := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog") + ":schedule_timeline:" + string(queryGroup)
	// The bytes go bad in place and keep the life the key had. Read and set
	// again rather than SET KEEPTTL, which Redis 5 does not have.
	life, err := fixture.redis.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if life < 0 {
		life = 0
	}
	if err := fixture.redis.Set(ctx, key, "{not a timeline", life).Err(); err != nil {
		t.Fatal(err)
	}

	// A new process takes over a period and a second after the Slot the
	// cursor waits for, so the rewrite's boundary leaves Slots behind it.
	boundary := fixture.evaluationAt(3) + 1
	fixture.clock.Store(boundary * 1000)
	fixture.replace("alarmd-worker-0")
	production := fixture.bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	rewritten, err := production.dependencies.Catalog.ReadFrozenSchedule(ctx, queryGroup, execution.EvaluationTime(fixture.evaluationAt(4)))
	if err != nil || rewritten.Segment.Start != execution.EvaluationTime(boundary) || rewritten.Segment.End != nil {
		t.Fatalf("the new leader left the timeline as %+v (%v), want it rewritten as one open Segment from its boundary %d",
			rewritten.Segment, err, boundary)
	}

	// Still ABNORMAL: the Worker records the lost Slots and detects again,
	// into the alert it already has, under the generation it had.
	fixture.mustAttempt(4)
	if _, lastFull := fixture.progressSlots(); lastFull != fixture.evaluationAt(4) {
		t.Fatalf("the first Slot after the rewrite did not complete: last full Slot %d, want %d", lastFull, fixture.evaluationAt(4))
	}
	var skip *observability.CursorAdvanceFacts
	fixture.observationsMu.Lock()
	for _, observation := range fixture.observations {
		if observation.Stage == observability.StageScheduleCursorAdvanced && observation.CursorAdvance != nil &&
			observation.CursorAdvance.Status == observability.CursorAdvanceApplied {
			facts := *observation.CursorAdvance
			skip = &facts
		}
	}
	fixture.observationsMu.Unlock()
	if skip == nil || skip.Reason != "SCHEDULE_REPAIRED" || skip.From != fixture.evaluationAt(2) || skip.To != fixture.evaluationAt(4) {
		t.Fatalf("the skip over the lost Slots = %+v, want SCHEDULE_REPAIRED from %d to %d", skip, fixture.evaluationAt(2), fixture.evaluationAt(4))
	}
	after := fixture.thresholdEventsFor(lifecycleHostA)
	if len(after) < 2 {
		t.Fatalf("host A sent %d events by the first Slot after the rebuild, want its ABNORMAL again", len(after))
	}
	for _, event := range after[1:] {
		if event.EventKind != contract.TriggerEventAbnormal || event.DedupeMD5 != raised[0].DedupeMD5 ||
			event.PlanRef.StateCompatibilityHash != raised[0].PlanRef.StateCompatibilityHash {
			t.Fatalf("host A's event after the rebuild = kind %s alert %s generation %s, want ABNORMAL into alert %s under generation %s",
				event.EventKind, event.DedupeMD5, event.PlanRef.StateCompatibilityHash, raised[0].DedupeMD5, raised[0].PlanRef.StateCompatibilityHash)
		}
	}

	// Normal again: the recovery comes on the next evaluation, for the
	// alert that was open - nothing warming holds it back, and the State
	// that says the series was ABNORMAL is still read.
	fixture.publishOpenAlert(raised[0].DedupeMD5)
	fixture.waitOpenAlertSetRead(4)
	fixture.serve(false, 5, lifecycleHostA)
	fixture.mustAttempt(5)
	var recovered []contract.TriggerEventV1
	for _, event := range fixture.thresholdEventsFor(lifecycleHostA) {
		if event.EventKind == contract.TriggerEventRecovery {
			recovered = append(recovered, event)
		}
	}
	if len(recovered) != 1 || recovered[0].DedupeMD5 != raised[0].DedupeMD5 ||
		recovered[0].PlanRef.StateCompatibilityHash != raised[0].PlanRef.StateCompatibilityHash {
		t.Fatalf("host A's recoveries after it read normal = %+v, want one for its open alert %s", recovered, raised[0].DedupeMD5)
	}

	// The skip was counted and named while it lasted; with the timeline
	// rewritten nothing is named any more.
	repository := fixture.bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Repository.(*controlplane.RedisCatalogRepository)
	if reading := repository.SkippedTimelinesReading(); reading.Counts[controlplane.SkippedTimelineUndecodable] != 1 || reading.Total != 0 {
		t.Fatalf("the skip reading = %+v, want the one undecodable skip counted and nothing still named", reading)
	}
	if got := repository.TimelineRepairCounts()[controlplane.TimelineRepairRewritten]; got != 1 {
		t.Fatalf("rewritten = %d, want the one timeline", got)
	}
	if facts := fixture.bundle.activationFleetFacts(); facts == nil || facts.SkippedTimelines != 0 || facts.SkippedNamed != "" {
		t.Fatalf("the activation row = %+v, want nothing skipped once the rewrite landed", facts)
	}
}
