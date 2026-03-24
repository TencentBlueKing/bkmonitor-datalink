// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"github.com/spf13/viper"
)

var (
	CmdbApiRateLimitQPS     float64
	CmdbApiRateLimitBurst   int
	CmdbApiRateLimitTimeout int
)

func initAlarmConfig() {
	// alarm config
	CmdbApiRateLimitQPS = viper.GetFloat64("taskConfig.alarm.cmdb_api_rate_limit.qps")
	CmdbApiRateLimitBurst = viper.GetInt("taskConfig.alarm.cmdb_api_rate_limit.burst")
	CmdbApiRateLimitTimeout = viper.GetInt("taskConfig.alarm.cmdb_api_rate_limit.timeout")

	// set default value
	if CmdbApiRateLimitQPS < 1 {
		CmdbApiRateLimitQPS = 100
	}

	// burst should be greater than qps
	if CmdbApiRateLimitBurst < int(CmdbApiRateLimitQPS)+1 {
		CmdbApiRateLimitBurst = int(CmdbApiRateLimitQPS) + 1
	}

	// set default value
	if CmdbApiRateLimitTimeout == 0 {
		CmdbApiRateLimitTimeout = 10
	}
}
