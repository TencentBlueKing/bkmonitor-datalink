package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

type closingReadHolds struct {
	testReadHolds
	closed []execution.FrozenQueryGroupSchedule
	err    error
	failed []error
	sites  []string
}

func (holds *closingReadHolds) PrepareSchedule(_ context.Context, schedule execution.FrozenQueryGroupSchedule, _ execution.OwnerFence) error {
	if schedule.Segment.End != nil {
		holds.closed = append(holds.closed, schedule)
	}
	return holds.err
}

func (holds *closingReadHolds) RetireCloseFailed(err error) { holds.failed = append(holds.failed, err) }

func (holds *closingReadHolds) RetireClosed(site string) { holds.sites = append(holds.sites, site) }

// A retired group closes its hold at the last boundary, and retires whether
// or not the closing succeeds: answering retry until it could close kept a
// group whose closing could never succeed unretired, and its successor
// waiting on it. A failed closing is counted; the successor reads the
// unclosed record as the hold bound.
func TestRetirementClosesTheHoldAndRetiresEvenWhenClosingFails(t *testing.T) {
	end := execution.EvaluationTime(600)
	schedule := schedulerSchedule(t, 60, 60, &end, "snapshot-1", 1)
	for _, closeErr := range []error{nil, errors.New("close failure")} {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &end}
		holds := &closingReadHolds{testReadHolds: testReadHolds{hold: 150 * time.Second}, err: closeErr}
		source := newProductionSlotSourceForTest(t, catalog, foundProgress(600, 540), time.Unix(600, 0))
		source.readHolds = holds
		_, due, facts, err := source.Next(context.Background(), "query-group-1")
		if err != nil || due || !facts.Retired || len(holds.closed) != 1 || *holds.closed[0].Segment.End != end {
			t.Fatalf("close error %v: retirement=%+v closed=%+v err=%v", closeErr, facts, holds.closed, err)
		}
		if (closeErr == nil) != (len(holds.failed) == 0) {
			t.Fatalf("close error %v: failures counted %v", closeErr, holds.failed)
		}
	}
}

// A cursor past a retirement off the grid - where completing the last Slot
// moves it, to the next grid point - is retired too: judged by equality
// alone, that group never took the retired path, and its read hold stayed
// open for its successor to read as the hold bound.
func TestACursorPastTheRetirementRetiresAndClosesTheHold(t *testing.T) {
	end := execution.EvaluationTime(630)
	schedule := schedulerSchedule(t, 60, 60, &end, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &end}
	holds := &closingReadHolds{testReadHolds: testReadHolds{hold: 150 * time.Second}}
	source := newProductionSlotSourceForTest(t, catalog, foundProgress(660, 600), time.Unix(660, 0))
	source.readHolds = holds
	_, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due || !facts.Retired || len(holds.closed) != 1 || *holds.closed[0].Segment.End != end {
		t.Fatalf("cursor 660 past retirement 630: retirement=%+v closed=%+v err=%v", facts, holds.closed, err)
	}
	if len(holds.sites) != 1 || holds.sites[0] != readhold.RetireCloseNext {
		t.Fatalf("closed at %v, want once by the round (%s)", holds.sites, readhold.RetireCloseNext)
	}
}

