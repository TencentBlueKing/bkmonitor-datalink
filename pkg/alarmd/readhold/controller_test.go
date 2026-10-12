// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package readhold

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

type memoryControl struct {
	values   map[execution.QueryGroupIdentity][]byte
	requests []ownership.FencedCASRequest
	refused  ownership.FencedCASStatus
	readErr  map[execution.QueryGroupIdentity]error
}

func (store *memoryControl) ReadControlBatch(_ context.Context, groups []execution.QueryGroupIdentity, _ string) ([]ownership.ControlRead, error) {
	reads := make([]ownership.ControlRead, len(groups))
	for index, qg := range groups {
		raw, found := store.values[qg]
		reads[index] = ownership.ControlRead{Raw: append([]byte(nil), raw...), Missing: !found, Err: store.readErr[qg]}
	}
	return reads, nil
}

func (store *memoryControl) FencedCompareAndSet(_ context.Context, request ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	store.requests = append(store.requests, request)
	if store.refused != "" {
		return store.refused, nil
	}
	current, found := store.values[request.Fence.QueryGroup]
	if request.ExpectedMissing == found || !bytes.Equal(request.Expected, current) {
		return ownership.FencedCASConflict, nil
	}
	store.values[request.Fence.QueryGroup] = append([]byte(nil), request.Value...)
	return ownership.FencedCASApplied, nil
}

func controllerFixture(t *testing.T) (*Controller, *memoryControl, *time.Time) {
	t.Helper()
	now := time.Unix(1800, 0)
	store := &memoryControl{values: make(map[execution.QueryGroupIdentity][]byte), readErr: make(map[execution.QueryGroupIdentity]error)}
	controller, err := NewController(Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute, Now: func() time.Time { return now }, Owner: func(qg execution.QueryGroupIdentity) (Owner, error) {
		return Owner{Fence: holdFence(qg), ContentScope: "view"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return controller, store, &now
}

func holdFence(qg execution.QueryGroupIdentity) execution.OwnerFence {
	return execution.OwnerFence{QueryGroup: qg, OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
}

func planRef() PlanRef {
	return PlanRef{Key: execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}}}
}

func groupSpec(qg execution.QueryGroupIdentity, delay time.Duration) GroupSpec {
	return GroupSpec{QueryGroup: qg, Plans: []PlanRef{planRef()}, Delay: delay, SettlingWait: 30 * time.Second, HoldLimit: 10 * time.Minute}
}

// linked is a cutover's link for Plans that left qg at closedAt, by a
// delay-only edit: by default every Plan of spec. With slot the link says the
// Plan's last Slot there and the fixture's 55 s offset; without, the old
// group's closed record says them.
func linked(spec GroupSpec, qg execution.QueryGroupIdentity, closedAt, slot execution.EvaluationTime, plans ...PlanRef) Previous {
	if len(plans) == 0 {
		plans = spec.Plans
	}
	links := make([]PlanLink, 0, len(plans))
	for _, plan := range plans {
		link := PlanLink{PlanRef: plan}
		if slot > 0 {
			link.PreviousSlot, link.CompletionOffsetMillis = slot, 55_000
		}
		links = append(links, link)
	}
	return Previous{QueryGroup: qg, ClosedAt: closedAt, Links: links}
}

func prepare(t *testing.T, controller *Controller, spec GroupSpec) {
	t.Helper()
	if err := controller.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if err := controller.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{spec.QueryGroup}); err != nil {
		t.Fatal(err)
	}
}

func scheduleFor(t *testing.T, qg execution.QueryGroupIdentity, start execution.EvaluationTime, end *execution.EvaluationTime) execution.FrozenQueryGroupSchedule {
	t.Helper()
	spec := execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 55}
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	plans := []execution.FrozenPlanSchedule{{Identity: planRef().Key.PlanIdentity, ScheduleRevision: revision, Spec: spec}}
	groupRevision, err := execution.DeriveQueryGroupScheduleRevision(plans)
	if err != nil {
		t.Fatal(err)
	}
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{Publication: execution.SnapshotPublicationRef{PublicationEpoch: 1, SnapshotRevision: "snapshot"}, QueryGroup: qg, QueryRevision: "query", ScheduleRevision: groupRevision, Start: start, End: end}, Plans: plans}
}

func holdContract(qg execution.QueryGroupIdentity, slot execution.EvaluationTime, hold int64) execution.FrozenExecutionContractRef {
	return execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: qg, EvaluationTime: slot}, SnapshotRevision: "snapshot", QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: "due", ReadHoldMillis: hold}
}

func observeEarly(t *testing.T, c *Controller, qg execution.QueryGroupIdentity, arrival time.Duration, hold int64) {
	t.Helper()
	if err := c.Observe(context.Background(), Evidence{Contract: holdContract(qg, 120, hold), ArrivalAge: arrival, Rung: "rung", Buckets: []int64{120}}); err != nil {
		t.Fatal(err)
	}
}

