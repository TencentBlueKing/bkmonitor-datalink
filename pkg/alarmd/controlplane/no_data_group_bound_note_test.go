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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// An item that raises its no-data group bound above the default is noted with
// the bound, the default and the measured one-off cost, and runs with that
// bound; one at the default or without a bound of its own is not noted.
func TestANoDataGroupBoundAboveTheDefaultIsNotedAndTheStrategyRuns(t *testing.T) {
	const cost = "a Plan's first no-data write per state generation blocks Redis about 120-180 ms at 100,000 groups, " +
		"610 ms at 200,000 and 2.9 s at 1,000,000, and the worker allocates about 3.5 KiB a group for it"
	for name, test := range map[string]struct {
		section string
		noted   bool
		bound   uint32
		detail  string
	}{
		"no bound of its own": {section: `{"is_enabled":true,"continuous":5}`},
		"at the default":      {section: `{"is_enabled":true,"continuous":5,"max_groups":100000}`, bound: 100000},
		"one above the default": {section: `{"is_enabled":true,"continuous":5,"max_groups":100001}`, bound: 100001,
			noted: true, detail: "max_groups=100001 default=100000; " + cost},
		"the ceiling": {section: `{"is_enabled":true,"continuous":5,"max_groups":1000000}`, bound: 1000000,
			noted: true, detail: "max_groups=1000000 default=100000; " + cost},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := buildHorizonCatalog(t, test.section, 0)
			var note controlplane.ObjectDisposition
			noted := false
			for _, disposition := range catalog.Dispositions {
				if disposition.Reason == controlplane.ReasonNoDataGroupBoundAboveDefault {
					note, noted = disposition, true
				}
				if disposition.Disposition != controlplane.DispositionAccepted &&
					disposition.Disposition != controlplane.DispositionConfigNoted {
					t.Fatalf("the strategy is filed as %+v; the note must not withhold anything", disposition)
				}
			}
			if noted != test.noted {
				t.Fatalf("noted = %t (%+v), want %t", noted, note, test.noted)
			}
			if noted && (note.Disposition != controlplane.DispositionConfigNoted || note.Scope != "PLAN" ||
				note.FieldPath != "items[0].no_data_config.max_groups" || note.Detail != test.detail) {
				t.Fatalf("note = %+v, want CONFIG_NOTED on the Plan's max_groups with detail %q", note, test.detail)
			}
			plan := onlyPlan(t, catalog)
			if plan.Plan.NoData == nil || plan.Plan.NoData.MaxGroups != test.bound {
				t.Fatalf("no_data = %+v, want it running with bound %d", plan.Plan.NoData, test.bound)
			}
		})
	}
}
