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
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// steppedStateCase is an item detected every fifteen seconds over one-minute
// windows, as the build before detect_interval became the request step
// compiled it (datalink 57e61ae6a): its state compatibility hash and its one
// Level's detect fingerprint.
type steppedStateCase struct {
	name     string
	document func(t *testing.T) json.RawMessage
	// threshold is a Plan whose detection reads no input but its own window.
	threshold bool
	state     string
	detect    string
}

func steppedStateCases() []steppedStateCase {
	return []steppedStateCase{
		{name: "threshold", threshold: true, document: func(t *testing.T) json.RawMessage {
			return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"bk_host_id"},
				[]any{[]any{map[string]any{"method": "gte", "threshold": 80}}})
		}, state: "26cbc0e207c3a8598288bd352aece737d115988a62cafa44b670fb9409e706c3",
			detect: "0f5271886252645e4c6fa73cac23f495031a66a75653f7bf0e908ddbd446cb55"},
		{name: "simple ring ratio", document: func(t *testing.T) json.RawMessage {
			return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
				map[string]any{"floor": 50, "ceil": nil})
		}, state: "5ffeff71a937586e4a0fe56635ca1d8a6e69222093faa1ab2b063c0e424cfe7d",
			detect: "47ea462b527351140e1acbbe240b288c25be77023327bda910b127c5835ccf09"},
		{name: "ring ratio amplitude", document: func(t *testing.T) json.RawMessage {
			return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindRingRatioAmplitude, "usage", "system.cpu", []string{"host"},
				map[string]any{"ratio": 1, "shock": 2, "threshold": 3})
		}, state: "ee18655f1eb99a69511680bec70c4a000b1936dea55dcaec1f8a94e44c6d820b",
			detect: "db0c5a087185ba6bd788fe337dba788e43551d70f09f7fee82d08606a525eb74"},
		{name: "year round amplitude", document: func(t *testing.T) json.RawMessage {
			return g4LegacyStrategyDocument(t, 300, strategy.DetectorKindYearRoundAmplitude, "usage", "system.cpu", []string{"host"},
				map[string]any{"days": 2, "method": "gte", "ratio": 2, "shock": 0})
		}, state: "dcfcbd1f3b658fe21a376c163070fcebac052f6e196e8a318748dcfc5aff58d4",
			detect: "503c802a2431b595846a3340f557aafe71e1dd51b3f66e170f80f6dcbe0922dc"},
	}
}

// A threshold Plan detected faster than it aggregates keeps its state
// generation across the upgrade: its detection reads no input whose request
// changed. Every other such Plan's detection reads requirements under the
// new query revision, and the previous point of a ring comparison moved, so
// no Level's detection is the one before and the Plan is not carried
// (StateCarryGroupChanged, its query revision moved it to another Query
// Group): it warms up whole, once.
func TestAThresholdDetectedFasterThanItAggregatesKeepsItsStateAcrossTheUpgrade(t *testing.T) {
	export := os.Getenv("ALARMD_DETECT_INTERVAL_GOLDEN_EXPORT") == "1"
	for _, test := range steppedStateCases() {
		t.Run(test.name, func(t *testing.T) {
			catalog := detectIntervalCatalog(t, withItemField(t, test.document(t), "detect_interval", 15))
			compiled := compileEveryLevel(t, catalog.QueryGroups[0])
			if compiled.Levels().Len() != 1 {
				t.Fatalf("%d Levels, want one", compiled.Levels().Len())
			}
			state, detect := compiled.StateCompatibilityHash(), compiled.Levels().At(0).Fingerprints().Detect
			if export {
				t.Logf("STATE %q state=%q detect=%q", test.name, state, detect)
				return
			}
			if test.threshold {
				if state != test.state || detect != test.detect {
					t.Fatalf("state %s detect %s, want the build before's %s and %s", state, detect, test.state, test.detect)
				}
				return
			}
			if detect == test.detect {
				t.Fatalf("detect %s is the build before's: the Plan would carry state its detection no longer reads", detect)
			}
			if state == test.state {
				t.Fatalf("state %s is the build before's although its detection changed", state)
			}
		})
	}
}
