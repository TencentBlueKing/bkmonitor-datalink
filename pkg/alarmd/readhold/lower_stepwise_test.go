// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package readhold

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// scheduleEvery is scheduleFor with the Query Group detected every interval.
func scheduleEvery(t *testing.T, qg execution.QueryGroupIdentity, start execution.EvaluationTime, interval int64) execution.FrozenQueryGroupSchedule {
	t.Helper()
	schedule := scheduleFor(t, qg, start, nil)
	spec := schedule.Plans[0].Spec
	spec.EvaluationIntervalSeconds = interval
	revision, err := execution.DerivePlanScheduleRevision(spec)
	if err != nil {
		t.Fatal(err)
	}
	schedule.Plans[0].Spec, schedule.Plans[0].ScheduleRevision = spec, revision
	groupRevision, err := execution.DeriveQueryGroupScheduleRevision(schedule.Plans)
	if err != nil {
		t.Fatal(err)
	}
	schedule.Segment.ScheduleRevision = groupRevision
	return schedule
}

// A hold halved at the limit - ten minutes to five, as trials can lower it
// once they reach the limit - comes down a step per Slot: no Slot is frozen
// more than a step below the Slot before it, so none is ready before the one
// before it is read, and none falls behind by more Slots than a replay covers.
// Frozen all at once, the first Slot under the lower hold was ready five
// minutes before the Slot ahead of it was read, five Slots behind when it was
// first attempted, and skipped unevaluated.
func TestALoweredHoldComesDownAStepPerSlot(t *testing.T) {
	for _, period := range []int64{60, 30} {
		t.Run(time.Duration(period*int64(time.Second)).String(), func(t *testing.T) {
			c, _, now := controllerFixture(t)
			c.options.OnLimitCut = func(cut LimitCut) { t.Errorf("a lowering stepped down told a limit cut %+v", cut) }
			spec := groupSpec("qg", 0)
			spec.Step = time.Duration(period) * time.Second
			spec.HoldLimit = 10 * time.Minute
			prepare(t, c, spec)
			observeEarly(t, c, "qg", 10*time.Minute+spec.SettlingWait, 0)
			schedule := scheduleEvery(t, "qg", 60, period)
			ctx := context.Background()
			first := execution.EvaluationTime(120)
			if got, err := c.SlotReadHold(ctx, schedule, first, holdFence("qg")); err != nil || got != 10*time.Minute {
				t.Fatalf("fixture: first Slot frozen at %v %v, want the limit", got, err)
			}
			// Three matching trials of half the hold after the quiet: lowered to 5 minutes.
			*now = now.Add(time.Hour)
			for index := range EarlierMatchesRequired {
				if err := c.EarlierRead(ctx, EarlierEvidence{Contract: holdContract("qg", first+execution.EvaluationTime(index), 600_000),
					CandidateHold: 5 * time.Minute, Observed: true, Equal: true}); err != nil {
					t.Fatal(err)
				}
			}
			verifyEnd(t, c, "qg", first+execution.EvaluationTime(EarlierMatchesRequired), true)
			if c.ReadHold("qg") != 5*time.Minute {
				t.Fatalf("fixture: lowered to %v, want 5 minutes", c.ReadHold("qg"))
			}
			previous, previousReady := 10*time.Minute, int64(first)*1000+spec.SettlingWait.Milliseconds()+600_000
			reached := false
			for index := int64(1); index <= 20; index++ {
				slot := first + execution.EvaluationTime(index*period)
				hold, err := c.SlotReadHold(ctx, schedule, slot, holdFence("qg"))
				if err != nil {
					t.Fatal(err)
				}
				if previous-hold > spec.Step {
					t.Fatalf("Slot %d frozen at %v, more than a step below the Slot before it at %v", slot, hold, previous)
				}
				// Ready no earlier than the Slot before it: the Runner reads them in order, none waits behind.
				if ready := int64(slot)*1000 + spec.SettlingWait.Milliseconds() + hold.Milliseconds(); ready < previousReady {
					t.Fatalf("Slot %d ready %d ms before the Slot before it", slot, previousReady-ready)
				} else {
					previousReady = ready
				}
				previous = hold
				reached = reached || hold == 5*time.Minute
			}
			if !reached {
				t.Fatalf("the hold never reached the lowered 5 minutes, last frozen at %v", previous)
			}
		})
	}
}

