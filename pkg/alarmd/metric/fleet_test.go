// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"
)

func gatherFleet(t *testing.T, verdict FleetVerdict) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindFleet(func() FleetVerdict { return verdict }); err != nil {
		t.Fatal(err)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		name := family.GetName()
		if len(name) < 17 || name[:17] != "bkmonitor_alarmd_" {
			continue
		}
		for _, series := range family.Metric {
			label := ""
			for _, pair := range series.Label {
				label = pair.GetValue()
			}
			if gathered[name] == nil {
				gathered[name] = map[string]float64{}
			}
			gathered[name][label] = series.GetGauge().GetValue()
		}
	}
	return gathered
}

// The page and the alert rule must not disagree about whether anything is
// wrong. That holds only while both read one judgment, so the judgment has to
// leave alarmd as a value rather than as arithmetic the host repeats.
func TestFleetVerdictIsExportedAsTheJudgmentItself(t *testing.T) {
	expected := 949
	gathered := gatherFleet(t, FleetVerdict{
		Health: "DEGRADED", Expected: &expected,
		Covered: 949, Determined: 941, Unknown: 8, Stalled: 4,
		Anomalies: []FleetCount{{Value: "DEGRADED_RUN", Count: 140, OldestAgeSeconds: 3600}},
		Gaps:      []FleetCount{{Value: "UNDETERMINED", Count: 1}},
	})

	if got := gathered["bkmonitor_alarmd_fleet_health"]["DEGRADED"]; got != 1 {
		t.Fatalf("judgment not exported: %v", gathered["bkmonitor_alarmd_fleet_health"])
	}
	objects := gathered["bkmonitor_alarmd_fleet_objects"]
	for state, want := range map[string]float64{
		"expected": 949, "covered": 949, "determined": 941, "unknown": 8,
	} {
		if objects[state] != want {
			t.Fatalf("objects[%s] = %v, want %v", state, objects[state], want)
		}
	}
	if got := gathered["bkmonitor_alarmd_fleet_stalled_objects"][""]; got != 4 {
		t.Fatalf("stalled = %v, want 4", got)
	}
	if got := gathered["bkmonitor_alarmd_fleet_anomalies"]["DEGRADED_RUN"]; got != 140 {
		t.Fatalf("anomalies = %v, want 140", got)
	}
	if got := gathered["bkmonitor_alarmd_fleet_anomaly_oldest_age_seconds"]["DEGRADED_RUN"]; got != 3600 {
		t.Fatalf("oldest age = %v, want 3600", got)
	}
	// The gaps are the judgment's own reasons. Exporting them is what lets an
	// alert say why the answer is UNKNOWN instead of only that it is.
	if got := gathered["bkmonitor_alarmd_fleet_gaps"]["UNDETERMINED"]; got != 1 {
		t.Fatalf("gaps = %v, want 1", got)
	}
}

// Zero expected objects is a real state that means something else entirely, so
// an unreadable denominator must be absent rather than zero.
func TestFleetVerdictOmitsTheDenominatorItCouldNotRead(t *testing.T) {
	gathered := gatherFleet(t, FleetVerdict{Health: "UNKNOWN", Covered: 12, Determined: 12})
	if _, present := gathered["bkmonitor_alarmd_fleet_objects"]["expected"]; present {
		t.Fatal("an unreadable denominator was exported as a number")
	}
	if got := gathered["bkmonitor_alarmd_fleet_objects"]["covered"]; got != 12 {
		t.Fatalf("covered = %v, want 12", got)
	}
}

// A scrape that reached no judgment must publish none. A default would export
// "healthy" for a deployment nobody managed to ask.
func TestFleetVerdictPublishesNothingWithoutAJudgment(t *testing.T) {
	gathered := gatherFleet(t, FleetVerdict{})
	for name := range gathered {
		if len(name) > 22 && name[:22] == "bkmonitor_alarmd_fleet" {
			t.Fatalf("published %s without a judgment", name)
		}
	}
}

func TestFleetVerdictSourceIsBoundOnce(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindFleet(func() FleetVerdict { return FleetVerdict{Health: "HEALTHY"} }); err != nil {
		t.Fatal(err)
	}
	// Two sources would be two answers to one question.
	if err := recorder.BindFleet(func() FleetVerdict { return FleetVerdict{Health: "DEGRADED"} }); err == nil {
		t.Fatal("a second judgment source was accepted")
	}
	if err := recorder.BindFleet(nil); err == nil {
		t.Fatal("a nil judgment source was accepted")
	}
}

// The acknowledgement partition is exported by state so an alert can watch
// lagging directly; unknown is exported as its own value rather than being
// folded into either side.
func TestFleetVerdictExportsTheWorkerAcknowledgementByState(t *testing.T) {
	gathered := gatherFleet(t, FleetVerdict{
		Health:  "HEALTHY",
		Workers: []FleetCount{{Value: "acked", Count: 2}, {Value: "lagging", Count: 1}, {Value: "unknown", Count: 0}},
	})
	workers := gathered["bkmonitor_alarmd_fleet_workers"]
	for state, want := range map[string]float64{"acked": 2, "lagging": 1, "unknown": 0} {
		if got, ok := workers[state]; !ok || got != want {
			t.Fatalf("fleet_workers[%s] = %v (present %v), want %v", state, got, ok, want)
		}
	}
}

