// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// NewLevelRequirement composes one Level's window requirement from the
// retention derived once for its Plan plus the Level identity facts only the
// window needs. It is the sole place those retention facts become a
// LevelRequirement, so retentionCutoff and StateTTL cannot end up reading
// different retention for the same Level: retentionCutoff keeps points back to
// (RetentionPoints-1)*EvaluationInterval + LatenessTolerance, StateTTL stores
// the key for RetentionPoints*EvaluationInterval + LatenessTolerance plus the
// restart margin, and the second is longer than the first only while both read
// these same three values.
//
// detectFingerprint and requiredPoints are window facts; StateTTL ignores them,
// so a caller that only needs a TTL may leave them empty.
func NewLevelRequirement(
	retention execution.StateRetentionRequirement, detectFingerprint string, requiredPoints uint32,
) LevelRequirement {
	return LevelRequirement{
		LevelID:            retention.LevelID,
		DetectFingerprint:  detectFingerprint,
		RequiredPoints:     requiredPoints,
		RetentionPoints:    retention.RetentionPoints,
		EvaluationInterval: retention.EvaluationInterval,
		LatenessTolerance:  retention.LatenessTolerance,
	}
}
