// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prettyprint

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestPrettyPrint(t *testing.T) {
	logger.SetLoggerLevel(logger.DebugLevelDesc)
	t.Run("Traces", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 2,
		})
		Pretty(define.RecordTraces, g.Generate())
	})

	t.Run("Metrics", func(t *testing.T) {
		g := generator.NewMetricsGenerator(define.MetricsOptions{
			GaugeCount: 2,
		})
		Pretty(define.RecordMetrics, g.Generate())
	})

	t.Run("Logs", func(t *testing.T) {
		g := generator.NewLogsGenerator(define.LogsOptions{
			LogCount: 2,
		})
		Pretty(define.RecordLogs, g.Generate())
	})

	t.Run("MemoryStats", func(t *testing.T) {
		RuntimeMemStats(t.Logf)
	})
}
