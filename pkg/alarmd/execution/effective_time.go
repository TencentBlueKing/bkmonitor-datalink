// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// BoundEffectiveTimeFact binds a resolved fact to one immutable Plan Level and
// series. Evaluation consumes facts only; it never resolves calendars itself.
type BoundEffectiveTimeFact struct {
	Consumer       ConsumerRef
	SeriesIdentity SeriesIdentityDigest
	Fact           strategy.EffectiveTimeFact
}

func findEffectiveTimeFact(
	input InternalExecution,
	plan PlanIdentity,
	levelID uint32,
	series SeriesIdentityDigest,
) (BoundEffectiveTimeFact, bool) {
	for _, binding := range input.EffectiveTimeFacts {
		if binding.Consumer.Plan == plan && binding.Consumer.HasLevel && binding.Consumer.LevelID == levelID &&
			binding.SeriesIdentity == series {
			return binding, true
		}
	}
	return BoundEffectiveTimeFact{}, false
}
