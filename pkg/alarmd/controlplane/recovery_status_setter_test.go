// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A level's status_setter is read the way the recovery checker reads it
// (recover.py:133-142, :396-403), and what this build does not execute is
// said rather than dropped: a -nodata ends only on points, noted; a close is
// executed, frozen into the recovery Plan as end "close", and says nothing;
// a value outside the four is noted with how it was read, and one read as a
// close closes; one the checker cannot split takes the default window.
// Nothing else about the level changes: its recovery Plan is the one an
// unwritten status_setter compiles to, apart from the malformed value's
// window and a close's end.
func TestAStatusSetterIsReadAsTheCheckerReadsItAndWhatDoesNotRunIsSaid(t *testing.T) {
	const (
		defaulted = "RECOVERY_STATUS_SETTER_DEFAULTED"
		window    = "RECOVERY_CONFIG_DEFAULTED"
	)
	plain := legacyRecoveryCatalog(t, `{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":3}}`, 1)
	plainPlan := plain.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0].RecoveryPlan.Config
	for _, test := range []struct {
		setter  string
		notes   []string
		detail  string
		windows uint32
		end     string
		nodata  bool
	}{
		{setter: `"recovery"`, windows: 3},
		{setter: `"close"`, windows: 3, end: "close"},
		{setter: `"recovery-nodata"`, windows: 3, nodata: true},
		{setter: `"close-nodata"`, windows: 3, end: "close", nodata: true},
		{setter: `"recover"`, notes: []string{defaulted}, detail: `status_setter="recover" read_as=recovery`, windows: 3},
		{setter: `""`, notes: []string{defaulted}, windows: 3},
		{setter: `"close-later"`, notes: []string{defaulted}, detail: `status_setter="close-later" read_as=close`, windows: 3, end: "close"},
		{setter: `"close-no-data"`, notes: []string{window}, detail: `recovery_windows=5; status_setter "close-no-data" is not read`, windows: 5},
		{setter: `null`, notes: []string{window}, windows: 5},
		{setter: `5`, notes: []string{window}, windows: 5},
		{setter: `["close-nodata"]`, notes: []string{defaulted}, detail: `status_setter="[\"close-nodata\"]" read_as=recovery`, windows: 3},
		{setter: `["-"]`, notes: []string{window}, windows: 5},
		{setter: `{"close":1}`, notes: []string{defaulted}, windows: 3},
		{setter: `{"-":1}`, notes: []string{window}, windows: 5},
	} {
		t.Run(test.setter, func(t *testing.T) {
			catalog := legacyRecoveryCatalog(t, `{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":3,"status_setter":`+test.setter+`}}`, 1)
			var reasons []string
			details := map[string]string{}
			for _, disposition := range catalog.Dispositions {
				if disposition.Disposition == controlplane.DispositionAccepted {
					continue
				}
				if disposition.Disposition != controlplane.DispositionConfigNoted || disposition.Scope != "LEVEL" || disposition.LevelID != 1 {
					t.Fatalf("disposition %+v, want only notes on level 1: the level runs", disposition)
				}
				reasons = append(reasons, disposition.Reason)
				details[disposition.Reason] = disposition.Detail
			}
			want := slices.Clone(test.notes)
			slices.Sort(reasons)
			slices.Sort(want)
			if !slices.Equal(reasons, want) {
				t.Fatalf("notes = %v, want %v", reasons, test.notes)
			}
			if test.detail != "" && !slices.Contains(mapValues(details), test.detail) {
				t.Fatalf("details = %v, want one to be %q", details, test.detail)
			}
			wantMissing := ""
			if test.nodata {
				wantMissing = "evaluate_as_zero"
			}
			if got := legacyRecoveryOf(t, catalog)[1]; !got.Enabled || got.ConsecutiveWindows != test.windows || got.End != test.end || got.MissingData != wantMissing {
				t.Fatalf("recovery = %+v, want enabled with %d windows, end %q, missing_data %q", got, test.windows, test.end, wantMissing)
			}
			if test.windows == 3 && test.end == "" && !test.nodata {
				if got := catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0].RecoveryPlan.Config; !bytes.Equal(got, plainPlan) {
					t.Fatalf("recovery Plan %s, want the unwritten status_setter's %s", got, plainPlan)
				}
			}
		})
	}
}

// A strategy whose recovery is defaulted as a whole, and a level whose
// trigger is borrowed, take "recovery" with the default window as the
// checker does, so a status_setter written on them is not read: no level
// ends closed or on no data.
func TestAStatusSetterIsNotReadWhereTheCheckerTakesItsDefault(t *testing.T) {
	catalog := legacyRecoveryCatalog(t,
		`{"level":1,"trigger_config":{"count":1,"check_window":1},"recovery_config":{"check_window":3,"status_setter":"close-nodata"}},`+
			`{"level":2,"trigger_config":{"count":1,"check_window":1},"recovery_config":{}}`, 1, 2)
	recoveries := legacyRecoveryOf(t, catalog)
	if len(recoveries) != 2 {
		t.Fatalf("recoveries = %+v, want both levels", recoveries)
	}
	for level, got := range recoveries {
		if got.End != "" || got.MissingData != "" {
			t.Fatalf("level %d recovery = %+v: a defaulted strategy's status_setter was read", level, got)
		}
	}
}

func mapValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
