// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"time"

	"github.com/spf13/viper"
)

const (
	MaxQueryTimeRangeConfigPath = "es.max_query_time_range"
	MaxQueryTimeRangeEnv        = "UNIFY_QUERY_ES_MAX_QUERY_TIME_RANGE"
	legacyMaxQueryTimeRangeEnv  = "UNIFY-QUERY_ES_MAX_QUERY_TIME_RANGE"
	defaultMaxQueryTimeRange    = 7 * 24 * time.Hour
)

func init() {
	viper.SetDefault(MaxQueryTimeRangeConfigPath, defaultMaxQueryTimeRange)
	// 显式绑定全下划线变量，避免现有 UNIFY-QUERY 前缀中的连字符影响部署系统注入。
	_ = viper.BindEnv(
		MaxQueryTimeRangeConfigPath,
		MaxQueryTimeRangeEnv,
		legacyMaxQueryTimeRangeEnv,
	)
}

func maxQueryTimeRange() time.Duration {
	duration := viper.GetDuration(MaxQueryTimeRangeConfigPath)
	if duration <= 0 {
		return defaultMaxQueryTimeRange
	}
	return duration
}
