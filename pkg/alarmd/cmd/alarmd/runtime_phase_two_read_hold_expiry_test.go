package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

func runtimeExpiredZeroBridge(t *testing.T) (*productionReadHolds, *cutoverStallFixture, execution.FrozenQueryGroupSchedule, execution.OwnerFence, execution.QueryGroupIdentity, execution.ScheduleProgress) {
	t.Helper()
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	old := f.queryGroup
	oldSchedule := f.initialSchedule
	installEditedStrategies(t, ctx, f.redisClient, 1725000600, func(first map[string]any) { first["items"].([]any)[0].(map[string]any)["time_delay"] = 120 })
	f.clock.Store((f.base + 59) * 1000)
	for n := 0; n < 2; n++ {
		if err := f.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	var next execution.QueryGroupIdentity
	for _, candidate := range f.bundle.queryGroups {
		schedule, err := f.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, candidate)
		if err == nil && len(schedule.Plans) == 1 && schedule.Plans[0].Key() == oldSchedule.Plans[0].Key() && candidate != old {
			next = candidate
		}
	}
	if next == "" {
		t.Fatal("delay cutover did not create B")
	}
	f.queryGroup = next
	f.runner = settledRunner(f.bundle, next)
	f.session = f.runner.(*settlingRuntime).phaseTwoQueryGroupRuntime.(*productionPhaseTwoQueryGroup).session
	_ = runOneSlotFull(t, f)
	p := f.progress(ctx)
	if p.LastCompletion == nil || p.LastCompletion.Contract.ReadHoldMillis != 0 || p.LastDataSlot != p.LastCompletion.Slot {
		t.Fatalf("no real h0 B completion: %+v", p)
	}
	actual := f.bundle.dependencies.ReadHolds
	control, ok := f.production.dependencies.Store.(readhold.Control)
	if !ok {
		t.Fatalf("no readhold control: %T", f.production.dependencies.Store)
	}
	// A zero hold inherited from a zero predecessor leaves no record: a
	// missing record is what tells a successor the hold was zero.
	loaded, err := control.ReadControlBatch(ctx, []execution.QueryGroupIdentity{next}, productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "schedule")+":"+readhold.Namespace)
	if err != nil || len(loaded) != 1 || !loaded[0].Missing {
		t.Fatalf("B wrote a zero record: %+v %v", loaded, err)
	}
	// Expire the exact local test keys, then jump the fixture clock by the
	// record lifetime. These are isolated fixture Redis keys, never runtime.
	bkey := productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(next) + "}:" + productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	if err := f.redisClient.PExpire(ctx, bkey, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	keys, err := f.redisClient.Keys(ctx, "*"+string(oldSchedule.Segment.Publication.SnapshotRevision)+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	objectKeys, err := f.redisClient.Keys(ctx, "*:qgobj:"+string(oldSchedule.Segment.ObjectDigest)).Result()
	if err != nil {
		t.Fatal(err)
	}
	keys = append(keys, objectKeys...)
	if len(keys) > 0 {
		if err := f.redisClient.Del(ctx, keys...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(5 * time.Millisecond)
	f.clock.Store(f.now().Add(readhold.RecordTTL + time.Hour).UnixMilli())
	slot := execution.EvaluationTime(f.now().Unix() - f.now().Unix()%60)
	last := *p.LastCompletion
	last.Slot = slot
	last.Contract.Slot.EvaluationTime = slot
	last.CompletedAt = f.now().UTC().Format(time.RFC3339Nano)
	p.NextSlot = slot + 60
	p.LastFullSlot = slot
	p.LastDataSlot = slot
	p.LastCompletion = &last
	schedule, err := actual.catalog.ReadFrozenSchedule(ctx, next, slot)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repository.ConfigureObjectCache(1, 1); err != nil {
		t.Fatal(err)
	}
	h, err := newProductionReadHolds(f.cfg, control, f.repository, actual.catalog, f.now, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := ownership.OpenSession(ctx, &fakePhaseTwoOwnershipStore{}, next, "new-owner", f.now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h.bind(next, session)
	lease, _ := session.Current()
	return h, f, schedule, lease.Fence, old, p
}

func TestRuntimeRecentRealZeroCompletionMustNotWaitForExpiredPredecessor(t *testing.T) {
	h, _, schedule, fence, _, _ := runtimeExpiredZeroBridge(t)
	if err := h.PrepareSchedule(context.Background(), schedule, fence); err != nil {
		t.Fatalf("real current h0 completion still blocked on expired predecessor: %v", err)
	}
	if got := h.ReadHold(schedule.Segment.QueryGroup); got != 0 {
		t.Fatalf("restored h=%s, want zero", got)
	}
}

// Past its lifetime a link carries nothing: the old group's record has
// aged out and every deadline the link kept has long passed. However the
// group's own last round ended -- query-free here, which can never prove a
// zero -- it prepares, and the link is counted as expired. It used to be read
// as a predecessor whose zero could not be proven, and the group stopped.
func TestRuntimeALinkPastItsLifetimeNoLongerStopsTheGroup(t *testing.T) {
	h, _, schedule, fence, _, _ := runtimeExpiredZeroBridge(t)
	if err := h.PrepareSchedule(context.Background(), schedule, fence); err != nil {
		t.Fatalf("a group stopped on a link past its lifetime: %v", err)
	}
	h.linksMu.Lock()
	expired := h.links["expired"]
	h.linksMu.Unlock()
	if expired != 1 || len(h.controller.Stats().Predecessors) != 0 {
		t.Fatalf("expired links %d, predecessors read %v: the link was not dropped unread", expired, h.controller.Stats().Predecessors)
	}
}

// A group that already took its predecessor's lateness keeps it in its own
// fenced record, renewed while it holds; the link is not needed again, and
// past its lifetime the old group is not read even if a record of it is.
func TestRuntimeALivePredecessorRecordIsNotReadPastTheLinksLifetime(t *testing.T) {
	h, f, schedule, fence, old, _ := runtimeExpiredZeroBridge(t)
	links, _, err := f.repository.ReadHoldPredecessors(context.Background(), schedule)
	if err != nil || len(links) != 1 {
		t.Fatalf("links=%+v %v", links, err)
	}
	spec, err := h.spec(context.Background(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	oldRecord := readhold.Record{SinceSlot: 1, HoldMillis: 150000, ArrivalAgeMillis: 180000, Closed: true, Plans: []readhold.PlanRecord{{PlanRef: spec.Plans[0], PreviousSlot: links[0].ClosedAt - 59, PreviousHoldMillis: 150000, ClosedAt: links[0].ClosedAt, ClosedQueryGroup: old}}}
	raw, err := json.Marshal(oldRecord)
	if err != nil {
		t.Fatal(err)
	}
	key := productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(old) + "}:" + productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	if err := f.redisClient.Set(context.Background(), key, raw, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err := h.PrepareSchedule(context.Background(), schedule, fence); err != nil {
		t.Fatalf("prepare = %v", err)
	}
	if h.controller.Inspect(old).Loaded {
		t.Fatal("the old group was read past the link's lifetime")
	}
}

// A row states the hold of a group this replica owns as the group page does:
// a record not read yet is unknown, not zero, until the group's first
// prepare reads it; a group it does not own is no row's.
func TestARowStatesAnUnreadHoldAsUnknownAsTheGroupPageDoes(t *testing.T) {
	h, _, schedule, fence, _, _ := runtimeExpiredZeroBridge(t)
	qg := schedule.Segment.QueryGroup
	if millis, unknown, owned := h.rowHold(qg); !owned || !unknown || millis != 0 {
		t.Fatalf("an unread record reads (%d, unknown %v, owned %v), want unknown", millis, unknown, owned)
	}
	if _, known := h.holdOf(string(qg)); known {
		t.Fatal("the group page knows a hold the row does not")
	}
	if err := h.PrepareSchedule(context.Background(), schedule, fence); err != nil {
		t.Fatal(err)
	}
	if millis, unknown, owned := h.rowHold(qg); !owned || unknown || millis != 0 {
		t.Fatalf("a prepared group with no hold reads (%d, unknown %v, owned %v), want a known zero", millis, unknown, owned)
	}
	if _, _, owned := h.rowHold("another-group"); owned {
		t.Fatal("a group this replica does not own was stated")
	}
}