func TestWholeWindowRaisesButNoiseDoesNot(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	ctx := context.Background()
	if hold, err := c.SlotReadHold(ctx, scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil || hold != 0 {
		t.Fatalf("initial hold %v, %v", hold, err)
	}
	if len(store.requests) != 0 {
		t.Fatal("an initial zero hold wrote Redis")
	}
	observeEarly(t, c, "qg", 180*time.Second, 0)
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatal("arrival age was not translated from the window end")
	}
	before, _ := c.Reading("qg")
	for _, e := range []Evidence{
		{Contract: holdContract("qg", 120, 0), Noise: true},
	} {
		if err := c.Observe(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := c.Reading("qg")
	if c.ReadHold("qg") != 150*time.Second || after.Noise != before.Noise+1 {
		t.Fatalf("noise raised hold: %+v", after)
	}
	spec := groupSpec("qg", 0)
	spec.HoldLimit = 200 * time.Second
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	observeEarly(t, c, "qg", 900*time.Second, 0)
	after, _ = c.Reading("qg")
	if c.ReadHold("qg") != 200*time.Second || !after.AtLimit {
		t.Fatalf("retention margin not enforced: %+v", after)
	}
	spec.HoldLimit = time.Duration(execution.MaxReadHoldMillis) * time.Millisecond
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	observeEarly(t, c, "qg", 900*time.Second, 0)
	if c.ReadHold("qg") != 10*time.Minute {
		t.Fatal("MaxReplayAge hold bound not enforced")
	}
}

func TestLoweringNeedsQuietAndThreeObservedEarlierReads(t *testing.T) {
	c, _, now := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	observeEarly(t, c, "qg", 180*time.Second, 0)
	ctx := context.Background()
	earlier := func(slot execution.EvaluationTime, observed, equal bool) {
		t.Helper()
		if err := c.EarlierRead(ctx, EarlierEvidence{Contract: holdContract("qg", slot, 150_000), CandidateHold: 75 * time.Second, Observed: observed, Equal: equal}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 3 {
		earlier(execution.EvaluationTime(180+index*60), true, true)
	}
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatal("lowered before the single-event quiet hour")
	}
	*now = now.Add(time.Hour)
	earlier(360, false, true)
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatal("an unobserved read lowered the hold")
	}
	earlier(420, true, false)
	*now = now.Add(time.Hour)
	for index := range 2 {
		earlier(execution.EvaluationTime(480+index*60), true, true)
	}
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatal("fewer than three fresh matches lowered the hold")
	}
	earlier(600, true, true)
	if c.ReadHold("qg") != 75*time.Second {
		t.Fatal("three matches after quiet did not halve the hold")
	}
	observeEarly(t, c, "qg", 180*time.Second, 75_000)
	reading, _ := c.Reading("qg")
	if c.ReadHold("qg") != 150*time.Second || reading.RaisedAfterLowering != 1 {
		t.Fatalf("raise after lowering was not counted: %+v", reading)
	}
	*now = now.Add(2 * time.Hour)
	observeEarly(t, c, "qg", 180*time.Second, 150_000)
	reading, _ = c.Reading("qg")
	if reading.MaxEarlyIntervalMillis != 2*time.Hour.Milliseconds() {
		t.Fatalf("event interval %+v", reading)
	}
	*now = now.Add(time.Hour)
	for index := range 3 {
		earlier(execution.EvaluationTime(660+index*60), true, true)
	}
	if c.ReadHold("qg") != 150*time.Second {
		t.Fatal("lowered before the longest observed event interval")
	}
}

func TestTransitionUsesClosedSchedulesLastSlotAndSurvivesRestore(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("old", 0))
	observeEarly(t, c, "old", 180*time.Second, 0)
	ctx := context.Background()
	boundary := execution.EvaluationTime(1200)
	newSpec := groupSpec("new", 120*time.Second)
	newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
	prepare(t, c, newSpec)
	newSchedule := scheduleFor(t, "new", boundary, nil)
	oldSchedule := scheduleFor(t, "old", 60, &boundary)
	if err := c.CloseSchedule(ctx, oldSchedule, holdFence("old")); err != nil {
		t.Fatal(err)
	}
	oldReading, _ := c.Reading("old")
	if oldReading.Plans[0].PreviousSlot != 1140 || oldReading.Plans[0].PreviousHoldMillis != 150_000 {
		t.Fatalf("closed record %+v", oldReading)
	}
	observeEarly(t, c, "old", 600*time.Second, 0)
	oldReading, _ = c.Reading("old")
	if c.ReadHold("old") != 150*time.Second || oldReading.Plans[0].PreviousHoldMillis != 150_000 {
		t.Fatal("a closed group raised or rewrote its transition hold")
	}
	// The transition keeps the old group's last deadline; the old arrival
	// age does not carry over, so after it the new group reads at zero until
	// it learns its own.
	for index, want := range []time.Duration{115 * time.Second, 55 * time.Second, 0} {
		slot := boundary + execution.EvaluationTime(index*60)
		got, err := c.SlotReadHold(ctx, newSchedule, slot, holdFence("new"))
		if err != nil || got != want {
			t.Fatalf("Slot %d hold %s, %v; want %s", slot, got, err, want)
		}
		if index == 0 && int64(slot)*1000+30_000+got.Milliseconds() < int64(1140)*1000+150_000+55_000 {
			t.Fatal("new read preceded old completion deadline")
		}
	}
	// A fresh controller recovers both the learned base and transition from
	// the same record before it considers any replay.
	next, err := NewController(c.options)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Configure(newSpec); err != nil {
		t.Fatal(err)
	}
	if err := next.RestoreBatch(ctx, []execution.QueryGroupIdentity{"new", "old"}); err != nil {
		t.Fatal(err)
	}
	if record := next.Inspect("new").Record; len(record.Plans) != 1 || record.Plans[0].InheritedQueryGroup != "old" ||
		record.Plans[0].InheritedClosedAt != boundary || next.ReadHoldAt("new", 1320) != 0 {
		t.Fatalf("takeover did not restore the bridge it would otherwise resolve again: %+v", record)
	}
	farSpec := groupSpec("far", 300*time.Second)
	farSpec.Previous = []Previous{linked(farSpec, "old", boundary, 0)}
	prepare(t, c, farSpec)
	if got, err := c.SlotReadHold(ctx, scheduleFor(t, "far", 1500, nil), 1500, holdFence("far")); err != nil || got != 0 {
		t.Fatalf("longer delay did not replace automatic hold: %s %v", got, err)
	}
	if store.requests[len(store.requests)-1].TTL != RecordTTL {
		t.Fatal("record lifetime is not seven days")
	}
}

func TestFencedRefusalChangesNoMemoryAndRenewalIsDaily(t *testing.T) {
	c, store, now := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	observeEarly(t, c, "qg", 180*time.Second, 0)
	before, _ := c.Reading("qg")
	store.refused = ownership.FencedCASStaleOwner
	if err := c.Observe(context.Background(), Evidence{Contract: holdContract("qg", 180, 0), ArrivalAge: 240 * time.Second}); !errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("stale fence=%v", err)
	}
	after, _ := c.Reading("qg")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale writer changed local hold")
	}
	store.refused = ""
	writes := len(store.requests)
	*now = now.Add(23 * time.Hour)
	if err := c.RenewDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.requests) != writes {
		t.Fatal("renewed before a day")
	}
	*now = now.Add(time.Hour)
	if err := c.RenewDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.requests) != writes+1 || store.requests[len(store.requests)-1].TTL != RecordTTL {
		t.Fatal("daily renewal did not retain seven days")
	}
	store.readErr["broken"] = errors.New("unanswered")
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"qg", "broken", "missing"}); err == nil {
		t.Fatal("unanswered restore was reported complete")
	}
	if !c.Inspect("qg").Loaded || c.Inspect("broken").Loaded || !c.Inspect("missing").Missing {
		t.Fatal("batch restore confused success, failure and missing")
	}
}

