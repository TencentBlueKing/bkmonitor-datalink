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
	RegisterHTTPRecordType("/test/throttle/traces", define.RecordTraces)
	RegisterHTTPRecordType("/test/throttle/metrics", define.RecordMetrics)
	RegisterHTTPRecordType("/test/throttle/logs", define.RecordLogs)
	RegisterHTTPRecordType("/test/throttle/profiles", define.RecordProfiles)

	assert.Equal(t, define.RecordTraces, ClassifyHTTP("/test/throttle/traces"))
	assert.Equal(t, define.RecordMetrics, ClassifyHTTP("/test/throttle/metrics"))
	assert.Equal(t, define.RecordLogs, ClassifyHTTP("/test/throttle/logs"))
	assert.Equal(t, define.RecordProfiles, ClassifyHTTP("/test/throttle/profiles"))
	assert.Equal(t, define.RecordUndefined, ClassifyHTTP("/debug/metrics"))
}

func TestClassifyGRPC(t *testing.T) {
	RegisterGRPCRecordType("/test.ThrottleTraceService/Export", define.RecordTraces)
	RegisterGRPCRecordType("/test.ThrottleMetricsService/Export", define.RecordMetrics)
	RegisterGRPCRecordType("/test.ThrottleLogsService/Export", define.RecordLogs)

	assert.Equal(t, define.RecordTraces, ClassifyGRPC("/test.ThrottleTraceService/Export"))
	assert.Equal(t, define.RecordMetrics, ClassifyGRPC("/test.ThrottleMetricsService/Export"))
	assert.Equal(t, define.RecordLogs, ClassifyGRPC("/test.ThrottleLogsService/Export"))
	assert.Equal(t, define.RecordUndefined, ClassifyGRPC("/custom.Service/Call"))
}

func TestRegisterRecordTypeConflict(t *testing.T) {
	RegisterHTTPRecordType("/test/throttle/conflict", define.RecordTraces)
	RegisterHTTPRecordType("/test/throttle/conflict", define.RecordTraces)

	assert.Panics(t, func() {
		RegisterHTTPRecordType("/test/throttle/conflict", define.RecordMetrics)
	})
}
