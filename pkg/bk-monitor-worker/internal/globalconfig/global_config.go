// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package globalconfig

import "github.com/spf13/viper"

const (
	TimeSeriesMetricExpiredDaysPath = "global_config.time_series_metric_expired_days" // 自定义指标过期时间
	IsRestrictDsBelongSpacePath     = "global_config.is_restrict_ds_belong_space"     // 是否限制数据源归属具体空间
)

func init() {
	viper.SetDefault(TimeSeriesMetricExpiredDaysPath, 30)
	viper.SetDefault(IsRestrictDsBelongSpacePath, true)
}
