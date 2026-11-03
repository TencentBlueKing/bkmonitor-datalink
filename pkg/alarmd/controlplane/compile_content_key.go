// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// compileContentKey names the content a frozen Plan was assembled from, for
// the compiler to remember the Plan's cache key by
// (strategy.CompileRequest.ContentKey).
//
// A Plan assembled by content (AssembleQueryGroup) has two sources: the Query
// Group object the Segment names at the evaluation time, which holds the
// Plan's body and the group's dataset contract, and the Plan's own output
// context the Segment names there, which holds the rest -- the strategy
// reference, the legacy output, the wire format, the subject facts. The
// Plan's own context, not the group's: two Plans of one group can render by
// different ones. Both are read by digest and checked against it, so the two
// digests with the Plan's identity and piece name every byte the compiler
// reads from the request but the state semantics, which it keys by value.
//
// Empty -- derive the key from the Plan -- for a group served from the
// Snapshot, and for a Plan the Segment names no context for.
func compileContentKey(byContent bool, segment execution.ScheduleSegmentFact, plan FrozenPlan) string {
	if !byContent || segment.ObjectDigest == "" {
		return ""
	}
	context := segment.OutputContextRefFor(plan.Identity)
	if context == "" {
		return ""
	}
	shard := execution.ShardOf(plan.Shard)
	return strings.Join([]string{
		string(segment.ObjectDigest), string(context),
		plan.Identity.TenantID, plan.Identity.BusinessID, plan.Identity.StrategyID,
		shard.Dimension, strconv.Itoa(shard.Index), strconv.Itoa(shard.Count), shard.MatcherDigest,
	}, "\x00")
}