func TestSharedGroupReopensWhileMigratedPlanKeepsClosedHold(t *testing.T) {
	c, _, _ := controllerFixture(t)
	a, b := planRef(), planRef()
	b.Key.StrategyID = "retained"
	oldSpec := groupSpec("old", 0)
	oldSpec.Plans = []PlanRef{a, b}
	prepare(t, c, oldSpec)
	observeEarly(t, c, "old", 180*time.Second, 0)
	boundary := execution.EvaluationTime(1200)
	oldSchedule := scheduleFor(t, "old", 60, &boundary)
	retainedPlan := oldSchedule.Plans[0]
	retainedPlan.Identity = b.Key.PlanIdentity
	oldSchedule.Plans = append(oldSchedule.Plans, retainedPlan)
	oldSchedule.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(oldSchedule.Plans)
	if err := c.CloseSchedule(context.Background(), oldSchedule, holdFence("old")); err != nil {
		t.Fatal(err)
	}
	closed, _ := c.Reading("old")
	fixed := closed.Plans[0]
	if fixed.ClosedAt != boundary || fixed.PreviousHoldMillis != 150_000 {
		t.Fatalf("Plan A was not closed: %+v", fixed)
	}
	// Only A moves to the new delay. The same old Query Group starts a new
	// schedule segment for B and must still learn later whole-window reads.
	retainedSpec := oldSpec
	retainedSpec.Plans = []PlanRef{b}
	if err := c.Configure(retainedSpec); err != nil {
		t.Fatal(err)
	}
	retainedSchedule := scheduleFor(t, "old", boundary, nil)
	retainedSchedule.Plans[0].Identity = b.Key.PlanIdentity
	retainedSchedule.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(retainedSchedule.Plans)
	if got, err := c.SlotReadHold(context.Background(), retainedSchedule, boundary, holdFence("old")); err != nil || got != 150*time.Second {
		t.Fatalf("reopened group = %s %v", got, err)
	}
	newContract := holdContract("old", 1260, 150_000)
	newContract.ScheduleSegmentStart = boundary
	if err := c.Observe(context.Background(), Evidence{Contract: newContract, ArrivalAge: 330 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if c.ReadHold("old") != 300*time.Second {
		t.Fatal("retained Plan could not raise h after the shared group reopened")
	}
	observeEarly(t, c, "old", 600*time.Second, 0)
	oldReading, _ := c.Reading("old")
	if oldReading.Closed || oldReading.SegmentStart != boundary || oldReading.Plans[0] != fixed || c.ReadHold("old") != 300*time.Second {
		t.Fatalf("old evidence altered a migrated Plan's fixed bridge: %+v", oldReading)
	}
	newSpec := groupSpec("new", 120*time.Second)
	newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
	prepare(t, c, newSpec)
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", boundary, nil), boundary, holdFence("new")); err != nil || got != 115*time.Second {
		t.Fatalf("migrated Plan used the reopened group's h: %s %v", got, err)
	}
}

// A successor reads only its predecessor's record. None is zero: a nonzero
// hold always has one, so a group alarmd never held reads at once after an
// edit. A record open, corrupt or unread is the hold bound -- a later read
// for the successor's first Slots, never a refusal.
func TestAPredecessorRecordGivesZeroOrTheBoundAndNeverAnError(t *testing.T) {
	boundary := execution.EvaluationTime(1200)
	for _, tc := range []struct {
		name   string
		store  func(*memoryControl)
		want   time.Duration
		reason string
	}{
		{"missing", func(*memoryControl) {}, 0, PredecessorZero},
		// 1140 s + 600 s bound + 55 s offset, read at 1200 s + 30 s wait.
		{"open", func(store *memoryControl) {
			store.values["old"], _ = json.Marshal(Record{SinceSlot: 1, HoldMillis: 150_000, SegmentStart: 60})
		}, 565 * time.Second, PredecessorRecordOpen},
		{"corrupt", func(store *memoryControl) { store.values["old"] = []byte("{not a record") }, 565 * time.Second, PredecessorRecordCorrupt},
		{"unread", func(store *memoryControl) { store.readErr["old"] = errors.New("unanswered") }, 565 * time.Second, PredecessorUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			tc.store(store)
			_ = c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"})
			newSpec := groupSpec("new", 0)
			newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 1140)}
			prepare(t, c, newSpec)
			schedule := scheduleFor(t, "new", boundary, nil)
			if got, err := c.SlotReadHold(context.Background(), schedule, boundary, holdFence("new")); err != nil || got != tc.want {
				t.Fatalf("first Slot after a %s predecessor record = %s %v; want %s", tc.name, got, err, tc.want)
			}
			if got := c.Stats().Predecessors; got[tc.reason] != 1 || len(got) != 1 {
				t.Fatalf("counted %v, want one %s", got, tc.reason)
			}
			if raw, written := store.values["old"]; tc.name != "corrupt" && tc.name != "open" && written {
				t.Fatalf("new owner wrote the old record: %s", raw)
			}
		})
	}
	// A reused Query Group record that predates this cutover must seed again;
	// having any record does not establish this Plan's newest closed bridge.
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("missing", 0))
	newSpec := groupSpec("new", 0)
	newSpec.Previous = []Previous{linked(newSpec, "missing", boundary, 1140)}
	existing := Record{HoldMillis: 150_000, SinceSlot: 60, SegmentStart: 60}
	store.values["new"], _ = json.Marshal(existing)
	prepare(t, c, newSpec)
	schedule := scheduleFor(t, "new", boundary, nil)
	if got, err := c.SlotReadHold(context.Background(), schedule, boundary, holdFence("new")); err != nil || got != 150*time.Second {
		t.Fatalf("existing active h = %s %v", got, err)
	}
	reading, _ := c.Reading("new")
	if len(reading.Plans) == 0 || reading.Plans[0].ClosedAt != boundary {
		t.Fatal("the reused record skipped the newest bridge")
	}
}

func TestEvidenceWriteCannotBypassNewPlansPredecessorSeed(t *testing.T) {
	c, store, _ := controllerFixture(t)
	a, b := planRef(), planRef()
	b.Key.StrategyID = "incoming"
	prepare(t, c, groupSpec("existing", 120*time.Second))
	observeEarly(t, c, "existing", 180*time.Second, 0)
	if c.ReadHold("existing") != 30*time.Second {
		t.Fatal("existing group's learned base")
	}
	spec := groupSpec("existing", 120*time.Second)
	spec.Plans = []PlanRef{a, b}
	spec.Previous = []Previous{linked(spec, "previous", 1200, 0, b)}
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"previous"}); err != nil {
		t.Fatal(err)
	}
	// A previous-segment sample completes after Configure but before the new
	// segment freezes. Its durable metrics cannot acknowledge an unread bridge.
	if err := c.Observe(context.Background(), Evidence{Contract: holdContract("existing", 180, 0), Noise: true}); err != nil {
		t.Fatal(err)
	}
	schedule := scheduleFor(t, "existing", 1200, nil)
	second := schedule.Plans[0]
	second.Identity = b.Key.PlanIdentity
	schedule.Plans = append(schedule.Plans, second)
	schedule.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(schedule.Plans)
	// The predecessor's closed record arrives only now. Had the noise write
	// acknowledged the bridge, the seed would skip it and read 30 s.
	previous := Record{HoldMillis: 150_000, SinceSlot: 60, SegmentStart: 60, Closed: true, Plans: []PlanRecord{{PlanRef: b,
		ClosedAt: 1200, PreviousHoldMillis: 150_000, PreviousSlot: 1140, CompletionOffsetMillis: 55_000}}}
	store.values["previous"], _ = json.Marshal(previous)
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"previous"}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.SlotReadHold(context.Background(), schedule, 1200, holdFence("existing")); err != nil || got != 115*time.Second {
		t.Fatalf("incoming Plan did not extend first read to the old completion deadline: %s %v", got, err)
	}
}

