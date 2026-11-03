package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The threshold is measured against what was last sent, not what was last
// read.
//
// A reading that climbs in steps each under a tenth would, measured against
// the last read, never be sent: every step compares against the step before
// it. The object drifts arbitrarily far from what the Leader holds while every
// individual comparison says "close enough", and the Leader places it on a
// number that is no longer true.
func TestTheCostThresholdIsMeasuredAgainstWhatWasSent(t *testing.T) {
	// Three steps: each is under a tenth against the step before it, so
	// measured that way none would ever be sent, and the first two are under a
	// tenth of the 1000 that was sent. The third has drifted 16% from it and
	// must go out.
	steps := []uint64{1040, 1080, 1160}
	for index, reading := range steps {
		if index == 0 {
			continue
		}
		if costReadingMoved(steps[index-1], reading) {
			t.Fatalf("step %d -> %d is under a tenth; a case that compares consecutive reads "+
				"cannot show the drift this guards against", steps[index-1], reading)
		}
	}
	if !costReadingMoved(1000, steps[len(steps)-1]) {
		t.Fatalf("%d against the 1000 that was sent was withheld, so the Leader keeps placing this "+
			"object on a number it has drifted 16%% away from", steps[len(steps)-1])
	}
	if costReadingMoved(1000, steps[0]) {
		t.Fatal("a reading 4% above what was sent was reported; the threshold is a tenth")
	}

	// And the source measures against the sent value rather than the read one.
	source := newWorkerCostSource(nil, nil, nil)
	group := execution.QueryGroupIdentity("qg-1")
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b", "c", "d"} })
	if costs := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: 1000}}); len(costs) != 1 {
		t.Fatalf("the first reading was withheld: %+v", costs)
	}
	for _, reading := range steps[:2] {
		if costs := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: reading}}); len(costs) != 0 {
			t.Fatalf("reading %d was reported; it is under a tenth of the 1000 sent", reading)
		}
	}
	costs := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: steps[2]}})
	if len(costs) != 1 || costs[0].RetainedBytesPeak != steps[2] {
		t.Fatalf("the drifted reading was withheld: %+v", costs)
	}
	if source.reported[group].peak != steps[2] {
		t.Fatalf("what was sent was recorded as %d, want %d", source.reported[group].peak, steps[2])
	}
}

// A reading that appears or disappears is always reported.
//
// A tenth of zero is zero, so a proportional test alone reports every change
// from an object that had no peak and none from an object that has just
// acquired one - the two cases a placement decision most needs to hear about.
func TestACostReadingAppearingOrGoingIsAlwaysReported(t *testing.T) {
	if !costReadingMoved(0, 1) {
		t.Fatal("an object that acquired a peak was withheld")
	}
	if !costReadingMoved(1, 0) {
		t.Fatal("an object whose peak went to zero was withheld")
	}
	if costReadingMoved(0, 0) {
		t.Fatal("an object with no peak before or after was reported")
	}
}

// An ask that cannot derive a rate carries the last one that could, not a
// number it made up and not a zero.
//
// Every case seeds a derivable rate first and then creates the condition, so
// "carried the last value" and "reset to zero" give different answers. Run on
// a fresh source they would not: the last value is zero there, and an
// implementation that zeroed on every rotation would pass every case while
// sending the Leader a not-reported and a recovery on a fixed cadence for an
// object whose cost never moved.
func TestAnUndeivableAskCarriesTheLastDerivedRate(t *testing.T) {
	const second = time.Second
	// Half a second of compute per second of schedule.
	seedFirst := observability.CostRetainedPeak{ComputeWallNS: 0}
	seedSecond := observability.CostRetainedPeak{ComputeWallNS: int64(second / 2)}

	for _, testCase := range []struct {
		name    string
		reading observability.CostRetainedPeak
		elapsed time.Duration
	}{
		{name: "the window rotated, so the difference is negative",
			reading: observability.CostRetainedPeak{ComputeWallNS: 0}, elapsed: second},
		{name: "the window carries an observation that was never measured",
			reading: observability.CostRetainedPeak{ComputeWallNS: int64(second), ComputeWallUnknown: 1}, elapsed: second},
		{name: "there is no span to divide by",
			reading: observability.CostRetainedPeak{ComputeWallNS: int64(second)}, elapsed: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := newWorkerCostSource(nil, nil, nil)
			group := execution.QueryGroupIdentity("qg-1")
			source.advanceCost(group, seedFirst, second)
			seeded := source.advanceCost(group, seedSecond, second)
			if seeded != 500 {
				t.Fatalf("the seed derived %d, want 500 milli; without a derived rate this case cannot "+
					"tell carrying it forward from resetting to zero", seeded)
			}
			if got := source.advanceCost(group, testCase.reading, testCase.elapsed); got != seeded {
				t.Fatalf("rate = %d, want the last derived %d carried forward", got, seeded)
			}
		})
	}
}

