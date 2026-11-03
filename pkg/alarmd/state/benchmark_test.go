// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"fmt"
	"testing"
)

func BenchmarkWindowApplyAndSummarize(b *testing.B) {
	requirement := requirement(5, "5", 30, 60)
	points := make([]StatePoint, 60)
	for position := range points {
		points[position] = StatePoint{
			RecordID: fmt.Sprintf("%064x", position+1), SourceTime: int64(100 + position*60),
			Levels: []PointLevelFact{fact(requirement, LevelFactResult(position%2+1))},
		}
	}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		window, err := NewWindow([]LevelRequirement{requirement})
		if err != nil {
			b.Fatal(err)
		}
		mustApply(b, window, points)
		history, _ := window.History(5)
		_ = history.Summarize(100+59*60, 30)
	}
}
