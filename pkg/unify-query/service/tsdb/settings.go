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
	VmTimeoutConfigPath = "victoria_metrics.timeout"

	VmContentTypeConfigPath = "victoria_metrics.content_type"

	VmMaxConditionNumConfigPath = "victoria_metrics.max_condition_num"

	VmInfluxCompatibleConfigPath = "victoria_metrics.influx_compatible"
	VmUseNativeOrConfigPath      = "victoria_metrics.use_native_or"

	// BkSql 配置
	BkSqlTimeoutConfigPath     = "bk_sql.timeout"
	BkSqlLimitConfigPath       = "bk_sql.limit"
	BkSqlToleranceConfigPath   = "bk_sql.tolerance"
	BkSqlContentTypeConfigPath = "bk_sql.content_type"

	EsTimeoutConfigPath    = "elasticsearch.timeout"
	EsMaxRoutingConfigPath = "elasticsearch.max_routing"
	EsMaxSizeConfigPath    = "elasticsearch.max_size"

	// query router 配置
	QueryRouterForceVmClusterNameConfigPath = "query_router.force_vm_cluster_name"
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

	// bksql 配置
	BkSqlTimeout     time.Duration
	BkSqlLimit       int
	BkSqlTolerance   int
	BkSqlContentType string

	// victoriaMetrics 配置
	VmTimeout time.Duration

	VmContentType     string
	VmMaxConditionNum int

	VmInfluxCompatible bool
	VmUseNativeOr      bool

	EsTimeout    time.Duration
	EsMaxRouting int
	EsMaxSize    int

	QueryRouterForceVmClusterName string
)
