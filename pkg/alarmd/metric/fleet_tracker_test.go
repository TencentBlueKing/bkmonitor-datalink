// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// Every kind is exported from the start, at zero before the tracker is
// bound, and reads the tracker's counts at scrape.
func TestFleetTrackerDroppedIsReadAtScrape(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	read := func() map[string]float64 {
		out := map[string]float64{}
		for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_fleet_tracker_dropped_total") {
			out[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return out
	}
	if got := read(); len(got) != len(fleet.TrackerDroppedKinds) || got[fleet.TrackerDroppedObservation] != 0 {
		t.Fatalf("unbound = %v, want every kind at zero", got)
	}
	dropped := fleet.TrackerDropped{Counts: map[string]uint64{fleet.TrackerDroppedObservation: 7, fleet.TrackerDroppedRange: 2}}
	r.SetFleetTrackerSource(func() fleet.TrackerDropped { return dropped })
	got := read()
	if len(got) != len(fleet.TrackerDroppedKinds) || got[fleet.TrackerDroppedObservation] != 7 || got[fleet.TrackerDroppedRange] != 2 || got[fleet.TrackerDroppedTruncation] != 0 {
		t.Fatalf("bound = %v", got)
	}
}
