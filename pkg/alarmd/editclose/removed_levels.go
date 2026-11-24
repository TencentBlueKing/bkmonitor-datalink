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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

// RemovedSeverities is level_removed for every threshold alert of an
// older version at once: the severities at which Decide closes such an
// alert, by its level alone. Empty with a word where Decide decides no
// alert's level - the Plans' levels not whole (levels_partial), or a level
// the link may store under a mapped name (severity_unmapped) - and empty
// with no word where every built-in level is still detected.
func RemovedSeverities(plan Plan) (severities []string, word string) {
	if !plan.LevelsWhole {
		return nil, OutcomeLevelsPartial
	}
	if len(plan.Levels) == 0 {
		return nil, OutcomeSeverityUnmapped
	}
	detected := map[string]bool{}
	for _, level := range plan.Levels {
		if !linkdoutput.SeverityIsBuiltIn(contract.LevelResultV1{LevelID: level.LevelID}) {
			return nil, OutcomeSeverityUnmapped
		}
		detected[linkdoutput.SeverityFor(contract.LevelResultV1{LevelID: level.LevelID, LevelCode: level.LevelCode})] = true
	}
	for severity := range builtInLevels {
		if !detected[severity] {
			severities = append(severities, severity)
		}
	}
	sort.Strings(severities)
	return severities, ""
}
