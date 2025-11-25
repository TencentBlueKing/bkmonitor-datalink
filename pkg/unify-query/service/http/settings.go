// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"time"
)

const (
	IPAddressConfigPath           = "http.address"
	PortConfigPath                = "http.port"
	UserNameConfigPath            = "http.username"
	PasswordConfigPath            = "http.password"
	WriteTimeOutConfigPath        = "http.write_timeout"
	ReadTimeOutConfigPath         = "http.read_timeout"
	SingleflightTimeoutConfigPath = "http.singleflight_timeout"
	SlowQueryThresholdConfigPath  = "http.slow_query_threshold"
	DefaultQueryListLimitPath     = "http.default_query_list_limit"

	QueryMaxRoutingConfigPath      = "http.query.max_routing"
	QueryContentTypeConfigPath     = "http.query.content_type"
	QueryContentEncodingConfigPath = "http.query.content_encoding"

	// 服务配置
	EnablePrometheusConfigPath = "http.prometheus.enable"
	PrometheusPathConfigPath   = "http.prometheus.path"
	EnableProfileConfigPath    = "http.profile.enable"
	ProfilePathConfigPath      = "http.profile.path"

	AlignInfluxdbResultConfigPath = "http.ts.align_influxdb_result"

	TSQueryHandlePathConfigPath                   = "http.path.ts"
	TSQueryInfoHandlePathConfigPath               = "http.path.ts_info"
	TSQueryExemplarHandlePathConfigPath           = "http.path.ts_exemplar"
	TSQueryPromQLHandlePathConfigPath             = "http.path.ts_promql"
	TSQueryReferenceQueryHandlePathConfigPath     = "http.path.ts_reference"
	TSQueryRawQueryHandlePathConfigPath           = "http.path.ts_raw"
	TSQueryStructToPromQLHandlePathConfigPath     = "http.path.ts_struct_to_promql"
	TSQueryPromQLToStructHandlePathConfigPath     = "http.path.ts_promql_to_struct"
	TSQueryLabelValuesPathConfigPath              = "http.path.ts_label_values"
	TSQueryClusterMetricsPathConfigPath           = "http.path.ts_cluster_metrics"
	FluxHandlePromqlPathConfigPath                = "http.path.promql"
	PrintHandlePathConfigPath                     = "http.path.print"
	InfluxDBPrintHandlePathConfigPath             = "http.path.influxdb_print"
	SpacePrintHandlePathConfigPath                = "http.path.space_print"
	SpaceKeyPrintHandlePathConfigPath             = "http.path.space_key_print"
	TsDBPrintHandlePathConfigPath                 = "http.path.tsdb_print"
	FeatureFlagHandlePathConfigPath               = "http.path.feature_flag_path"
	ESHandlePathConfigPath                        = "http.path.es"
	ProxyConfigPath                               = "http.path.proxy"
	TSQueryRawMAXLimitConfigPath                  = "http.query.raw.max_limit"
	TSQueryRawQueryWithScrollHandlePathConfigPath = "http.path.ts_raw_with_scroll"
	CheckQueryTsConfigPath                        = "http.path.check_query_ts"
	CheckQueryPromQLConfigPath                    = "http.path.check_query_promql"

	// 查询配置
	InfoDefaultLimit = "http.info.limit"

	// 分段查询配置
	SegmentedEnable      = "http.segmented.enable"
	SegmentedMaxRoutines = "http.segmented.max_routines"
	SegmentedMinInterval = "http.segmented.min_interval"

	// 滚动查询配置
	ScrollSliceLimitConfigPath         = "scroll.slice_limit"
	ScrollSessionLockTimeoutConfigPath = "scroll.session_lock_timeout"
	ScrollWindowTimeoutConfigPath      = "scroll.window_timeout"

	// 查询缓存配置
	QueryCacheEnabledConfigPath         = "http.query_cache.enabled"
	QueryCacheShortTermRetryConfigPath  = "http.query_cache.short_term_retry"
	QueryCacheMediumTermRetryConfigPath = "http.query_cache.medium_term_retry"
	QueryCacheSumRetryConfigPath        = "http.query_cache.sum_retry"
	QueryCacheShortRetryConfigPath      = "http.query_cache.short_retry"

	// 集群指标查询配置
	ClusterMetricQueryPrefixConfigPath  = "http.cluster_metric.prefix"
	ClusterMetricQueryTimeoutConfigPath = "http.cluster_metric.timeout"

	JwtPublicKeyConfigPath       = "jwt.public_key"
	JwtBkAppCodeSpacesConfigPath = "jwt.bk_app_code_spaces"
	JwtEnabledConfigPath         = "jwt.enabled"
)

var (
	IPAddress           string
	Port                int
	Username            string
	Password            string
	AlignInfluxdbResult bool
	TestV               bool

	WriteTimeout          time.Duration
	ReadTimeout           time.Duration
	SingleflightTimeout   time.Duration
	SlowQueryThreshold    time.Duration
	DefaultQueryListLimit int

	QueryMaxRouting int

	ClusterMetricQueryPrefix  string
	ClusterMetricQueryTimeout time.Duration

	JwtPublicKey       string
	JwtBkAppCodeSpaces map[string][]string
	JwtEnabled         bool

	ScrollWindowTimeout      string
	ScrollSessionLockTimeout string
	ScrollSliceLimit         int

	QueryCacheEnabled         bool
	QueryCacheShortTermRetry  time.Duration
	QueryCacheMediumTermRetry time.Duration
	QueryCacheSumRetry        int
	QueryCacheShortRetry      int
)