// A drained retired group closes its hold when it is released, before any
// round comes back to it - the release of a group drained in the cutover's
// own round left its record open for good. One that has not drained is not
// closed at its release: whoever takes it over closes it at the retirement.
func TestARetiredGroupClosesItsHoldWhenReleasedOnlyOnceDrained(t *testing.T) {
	end := execution.EvaluationTime(630)
	schedule := schedulerSchedule(t, 60, 60, &end, "snapshot-1", 1)
	// A Slot in flight sits at the cursor (Progress refuses a projection
	// anywhere else), so one short of the retirement is the case that has one.
	// Released because the leader took it off this Worker: the assignment
	// names another, and the lease is still this Worker's.
	movedOn := []ownership.AssignmentRecord{testAssignment("worker-2", 4)}
	for name, test := range map[string]struct {
		progress execution.ProgressLoadResult
		records  []ownership.AssignmentRecord
		closes   bool
	}{
		"drained past the retirement":                  {progress: foundProgress(660, 600), closes: true},
		"drained at the retirement":                    {progress: foundProgress(630, 600), closes: true},
		"drained, its assignment moved to another":     {progress: foundProgress(660, 600), records: movedOn, closes: true},
		"short of the retirement":                      {progress: foundProgress(600, 540)},
		"short of it, its assignment moved to another": {progress: foundProgress(600, 540), records: movedOn},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &end}
			holds := &closingReadHolds{testReadHolds: testReadHolds{hold: 150 * time.Second}}
			reader := &fakeProgressReader{result: test.progress, catalog: catalog}
			source := mustProductionSlotSource(t, &sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}, records: test.records},
				catalog, reader, time.Unix(700, 0))
			source.readHolds = holds
			closed, err := source.CloseRetiredReadHold(context.Background())
			if err != nil || closed != test.closes || (len(holds.closed) == 1) != test.closes || len(holds.failed) != 0 {
				t.Fatalf("CloseRetiredReadHold() = (%t, %v), closed %+v, failed %v; want closing %t", closed, err, holds.closed, holds.failed, test.closes)
			}
			if test.closes && (len(holds.sites) != 1 || holds.sites[0] != readhold.RetireCloseRelease) {
				t.Fatalf("closed at %v, want once by the release (%s)", holds.sites, readhold.RetireCloseRelease)
			}
		})
	}
	// A close that cannot even ask - the lease is gone - is a close that
	// failed, counted as one.
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}, retiredAt: &end}
	holds := &closingReadHolds{testReadHolds: testReadHolds{hold: 150 * time.Second}}
	reader := &fakeProgressReader{result: foundProgress(660, 600), catalog: catalog}
	source := mustProductionSlotSource(t, &fakeSession{err: ownership.ErrStaleFence}, catalog, reader, time.Unix(700, 0))
	source.readHolds = holds
	if closed, err := source.CloseRetiredReadHold(context.Background()); closed || !errors.Is(err, ownership.ErrStaleFence) ||
		len(holds.failed) != 1 || len(holds.closed) != 0 {
		t.Fatalf("CloseRetiredReadHold() without a lease = (%t, %v), failed %v; want one counted failure", closed, err, holds.failed)
	}
}

// A hold its group could not prepare for a reason of the hold's own does not
// refuse the Slot; any other preparing error still does.
func TestADegradedReadHoldDoesNotRefuseTheSlot(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	for _, tc := range []struct {
		err     error
		refused bool
	}{
		{fmt.Errorf("%w: route unreadable", ErrReadHoldDegraded), false},
		{errors.New("owner fence is stale"), true},
	} {
		catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
		source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(160, 0))
		source.readHolds = &closingReadHolds{testReadHolds: testReadHolds{hold: time.Minute}, err: tc.err}
		slot, due, _, err := source.Next(context.Background(), "query-group-1")
		var retry *SourceRetryError
		if refused := errors.As(err, &retry); refused != tc.refused {
			t.Fatalf("prepare error %v: refused=%t, want %t (%v)", tc.err, refused, tc.refused, err)
		}
		if !tc.refused && (!due || slot.Contract.ReadHoldMillis != 60_000) {
			t.Fatalf("prepare error %v: slot=%+v due=%t", tc.err, slot, due)
		}
	}
}

func TestQueryFreeExpiredSlotKeepsItsOwnReadHold(t *testing.T) {
	end := execution.EvaluationTime(600)
	closed := schedulerSchedule(t, 60, 60, &end, "snapshot-old", 1)
	open := schedulerSchedule(t, 60, 600, nil, "snapshot-current", 2)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{closed, open}, freezeErr: &controlplane.FreezeSlotContractError{Class: controlplane.FreezeSlotFailureSnapshotRead, Err: controlplane.ErrSnapshotUnavailable}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(100_000, 0), testRecoveryLimits())
	source.readHolds = &testReadHolds{hold: time.Minute}
	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.Contract.ReadHoldMillis != 60_000 || slot.Recovery.Disposition != ReplayExpired {
		t.Fatalf("slot=%+v due=%t err=%v", slot, due, err)
	}
	if slot.EarliestQueryDeadlineUnixMilli != 120_000+60_000+closed.Plans[0].Spec.CompletionOffsetSeconds()*1000-source.queryReserve.Milliseconds() {
		t.Fatalf("deadline=%d", slot.EarliestQueryDeadlineUnixMilli)
	}
}

type testReadHolds struct{ hold time.Duration }

func (holds *testReadHolds) ReadHold(execution.QueryGroupIdentity) time.Duration { return holds.hold }

