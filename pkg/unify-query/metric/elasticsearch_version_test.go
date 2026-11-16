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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestElasticsearchVersionObserve(t *testing.T) {
	for _, tc := range []struct {
		name   string
		major  uint64
		result string
		label  string
	}{
		{name: "ES5", major: 5, result: "success", label: "5"},
		{name: "ES6", major: 6, result: "success", label: "6"},
		{name: "ES7", major: 7, result: "success", label: "7"},
		{name: "ES8", major: 8, result: "success", label: "8"},
		{name: "ES9", major: 9, result: "success", label: "9"},
		{name: "其他版本保持有限标签", major: 42, result: "success", label: "other"},
		{name: "版本请求被拒绝", result: "forbidden", label: "unknown"},
		{name: "无效版本不报告大版本", major: 8, result: "invalid_response", label: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := elasticsearchVersionRequests.WithLabelValues(tc.label, tc.result)
			histogram := elasticsearchVersionSeconds.WithLabelValues(tc.result).(prometheus.Metric)
			var beforeCounter, beforeDuration dto.Metric
			require.NoError(t, counter.Write(&beforeCounter))
			require.NoError(t, histogram.Write(&beforeDuration))
			ElasticsearchVersionObserve(context.Background(), tc.major, tc.result, 250*time.Millisecond)
			var afterCounter, afterDuration dto.Metric
			require.NoError(t, counter.Write(&afterCounter))
			require.NoError(t, histogram.Write(&afterDuration))
			require.Equal(t, beforeCounter.GetCounter().GetValue()+1, afterCounter.GetCounter().GetValue())
			require.Equal(t, beforeDuration.GetHistogram().GetSampleCount()+1, afterDuration.GetHistogram().GetSampleCount())
			require.InDelta(t, beforeDuration.GetHistogram().GetSampleSum()+0.25, afterDuration.GetHistogram().GetSampleSum(), 1e-9)
		})
	}
}
