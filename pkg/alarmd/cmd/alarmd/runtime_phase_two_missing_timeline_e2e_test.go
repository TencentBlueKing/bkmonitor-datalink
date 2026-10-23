// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A Query Group whose timeline key goes missing with no new publication to
// come, through the production wiring against Redis. The Worker meets the
// absence, names the round SCHEDULE_MISSING and the Query Group on its
// registration; the leader's next activation reads the key, finds it absent
// and opens the timeline again under the publication it already runs. The
// Worker's next round records the Slots lost with the old timeline under
// SCHEDULE_REOPENED, runs the first Slot of the new Segment and drops the
// name.
//
// Before, only a cutover to a new publication opened a missing key again:
// the round failed as BLOCKED_EXACT_SET_UNAVAILABLE, nothing named the Query
// Group, and it ran nothing until the next strategy change.
//
// As in the unreadable case beside this one, the Worker shares its process
// and so its timeline cache with the leader; moving the revision its
// Assignment record names makes its next read go to the store, as a cache
// eviction or a Worker process of its own does in production.
func TestAMissingTimelineIsReportedAndReopenedUnderTheCurrentPublication(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	base := fixture.base
	queryGroup := fixture.queryGroup
	key := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog") + ":schedule_timeline:" + string(queryGroup)
	if err := fixture.redisClient.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	bundle := fixture.bundle
	recordKey := fixture.production.dependencies.Store.(*ownership.RedisStore).AssignmentKey(queryGroup)
	if err := fixture.redisClient.HSet(ctx, recordKey, "timeline_record_revision", "5").Err(); err != nil {
		t.Fatal(err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("the round that publishes the moved revision: %v", err)
	}

	// The Worker meets the absence on the Slot after the one it completed.
	fixture.clock.Store((base + 61) * 1000)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatalf("the round that meets the missing key: %v", err)
	}
	if names, total := bundle.unreadableTimelinesRegistration(); total != 1 || len(names) != 1 || names[0] != string(queryGroup) {
		t.Fatalf("the Worker names %v (%d), want the Query Group whose timeline key is gone", names, total)
	}
	named := false
	for _, observation := range fixture.observed() {
		if observation.Trace.QueryGroupKey == string(queryGroup) && observation.ReasonCode == "SCHEDULE_MISSING" {
			named = true
		}
	}
	if !named {
		t.Fatal("no line names the round SCHEDULE_MISSING")
	}
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		t.Fatal(err)
	}

	// No new publication. The leader's round reads the registration after
	// its activation; the next round's activation acts on what it read.
	for round := 0; round < 2; round++ {
		if err := bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("leader round %d: %v", round, err)
		}
	}
	if got := fixture.repository.TimelineRepairCounts()[controlplane.TimelineRepairOutcome("reopened")]; got != 1 {
		t.Fatalf("reopened = %d, want the one timeline opened again under the current publication", got)
	}
	reopened, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, queryGroup, execution.EvaluationTime(base+120))
	if err != nil || reopened.Segment.Start != execution.EvaluationTime(base+61) || reopened.Segment.End != nil {
		t.Fatalf("the reopened timeline holds %+v (%v), want one open Segment from the leader's boundary %d", reopened.Segment, err, base+61)
	}
	if stamped, err := fixture.redisClient.HGet(ctx, recordKey, "timeline_record_revision").Result(); err != nil || stamped != "6" {
		t.Fatalf("the Assignment record names timeline revision %q (%v), want 6: one above what it named", stamped, err)
	}

	fixture.clock.Store((base + 121) * 1000)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatalf("the round after the reopen: %v", err)
	}
	if after := fixture.progress(ctx); after.LastFullSlot != execution.EvaluationTime(base+120) {
		t.Fatalf("Progress after the reopen = %+v, want the new Segment's first Slot %d completed", after, base+120)
	}
	var skip *observability.CursorAdvanceFacts
	for _, observation := range fixture.observed() {
		if observation.Stage == observability.StageScheduleCursorAdvanced && observation.Trace.QueryGroupKey == string(queryGroup) &&
			observation.CursorAdvance != nil && observation.CursorAdvance.Status == observability.CursorAdvanceApplied {
			facts := *observation.CursorAdvance
			skip = &facts
		}
	}
	if skip == nil || skip.Reason != "SCHEDULE_REOPENED" || skip.From != base+60 || skip.To != base+120 ||
		skip.RepairedAtUnixMilli != (base+61)*1000 {
		t.Fatalf("the skip over the lost Slots = %+v, want SCHEDULE_REOPENED from %d to %d naming the reopen at %d", skip, base+60, base+120, base+61)
	}
	if names, total := bundle.unreadableTimelinesRegistration(); total != 0 || len(names) != 0 {
		t.Fatalf("after a Slot ran the Worker still names %v (%d)", names, total)
	}
}