func TestUnfinishedSlotKeepsItsFrozenHoldAfterRestart(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(160, 0))
	source.readHolds = &testReadHolds{hold: time.Minute}
	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.Contract.ReadHoldMillis != 60_000 {
		t.Fatalf("held slot: %+v %v %v", slot, due, err)
	}
	load := foundProgress(slot.ExpectedNextSlot, 0)
	load.Progress.UnfinishedSlot = &execution.UnfinishedSlotProjection{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli, KeepUntilUnixMilli: slot.KeepUntilUnixMilli,
	}
	// The production holds freeze through SlotReadHold; the unfinished Slot
	// keeps its contract's hold through either.
	for _, holds := range []ReadHolds{&testReadHolds{}, &slotTestReadHolds{}} {
		for _, freezeErr := range []error{nil, controlplane.ErrSnapshotUnavailable} {
			catalog.freezeErr = freezeErr
			restarted := newProductionSlotSourceForTest(t, catalog, load, time.Unix(160, 0))
			restarted.readHolds = holds
			restored, due, _, err := restarted.Next(context.Background(), "query-group-1")
			if err != nil || !due || restored.Contract != slot.Contract || restored.EarliestQueryDeadlineUnixMilli != slot.EarliestQueryDeadlineUnixMilli {
				t.Fatalf("restart through %T with current hold zero and freeze error %v: %+v %v %v", holds, freezeErr, restored, due, err)
			}
		}
	}
}

// slotTestReadHolds freezes every new Slot at its hold through SlotReadHold,
// as the production holds do.
type slotTestReadHolds struct{ testReadHolds }

func (holds *slotTestReadHolds) SlotReadHold(context.Context, execution.FrozenQueryGroupSchedule, execution.EvaluationTime, execution.OwnerFence) (time.Duration, error) {
	return holds.hold, nil
}
func (holds *slotTestReadHolds) PeekSlotReadHold(execution.FrozenQueryGroupSchedule, execution.EvaluationTime) (time.Duration, error) {
	return holds.hold, nil
}

func TestReadHoldShiftsReplayClockWithoutWideningSettlingBudget(t *testing.T) {
	schedule := schedulerSchedule(t, 10, 60, nil, "snapshot-1", 1)
	schedule.Plans[0].Spec.CompletionDeadlineOffsetSeconds = 30
	schedule.Plans[0].ScheduleRevision = mustPlanScheduleRevision(t, schedule.Plans[0].Spec)
	schedule.Segment.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(schedule.Plans)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(105, 0))
	distance, recheck, err := source.replayDistance(context.Background(), 60, 20_000, time.Unix(105, 0))
	if err != nil || distance != 3 || recheck != 110_000 {
		t.Fatalf("distance=%d recheck=%d err=%v", distance, recheck, err)
	}
	facts := SlotRecoveryFacts{Distance: distance, RecheckAtUnixMilli: recheck}
	expired, err := source.replayWaitOutlastsDistance(context.Background(), 60, 105_000, 20_000, &facts)
	if err != nil || expired {
		t.Fatalf("held wait used the hold as settling budget: %+v %v", facts, err)
	}
}

func TestSupplementCarriesOriginalReadHoldAndDigest(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(160, 0))
	source.readHolds = &testReadHolds{hold: time.Minute}
	first, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due {
		t.Fatalf("first: %v %v", due, err)
	}
	supplementSource := newProductionSlotSourceForTest(t, catalog, foundProgress(120, 60), time.Unix(160, 0))
	supplementSource.readHolds = &testReadHolds{}
	supplement, err := supplementSource.FreezeSupplement(context.Background(), 60, first.Contract.ReadHoldMillis)
	if err != nil || supplement.Contract != first.Contract {
		t.Fatalf("supplement %+v err=%v; first %+v", supplement.Contract, err, first.Contract)
	}
	firstDigest, _ := contract.DeriveCanonicalDigestV2("alarmd-go-access-execution-v1", first.Contract)
	supplementDigest, _ := contract.DeriveCanonicalDigestV2("alarmd-go-access-execution-v1", supplement.Contract)
	if firstDigest != supplementDigest {
		t.Fatal("supplement rebuilt a different execution")
	}
}

type changingReadHold struct{}

func (changingReadHold) ReadHold(execution.QueryGroupIdentity) time.Duration { return 0 }
func (changingReadHold) SlotReadHold(_ context.Context, _ execution.FrozenQueryGroupSchedule, at execution.EvaluationTime, _ execution.OwnerFence) (time.Duration, error) {
	return changingReadHold{}.PeekSlotReadHold(execution.FrozenQueryGroupSchedule{}, at)
}
func (changingReadHold) PeekSlotReadHold(_ execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) (time.Duration, error) {
	if at == 120 {
		return 0, nil
	}
	return time.Minute, nil
}
func TestExpiredRangeFallsBackToOneSlotAcrossAHoldTransition(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.Unix(1000, 0), testRecoveryLimits())
	source.readHolds = changingReadHold{}
	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	slot, due, _, err := source.Next(ctx, "query-group-1")
	if err != nil || !due || slot.ExpiredRange != nil || slot.Contract.ReadHoldMillis != 0 {
		t.Fatalf("hold transition blocked the valid first Slot: %+v %v", slot, err)
	}
}