// Before any rate has been derived there is nothing to carry, and the Query
// Group reports zero - which the Leader reads as unreported.
func TestTheFirstAskForAQueryGroupReportsNoRate(t *testing.T) {
	source := newWorkerCostSource(nil, nil, nil)
	got := source.advanceCost(execution.QueryGroupIdentity("qg-1"),
		observability.CostRetainedPeak{ComputeWallNS: int64(time.Second)}, time.Second)
	if got != 0 {
		t.Fatalf("the first ask reported %d, want zero: there is no earlier reading to difference against", got)
	}
}

// A rate that moves is reported even when the retained bytes do not.
//
// The threshold watched only the peak at first, so an object whose bytes held
// steady while its compute tripled kept the Leader on the cost of its first
// reading - and the Leader places objects by that number.
func TestARateThatMovesIsReportedWhenTheBytesDoNot(t *testing.T) {
	const second = time.Second
	source := newWorkerCostSource(nil, nil, nil)
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b", "c", "d"} })
	const peak = uint64(4096)

	// Two asks to seed a rate, both carrying the same peak.
	source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: peak}})
	source.lastAsked = source.now().Add(-second)
	first := source.report([]observability.CostRetainedPeak{
		{QueryGroupKey: "qg-1", RetainedBytesPeak: peak, ComputeWallNS: int64(second / 10)}})
	if len(first) != 1 || first[0].CostPerSecondMilli == 0 {
		t.Fatalf("the seeding ask did not report a rate: %+v", first)
	}
	sent := first[0].CostPerSecondMilli

	// The peak does not move; the compute does, by far more than a tenth.
	source.lastAsked = source.now().Add(-second)
	next := source.report([]observability.CostRetainedPeak{
		{QueryGroupKey: "qg-1", RetainedBytesPeak: peak, ComputeWallNS: int64(second/10) + int64(second/2)}})
	if len(next) != 1 {
		t.Fatalf("a Query Group whose cost moved while its bytes held steady was withheld; the Leader "+
			"keeps placing it on %d", sent)
	}
	if next[0].CostPerSecondMilli == sent {
		t.Fatalf("the reported rate did not move from %d, so this case cannot show the threshold "+
			"watching it", sent)
	}
	if next[0].RetainedBytesPeak != peak {
		t.Fatalf("peak = %d, want it unchanged at %d", next[0].RetainedBytesPeak, peak)
	}
}

// An entry cut by the owned bound is not recorded as sent.
//
// Recording it would make the next ask compare a reading against a value the
// Leader never received, and an object that then stayed steady would be
// withheld by the threshold forever - silently, because from here it looks
// exactly like an object already reported.
func TestAnEntryCutByTheBoundIsSentInFull(t *testing.T) {
	source := newWorkerCostSource(nil, nil, nil)
	source.boundByOwned(func() []execution.QueryGroupIdentity { return nil })
	group := execution.QueryGroupIdentity("qg-1")

	costs := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: 100}})
	if len(costs) != 0 {
		t.Fatalf("the bound of zero reported %d entries", len(costs))
	}
	if _, sent := source.reported[group]; sent {
		t.Fatal("an entry cut by the bound was recorded as sent, so it will be withheld once it steadies")
	}
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b", "c", "d"} })
	costs = source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: 100}})
	if len(costs) != 1 || costs[0].RetainedBytesPeak != 100 {
		t.Fatalf("the entry did not go out whole on the next ask: %+v", costs)
	}
}

