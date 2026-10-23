// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// readWaitOf is how long after its evaluation time a group on a schedule
// whose completion offset is its interval is read, as access reads it: the
// settling wait and the group's phase. A harness that steps its clock to a
// round's read uses it, never the settling wait alone, which every staggered
// group is read after.
func readWaitOf(cfg config.Config, queryGroup execution.QueryGroupIdentity, intervalSeconds int64) time.Duration {
	budget := time.Duration(intervalSeconds)*time.Second - cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	return execution.ReadWaitWithinQueryBudget(queryGroup, budget, cfg.PhaseTwo.Access.MinReadyDelay.Duration())
}

// firstReadWaitOf is how long after its evaluation time a group nothing has
// measured is first read: where its data is taken to land - the settling
// floor, or the deployment's ready delay as its schedule's budget allows it
// if later, the cold start its read hold is seeded with - and its phase
// after that.
func firstReadWaitOf(cfg config.Config, queryGroup execution.QueryGroupIdentity, intervalSeconds int64) time.Duration {
	budget := time.Duration(intervalSeconds)*time.Second - cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	floor := execution.SettlingFloorWithinQueryBudget(budget, cfg.PhaseTwo.Access.MinReadyDelay.Duration())
	landing := max(floor, execution.SettlingWaitWithinQueryBudget(budget, cfg.PhaseTwo.Access.MinReadyDelay.Duration()))
	return landing + readWaitOf(cfg, queryGroup, intervalSeconds) - floor
}

// roundReadAtMillis is the instant a harness runs a round: half the interval
// after the evaluation time, as the harnesses always have, or the group's
// read if that is later.
func roundReadAtMillis(cfg config.Config, queryGroup execution.QueryGroupIdentity, evaluationAt, intervalSeconds int64) int64 {
	return evaluationAt*1000 + max(intervalSeconds*1000/2, firstReadWaitOf(cfg, queryGroup, intervalSeconds).Milliseconds())
}

// latestRoundReadAtMillis is the instant a harness runs a round of every
// Query Group the bundle owns: the latest of their reads.
func latestRoundReadAtMillis(bundle *phaseTwoWorkerBundle, evaluationAt, intervalSeconds int64) int64 {
	latest := evaluationAt*1000 + intervalSeconds*1000/2
	for _, queryGroup := range bundle.queryGroups {
		latest = max(latest, roundReadAtMillis(bundle.dependencies.Config, queryGroup, evaluationAt, intervalSeconds))
	}
	return latest
}
