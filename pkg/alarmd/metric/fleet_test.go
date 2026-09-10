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
