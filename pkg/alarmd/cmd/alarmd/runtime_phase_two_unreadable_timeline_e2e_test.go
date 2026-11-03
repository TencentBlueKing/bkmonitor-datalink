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

// The whole way out, through the production wiring against Redis. A Query
// Group has run a Slot; then its timeline's bytes stop decoding. The Worker
// meets them the next time it reads the stored timeline rather than its
// cached copy, names the Query Group on its registration, and the leader's
// round reads the name; the next activation reads the timeline again,
// cannot decode it either and rewrites it from its boundary. The Worker's
// next round records the Slots lost with the old timeline under
// SCHEDULE_REPAIRED, runs the first Slot of the new Segment, detects again,
// and drops the name.
//
// Before this the Query Group ran nothing for as long as the key lived: no
// report reached the leader, a reconcile with no new publication read
// nothing, and the renewal kept the bad bytes alive.
//
// The Worker here shares its process, and so its timeline cache, with the
// leader, and the cache holds the timeline as it was before the bytes went
// bad. Moving the timeline revision its Assignment record names makes the
// Worker's next read ask for a revision it has not cached, so it reads the
// stored bytes - what a cache eviction, or a Worker process of its own
// restarting, does in production. A leader restarting is not this case: see
// the report that accompanies this change.
func TestAnUnreadableTimelineIsReportedRewrittenAndDetectedAgain(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	base := fixture.base
	queryGroup := fixture.queryGroup
	key := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog") + ":schedule_timeline:" + string(queryGroup)
	// The key keeps the life it had, as bytes gone bad in place would. Read
	// and set again rather than SET KEEPTTL, which Redis 5 does not have.
	life, err := fixture.redisClient.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if life < 0 {
		life = 0
	}
	if err := fixture.redisClient.Set(ctx, key, "{not a timeline", life).Err(); err != nil {
		t.Fatal(err)
	}
	bundle := fixture.bundle
	recordKey := fixture.production.dependencies.Store.(*ownership.RedisStore).AssignmentKey(queryGroup)
	if err := fixture.redisClient.HSet(ctx, recordKey, "timeline_record_revision", "5").Err(); err != nil {
		t.Fatal(err)
	}
	// The leader's round carries the record's revision to the view.
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("the round that publishes the moved revision: %v", err)
	}

	// The Worker meets the bytes on the Slot after the one it completed.
	fixture.clock.Store((base + 61) * 1000)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatalf("the round that meets the bytes: %v", err)
	}
	if names, total := bundle.unreadableTimelinesRegistration(); total != 1 || len(names) != 1 || names[0] != string(queryGroup) {
		t.Fatalf("the Worker names %v (%d), want the Query Group whose timeline does not decode", names, total)
	}
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		t.Fatal(err)
	}

	// The leader's round reads the registration after its activation; the
	// next round's activation acts on what it read.
	for round := 0; round < 2; round++ {
		if err := bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("leader round %d: %v", round, err)
		}
	}
	if got := fixture.repository.TimelineRepairCounts()[controlplane.TimelineRepairRewritten]; got != 1 {
		t.Fatalf("rewritten = %d, want the one timeline rewritten", got)
	}
	rewritten, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, queryGroup, execution.EvaluationTime(base+120))
	if err != nil || rewritten.Segment.Start != execution.EvaluationTime(base+61) || rewritten.Segment.End != nil {
		t.Fatalf("the rewritten timeline holds %+v (%v), want one open Segment from the leader's boundary %d", rewritten.Segment, err, base+61)
	}
	if stamped, err := fixture.redisClient.HGet(ctx, recordKey, "timeline_record_revision").Result(); err != nil || stamped != "6" {
		t.Fatalf("the Assignment record names timeline revision %q (%v), want 6: one above what it named", stamped, err)
	}

	// The Worker detects again, past a recorded skip.
	fixture.clock.Store((base + 121) * 1000)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatalf("the round after the rewrite: %v", err)
	}
	after := fixture.progress(ctx)
	if after.LastFullSlot != execution.EvaluationTime(base+120) {
		t.Fatalf("Progress after the rewrite = %+v, want the new Segment's first Slot %d completed", after, base+120)
	}
	var skip *observability.CursorAdvanceFacts
	for _, observation := range fixture.observed() {
		if observation.Stage == observability.StageScheduleCursorAdvanced && observation.Trace.QueryGroupKey == string(queryGroup) &&
			observation.CursorAdvance != nil && observation.CursorAdvance.Status == observability.CursorAdvanceApplied {
			facts := *observation.CursorAdvance
			skip = &facts
		}
	}
	if skip == nil || skip.Reason != "SCHEDULE_REPAIRED" || skip.From != base+60 || skip.To != base+120 ||
		skip.RepairedAtUnixMilli != (base+61)*1000 {
		t.Fatalf("the skip over the lost Slots = %+v, want SCHEDULE_REPAIRED from %d to %d naming the rewrite at %d", skip, base+60, base+120, base+61)
	}
	if names, total := bundle.unreadableTimelinesRegistration(); total != 0 || len(names) != 0 {
		t.Fatalf("after a Slot ran the Worker still names %v (%d)", names, total)
	}
}

// A timeline whose key went missing - evicted, expired, deleted - is opened
// again by the next cutover, and the Worker whose cursor stood in the lost
// timeline records the Slots it lost under SCHEDULE_REOPENED before it
// detects again, through the production wiring against Redis. The jump used
// to be silent, which made a missing key the one loss that passed for
// benign.
func TestAMissingTimelineIsReopenedAndItsLostSlotsAreRecorded(t *testing.T) {
	fixture := startCutoverFixture(t, nil)
	ctx := context.Background()
	base := fixture.base
	queryGroup := fixture.queryGroup
	key := productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog") + ":schedule_timeline:" + string(queryGroup)
	if err := fixture.redisClient.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	// A publication that changes the sibling strategy: its cutover meets the
	// missing key. The first refresh publishes, the second activates.
	installCutoverStallStrategies(t, ctx, fixture.redisClient, "system.disk", 1725000600)
	fixture.clock.Store((base + 90) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("publication round %d: %v", round, err)
		}
	}
	reopened, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, queryGroup, execution.EvaluationTime(base+120))
	if err != nil || reopened.Segment.Start != execution.EvaluationTime(base+90) || reopened.Segment.End != nil {
		t.Fatalf("the reopened timeline holds %+v (%v), want one open Segment from the cutover's boundary %d", reopened.Segment, err, base+90)
	}

	fixture.clock.Store((base + 121) * 1000)
	if err := runScheduledOnceSettled(ctx, fixture.bundle); err != nil {
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
		skip.RepairedAtUnixMilli != (base+90)*1000 {
		t.Fatalf("the skip over the lost Slots = %+v, want SCHEDULE_REOPENED from %d to %d naming the reopen at %d", skip, base+60, base+120, base+90)
	}
}
