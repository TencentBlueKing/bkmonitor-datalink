// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"github.com/spf13/viper"
)

const (
	UrlPath      = "elasticsearch.url"
	UsernamePath = "elasticsearch.username"
	PasswordPath = "elasticsearch.password"
	TimeoutPath  = "elasticsearch.timeout"

	SegmentDocCountPath     = "elasticsearch.segment.doc_count"
	SegmentStoreSizePath    = "elasticsearch.segment.store_size"
	SegmentMaxNumPath       = "elasticsearch.segment.max_num"
	SegmentMaxTimeRangePath = "elasticsearch.segment.max_time_range"

	MaxRoutingPath = "elasticsearch.max_routing"

	MaxSizePath   = "elasticsearch.max_size"
	KeepAlivePath = "elasticsearch.keep_alive"

	MappingCacheMaxCostPath     = "elasticsearch.mapping_cache.max_cost"
	MappingCacheNumCountersPath = "elasticsearch.mapping_cache.num_counters"
	MappingCacheBufferItemsPath = "elasticsearch.mapping_cache.buffer_items"
	MappingCacheTTLPath         = "elasticsearch.mapping_cache.ttl"
)

func init() {
	viper.SetDefault(SegmentDocCountPath, 1e4)
	viper.SetDefault(SegmentStoreSizePath, "100MB")
	viper.SetDefault(SegmentMaxNumPath, 20)
	viper.SetDefault(SegmentMaxTimeRangePath, "1h")
	viper.SetDefault(MaxRoutingPath, 10)
	viper.SetDefault(MaxSizePath, 1e4)
	viper.SetDefault(KeepAlivePath, "5s")

	viper.SetDefault(MappingCacheMaxCostPath, 1000000)
	viper.SetDefault(MappingCacheNumCountersPath, 10000000)
	viper.SetDefault(MappingCacheBufferItemsPath, 64)
	viper.SetDefault(MappingCacheTTLPath, "30m")
}