func TestCloseScheduleProtectsFinalFrozenHoldAndFuturePendingRaise(t *testing.T) {
	for _, arm := range []struct {
		name      string
		since     execution.EvaluationTime
		pending   int64
		arrival   int64
		closed    int64
		firstHold time.Duration
	}{
		{name: "frozen final Slot before pending lowering", since: 1140, pending: 75_000, arrival: 105_000, closed: 150_000, firstHold: 115 * time.Second},
		{name: "future final Slot after pending raise", since: 1080, pending: 300_000, arrival: 330_000, closed: 300_000, firstHold: 265 * time.Second},
	} {
		t.Run(arm.name, func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			pending := arm.pending
			old := Record{HoldMillis: 150_000, SinceSlot: arm.since, PreviousHoldMillis: 150_000, PreviousSinceSlot: 60,
				PendingHoldMillis: &pending, ArrivalAgeMillis: arm.arrival, SegmentStart: 60, Plans: []PlanRecord{{PlanRef: planRef()}}}
			store.values["old"], _ = json.Marshal(old)
			prepare(t, c, groupSpec("old", 0))
			boundary := execution.EvaluationTime(1200)
			if err := c.CloseSchedule(context.Background(), scheduleFor(t, "old", 60, &boundary), holdFence("old")); err != nil {
				t.Fatal(err)
			}
			closed, _ := c.Reading("old")
			if closed.Plans[0].PreviousHoldMillis != arm.closed {
				t.Fatalf("closed hold = %d, want %d", closed.Plans[0].PreviousHoldMillis, arm.closed)
			}
			newSpec := groupSpec("new", 120*time.Second)
			newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
			prepare(t, c, newSpec)
			hold, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", boundary, nil), boundary, holdFence("new"))
			if err != nil || hold != arm.firstHold {
				t.Fatalf("new Slot hold = %s %v, want %s", hold, err, arm.firstHold)
			}
			if int64(boundary)*1000+30_000+hold.Milliseconds() < int64(1140)*1000+arm.closed+55_000 {
				t.Fatal("new first read preceded the final old Slot's completion deadline")
			}
		})
	}
}

func TestInheritedAckSurvivesClosingAndReopeningTheSameGroup(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("a", 0))
	observeEarly(t, c, "a", 180*time.Second, 0)
	first := execution.EvaluationTime(1200)
	if err := c.CloseSchedule(context.Background(), scheduleFor(t, "a", 60, &first), holdFence("a")); err != nil {
		t.Fatal(err)
	}
	spec := groupSpec("b", 120*time.Second)
	spec.Previous = []Previous{linked(spec, "a", first, 0)}
	prepare(t, c, spec)
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "b", first, nil), first, holdFence("b")); err != nil {
		t.Fatal(err)
	}
	second := execution.EvaluationTime(1500)
	if err := c.CloseSchedule(context.Background(), scheduleFor(t, "b", first, &second), holdFence("b")); err != nil {
		t.Fatal(err)
	}
	reading, _ := c.Reading("b")
	if plan := reading.Plans[0]; plan.ClosedQueryGroup != "b" || plan.ClosedAt != second || plan.InheritedQueryGroup != "a" || plan.InheritedClosedAt != first {
		t.Fatalf("a closure overwrote the inherited acknowledgment: %+v", plan)
	}
	delete(store.values, "a")
	c.Forget("a")
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "b", second, nil), second, holdFence("b")); err != nil || got != 0 {
		t.Fatalf("reopening depended on the expired first predecessor: %s %v", got, err)
	}
	// The acknowledgment is durable; takeover does not need A's old record.
	restored, _ := NewController(c.options)
	if err := restored.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"b"}); err != nil {
		t.Fatal(err)
	}
	if got, err := restored.SlotReadHold(context.Background(), scheduleFor(t, "b", second, nil), second+60, holdFence("b")); err != nil || got != 0 {
		t.Fatalf("restored acknowledgment = %s %v", got, err)
	}
	if got := restored.Stats().Predecessors; len(got) != 0 {
		t.Fatalf("takeover read the first predecessor again: %v", got)
	}
}

func TestUnknownSlowPlansFrozenHoldUsesTheConfiguredBound(t *testing.T) {
	c, store, _ := controllerFixture(t)
	fast, slow := planRef(), planRef()
	slow.Key.StrategyID = "slow"
	pending := int64(0)
	record := Record{HoldMillis: 75_000, SinceSlot: 1080, PreviousHoldMillis: 150_000, PreviousSinceSlot: 960,
		PendingHoldMillis: &pending, ArrivalAgeMillis: 30_000, SegmentStart: 60, Plans: []PlanRecord{{PlanRef: fast}, {PlanRef: slow}}}
	store.values["old"], _ = json.Marshal(record)
	spec := groupSpec("old", 0)
	spec.Plans = []PlanRef{fast, slow}
	prepare(t, c, spec)
	boundary := execution.EvaluationTime(1200)
	old := scheduleFor(t, "old", 60, &boundary)
	slowPlan := old.Plans[0]
	slowPlan.Identity = slow.Key.PlanIdentity
	slowPlan.Spec.EvaluationIntervalSeconds, slowPlan.Spec.CompletionDeadlineOffsetSeconds = 600, 595
	slowPlan.ScheduleRevision, _ = execution.DerivePlanScheduleRevision(slowPlan.Spec)
	old.Plans = append(old.Plans, slowPlan)
	old.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(old.Plans)
	if err := c.CloseSchedule(context.Background(), old, holdFence("old")); err != nil {
		t.Fatal(err)
	}
	closed, _ := c.Reading("old")
	if plan := closed.Plans[1]; !plan.PreviousHoldUnknown || plan.PreviousSlot != 600 || plan.PreviousHoldMillis != c.options.MaxHold.Milliseconds() {
		t.Fatalf("unknown old hold was understated: %+v", plan)
	}
	newSpec := groupSpec("new", 0)
	newSpec.Plans = spec.Plans
	newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
	prepare(t, c, newSpec)
	newSchedule := old
	newSchedule.Segment.QueryGroup, newSchedule.Segment.Start, newSchedule.Segment.End = "new", boundary, nil
	if got, err := c.SlotReadHold(context.Background(), newSchedule, boundary, holdFence("new")); err != nil || got != 565*time.Second {
		t.Fatalf("new read lost the conservative old deadline: %s %v", got, err)
	}
}

