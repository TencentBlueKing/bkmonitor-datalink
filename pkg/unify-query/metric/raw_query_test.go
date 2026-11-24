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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

func TestRawQueryDecisionAdd(t *testing.T) {
	ctx := context.Background()
	reasons := []string{
		RawQueryDecisionHistoryDuplicate,
		RawQueryDecisionMergeDB,
		RawQueryDecisionMissingIndex,
		RawQueryDecisionPaginationFinished,
	}
	// 先检查非法输入，确保非正数不会为尚未出现的合法原因创建序列。
	before := rawQueryDecisionSnapshot(t)
	const requestValue = "https://example.invalid/index/_search?query=private"
	for _, reason := range append(reasons, "", requestValue) {
		RawQueryDecisionAdd(ctx, reason, 0)
		RawQueryDecisionAdd(ctx, reason, -1)
	}
	RawQueryDecisionAdd(ctx, "", 2)
	RawQueryDecisionAdd(ctx, requestValue, 2)
	require.Equal(t, before, rawQueryDecisionSnapshot(t))

	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			beforeValue := rawQueryDecisionCounterValue(t, reason)
			RawQueryDecisionAdd(ctx, reason, 2)
			RawQueryDecisionAdd(ctx, reason, 3)
			require.Equal(t, beforeValue+5, rawQueryDecisionCounterValue(t, reason))
		})
	}
	metrics := rawQueryDecisionSnapshot(t)
	require.NotEmpty(t, metrics)
	for _, metric := range metrics {
		labels := make(map[string]string)
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
			require.NotContains(t, label.GetValue(), requestValue)
		}
		require.Len(t, labels, 3)
		require.Contains(t, labels, "version")
		require.Contains(t, labels, "commit_id")
		require.Contains(t, reasons, labels["reason"])
		require.Equal(t, config.Version, labels["version"])
		require.Equal(t, config.CommitHash, labels["commit_id"])
	}
}

// rawQueryDecisionSnapshot 从真实默认注册表读取快照，不通过指标写入 API 创建序列。
func rawQueryDecisionSnapshot(t *testing.T) []*dto.Metric {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "unify_query_raw_query_decisions_total" {
			require.Equal(t, dto.MetricType_COUNTER, family.GetType())
			return family.GetMetric()
		}
	}
	return nil
}

func rawQueryDecisionCounterValue(t *testing.T, reason string) float64 {
	t.Helper()
	for _, metric := range rawQueryDecisionSnapshot(t) {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "reason" && label.GetValue() == reason {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
