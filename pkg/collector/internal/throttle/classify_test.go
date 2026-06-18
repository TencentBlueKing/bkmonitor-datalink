// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestClassifyHTTP(t *testing.T) {
	assert.Equal(t, define.RecordTraces, ClassifyHTTP("/v1/traces"))
	assert.Equal(t, define.RecordTraces, ClassifyHTTP("/v1/trace"))
	assert.Equal(t, define.RecordMetrics, ClassifyHTTP("/v1/metrics"))
	assert.Equal(t, define.RecordMetrics, ClassifyHTTP("/prometheus/write"))
	assert.Equal(t, define.RecordLogs, ClassifyHTTP("/v1/logs"))
	assert.Equal(t, define.RecordProfiles, ClassifyHTTP("/pyroscope/ingest"))
	assert.Equal(t, define.RecordProfiles, ClassifyHTTP("/push.v1.PusherService/Push"))
	assert.Equal(t, define.RecordUndefined, ClassifyHTTP("/debug/metrics"))
}

func TestClassifyGRPC(t *testing.T) {
	assert.Equal(t, define.RecordTraces, ClassifyGRPC(grpcTraceExport))
	assert.Equal(t, define.RecordMetrics, ClassifyGRPC(grpcMetricsExport))
	assert.Equal(t, define.RecordLogs, ClassifyGRPC(grpcLogsExport))
	assert.Equal(t, define.RecordUndefined, ClassifyGRPC("/custom.Service/Call"))
}