// A group with no stored record never held: a nonzero hold is always
// written. Restarted inside its Segment and closed before a slow Plan's next
// Slot, that Plan's final Slot is before the restart, and its hold there is
// still known -- zero -- because the restart's record starts from the first
// Slot, not from the restart. The close writes nothing, which is what tells
// the successor the hold was zero.
func TestARestartedGroupThatNeverHeldClosesAtAKnownZero(t *testing.T) {
	c, store, _ := controllerFixture(t)
	fast, slow := planRef(), planRef()
	slow.Key.StrategyID = "slow"
	spec := groupSpec("old", 0)
	spec.Plans = []PlanRef{fast, slow}
	prepare(t, c, spec)
	boundary := execution.EvaluationTime(1200)
	old := scheduleFor(t, "old", 60, &boundary)
	slowPlan := old.Plans[0]
	slowPlan.Identity = slow.Key.PlanIdentity
	slowPlan.Spec.EvaluationIntervalSeconds, slowPlan.Spec.CompletionDeadlineOffsetSeconds = 600, 595
	slowPlan.ScheduleRevision, _ = execution.DerivePlanScheduleRevision(slowPlan.Spec)
	old.Plans = append(old.Plans, slowPlan)
	old.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(old.Plans)
	open := old
	open.Segment.End = nil
	if got, err := c.SlotReadHold(context.Background(), open, 1140, holdFence("old")); err != nil || got != 0 {
		t.Fatalf("restart read = %s %v, want a zero hold", got, err)
	}
	if err := c.CloseSchedule(context.Background(), old, holdFence("old")); err != nil {
		t.Fatal(err)
	}
	if _, written := store.values["old"]; written {
		t.Fatal("a group that never held wrote a record at its close")
	}
	closed, _ := c.Reading("old")
	for _, plan := range closed.Plans {
		if plan.Key == slow.Key && (plan.PreviousHoldUnknown || plan.PreviousHoldMillis != 0 || plan.PreviousSlot != 600) {
			t.Fatalf("slow Plan closed as %+v, want its final Slot 600 at a known zero", plan)
		}
	}
}

// A fresh record is written when it carries anything a successor or a
// restart needs, a Plan's closing fact included. No path today closes a
// Plan with a nonzero or unknown fact on a record never written -- the test
// above is why -- so the rule is held here directly: dropping a Plan's
// unknown or nonzero fact would have a successor read the missing record as
// zero, the early direction.
func TestARecordCarriesAPlansClosingFact(t *testing.T) {
	for name, record := range map[string]Record{
		"unknown": {SinceSlot: 1, Plans: []PlanRecord{{PlanRef: planRef(), PreviousHoldUnknown: true}}},
		"nonzero": {SinceSlot: 1, Plans: []PlanRecord{{PlanRef: planRef(), PreviousHoldMillis: 30_000}}},
	} {
		if !carries(record) {
			t.Errorf("%s: a record whose only fact is a Plan's closing hold is not carried", name)
		}
	}
	if carries(Record{SinceSlot: 1, Plans: []PlanRecord{{PlanRef: planRef(), PreviousSlot: 600}}}) {
		t.Error("a record with nothing but a zero closing hold is carried")
	}
}

// Configure refuses a Plan without a complete identity, and Observe refuses
// a negative arrival age: neither is a value the hold can be computed from.
func TestAnIncompletePlanAndANegativeArrivalAreRefused(t *testing.T) {
	c, _, _ := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Plans[0].Key.StrategyID = ""
	if err := c.Configure(spec); err == nil {
		t.Fatal("a Plan without a strategy was configured")
	}
	prepare(t, c, groupSpec("qg", 0))
	if err := c.Observe(context.Background(), Evidence{Contract: holdContract("qg", 120, 0), ArrivalAge: -time.Second}); err == nil {
		t.Fatal("a negative arrival age was observed")
	}
}

func TestExpiredDepartedPlansAndSatisfiedTransitionsArePruned(t *testing.T) {
	c, store, now := controllerFixture(t)
	active, departed := planRef(), planRef()
	departed.Key.StrategyID = "departed"
	*now = now.Add(8 * 24 * time.Hour)
	slot := execution.EvaluationTime(now.Unix() / 60 * 60)
	record := Record{HoldMillis: 30_000, SinceSlot: 600, SegmentStart: 60, Plans: []PlanRecord{
		{PlanRef: active, ClosedAt: 1200, ClosedQueryGroup: "previous", InheritedQueryGroup: "previous", InheritedClosedAt: 1200},
		{PlanRef: departed, ClosedAt: 1500, ClosedQueryGroup: "qg"},
	}, Transitions: []Transition{{Key: active.Key, DeadlineMillis: int64(slot-60)*1000 + 30_000}, {Key: active.Key, DeadlineMillis: int64(slot+120)*1000 + 30_000}}}
	store.values["qg"], _ = json.Marshal(record)
	prepare(t, c, groupSpec("qg", 0))
	schedule := scheduleFor(t, "qg", 60, nil)
	if got, err := c.SlotReadHold(context.Background(), schedule, slot, holdFence("qg")); err != nil || got != 2*time.Minute {
		t.Fatalf("active transition = %s %v", got, err)
	}
	pruned, _ := c.Reading("qg")
	if len(pruned.Plans) != 1 || pruned.Plans[0].InheritedQueryGroup != "previous" || pruned.Plans[0].InheritedClosedAt != 1200 || len(pruned.Transitions) != 1 {
		t.Fatalf("pruning lost the active acknowledgment or kept retired entries: %+v", pruned)
	}
	if got, err := c.SlotReadHold(context.Background(), schedule, slot+180, holdFence("qg")); err != nil || got != 30*time.Second {
		t.Fatalf("satisfied transition = %s %v", got, err)
	}
	pruned, _ = c.Reading("qg")
	if len(pruned.Transitions) != 0 {
		t.Fatal("a past completion deadline stayed in the active record")
	}
}

func TestZeroSlotSegmentClosesItsBridgeWithoutErasingAnEarlierDeadline(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		t.Run(map[bool]string{false: "new group without any Slot", true: "earlier closed completion remains"}[earlier], func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			if earlier {
				record := Record{HoldMillis: 150_000, SinceSlot: 60, PreviousSinceSlot: 1, ArrivalAgeMillis: 180_000, SegmentStart: 60,
					Plans: []PlanRecord{{PlanRef: planRef(), ClosedQueryGroup: "old", ClosedAt: 61,
						PreviousSlot: 60, PreviousHoldMillis: 150_000, CompletionOffsetMillis: 55_000,
						InheritedQueryGroup: "ancestor", InheritedClosedAt: 50}}}
				store.values["old"], _ = json.Marshal(record)
			}
			prepare(t, c, groupSpec("old", 0))
			end := execution.EvaluationTime(90)
			old := scheduleFor(t, "old", 61, &end)
			if _, found := old.FirstSlot(); found {
				t.Fatal("fixture has a Slot in its supposedly empty segment")
			}
			if err := c.CloseSchedule(context.Background(), old, holdFence("old")); err != nil {
				t.Fatal(err)
			}
			closed, _ := c.Reading("old")
			if len(closed.Plans) != 1 || closed.Plans[0].ClosedAt != end || closed.Plans[0].ClosedQueryGroup != "old" {
				t.Fatalf("empty segment never closed its Plan bridge: %+v", closed)
			}
			newSpec := groupSpec("new", 120*time.Second)
			newSpec.Previous = []Previous{linked(newSpec, "old", end, 0)}
			prepare(t, c, newSpec)
			want := time.Duration(0)
			if earlier {
				want = 115 * time.Second
				if plan := closed.Plans[0]; plan.PreviousSlot != 60 || plan.InheritedQueryGroup != "ancestor" || plan.InheritedClosedAt != 50 {
					t.Fatalf("empty segment erased an earlier fact: %+v", plan)
				}
			} else if closed.Plans[0].PreviousSlot != 0 {
				t.Fatal("empty segment invented a predecessor Slot")
			}
			if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", end, nil), 120, holdFence("new")); err != nil || got != want {
				t.Fatalf("empty predecessor = %s %v; want %s", got, err, want)
			}
		})
	}
}

