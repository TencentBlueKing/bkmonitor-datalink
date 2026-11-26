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

// ConditionOperatorOther 是 operator 标签里不在固定取值中的算子。
const ConditionOperatorOther = "other"

var conditionOperatorUnsupportedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "unify_query",
		Name:      "condition_operator_unsupported_total",
		Help:      "queries refused because a condition operator has no rendering on the storage they route to",
	},
	[]string{"operator", "storage", "version", "commit_id"},
)

// ConditionOperatorUnsupportedAdd 记一次因条件算子在该存储上无法渲染而被拒绝的查询。
// operator 只保留 gt、gte、lt、lte，其余记为 other；storage 只接受 victoria_metrics、influxdb、promql，
// 其余记为 other。请求里的原始算子不进标签。
func ConditionOperatorUnsupportedAdd(ctx context.Context, operator, storage string) {
	switch operator {
	case "gt", "gte", "lt", "lte":
	default:
		operator = ConditionOperatorOther
	}
	switch storage {
	case "victoria_metrics", "influxdb", "promql":
	default:
		storage = ConditionOperatorOther
	}
	metric, _ := conditionOperatorUnsupportedTotal.GetMetricWithLabelValues(operator, storage, config.Version, config.CommitHash)
	counterAdd(ctx, metric, 1)
}
