// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The coverage gauge splits the tracked strategies into those calibration
// reaches within its interval and those it cannot, and is absent without a
// Console to calibrate against.
func TestCalibrationCoverageSaysWhatTheConsolePaceCannotReach(t *testing.T) {
	read := func(stats openalerts.Stats) map[string]float64 {
		recorder := NewRecorder(BuildInfo{})
		recorder.SetOpenAlertSetSource(func() openalerts.Stats { return stats })
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		cells := map[string]float64{}
		for _, family := range families {
			if family.GetName() != "alarmd_linkd_calibration_coverage" {
				continue
			}
			for _, series := range family.Metric {
				cells[series.Label[0].GetValue()] = series.GetGauge().GetValue()
			}
		}
		return cells
	}
	if cells := read(openalerts.Stats{CalibrationConfigured: true, Tracked: 4000, CalibrationShort: 400}); cells["covered"] != 3600 || cells["short"] != 400 {
		t.Fatalf("coverage %v, want 3600 covered and 400 short", cells)
	}
	if cells := read(openalerts.Stats{Tracked: 4000}); len(cells) != 0 {
		t.Fatalf("coverage without a Console: %v", cells)
	}
}