func TestClosingAnUnseededEmptyMiddleGroupInheritsItsPredecessor(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("a", 0))
	observeEarly(t, c, "a", 180*time.Second, 0)
	first := execution.EvaluationTime(1201)
	if err := c.CloseSchedule(context.Background(), scheduleFor(t, "a", 60, &first), holdFence("a")); err != nil {
		t.Fatal(err)
	}
	bSpec := groupSpec("b", 120*time.Second)
	bSpec.Previous = []Previous{linked(bSpec, "a", first, 0)}
	prepare(t, c, bSpec)
	second := execution.EvaluationTime(1230)
	bSchedule := scheduleFor(t, "b", first, &second)
	// B closes without ever freezing a Slot or calling SlotReadHold.
	if err := c.CloseSchedule(context.Background(), bSchedule, holdFence("b")); err != nil {
		t.Fatal(err)
	}
	b, _ := c.Reading("b")
	if plan := b.Plans[0]; plan.PreviousSlot != 1200 || plan.PreviousHoldMillis != 150_000 || plan.InheritedQueryGroup != "a" || plan.InheritedClosedAt != first {
		t.Fatalf("empty middle group never inherited its bridge: %+v", plan)
	}
	cSpec := groupSpec("c", 300*time.Second)
	cSpec.Previous = []Previous{linked(cSpec, "b", second, 0)}
	prepare(t, c, cSpec)
	for index, want := range []time.Duration{115 * time.Second, 55 * time.Second, 0} {
		slot := execution.EvaluationTime(1260 + index*60)
		if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "c", second, nil), slot, holdFence("c")); err != nil || got != want {
			t.Fatalf("A-to-empty-B-to-C Slot %d = %s %v; want %s", slot, got, err, want)
		}
	}
}

func TestChangedReadinessBaselineReprojectsTheConfirmedArrivalAge(t *testing.T) {
	for _, ack := range []bool{false, true} {
		t.Run(map[bool]string{false: "without predecessor", true: "acknowledged predecessor is unavailable"}[ack], func(t *testing.T) {
			c, store, now := controllerFixture(t)
			record := Record{HoldMillis: 150_000, SinceSlot: 120, PreviousSinceSlot: 1, ArrivalAgeMillis: 180_000,
				SegmentStart: 60, QuietSinceMillis: 1000_000, EarlierMatches: 2}
			spec := groupSpec("qg", 0)
			if ack {
				spec.Previous = []Previous{linked(spec, "old", 60, 0)}
				record.Plans = []PlanRecord{{PlanRef: planRef(),
					InheritedQueryGroup: "old", InheritedClosedAt: 60}}
			}
			store.values["qg"], _ = json.Marshal(record)
			prepare(t, c, spec)
			for index, baseline := range []struct {
				wait time.Duration
				want time.Duration
			}{{10 * time.Second, 170 * time.Second}, {60 * time.Second, 120 * time.Second}} {
				spec.SettlingWait = baseline.wait
				if err := c.Configure(spec); err != nil {
					t.Fatal(err)
				}
				slot := execution.EvaluationTime(180 + index*60)
				if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), slot, holdFence("qg")); err != nil || got != baseline.want {
					t.Fatalf("changed wait %s: hold=%s err=%v; want %s", baseline.wait, got, err, baseline.want)
				}
				got, _ := c.Reading("qg")
				if got.ArrivalAgeMillis != 180_000 || got.EarlierMatches != 0 || got.QuietSinceMillis != now.UnixMilli() {
					t.Fatalf("changed baseline lost A or retained old decrease evidence: %+v", got)
				}
			}
		})
	}
}

func TestRestoreWithTheSameBaselinePreservesQuietEvidence(t *testing.T) {
	c, store, _ := controllerFixture(t)
	record := Record{HoldMillis: 150_000, SinceSlot: 120, PreviousSinceSlot: 1, ArrivalAgeMillis: 180_000,
		SegmentStart: 60, QuietSinceMillis: 1000_000, EarlierMatches: 2}
	store.values["qg"], _ = json.Marshal(record)
	prepare(t, c, groupSpec("qg", 0))
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 180, holdFence("qg")); err != nil || got != 150*time.Second {
		t.Fatalf("restored hold=%s err=%v", got, err)
	}
	got, _ := c.Reading("qg")
	if got.EarlierMatches != record.EarlierMatches || got.QuietSinceMillis != record.QuietSinceMillis {
		t.Fatalf("unchanged readiness baseline reset persisted quiet evidence: %+v", got)
	}
}

func TestAReleasedPredecessorStaysAbsentAfterSuccessfulInheritance(t *testing.T) {
	c, _, _ := controllerFixture(t)
	if inspection := c.Inspect("old"); inspection.Loaded || len(c.groups) != 0 {
		t.Fatal("inspecting an absent group allocated control state")
	}
	prepare(t, c, groupSpec("old", 0))
	observeEarly(t, c, "old", 180*time.Second, 0)
	end := execution.EvaluationTime(90)
	if err := c.CloseSchedule(context.Background(), scheduleFor(t, "old", 60, &end), holdFence("old")); err != nil {
		t.Fatal(err)
	}
	spec := groupSpec("new", 120*time.Second)
	spec.Previous = []Previous{linked(spec, "old", end, 0)}
	prepare(t, c, spec)
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", end, nil), 120, holdFence("new")); err != nil || got != 115*time.Second {
		t.Fatalf("first inherited hold=%s err=%v", got, err)
	}
	c.Forget("old")
	assertReleased := func() {
		t.Helper()
		if c.Inspect("old").Loaded || len(c.groups) != 1 {
			t.Fatal("the released predecessor was recreated")
		}
	}
	assertReleased()
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", end, nil), 180, holdFence("new")); err != nil || got != 55*time.Second {
		t.Fatalf("seeded next hold=%s err=%v", got, err)
	}
	assertReleased()
	// A new configuration and an owner restore both use the durable ACK,
	// without reloading or retaining the old Query Group's record.
	spec.SettlingWait = 10 * time.Second
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"new"}); err != nil {
		t.Fatal(err)
	}
	// 60 s + 150 s + 55 s, read at 240 s + 10 s wait.
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", end, nil), 240, holdFence("new")); err != nil || got != 15*time.Second {
		t.Fatalf("restored ACK hold=%s err=%v", got, err)
	}
	assertReleased()
	closed := execution.EvaluationTime(300)
	if err := c.CloseSchedule(context.Background(), scheduleFor(t, "new", end, &closed), holdFence("new")); err != nil {
		t.Fatal(err)
	}
	assertReleased()
}

