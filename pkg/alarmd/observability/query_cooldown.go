// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "time"

const StageQueryCooldown = "query_cooldown"

// QueryCooldownFacts describes a policy transition or a failed real probe.
// Query Group identity belongs in Trace, never in metric labels.
type QueryCooldownFacts struct {
	Event       string    `json:"event"`
	Until       time.Time `json:"until"`
	LastQueryAt time.Time `json:"last_query_at"`
	Failures    uint32    `json:"failures"` // Saturates at 32; 32 means at least 32.
}

func normalizeQueryCooldownFacts(facts *QueryCooldownFacts) *QueryCooldownFacts {
	if facts == nil {
		return nil
	}
	copy := *facts
	switch copy.Event {
	case "entered", "extended", "recovered", "config_changed", "disabled":
	default:
		copy.Event = "other"
	}
	return &copy
}
