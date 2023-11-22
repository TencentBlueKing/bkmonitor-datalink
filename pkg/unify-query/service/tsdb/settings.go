// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"time"
)

const (
	InfluxDBPerQueryMaxGoroutineConfigPath = "influxdb.per_query_max_goroutine" // 单指标查询的最多并查询数

	InfluxDBTimeoutConfigPath     = "influxdb.timeout"
	InfluxDBContentTypeConfigPath = "influxdb.content_type"
	InfluxDBChunkSizeConfigPath   = "influxdb.chunk_size"

	InfluxDBQueryRawUriPathConfigPath        = "influxdb.query_raw.uri_path"
	InfluxDBQueryRawAcceptConfigPath         = "influxdb.query_raw.accept"
	InfluxDBQueryRawAcceptEncodingConfigPath = "influxdb.query_raw.accept_encoding"

	InfluxDBQueryReadRateLimitConfigPath = "influxdb.max_read_rate_limiter"
	InfluxDBMaxLimitConfigPath           = "influxdb.max_limit"
	InfluxDBMaxSLimitConfigPath          = "influxdb.max_slimit"
	InfluxDBToleranceConfigPath          = "influxdb.tolerance"

	InfluxDBRouterPrefixConfigPath = "influxdb.router.prefix"

	// VmTimeoutConfigPath 配置
	VmAddressConfigPath = "victoria_metrics.address"
	VmUriPathConfigPath = "victoria_metrics.uri_path"
	VmTimeoutConfigPath = "victoria_metrics.timeout"

	VmContentTypeConfigPath = "victoria_metrics.content_type"

	VmCodeConfigPath            = "victoria_metrics.code"
	VmSecretConfigPath          = "victoria_metrics.secret"
	VmTokenConfigPath           = "victoria_metrics.token"
	VmMaxConditionNumConfigPath = "victoria_metrics.max_condition_num"

	VmAuthenticationMethodConfigPath = "victoria_metrics.authentication_method"

	VmInfluxCompatibleConfigPath = "victoria_metrics.influx_compatible"
	VmUseNativeOrConfigPath      = "victoria_metrics.use_native_or"

	// OfflineDataArchive 配置
	OfflineDataArchiveAddressConfigPath = "offline_data_archive.address"
	OfflineDataArchiveTimeoutConfigPath = "offline_data_archive.timeout"

	OfflineDataArchiveGrpcMaxCallRecvMsgSizeConfigPath = "offline_data_archive.grpc_max_call_recv_msg_size"
	OfflineDataArchiveGrpcMaxCallSendMsgSizeConfigPath = "offline_data_archive.grpc_max_call_send_msg_size"
)

var (
	// InfluxDB 配置
	InfluxDBTimeout     time.Duration
	InfluxDBContentType string
	InfluxDBChunkSize   int

	InfluxDBQueryRawUriPath        string
	InfluxDBQueryRawAccept         string
	InfluxDBQueryRawAcceptEncoding string

	InfluxDBQueryReadRateLimit float64
	InfluxDBMaxLimit           int
	InfluxDBMaxSLimit          int
	InfluxDBTolerance          int

	InfluxDBRouterPrefix string

	// victoriaMetrics 配置

	// victoriaMetrics 配置
	VmAddress string
	VmTimeout time.Duration
	VmUriPath string

	VmAuthenticationMethod string
	VmContentType          string
	VmMaxConditionNum      int

	VmCode   string
	VmSecret string
	VmToken  string

	AuthenticationMethod string

	VmInfluxCompatible bool
	VmUseNativeOr      bool

	OfflineDataArchiveAddress string
	OfflineDataArchiveTimeout time.Duration

	OfflineDataArchiveGrpcMaxCallRecvMsgSize int
	OfflineDataArchiveGrpcMaxCallSendMsgSize int
)