// A transition that asks for more than the group's hold limit is frozen at
// the limit -- the same on every retry of the Slot, which used to be refused
// on every retry -- and counted, a known hold apart from the bound.
func TestATransitionOverTheLimitIsFrozenAtTheLimitAndCounted(t *testing.T) {
	boundary := execution.EvaluationTime(1200)
	for _, tc := range []struct {
		name   string
		record *Record
		source string
	}{
		// 1140 s + 590 s + 200 s, read at 1200 s + 30 s wait: 700 s > 600 s.
		{"known", &Record{SinceSlot: 1, SegmentStart: 60, Closed: true, Plans: []PlanRecord{{PlanRef: planRef(), ClosedAt: boundary,
			ClosedQueryGroup: "old", PreviousSlot: 1140, PreviousHoldMillis: 590_000, CompletionOffsetMillis: 200_000}}}, ClampKnown},
		// An open record is the 600 s bound: 1140 s + 600 s + 200 s.
		{"fallback", &Record{SinceSlot: 1, HoldMillis: 1_000, SegmentStart: 60}, ClampFallback},
		// The old owner closed with its own bound for a Slot its record no
		// longer covered: still the bound, not a known hold.
		{"inherited bound", &Record{SinceSlot: 1, SegmentStart: 60, Closed: true, Plans: []PlanRecord{{PlanRef: planRef(), ClosedAt: boundary,
			ClosedQueryGroup: "old", PreviousSlot: 1140, PreviousHoldMillis: 600_000, PreviousHoldUnknown: true, CompletionOffsetMillis: 200_000}}}, ClampFallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			store.values["old"], _ = json.Marshal(tc.record)
			if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"}); err != nil {
				t.Fatal(err)
			}
			newSpec := groupSpec("new", 0)
			newSpec.Previous = []Previous{{QueryGroup: "old", ClosedAt: boundary, Links: []PlanLink{{PlanRef: planRef(), PreviousSlot: 1140, CompletionOffsetMillis: 200_000}}}}
			prepare(t, c, newSpec)
			schedule := scheduleFor(t, "new", boundary, nil)
			for retry := 0; retry < 2; retry++ {
				if got, err := c.SlotReadHold(context.Background(), schedule, boundary, holdFence("new")); err != nil || got != 10*time.Minute {
					t.Fatalf("retry %d of an over-limit Slot = %s %v; want the 10 m limit", retry, got, err)
				}
			}
			if got := c.Stats().Clamped; got[tc.source] == 0 || len(got) != 1 {
				t.Fatalf("clamped %v, want %s only", got, tc.source)
			}
		})
	}
}

// Below one step of the group's period a lowering goes to zero: the early
// read tries zero, and the controller takes nothing else.
func TestALoweringBelowOneStepGoesToZero(t *testing.T) {
	c, _, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 105*time.Second, 0)
	if c.ReadHold("qg") != 75*time.Second {
		t.Fatalf("raised to %s, want 75 s", c.ReadHold("qg"))
	}
	*now = now.Add(time.Hour)
	earlier := func(slot execution.EvaluationTime, candidate time.Duration) {
		t.Helper()
		if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", slot, 75_000), CandidateHold: candidate, Observed: true, Equal: true}); err != nil {
			t.Fatal(err)
		}
	}
	// Half of 75 s is less than a step: a read at 37.5 s is not the step.
	for index := range 3 {
		earlier(execution.EvaluationTime(180+index*60), 37500*time.Millisecond)
	}
	if c.ReadHold("qg") != 75*time.Second {
		t.Fatal("a half-step candidate was taken")
	}
	for index := range 3 {
		earlier(execution.EvaluationTime(360+index*60), 0)
	}
	if c.ReadHold("qg") != 0 {
		t.Fatalf("lowering below one step kept %s", c.ReadHold("qg"))
	}
}

// A group at zero that closes a Segment, or freezes a Slot of a closed one,
// writes no record: a missing record is what tells its successor its hold
// was zero.
func TestAZeroGroupClosingASegmentWritesNoRecord(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	end := execution.EvaluationTime(300)
	closed := scheduleFor(t, "qg", 60, &end)
	if got, err := c.SlotReadHold(context.Background(), closed, 240, holdFence("qg")); err != nil || got != 0 {
		t.Fatalf("a zero Slot of a closed Segment = %s %v", got, err)
	}
	if err := c.CloseSchedule(context.Background(), closed, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if len(store.requests) != 0 || len(store.values) != 0 {
		t.Fatalf("a zero group wrote %d records", len(store.requests))
	}
}

// A lowered hold stays lowered on a restore: the arrival age is lowered with
// it, or the reseed raises the hold again on every takeover.
func TestALoweredHoldStaysLoweredAfterRestore(t *testing.T) {
	c, _, now := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	observeEarly(t, c, "qg", 180*time.Second, 0)
	*now = now.Add(time.Hour)
	for index := range 3 {
		if err := c.EarlierRead(context.Background(), EarlierEvidence{Contract: holdContract("qg", execution.EvaluationTime(180+index*60), 150_000),
			CandidateHold: 75 * time.Second, Observed: true, Equal: true}); err != nil {
			t.Fatal(err)
		}
	}
	if c.ReadHold("qg") != 75*time.Second {
		t.Fatalf("not lowered: %s", c.ReadHold("qg"))
	}
	restored, err := NewController(c.options)
	if err != nil {
		t.Fatal(err)
	}
	prepare(t, restored, groupSpec("qg", 0))
	if got, err := restored.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 600, holdFence("qg")); err != nil || got != 75*time.Second {
		t.Fatalf("a restore raised the lowered hold again: %s %v", got, err)
	}
}

// A write that loses its CAS to a changed record leaves the group to be read
// again, and the write after that lands: the conflict heals itself.
func TestACASConflictIsReadAgainAndTheNextWriteLands(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	observeEarly(t, c, "qg", 180*time.Second, 0)
	changed := Record{SinceSlot: 1, HoldMillis: 90_000, ArrivalAgeMillis: 120_000, SegmentStart: 60}
	store.values["qg"], _ = json.Marshal(changed)
	evidence := Evidence{Contract: holdContract("qg", 180, 0), ArrivalAge: 240 * time.Second}
	if err := c.Observe(context.Background(), evidence); !errors.Is(err, ErrConflict) {
		t.Fatalf("a changed record did not conflict: %v", err)
	}
	if c.Inspect("qg").Loaded {
		t.Fatal("a conflicted group stayed loaded with the stale record")
	}
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"qg"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Observe(context.Background(), evidence); err != nil {
		t.Fatalf("the write after a reread still failed: %v", err)
	}
	if reading, _ := c.Reading("qg"); reading.ArrivalAgeMillis != 240_000 {
		t.Fatalf("the write did not land on the reread record: %+v", reading)
	}
}