// The first reading of a Query Group is sent even when it is all zeros.
//
// The threshold compares a reading against what was last sent; for an object
// never sent there is nothing to compare against, and "moved by a tenth of
// nothing" is false. A first reading of a nonzero peak passes the threshold
// anyway (a move away from zero always counts), so the cases above cannot
// tell "first readings are always sent" from "first readings pass the
// threshold": only a cold object, peak 0 and no compute, separates them. The
// Leader needs that object in its candidates as much as any other - an
// object it never heard of cannot be placed.
func TestAColdQueryGroupsFirstReadingIsSent(t *testing.T) {
	source := newWorkerCostSource(nil, nil, nil)
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b", "c", "d"} })
	first := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-cold"}})
	if len(first) != 1 || first[0].QueryGroup != "qg-cold" || first[0].RetainedBytesPeak != 0 || first[0].CostPerSecondMilli != 0 {
		t.Fatalf("first report = %+v, want the cold object once with zero peak and zero rate: an object the "+
			"Leader never hears of cannot be placed", first)
	}
	// And not again while it stays cold.
	if again := source.report([]observability.CostRetainedPeak{{QueryGroupKey: "qg-cold"}}); len(again) != 0 {
		t.Fatalf("a cold object was reported again without moving: %+v", again)
	}
}

// A new stream reports every reading again. The threshold is measured
// against what was sent, and what was sent went to whichever Leader was
// listening then; the Leader listening now may have started with an empty
// ledger. After a rolling restart it has, and a Worker that kept its
// readings as sent re-sent only the ones that had moved by a tenth: on a
// steady replica almost none, so the new Leader read most objects as unread
// for as long as they stayed steady and the byte-feasibility moves never
// had a destination - 85 percent unread ten minutes after a roll, on every
// Worker.
func TestANewStreamReportsEveryReadingAgain(t *testing.T) {
	source := newWorkerCostSource(nil, nil, nil)
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-1", "qg-2"} })
	readings := []observability.CostRetainedPeak{{QueryGroupKey: "qg-1", RetainedBytesPeak: 1000}, {QueryGroupKey: "qg-2", RetainedBytesPeak: 2000}}
	if costs := source.report(readings); len(costs) != 2 {
		t.Fatalf("first report carried %d of two readings", len(costs))
	}
	if costs := source.report(readings); len(costs) != 0 {
		t.Fatalf("a steady reading was reported again on the same stream: %+v", costs)
	}
	source.SessionStarted()
	if costs := source.report(readings); len(costs) != 2 {
		t.Fatalf("after a new stream began, %d of two steady readings were reported; the Leader listening now has never heard them", len(costs))
	}
}

// Costs reads the census pruned to the roster: a Query Group this Worker let
// go is not reported, and one it holds is, whatever the cost summary's own
// roster admitted.
func TestCostsReadTheCensusPrunedToTheRoster(t *testing.T) {
	now := time.Unix(600, 0)
	census := observability.NewRetainedPeakCensus(time.Minute, func() time.Time { return now })
	for _, group := range []string{"qg-1", "qg-2", "qg-gone"} {
		census.Observe(context.Background(), observability.Observation{Stage: observability.StageSlotCompleted,
			Trace: observability.TraceFields{QueryGroupKey: group}, SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: 4096}})
	}
	meter := &recordingCensusMeter{}
	source := newWorkerCostSource(census, meter, func() time.Time { return now })
	source.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-1", "qg-2"} })
	costs := source.Costs()
	if len(costs) != 2 || costs[0].QueryGroup != "qg-1" || costs[1].QueryGroup != "qg-2" {
		t.Fatalf("costs = %+v, want the two owned Query Groups and not the one let go", costs)
	}
	// The census's own size is published beside the report, after the
	// pruning -- the two it holds for the roster, not the three it held --
	// and its overflow with it, so a census nothing prunes is readable
	// rather than only counted.
	if meter.calls != 1 || meter.groups != 2 || meter.overflow != 0 {
		t.Fatalf("census meter = %+v, want one reading of two groups and no overflow", meter)
	}
}

// recordingCensusMeter keeps the last census reading published.
type recordingCensusMeter struct {
	groups   int
	overflow uint64
	calls    int
}

func (m *recordingCensusMeter) SetRetainedPeakCensus(groups int, overflow uint64) {
	m.groups, m.overflow, m.calls = groups, overflow, m.calls+1
}
