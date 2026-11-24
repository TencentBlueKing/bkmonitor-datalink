// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package editclose

import (
	"slices"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The oracle is Decide: a severity is removed exactly when Decide closes a
// threshold alert of an older version at it by level_removed, and the word
// is the one Decide keeps every such alert under.
func TestRemovedSeveritiesAreDecidesLevelRemoved(t *testing.T) {
	level := func(id uint32, code string) contract.LevelDefinitionV2 {
		return contract.LevelDefinitionV2{LevelID: id, LevelCode: code}
	}
	plans := map[string]Plan{
		"all three":       {Revision: 5, LevelsWhole: true, Levels: []contract.LevelDefinitionV2{level(1, ""), level(2, ""), level(3, "")}},
		"critical only":   {Revision: 5, LevelsWhole: true, Levels: []contract.LevelDefinitionV2{level(1, "")}},
		"warning, info":   {Revision: 5, LevelsWhole: true, Levels: []contract.LevelDefinitionV2{level(2, ""), level(3, "")}},
		"not whole":       {Revision: 5, LevelsWhole: false, Levels: []contract.LevelDefinitionV2{level(1, "")}},
		"none":            {Revision: 5, LevelsWhole: true},
		"a mapped level":  {Revision: 5, LevelsWhole: true, Levels: []contract.LevelDefinitionV2{level(1, ""), level(7, "custom")}},
		"a derived level": {Revision: 5, LevelsWhole: true, Levels: []contract.LevelDefinitionV2{level(1, ""), level(9, "")}},
	}
	for name, plan := range plans {
		removed, word := RemovedSeverities(plan)
		for _, severity := range []string{"critical", "warning", "info"} {
			decision, _ := Decide(plan, Alert{Revision: 4, DimensionsRead: true, EvaluationFamily: familyMetricAlgorithm, Severity: severity})
			if (decision == OutcomeLevelRemoved) != slices.Contains(removed, severity) {
				t.Fatalf("%s: %s is removed %v, Decide says %s", name, severity, removed, decision)
			}
			if word != "" && decision != word && decision != OutcomeKept {
				t.Fatalf("%s: word %s, Decide keeps %s under %s", name, word, severity, decision)
			}
		}
	}
}