// A record at zero is not renewed: it ages out, where renewing it kept a
// zero key alive for good.
func TestAZeroRecordIsNotRenewed(t *testing.T) {
	c, store, now := controllerFixture(t)
	store.values["qg"], _ = json.Marshal(Record{SinceSlot: 1, SegmentStart: 60, ArrivalAgeMillis: 30_000})
	prepare(t, c, groupSpec("qg", 0))
	*now = now.Add(2 * RenewInterval)
	if err := c.RenewDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.requests) != 0 {
		t.Fatal("a zero record was renewed")
	}
}

// A group whose record could not be read freezes no Slot: at zero it could
// drop the hold or the deadline its record keeps. Once read, the stored hold
// is used.
func TestAGroupWhoseRecordWasNotReadFreezesNoSlot(t *testing.T) {
	c, store, _ := controllerFixture(t)
	store.values["qg"], _ = json.Marshal(Record{SinceSlot: 1, HoldMillis: 150_000, SegmentStart: 60})
	store.readErr["qg"] = errors.New("unanswered")
	if err := c.Configure(groupSpec("qg", 0)); err != nil {
		t.Fatal(err)
	}
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"qg"}); err == nil {
		t.Fatal("an unanswered read restored")
	}
	schedule := scheduleFor(t, "qg", 60, nil)
	if _, err := c.SlotReadHold(context.Background(), schedule, 600, holdFence("qg")); !errors.Is(err, ErrNotRestored) {
		t.Fatalf("an unread group froze a Slot: %v", err)
	}
	delete(store.readErr, "qg")
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"qg"}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.SlotReadHold(context.Background(), schedule, 600, holdFence("qg")); err != nil || got != 150*time.Second {
		t.Fatalf("the read group did not use its stored hold: %s %v", got, err)
	}
}

// A frozen Segment's schedule is checked once, not for every Slot: the
// zero-hold path a Slot takes allocates a few objects, where recomputing
// the schedule's digests allocated about a hundred.
func TestTheZeroHoldPathDoesNotCheckTheScheduleEverySlot(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	schedule := scheduleFor(t, "qg", 60, nil)
	ctx := context.Background()
	if _, err := c.SlotReadHold(ctx, schedule, 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(50, func() {
		if _, err := c.SlotReadHold(ctx, schedule, 180, holdFence("qg")); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 20 {
		t.Fatalf("a zero-hold Slot allocates %.0f objects; the schedule is checked every Slot again", allocs)
	}
}

// A link from a Segment that held no Slot of the Plan has no deadline to
// keep: whatever its predecessor's record says decides nothing, is not
// counted, and gives the successor nothing to write.
func TestALinkWithoutALastSlotDecidesNothing(t *testing.T) {
	c, store, _ := controllerFixture(t)
	store.values["old"], _ = json.Marshal(Record{SinceSlot: 1, HoldMillis: 150_000, SegmentStart: 60})
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"}); err != nil {
		t.Fatal(err)
	}
	newSpec := groupSpec("new", 0)
	newSpec.Previous = []Previous{linked(newSpec, "old", 1200, 0)}
	prepare(t, c, newSpec)
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", 1200, nil), 1200, holdFence("new")); err != nil || got != 0 {
		t.Fatalf("a slot-less link held the successor: %s %v", got, err)
	}
	if got := c.Stats().Predecessors; len(got) != 0 {
		t.Fatalf("a slot-less link was counted: %v", got)
	}
	if _, written := store.values["new"]; written {
		t.Fatal("a slot-less link made the successor write a record")
	}
}

// A Plan that left a group twice finds the group's record closed at the
// first boundary while the second is still open: the earlier closure is not
// this one. Read as closed, its long-past deadline would let the group's
// held last Slot overtake the new group; read as open, it is the bound.
func TestARecordClosedAtAnEarlierBoundaryIsOpenForALaterLink(t *testing.T) {
	c, store, _ := controllerFixture(t)
	store.values["old"], _ = json.Marshal(Record{SinceSlot: 1, HoldMillis: 150_000, SegmentStart: 660,
		Plans: []PlanRecord{{PlanRef: planRef(), ClosedAt: 600, ClosedQueryGroup: "old", PreviousSlot: 540, PreviousHoldMillis: 150_000,
			CompletionOffsetMillis: 55_000}}})
	if err := c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"old"}); err != nil {
		t.Fatal(err)
	}
	newSpec := groupSpec("new", 0)
	newSpec.Previous = []Previous{linked(newSpec, "old", 1200, 1140)}
	prepare(t, c, newSpec)
	// 1140 s + 600 s bound + 55 s offset, read at 1200 s + 30 s wait.
	if got, err := c.SlotReadHold(context.Background(), scheduleFor(t, "new", 1200, nil), 1200, holdFence("new")); err != nil || got != 565*time.Second {
		t.Fatalf("a link at a later boundary read the earlier closure: %s %v", got, err)
	}
	if got := c.Stats().Predecessors; got[PredecessorRecordOpen] != 1 || len(got) != 1 {
		t.Fatalf("counted %v, want one %s", got, PredecessorRecordOpen)
	}
}

// Each Segment's schedule is checked when the group first freezes a Slot of
// it, not only the first Segment's.
func TestEverySegmentsScheduleIsChecked(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	invalid := scheduleFor(t, "qg", 600, nil)
	invalid.Segment.ScheduleRevision = "not-the-plans-revision"
	if _, err := c.SlotReadHold(context.Background(), invalid, 600, holdFence("qg")); err == nil {
		t.Fatal("a second Segment's invalid schedule was not checked")
	}
}

// The hold a reader states is the pending one while a change is pending,
// the current one otherwise; a record not read yet, or one that did not
// decode, is not known; none at all is a known zero.
func TestTheHoldReadersStateIsPendingFirstAndUnknownWhenUnread(t *testing.T) {
	pending := int64(30_000)
	for name, tc := range map[string]struct {
		inspection Inspection
		hold       int64
		known      bool
	}{
		"current":   {Inspection{Loaded: true, Record: Record{HoldMillis: 20_000}}, 20_000, true},
		"pending":   {Inspection{Loaded: true, Record: Record{HoldMillis: 20_000, PendingHoldMillis: &pending}}, 30_000, true},
		"no record": {Inspection{Loaded: true, Missing: true}, 0, true},
		"not read":  {Inspection{}, 0, false},
		"corrupt":   {Inspection{Loaded: true, Corrupt: true, Record: Record{HoldMillis: 20_000}}, 0, false},
	} {
		if hold, known := tc.inspection.Hold(); hold != tc.hold || known != tc.known {
			t.Errorf("%s: (%d, %v), want (%d, %v)", name, hold, known, tc.hold, tc.known)
		}
	}
}
