package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// A round read at its group's read hold limit, the group's data measured
// landing past it, says so on its own completion line (read_at_limit) and is
// counted under the source the group reads, every round, through the
// production Worker; a round under the limit, its data read whole, does
// neither.
func TestARoundReadAtTheHoldLimitSaysSoAndIsCounted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		arrival time.Duration
		atLimit bool
	}{
		{"at the limit", 700 * time.Second, true},
		{"under it", 100 * time.Second, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var output lockedLogBuffer
			f := startCutoverFixtureWith(t, nil, observability.New(observability.ComponentRuntime, &output), nil)
			engine, holds := f.bundle.dependencies.Lookback, f.bundle.dependencies.ReadHolds
			_, raised, _, _, _ := heldTrialRun(t, f, c.arrival, 20*time.Minute, false,
				func(time.Duration, time.Duration, time.Duration, map[string]uint64) bool { return false })
			record, _ := holds.controller.Reading(f.queryGroup)
			if record.HeldAtLimit() != c.atLimit || raised <= 0 {
				t.Fatalf("fixture: hold %v, record %+v; want held at the limit %t", raised, record, c.atLimit)
			}
			counted := uint64(0)
			for _, rounds := range productionLookbackStats(engine, holds).RoundsReadAtLimit {
				counted += rounds
			}
			marked := 0
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, `"read_at_limit":true`) {
					if !strings.Contains(line, `"slot_completed"`) || !strings.Contains(line, string(f.queryGroup)) {
						t.Fatalf("read_at_limit on %s, want the group's slot_completed line", line)
					}
					marked++
				}
			}
			if c.atLimit && (counted == 0 || marked == 0 || uint64(marked) > counted) {
				t.Fatalf("rounds at the limit: %d counted, %d lines marked; want every round counted and its line written", counted, marked)
			}
			if !c.atLimit && (counted != 0 || marked != 0) {
				t.Fatalf("rounds under the limit: %d counted, %d lines marked; want none", counted, marked)
			}
		})
	}
}

// A round is read at the limit only when both say so: the Slot frozen with a
// hold at the limit its group's record keeps, and the record held there with
// its data still landing past it - read early within its quiet bar. A Slot
// frozen under the limit before the group was raised to it, one at the limit
// whose data lands within it, and one of a group quiet past its bar - being
// tried lower - are not, and the strategy page reads the last two as held,
// not at the limit; a group whose source is not known yet is counted under
// other.
func TestARoundIsReadAtTheLimitOnlyWhenItsHoldAndItsDataBothSaySo(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	// Two hours on, so a quiet started at the fixture's start is a real one.
	started := *at
	*at = at.Add(2 * time.Hour)
	records := map[execution.QueryGroupIdentity]readhold.Record{
		"limited": {SinceSlot: 1, HoldMillis: 600000, AtLimit: true, LimitMillis: 600000, QuietSinceMillis: at.UnixMilli()},
		"within":  {SinceSlot: 1, HoldMillis: 600000, LimitMillis: 600000, QuietSinceMillis: at.UnixMilli()},
		"quiet": {SinceSlot: 1, HoldMillis: 600000, AtLimit: true, LimitMillis: 600000,
			QuietSinceMillis: started.UnixMilli()},
	}
	groups := []execution.QueryGroupIdentity{"limited", "within", "quiet"}
	for qg, record := range records {
		raw, _ := json.Marshal(record)
		c.values[qg] = raw
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
	}
	h.restore(context.Background(), groups)
	ref := func(qg execution.QueryGroupIdentity, hold int64) execution.FrozenExecutionContractRef {
		return execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 120}, ReadHoldMillis: hold}
	}
	for _, test := range []struct {
		name string
		ref  execution.FrozenExecutionContractRef
		want bool
	}{
		{"frozen at the limit, data past it", ref("limited", 600000), true},
		{"frozen under the limit before the raise", ref("limited", 300000), false},
		{"at the limit, data within it", ref("within", 600000), false},
		{"at the limit, quiet past its bar", ref("quiet", 600000), false},
		{"no record", ref("unknown", 600000), false},
	} {
		if got := h.roundReadAtLimit(test.ref); got != test.want {
			t.Errorf("%s: read at the limit %t, want %t", test.name, got, test.want)
		}
	}
	if got := h.readAtLimit; len(got) != 1 || got[lookback.SourceOther] != 1 {
		t.Fatalf("counted %v, want the one round under other, its source not known yet", got)
	}
	facts := h.fleetFacts()
	for qg, want := range map[string]bool{"limited": true, "within": false, "quiet": false} {
		if facts[qg].AtLimit != want {
			t.Errorf("the page reads %s at the limit %t, want %t", qg, facts[qg].AtLimit, want)
		}
	}
}
