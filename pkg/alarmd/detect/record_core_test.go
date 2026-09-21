// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestPreparedRecordCoreKeepsNormalizationFailuresUnavailable(t *testing.T) {
	for name, value := range map[string]json.RawMessage{"absent": nil, "null": json.RawMessage(`null`), "type": json.RawMessage(`"70"`), "overflow": json.RawMessage(`1e100`)} {
		t.Run(name, func(t *testing.T) {
			fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithmFor("value", "GTE", "50", "percent", "")}, contract.LevelConnectorAND, value, "percent")
			if fact.Result != FactResultUnavailable {
				t.Fatalf("fact result = %s, want UNAVAILABLE", fact.Result)
			}
		})
	}
}

func TestCombineAlgorithmTruthUsesStrongThreeValueLogic(t *testing.T) {
	tests := []struct {
		name      string
		connector string
		truths    []algorithmTruth
		want      bool
		decided   bool
	}{
		{name: "AND false dominates unknown", connector: contract.LevelConnectorAND, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthFalse}, want: false, decided: true},
		{name: "AND true cannot dominate unknown", connector: contract.LevelConnectorAND, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthTrue}, decided: false},
		{name: "OR true dominates unknown", connector: contract.LevelConnectorOR, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthTrue}, want: true, decided: true},
		{name: "OR false cannot dominate unknown", connector: contract.LevelConnectorOR, truths: []algorithmTruth{algorithmTruthUnknown, algorithmTruthFalse}, decided: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, decided := combineAlgorithmTruth(test.connector, test.truths)
			if got != test.want || decided != test.decided {
				t.Fatalf("combineAlgorithmTruth()=(%t,%t), want (%t,%t)", got, decided, test.want, test.decided)
			}
		})
	}
}
