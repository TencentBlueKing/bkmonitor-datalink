// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A level whose recovery_config states disable_recovery true does not
// recover: its recovery Plan is disabled, with no window, no ending and no
// -nodata - the switch wins over a status_setter written beside it - and the
// level is noted RECOVERY_DISABLED_BY_CONFIG, still running. Only an explicit
// JSON true disables: absent, false, and a string or a number keep the level
// recovering as written, which is how a document that does not state the
// key reads; a writer whose page shows another default writes the value.
func TestAnExplicitDisableRecoveryTurnsTheLevelsRecoveryOff(t *testing.T) {
	for _, test := range []struct {
		name, recovery string
		disabled       bool
		end, missing   string
	}{
		{name: "true", recovery: `{"check_window":3,"disable_recovery":true}`, disabled: true},
		{name: "true with recovery-nodata", recovery: `{"check_window":3,"status_setter":"recovery-nodata","disable_recovery":true}`, disabled: true},
		{name: "true with close-nodata", recovery: `{"check_window":3,"status_setter":"close-nodata","disable_recovery":true}`, disabled: true},
		{name: "true without a window", recovery: `{"disable_recovery":true}`, disabled: true},
		{name: "absent", recovery: `{"check_window":3}`},
		{name: "false", recovery: `{"check_window":3,"disable_recovery":false}`},
		{name: "false with close-nodata", recovery: `{"check_window":3,"status_setter":"close-nodata","disable_recovery":false}`, end: "close", missing: "evaluate_as_zero"},
		{name: "null", recovery: `{"check_window":3,"disable_recovery":null}`},
		{name: "a string", recovery: `{"check_window":3,"disable_recovery":"true"}`},
		{name: "a number", recovery: `{"check_window":3,"disable_recovery":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := legacyRecoveryCatalog(t, `{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":`+test.recovery+`}`, 1)
			var notes []string
			for _, disposition := range catalog.Dispositions {
				if disposition.Disposition == controlplane.DispositionAccepted {
					continue
				}
				if disposition.Disposition != controlplane.DispositionConfigNoted || disposition.Scope != "LEVEL" || disposition.LevelID != 1 {
					t.Fatalf("disposition %+v, want only notes on level 1: the level runs", disposition)
				}
				notes = append(notes, disposition.Reason)
			}
			noted := slices.Contains(notes, controlplane.ReasonRecoveryDisabledByConfig)
			got := legacyRecoveryOf(t, catalog)[1]
			if test.disabled {
				if !noted || len(notes) != 1 || got.Enabled || got.ConsecutiveWindows != 0 || got.End != "" || got.MissingData != "" {
					t.Fatalf("recovery %+v notes %v, want disabled with nothing else and only %s noted", got, notes, controlplane.ReasonRecoveryDisabledByConfig)
				}
				return
			}
			if noted || !got.Enabled || got.ConsecutiveWindows != 3 || got.End != test.end || got.MissingData != test.missing {
				t.Fatalf("recovery %+v notes %v, want enabled on 3 windows, end %q, missing_data %q, nothing noted", got, notes, test.end, test.missing)
			}
		})
	}
}

// A level that does not recover has no window to read, so one written
// without check_window does not default the strategy's other levels: they
// recover on their own windows, and nothing says a window was defaulted.
func TestADisabledLevelWithoutAWindowDefaultsNoOtherLevel(t *testing.T) {
	catalog := legacyRecoveryCatalog(t,
		`{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"disable_recovery":true}},`+
			`{"level":2,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":3}}`, 1, 2)
	recoveries := legacyRecoveryOf(t, catalog)
	if recoveries[1].Enabled || !recoveries[2].Enabled || recoveries[2].ConsecutiveWindows != 3 {
		t.Fatalf("recoveries = %+v, want level 1 off and level 2 on its own 3 windows", recoveries)
	}
	for _, disposition := range catalog.Dispositions {
		if disposition.Reason == controlplane.ReasonRecoveryConfigDefaulted {
			t.Fatalf("a window was defaulted beside a disabled level: %+v", disposition)
		}
	}
}