// A limit configured under the hold takes effect at once: the stepping
// never freezes a Slot past the limit. The cut is told, once, with the
// holds and the period, for the operator who lowered the limit to see.
func TestALoweredLimitIsNotStepped(t *testing.T) {
	c, _, _ := controllerFixture(t)
	var cuts []LimitCut
	c.options.OnLimitCut = func(cut LimitCut) { cuts = append(cuts, cut) }
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 10*time.Minute+spec.SettlingWait, 0)
	schedule := scheduleEvery(t, "qg", 60, 60)
	ctx := context.Background()
	if got, err := c.SlotReadHold(ctx, schedule, 120, holdFence("qg")); err != nil || got != 10*time.Minute {
		t.Fatalf("fixture: %v %v", got, err)
	}
	spec.HoldLimit = 5 * time.Minute
	if err := c.Configure(spec); err != nil {
		t.Fatal(err)
	}
	if got, err := c.SlotReadHold(ctx, schedule, 180, holdFence("qg")); err != nil || got != 5*time.Minute {
		t.Fatalf("under a limit lowered to 5 minutes frozen at %v %v, want the limit at once", got, err)
	}
	if got, err := c.SlotReadHold(ctx, schedule, 240, holdFence("qg")); err != nil || got != 5*time.Minute {
		t.Fatalf("the Slot after frozen at %v %v, want the limit", got, err)
	}
	want := LimitCut{QueryGroup: "qg", Slot: 180, From: 10 * time.Minute, To: 5 * time.Minute, Period: time.Minute}
	if len(cuts) != 1 || cuts[0] != want {
		t.Fatalf("cuts told %+v, want %+v once", cuts, want)
	}
}

// A raise is not stepped: a Slot frozen with more hold than the one before
// it is ready after it, and a raise is the data landing later than the hold.
func TestARaisedHoldTakesEffectAtOnce(t *testing.T) {
	c, _, _ := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 60*time.Second+spec.SettlingWait, 0)
	schedule := scheduleEvery(t, "qg", 60, 60)
	ctx := context.Background()
	if got, err := c.SlotReadHold(ctx, schedule, 120, holdFence("qg")); err != nil || got != time.Minute {
		t.Fatalf("fixture: %v %v", got, err)
	}
	observeEarly(t, c, "qg", 8*time.Minute+spec.SettlingWait, 60_000)
	if got, err := c.SlotReadHold(ctx, schedule, 180, holdFence("qg")); err != nil || got != 8*time.Minute {
		t.Fatalf("a raise frozen at %v %v, want 8 minutes at once", got, err)
	}
}

// A group's last lowering is read from its record, so the process that takes
// it over - a restart, a takeover - knows where it was lowered, and a raise
// after it is no lowering.
func TestTheLastLoweringIsReadFromTheRecordByTheNextOwner(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.Step = time.Minute
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 10*time.Minute+spec.SettlingWait, 0)
	schedule := scheduleEvery(t, "qg", 60, 60)
	ctx := context.Background()
	if _, err := c.SlotReadHold(ctx, schedule, 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if _, lowered := c.LastLowering("qg"); lowered {
		t.Fatal("a hold never lowered has a lowering")
	}
	*now = now.Add(time.Hour)
	for index := range EarlierMatchesRequired {
		if err := c.EarlierRead(ctx, EarlierEvidence{Contract: holdContract("qg", execution.EvaluationTime(120+index), 600_000),
			CandidateHold: 5 * time.Minute, Observed: true, Equal: true}); err != nil {
			t.Fatal(err)
		}
	}
	verifyEnd(t, c, "qg", 123, true)
	if _, err := c.SlotReadHold(ctx, schedule, 180, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	next, err := NewController(Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute, Now: func() time.Time { return *now },
		Owner: func(qg execution.QueryGroupIdentity) (Owner, error) {
			return Owner{Fence: holdFence(qg), ContentScope: "view"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if _, lowered := next.LastLowering("qg"); lowered {
		t.Fatal("a record not loaded has a lowering")
	}
	prepare(t, next, spec)
	if slot, lowered := next.LastLowering("qg"); !lowered || slot != 180 {
		t.Fatalf("the next owner reads the last lowering at %d %v, want the Slot 180 was frozen lower at", slot, lowered)
	}
	observeEarly(t, next, "qg", 10*time.Minute+spec.SettlingWait, 180_000)
	if _, err := next.SlotReadHold(ctx, schedule, 240, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if _, lowered := next.LastLowering("qg"); lowered {
		t.Fatal("a raise after the lowering still reads as one")
	}
}
