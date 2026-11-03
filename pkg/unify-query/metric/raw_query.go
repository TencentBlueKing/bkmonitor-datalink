// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

const (
	RawQueryDecisionHistoryDuplicate   = "history_duplicate"   // 同一逻辑查询和 TsDB 的历史分段重复命中存储目标，复用已有执行查询。
	RawQueryDecisionMergeDB            = "merge_db"            // 启用 IsMergeDB 后，额外查询项合并到同一个 DBs 查询中。
	RawQueryDecisionMissingIndex       = "missing_index"       // ES 执行目标的 DB 与 DBs 均无有效索引前缀。
	RawQueryDecisionPaginationFinished = "pagination_finished" // SearchAfter 后续页中，目标的 SearchAfter 和正数 From 游标均不存在。
)

var rawQueryDecisionsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "unify_query",
		Name:      "raw_query_decisions_total",
		Help:      "logical query items handled by Raw planning and pre-execution decisions",
	},
	[]string{"reason", "version", "commit_id"},
)

// RawQueryDecisionAdd 按固定原因累计规划和执行准备阶段处理的逻辑查询项数。
// reason 仅接受固定枚举；非法原因和非正数不得创建指标序列。
func RawQueryDecisionAdd(ctx context.Context, reason string, count int) {
	if count <= 0 {
		return
	}
	switch reason {
	case RawQueryDecisionHistoryDuplicate, RawQueryDecisionMergeDB,
		RawQueryDecisionMissingIndex, RawQueryDecisionPaginationFinished:
	default:
		return
	}
	metric, _ := rawQueryDecisionsTotal.GetMetricWithLabelValues(reason, config.Version, config.CommitHash)
	counterAdd(ctx, metric, float64(count))
}