// The columns that partition determined, exported so a change that moves
// objects between them can be attributed from outside.
//
// This exists because an attribution had to be retracted. A release that moved
// objects out of the anomaly column was credited with a drop of twenty-two,
// and the next release, which touched none of that code, showed a similar drop
// at the same age on a different generation. The anomaly count drifts with the
// deployment, so a before-and-after on it says nothing about a change.
//
// The identity the columns make possible is checked here the way a reader
// would check it: the columns add up to determined, and moving an object from
// one to another leaves that sum alone.
func TestTheColumnsThatPartitionDeterminedAreExported(t *testing.T) {
	before := FleetVerdict{
		Health: "DEGRADED", Covered: 979, Determined: 979, Unknown: 0,
		Healthy: 844, Anomalous: 90, Demoted: 33, Undecidable: 12, ByDesign: 0,
	}
	gathered := gatherFleet(t, before)
	objects := gathered["bkmonitor_alarmd_fleet_partition_objects"]
	for state, want := range map[string]float64{
		"healthy": 844, "anomalous": 90, "demoted": 33, "undecidable": 12, "by_design": 0,
		"unknown": 0,
	} {
		got, sent := objects[state]
		if !sent {
			t.Fatalf("fleet_objects has no %q series; the split cannot be checked from outside, "+
				"and a before-and-after on the anomaly count alone attributes nothing", state)
		}
		if got != want {
			t.Errorf("fleet_objects{state=%q} = %v, want %v", state, got, want)
		}
	}
	// by_design is zero here and is still sent. Zero is a measurement this
	// build can always make; a build without the column sent no series at all,
	// and that absence is what a reader needs to be able to tell apart.
	sum := 0.0
	for _, count := range objects {
		sum += count
	}
	if sum != float64(before.Covered) {
		t.Fatalf("columns sum to %v, want covered %v: the identity a reader would use to "+
			"attribute a change does not hold", sum, before.Covered)
	}
	// And the two families are kept apart, because one adds up and the other
	// does not: fleet_objects holds overlapping coverage states, so a reader
	// who sums the family gets a plausible number that means nothing. Mixing
	// them under one name is how that happens.
	coverage := gathered["bkmonitor_alarmd_fleet_objects"]
	for _, column := range []string{"healthy", "anomalous", "demoted", "undecidable", "by_design"} {
		if _, leaked := coverage[column]; leaked {
			t.Errorf("fleet_objects carries the %q column; that family's states overlap, and a "+
				"family where some members partition and others overlap sums to nonsense", column)
		}
	}

	// Moving objects between columns is exactly what a classification change
	// does, and it is what the identity has to survive: one column falls, the
	// other rises, the sum is untouched. A drift in the deployment moves both
	// sides, so the difference is the part that carries the attribution.
	after := before
	after.Anomalous -= 6
	after.ByDesign += 6
	moved := gatherFleet(t, after)["bkmonitor_alarmd_fleet_partition_objects"]
	if moved["anomalous"] != objects["anomalous"]-6 || moved["by_design"] != objects["by_design"]+6 {
		t.Fatalf("a move between columns did not show as one falling and the other rising: %v", moved)
	}
	movedSum := 0.0
	for _, count := range moved {
		movedSum += count
	}
	if movedSum != sum {
		t.Fatalf("the sum changed across a move between columns: %v then %v -- an object was "+
			"created or lost, which is the one thing the identity must rule out", sum, movedSum)
	}
}

// A line that is down is a series at zero, not a series that is gone: the
// producer fills the whole closed table and the collector must not drop the
// zeros, or "this line went down" and "this build has no such family" read
// the same to the rule watching it.
func TestCheckLinesAndDegradationKindsAreExportedAtZero(t *testing.T) {
	gathered := gatherFleet(t, FleetVerdict{
		Health: "DEGRADED",
		Checks: []FleetCount{
			{Value: "CUTOVER_FAILING", Count: 1}, {Value: "REPLICA_DEGRADED", Count: 0},
			{Value: "SLOTS_OVERDUE", Count: 14}, {Value: "DETECTION_ABANDONED", Count: 0},
		},
		Degradations: []FleetCount{
			{Value: "ACTIVATION_BEHIND", Count: 1}, {Value: "OPEN_ALERT_SET_STALE", Count: 0},
		},
	})

	checks := gathered["bkmonitor_alarmd_fleet_checks"]
	for code, want := range map[string]float64{
		"CUTOVER_FAILING": 1, "REPLICA_DEGRADED": 0, "SLOTS_OVERDUE": 14, "DETECTION_ABANDONED": 0,
	} {
		got, present := checks[code]
		if !present || got != want {
			t.Errorf("fleet_checks{code=%s} = %v (present=%v), want %v as a series", code, got, present, want)
		}
	}
	degradations := gathered["bkmonitor_alarmd_fleet_degradations"]
	for kind, want := range map[string]float64{"ACTIVATION_BEHIND": 1, "OPEN_ALERT_SET_STALE": 0} {
		got, present := degradations[kind]
		if !present || got != want {
			t.Errorf("fleet_degradations{kind=%s} = %v (present=%v), want %v as a series", kind, got, present, want)
		}
	}
}
