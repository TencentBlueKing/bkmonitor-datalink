// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// The copy's facts carry what calibration needs and can do, the shortfall,
// the strategies past their calibration age and those refused for size, as
// fleet.get shows them.
func TestOpenAlertFactsNameWhatCalibrationAndReadsCannotReach(t *testing.T) {
	key := openalerts.StrategyKey{TenantID: "t", StrategyID: "101"}
	facts := openAlertSetFacts(openalerts.Stats{CalibrationNeeded: 3, CalibrationCapacity: 2, CalibrationShort: 400,
		CalibrationOverAge: []openalerts.CalibrationAge{{Strategy: key, Age: 4 * time.Hour}},
		TooLarge:           []openalerts.TooLargeSet{{Strategy: key, Refusal: openalerts.SetTooLargeError{Members: 90000, MaxMembers: 50000, MaxBytes: 1 << 20}}},
	}, false, time.Unix(1700000000, 0))
	if facts.CalibrationNeeded != 3 || facts.CalibrationCapacity != 2 || facts.CalibrationShort != 400 ||
		len(facts.CalibrationOverAge) != 1 || facts.CalibrationOverAge[0].StrategyID != "101" || facts.CalibrationOverAge[0].AgeSeconds != 14400 ||
		len(facts.TooLarge) != 1 || facts.TooLarge[0].Members != 90000 || facts.TooLarge[0].MaxMembers != 50000 {
		t.Fatalf("facts %+v", facts)
	}
}
