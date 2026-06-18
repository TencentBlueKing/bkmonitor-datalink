// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"

const (
	grpcTraceExport   = "/opentelemetry.proto.collector.trace.v1.TraceService/Export"
	grpcMetricsExport = "/opentelemetry.proto.collector.metrics.v1.MetricsService/Export"
	grpcLogsExport    = "/opentelemetry.proto.collector.logs.v1.LogsService/Export"
)

// HTTP 路径 → 数据类型。路径取自各 receiver 的入站常量，与 receiver 自身落 RecordType 解耦。
var httpRecordTypes = map[string]define.RecordType{
	"/v1/traces":        define.RecordTraces,
	"/v1/trace":         define.RecordTraces,
	"/v1/metrics":       define.RecordMetrics,
	"/v1/logs":          define.RecordLogs,
	"/prometheus/write": define.RecordMetrics,
	"/pyroscope/ingest": define.RecordProfiles,
}

var grpcRecordTypes = map[string]define.RecordType{
	grpcTraceExport:   define.RecordTraces,
	grpcMetricsExport: define.RecordMetrics,
	grpcLogsExport:    define.RecordLogs,
}

// ClassifyHTTP 按请求路径归类。表外的端点返回 RecordUndefined，由中间件放行、不限流。
func ClassifyHTTP(path string) define.RecordType {
	if rt, ok := httpRecordTypes[path]; ok {
		return rt
	}
	return define.RecordUndefined
}

// ClassifyGRPC 按 gRPC 全方法名归类，未注册同样返回 RecordUndefined。
func ClassifyGRPC(method string) define.RecordType {
	if rt, ok := grpcRecordTypes[method]; ok {
		return rt
	}
	return define.RecordUndefined
}
